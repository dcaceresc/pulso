package app

import (
	"context"
	"errors"
	"net/netip"
	"os/exec"
	"reflect"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/dcaceresc/pulso/internal/firewall"
	"github.com/dcaceresc/pulso/internal/netcheck"
	"github.com/dcaceresc/pulso/internal/ui"
)

// completeSetup alimenta el modelo con los mensajes de los comandos. El
// vigilante del emparejamiento se bloquea a propósito; se abandona tras una
// espera corta.
func completeSetup(t *testing.T, model tea.Model, cmd tea.Cmd) tea.Model {
	t.Helper()
	if cmd == nil {
		return model
	}
	done := make(chan tea.Msg, 1)
	go func() { done <- cmd() }()
	var msg tea.Msg
	select {
	case msg = <-done:
	case <-time.After(100 * time.Millisecond):
		return model
	}
	if batch, ok := msg.(tea.BatchMsg); ok {
		for _, sub := range batch {
			model = completeSetup(t, model, sub)
		}
		return model
	}
	model, next := model.Update(msg)
	return completeSetup(t, model, next)
}

func setupDiagnosisKey(s string) tea.KeyMsg {
	return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)}
}

func TestPairingDiagnosisDoesNotAssignVPNPolicyToLAN(t *testing.T) {
	vpn := netcheck.Interface{Name: "tun0", Address: netip.MustParseAddr("10.7.0.2"), Virtual: true}
	wifi := netcheck.Interface{Name: "Wi-Fi", Address: netip.MustParseAddr("192.168.1.11")}
	for _, candidates := range [][]netcheck.Interface{{vpn}, {vpn, wifi}} {
		_, err := pairingDiagnosis(t.Context(), func() (netcheck.Report, error) {
			return netcheck.Report{LAN: vpn.Address, Candidates: candidates}, nil
		}, func(_ context.Context, opts firewall.Options) firewall.Plan {
			if opts.LocalAddr.IsValid() {
				t.Fatalf("profile lookup addressed to a VPN: %v", opts.LocalAddr)
			}
			return firewall.Plan{}
		})
		if err != nil {
			t.Fatal(err)
		}
	}
}

func TestPairingDiagnosisUsesLANPrefix(t *testing.T) {
	var got firewall.Options
	addr := netip.MustParseAddr("192.168.1.120")
	report := netcheck.Report{LAN: addr, Candidates: []netcheck.Interface{{Name: "Ethernet", Address: addr}}}
	plan := firewall.Plan{Backend: firewall.BackendUFW}
	diagnosis, err := pairingDiagnosis(context.Background(),
		func() (netcheck.Report, error) { return report, nil },
		func(_ context.Context, opts firewall.Options) firewall.Plan {
			got = opts
			return plan
		})
	if err != nil {
		t.Fatalf("diagnosis error = %v", err)
	}
	if want := netip.MustParsePrefix("192.168.1.0/24"); got.Prefix != want {
		t.Fatalf("prefix = %v, want %v", got.Prefix, want)
	}
	if got.LocalAddr != report.LAN || got.Port != 0 {
		t.Fatalf("local address = %v, port = %d; want %v and unknown Connect port", got.LocalAddr, got.Port, report.LAN)
	}
	if diagnosis.Network.LAN != report.LAN || diagnosis.Firewall.Backend != firewall.BackendUFW {
		t.Fatalf("diagnosis = %+v", diagnosis)
	}
}

func TestPairingDiagnosisWithoutLAN(t *testing.T) {
	inspected := false
	diagnosis, err := pairingDiagnosis(context.Background(),
		func() (netcheck.Report, error) { return netcheck.Report{}, nil },
		func(_ context.Context, opts firewall.Options) firewall.Plan {
			inspected = true
			if opts.Prefix.IsValid() {
				t.Fatalf("prefix = %v, want none without a LAN address", opts.Prefix)
			}
			return firewall.Plan{Backend: firewall.BackendNone}
		})
	if err != nil || !inspected || diagnosis.Network.LAN.IsValid() {
		t.Fatalf("diagnosis = %+v, %v, inspected=%v", diagnosis, err, inspected)
	}
}

func TestPairingDiagnosisNetworkFailure(t *testing.T) {
	want := errors.New("no interfaces")
	inspected := false
	diagnosis, err := pairingDiagnosis(context.Background(),
		func() (netcheck.Report, error) { return netcheck.Report{}, want },
		func(context.Context, firewall.Options) firewall.Plan {
			inspected = true
			return firewall.Plan{}
		})
	if !errors.Is(err, want) || inspected || diagnosis.Firewall.Backend != firewall.BackendNone {
		t.Fatalf("diagnosis = %+v, %v, inspected=%v", diagnosis, err, inspected)
	}
}

func TestPairingRulesCommand(t *testing.T) {
	if cmd := pairingRulesCmd(nil, func(string, ...string) *exec.Cmd { return nil }); cmd != nil {
		t.Fatal("an empty argv must not schedule a command")
	}
	var gotName string
	var gotArgs []string
	argv := []string{"sudo", "ufw", "allow", "from", "192.168.1.0/24"}
	cmd := pairingRulesCmd(argv, func(name string, args ...string) *exec.Cmd {
		gotName, gotArgs = name, args
		return exec.Command("true")
	})
	if cmd == nil || gotName != "sudo" || !reflect.DeepEqual(gotArgs, argv[1:]) {
		t.Fatalf("command = %v %q %q", cmd != nil, gotName, gotArgs)
	}
	if msg := cmd(); msg == nil {
		t.Fatal("the command must report its result to the wizard")
	}
}

func TestSetupPairingDiagnosisWiring(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	report := netcheck.Report{LAN: netip.MustParseAddr("192.168.1.120")}
	plan := firewall.Plan{
		Backend:  firewall.BackendUFW,
		Findings: []firewall.Finding{{Kind: firewall.ServiceMissing, Backend: firewall.BackendUFW}},
		Commands: []firewall.Command{{Argv: []string{"sudo", "ufw", "allow", "from", "192.168.1.0/24"}, Finding: firewall.ServiceMissing}},
	}
	var applied [][]string
	model := ui.NewSetupModel(ctx, ui.SetupService{
		Inspect: func(context.Context) (ui.SetupState, error) { return ui.SetupAwaitPairing, nil },
		Watch: func(watchCtx context.Context, _ func(string) bool) error {
			<-watchCtx.Done()
			return watchCtx.Err()
		},
		Diagnose: func(ctx context.Context) (ui.PairingDiagnosis, error) {
			return pairingDiagnosis(ctx,
				func() (netcheck.Report, error) { return report, nil },
				func(context.Context, firewall.Options) firewall.Plan { return plan })
		},
		Apply: func(argv []string) tea.Cmd {
			return func() tea.Msg {
				applied = append(applied, append([]string(nil), argv...))
				return ui.SetupApplyResult{Argv: argv}
			}
		},
	})
	model = completeSetup(t, model, model.Init())
	expectSetupText(t, model.View(), "Net 192.168.1.120 · LAN blocked", "d: diagnostics")

	model, _ = model.Update(setupDiagnosisKey("d"))
	expectSetupText(t, model.View(), "a: apply rules", "sudo ufw allow from 192.168.1.0/24")
	model, _ = model.Update(setupDiagnosisKey("a"))
	expectSetupText(t, model.View(), "a: confirm · any other key cancels")
	model, cmd := model.Update(setupDiagnosisKey("a"))
	model = completeSetup(t, model, cmd)
	want := [][]string{{"sudo", "ufw", "allow", "from", "192.168.1.0/24"}}
	if !reflect.DeepEqual(applied, want) {
		t.Fatalf("applied = %q, want %q", applied, want)
	}
}
