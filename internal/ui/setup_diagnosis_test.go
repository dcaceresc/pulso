package ui

import (
	"context"
	"errors"
	"net/netip"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/dcaceresc/pulso/internal/firewall"
	"github.com/dcaceresc/pulso/internal/netcheck"
)

// completeSetup alimenta el modelo con los mensajes que producen los comandos.
// El vigilante del emparejamiento se bloquea a propósito en las pruebas, así que
// se abandona tras una espera corta.
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

func setupDiagnosisFixture() PairingDiagnosis {
	return PairingDiagnosis{
		Network: netcheck.Report{
			LAN: netip.MustParseAddr("192.168.1.120"),
			Candidates: []netcheck.Interface{
				{Name: "wlp1s0", Address: netip.MustParseAddr("192.168.1.120")},
				{Name: "docker0", Address: netip.MustParseAddr("172.18.0.1"), Virtual: true},
				{Name: "br-abcd", Address: netip.MustParseAddr("172.19.0.1"), Virtual: true},
			},
			Virtual: []netcheck.Interface{
				{Name: "docker0", Address: netip.MustParseAddr("172.18.0.1"), Virtual: true},
				{Name: "br-abcd", Address: netip.MustParseAddr("172.19.0.1"), Virtual: true},
			},
		},
		Firewall: firewall.Plan{
			Backend: firewall.BackendUFW,
			Findings: []firewall.Finding{
				{Kind: firewall.Active, Backend: firewall.BackendUFW},
				{Kind: firewall.MDNSMissing, Backend: firewall.BackendUFW, Port: 5353},
				{Kind: firewall.ServiceMissing, Backend: firewall.BackendUFW},
			},
			Commands: []firewall.Command{
				{Argv: []string{"sudo", "ufw", "allow", "from", "192.168.1.0/24", "to", "any", "port", "5353", "proto", "udp"}, Finding: firewall.MDNSMissing},
				{Argv: []string{"sudo", "ufw", "allow", "from", "192.168.1.0/24"}, Finding: firewall.ServiceMissing},
			},
		},
	}
}

func setupDiagnosisService(diagnose func(context.Context) (PairingDiagnosis, error), apply func([]string) tea.Cmd) SetupService {
	return SetupService{
		Inspect: func(context.Context) (SetupState, error) { return SetupAwaitPairing, nil },
		Watch: func(watchCtx context.Context, _ func(string) bool) error {
			<-watchCtx.Done()
			return watchCtx.Err()
		},
		Diagnose: diagnose,
		Apply:    apply,
	}
}

// pairingDiagnosisModel enters the pairing step and completes the asynchronous
// diagnosis so the summary is visible.
func pairingDiagnosisModel(t *testing.T, ctx context.Context, service SetupService) tea.Model {
	t.Helper()
	model := NewSetupModel(ctx, service)
	return completeSetup(t, model, model.Init())
}

func TestSetupPairingShowsCompactDiagnosis(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	diagnoses := 0
	model := pairingDiagnosisModel(t, ctx, setupDiagnosisService(
		func(context.Context) (PairingDiagnosis, error) {
			diagnoses++
			return setupDiagnosisFixture(), nil
		}, nil))
	view := model.View()
	expectSetupBounds(t, view, 80, 24)
	expectSetupText(t, view, "Waiting for pairing",
		"Net 192.168.1.120 · 2 virtual interfaces · LAN blocked",
		"Enter: recheck", "d: diagnostics", "1. Leave Pulso open")
	if diagnoses != 1 {
		t.Fatalf("diagnoses = %d, want one asynchronous run", diagnoses)
	}
}

func TestSetupPairingDetailListsFindingsAndCommands(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	model := pairingDiagnosisModel(t, ctx, setupDiagnosisService(
		func(context.Context) (PairingDiagnosis, error) { return setupDiagnosisFixture(), nil },
		func([]string) tea.Cmd { return nil }))
	model, cmd := model.Update(setupRunes("d"))
	if cmd != nil {
		t.Fatal("opening the detail must not run a command")
	}
	view := model.View()
	expectSetupBounds(t, view, 80, 24)
	expectSetupText(t, view, "a: apply rules", "Enter: back",
		"Virtual interface: docker0 172.18.0.1",
		"Discovery (UDP 5353) is blocked.",
		"sudo ufw allow from 192.168.1.0/24 to any port 5353 proto udp",
		"sudo ufw allow from 192.168.1.0/24")
}

func TestSetupPairingWindowsPolicy(t *testing.T) {
	for _, tc := range []struct {
		name, indicator, status, discovery string
		policy, udp                        firewall.Kind
	}{
		{"active only", "● Review", "Firewall active on the LAN interface.", "Discovery (UDP 5353): not verified.", firewall.Active, firewall.MDNSUnverified},
		{"policy unavailable", "● Review", "LAN firewall policy could not be determined.", "Discovery (UDP 5353): not verified.", firewall.Unverifiable, firewall.MDNSUnverified},
		{"confirmed blanket block", "● Blocked", "Firewall active on the LAN interface.", "Policy blocks inbound discovery (UDP 5353).", firewall.Active, firewall.MDNSMissing},
	} {
		t.Run(tc.name, func(t *testing.T) {
			const backend = firewall.BackendWindows
			service := setupDiagnosisService(
				func(context.Context) (PairingDiagnosis, error) {
					return PairingDiagnosis{Firewall: firewall.Plan{Backend: backend, Findings: []firewall.Finding{
						{Kind: tc.policy, Backend: backend},
						{Kind: tc.udp, Backend: backend, Port: 5353},
						{Kind: firewall.ServiceUnverified, Backend: backend},
					}}}, nil
				}, func([]string) tea.Cmd { t.Fatal("Windows policy must not offer to apply a rule"); return nil })
			service.Authorize = func(context.Context, string, func(string)) (CatalogAPI, error) { return nil, nil }
			model := pairingDiagnosisModel(t, t.Context(), service)
			expectSetupText(t, model.View(), tc.indicator, "d: diagnostics")
			model, _ = model.Update(setupRunes("d"))
			view := model.View()
			expectSetupText(t, view, tc.status, tc.discovery, "TCP: not verified (Connect port unknown).")
			for _, unwanted := range []string{"netsh", "a: apply rules", "The LAN cannot reach this device."} {
				if strings.Contains(view, unwanted) {
					t.Fatalf("unexpected %q in %s", unwanted, view)
				}
			}
			if tc.udp == firewall.MDNSUnverified {
				model, _ = model.Update(setupPairingUpdate{session: model.(*setupModel).pairing, status: setupPairingReady})
				if m := model.(*setupModel); m.progress != SetupReady || m.phase == setupPairingDetail {
					t.Fatal("pairing readiness did not advance past the diagnostics")
				}
			}
		})
	}
}

func TestSetupPairingApplyNeedsSecondConfirmation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var applied [][]string
	model := pairingDiagnosisModel(t, ctx, setupDiagnosisService(
		func(context.Context) (PairingDiagnosis, error) { return setupDiagnosisFixture(), nil },
		func(argv []string) tea.Cmd {
			return func() tea.Msg {
				applied = append(applied, append([]string(nil), argv...))
				return SetupApplyResult{Argv: argv}
			}
		}))
	model, _ = model.Update(setupRunes("d"))

	// La primera `a` solo pide confirmación.
	model, cmd := model.Update(setupRunes("a"))
	if cmd != nil || len(applied) != 0 {
		t.Fatalf("first a ran %d commands", len(applied))
	}
	expectSetupText(t, model.View(), "a: confirm · any other key cancels")

	// Cualquier otra tecla cancela sin ejecutar nada.
	model, cmd = model.Update(setupRunes("x"))
	if cmd != nil || len(applied) != 0 {
		t.Fatalf("cancel ran %d commands", len(applied))
	}
	expectSetupText(t, model.View(), "a: apply rules")
	if strings.Contains(model.View(), "a: confirm") {
		t.Fatal("the cancellation kept asking for confirmation")
	}

	model, cmd = model.Update(setupRunes("a"))
	if cmd != nil || len(applied) != 0 {
		t.Fatal("confirmation must wait for the second a")
	}
	model, cmd = model.Update(setupRunes("a"))
	if cmd == nil || len(applied) != 0 {
		t.Fatal("the second a must run the commands through the returned command")
	}
	model = completeSetup(t, model, cmd)
	want := [][]string{
		{"sudo", "ufw", "allow", "from", "192.168.1.0/24", "to", "any", "port", "5353", "proto", "udp"},
		{"sudo", "ufw", "allow", "from", "192.168.1.0/24"},
	}
	if !reflect.DeepEqual(applied, want) {
		t.Fatalf("applied = %q, want %q", applied, want)
	}
}

func TestSetupPairingDetailKeepsWatcherAndReturns(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var watchCanceled atomic.Bool
	var applied [][]string
	service := setupDiagnosisService(
		func(context.Context) (PairingDiagnosis, error) { return setupDiagnosisFixture(), nil },
		func(argv []string) tea.Cmd {
			return func() tea.Msg {
				applied = append(applied, append([]string(nil), argv...))
				return SetupApplyResult{Argv: argv}
			}
		})
	service.Watch = func(watchCtx context.Context, _ func(string) bool) error {
		<-watchCtx.Done()
		watchCanceled.Store(true)
		return watchCtx.Err()
	}
	model := pairingDiagnosisModel(t, ctx, service)
	model, _ = model.Update(setupRunes("d"))
	expectSetupText(t, model.View(), "a: apply rules")
	model, _ = model.Update(setupRunes("a"))
	model, cmd := model.Update(setupRunes("a"))
	model = completeSetup(t, model, cmd)
	if watchCanceled.Load() {
		t.Fatal("applying rules canceled the pairing watcher")
	}
	if len(applied) != 2 {
		t.Fatalf("applied = %d commands, want the whole plan", len(applied))
	}

	model, cmd = model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	model = completeSetup(t, model, cmd)
	expectSetupText(t, model.View(), "Waiting for pairing", "d: diagnostics")
	if strings.Contains(model.View(), "a: apply rules") {
		t.Fatal("Enter did not leave the detail")
	}
}

func TestSetupPairingWithoutDiagnosisIsUnchanged(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	model := NewSetupModel(ctx, SetupService{
		Inspect: func(context.Context) (SetupState, error) { return SetupAwaitPairing, nil },
		Watch: func(watchCtx context.Context, _ func(string) bool) error {
			<-watchCtx.Done()
			return watchCtx.Err()
		},
	})
	// Sin gancho Diagnose ningún comando nuevo se ejecuta.
	model, _ = runSetupCmd(t, model, model.Init())
	view := model.View()
	expectSetupBounds(t, view, 80, 24)
	expectSetupText(t, view, "Waiting for pairing",
		"Enter: recheck | b: back to the key | Esc: quit", "1. Leave Pulso open")
	if strings.Contains(view, "d: diagnostics") || strings.Contains(view, "Net ") {
		t.Fatalf("nil Diagnose added diagnosis content:\n%s", view)
	}
	// Sin diagnóstico tampoco hay nivel que mostrar: el indicador solo aparece
	// cuando hay datos que lo justifiquen.
	if strings.Contains(view, playerMarkNotice+" ") {
		t.Fatalf("nil Diagnose added a level indicator:\n%s", view)
	}
	model, cmd := model.Update(setupRunes("d"))
	if cmd != nil {
		t.Fatal("d must do nothing without a diagnosis hook")
	}
	expectSetupText(t, model.View(), "Waiting for pairing")
	if strings.Contains(model.View(), "a: apply rules") {
		t.Fatal("d opened the detail without a diagnosis hook")
	}
}

func TestSetupPairingApplyFailureIsLocalized(t *testing.T) {
	for _, tc := range []struct{ language, failed, waiting string }{
		{"en", "Could not apply: sudo ufw allow from 192.168.1.0/24", "Waiting for pairing"},
		{"es", "No se pudo aplicar: sudo ufw allow from 192.168.1.0/24", "Esperando el emparejamiento"},
	} {
		t.Run(tc.language, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			diagnoses := 0
			fixture := PairingDiagnosis{
				Network: netcheck.Report{LAN: netip.MustParseAddr("192.168.1.120")},
				Firewall: firewall.Plan{
					Backend:  firewall.BackendUFW,
					Findings: []firewall.Finding{{Kind: firewall.ServiceMissing, Backend: firewall.BackendUFW}},
					Commands: []firewall.Command{{Argv: []string{"sudo", "ufw", "allow", "from", "192.168.1.0/24"}, Finding: firewall.ServiceMissing}},
				},
			}
			service := setupDiagnosisService(
				func(context.Context) (PairingDiagnosis, error) {
					diagnoses++
					return fixture, nil
				},
				func(argv []string) tea.Cmd {
					return func() tea.Msg { return SetupApplyResult{Argv: argv, Err: errors.New("exit status 1")} }
				})
			model := NewSetupModelWithPreferences(ctx, service, preferences{Language: tc.language}, nil)
			model = completeSetup(t, model, model.Init())
			model, _ = model.Update(setupRunes("d"))
			model, _ = model.Update(setupRunes("a"))
			model, cmd := model.Update(setupRunes("a"))
			model = completeSetup(t, model, cmd)

			view := model.View()
			expectSetupBounds(t, view, 80, 24)
			expectSetupText(t, view, tc.failed)
			if strings.Contains(view, "exit status 1") {
				t.Fatal("the raw command error reached the screen")
			}
			if diagnoses != 2 {
				t.Fatalf("diagnoses = %d, want a second run after applying", diagnoses)
			}

			// El paso de emparejamiento sigue utilizable después del fallo.
			model, cmd = model.Update(tea.KeyMsg{Type: tea.KeyEnter})
			model = completeSetup(t, model, cmd)
			expectSetupText(t, model.View(), tc.waiting)
			if strings.Contains(model.View(), "a: apply rules") {
				t.Fatal("the detail did not close")
			}
		})
	}
}

func TestSetupPairingStoppedShowsDiagnosis(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	checks, diagnoses := 0, 0
	model := NewSetupModel(ctx, SetupService{
		Inspect: func(context.Context) (SetupState, error) {
			checks++
			if checks == 1 {
				return SetupAwaitPairing, nil
			}
			return SetupNeedsKey, nil
		},
		Watch: func(watchCtx context.Context, _ func(string) bool) error {
			<-watchCtx.Done()
			return watchCtx.Err()
		},
		Diagnose: func(context.Context) (PairingDiagnosis, error) {
			diagnoses++
			return setupDiagnosisFixture(), nil
		},
		Apply: func([]string) tea.Cmd { return nil },
	})
	model = completeSetup(t, model, model.Init())
	model, cmd := model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	model = completeSetup(t, model, cmd)
	view := model.View()
	expectSetupBounds(t, view, 80, 24)
	expectSetupText(t, view, "Soloist stopped", "● Blocked",
		"Net 192.168.1.120 · 2 virtual interfaces · LAN blocked", "d: diagnostics")
	if diagnoses != 2 {
		t.Fatalf("diagnoses = %d, want one per pairing step", diagnoses)
	}
	model, cmd = model.Update(setupRunes("d"))
	if cmd != nil {
		t.Fatal("opening the detail must not run a command")
	}
	expectSetupText(t, model.View(), "a: apply rules")
}
