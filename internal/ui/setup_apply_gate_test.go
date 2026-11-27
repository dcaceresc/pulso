package ui

import (
	"context"
	"net/netip"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/dcaceresc/pulso/internal/firewall"
	"github.com/dcaceresc/pulso/internal/netcheck"
)

// Independent verifier probes for S23/T34: no plan command may reach the
// executor without the two-step `a` gate.

type probeCall struct{ argv []string }

// probeRecorder acumula llamadas desde las goroutines de Bubble Tea sin
// carreras: el test lee mientras el watcher y el ejecutor falsos escriben.
type probeRecorder struct {
	mu        sync.Mutex
	calls     []probeCall
	cancelled atomic.Bool
}

func (r *probeRecorder) record(argv []string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls = append(r.calls, probeCall{append([]string(nil), argv...)})
}

func (r *probeRecorder) snapshot() []probeCall {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]probeCall(nil), r.calls...)
}

func (r *probeRecorder) count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.calls)
}

func probePlan() PairingDiagnosis {
	return PairingDiagnosis{
		Network: netcheck.Report{LAN: netip.MustParseAddr("192.168.1.120")},
		Firewall: firewall.Plan{
			Backend: firewall.BackendUFW,
			Findings: []firewall.Finding{
				{Kind: firewall.Active, Backend: firewall.BackendUFW},
				{Kind: firewall.ServiceMissing, Backend: firewall.BackendUFW},
			},
			Commands: []firewall.Command{
				{Argv: []string{"sudo", "ufw", "allow", "from", "192.168.1.0/24", "to", "any", "port", "5353", "proto", "udp"}},
				{Argv: []string{"sudo", "ufw", "allow", "from", "192.168.1.0/24"}},
				{Argv: []string{"sudo", "ufw", "allow", "from", "192.168.1.0/24", "to", "any", "port", "37200", "proto", "tcp"}},
			},
		},
	}
}

func probeService(diagnose func(context.Context) (PairingDiagnosis, error), recorder *probeRecorder) SetupService {
	return SetupService{
		Inspect: func(context.Context) (SetupState, error) { return SetupAwaitPairing, nil },
		Watch: func(ctx context.Context, _ func(string) bool) error {
			<-ctx.Done()
			if recorder != nil {
				recorder.cancelled.Store(true)
			}
			return ctx.Err()
		},
		Diagnose: diagnose,
		Apply: func(argv []string) tea.Cmd {
			recorder.record(argv)
			return func() tea.Msg { return SetupApplyResult{Argv: argv} }
		},
	}
}

// probePump resolves one command tree, feeding resulting messages back.
func probePump(t *testing.T, model tea.Model, cmd tea.Cmd) tea.Model {
	t.Helper()
	if cmd == nil {
		return model
	}
	done := make(chan tea.Msg, 1)
	go func() { done <- cmd() }()
	var msg tea.Msg
	select {
	case msg = <-done:
	case <-time.After(200 * time.Millisecond):
		return model
	}
	if batch, ok := msg.(tea.BatchMsg); ok {
		for _, sub := range batch {
			model = probePump(t, model, sub)
		}
		return model
	}
	next, cmd := model.Update(msg)
	return probePump(t, next, cmd)
}

func probeRunes(s string) tea.KeyMsg {
	return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)}
}

func probeReady(t *testing.T, service SetupService) tea.Model {
	t.Helper()
	model := NewSetupModel(context.Background(), service)
	model = probePump(t, model, model.Init())
	if !strings.Contains(model.View(), "Waiting for pairing") {
		t.Fatalf("probe did not reach the pairing step:\n%s", model.View())
	}
	return model
}

func TestApplyGatePairingKeyDoesNothingWithoutDetail(t *testing.T) {
	var recorder probeRecorder
	model := probeReady(t, probeService(func(context.Context) (PairingDiagnosis, error) { return probePlan(), nil }, &recorder))
	for i := 0; i < 3; i++ {
		var cmd tea.Cmd
		model, cmd = model.Update(probeRunes("a"))
		if cmd != nil || recorder.count() != 0 {
			t.Fatalf("press %d of a at the pairing step reached the executor: calls=%v cmd=%v", i+1, recorder.snapshot(), cmd)
		}
	}
	if strings.Contains(model.View(), "a: apply rules") {
		t.Fatal("the pairing step offers an apply key")
	}
}

func TestApplyGateApplyGate(t *testing.T) {
	var recorder probeRecorder
	model := probeReady(t, probeService(func(context.Context) (PairingDiagnosis, error) { return probePlan(), nil }, &recorder))
	model, _ = model.Update(probeRunes("d"))

	// One press arms only.
	model, cmd := model.Update(probeRunes("a"))
	if cmd != nil || recorder.count() != 0 {
		t.Fatalf("first a executed: %d calls, cmd=%v", recorder.count(), cmd)
	}
	if !strings.Contains(model.View(), "confirm") {
		t.Fatalf("first a did not ask for confirmation:\n%s", model.View())
	}

	// Any other key cancels the arming.
	model, cmd = model.Update(probeRunes("x"))
	if cmd != nil || recorder.count() != 0 {
		t.Fatalf("cancel executed: %d calls", recorder.count())
	}
	if strings.Contains(model.View(), "confirm") {
		t.Fatal("cancel kept the confirmation prompt")
	}

	// Enter cancels too: it leaves the detail and only re-diagnoses.
	model, _ = model.Update(probeRunes("a"))
	model, cmd = model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if recorder.count() != 0 {
		t.Fatalf("Enter after arming executed: %d calls", recorder.count())
	}
	if strings.Contains(model.View(), "a: confirm") {
		t.Fatal("Enter kept the confirmation prompt")
	}
	model, cmd = model.Update(probeRunes("a"))
	if cmd != nil || recorder.count() != 0 {
		t.Fatalf("a after Enter executed without a fresh detail: %d calls", recorder.count())
	}
	model, _ = model.Update(probeRunes("d"))

	// Multirune and pasted keys never arm.
	model, _ = model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("aa")})
	model, _ = model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("a"), Paste: true})
	model, cmd = model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("a"), Paste: true})
	if cmd != nil || recorder.count() != 0 {
		t.Fatalf("multirune or pasted a executed: %d calls", recorder.count())
	}

	// Uppercase second press applies the planned argvs in order.
	model, _ = model.Update(probeRunes("A"))
	model, cmd = model.Update(probeRunes("A"))
	if cmd == nil {
		t.Fatal("the second a scheduled nothing")
	}
	model = probePump(t, model, cmd)
	want := [][]string{
		{"sudo", "ufw", "allow", "from", "192.168.1.0/24", "to", "any", "port", "5353", "proto", "udp"},
		{"sudo", "ufw", "allow", "from", "192.168.1.0/24"},
		{"sudo", "ufw", "allow", "from", "192.168.1.0/24", "to", "any", "port", "37200", "proto", "tcp"},
	}
	var got [][]string
	for _, c := range recorder.snapshot() {
		got = append(got, c.argv)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("executed %q, want %q", got, want)
	}
}

func TestApplyGateApplyingRejectsKeys(t *testing.T) {
	var recorder probeRecorder
	model := probeReady(t, probeService(func(context.Context) (PairingDiagnosis, error) { return probePlan(), nil }, &recorder))
	model, _ = model.Update(probeRunes("d"))
	model, _ = model.Update(probeRunes("a"))
	model, cmd := model.Update(probeRunes("a"))
	if cmd == nil || recorder.count() != 1 {
		t.Fatalf("calls=%d cmd=%v", recorder.count(), cmd)
	}
	// The first command occupies the terminal; do not resolve it yet.
	for _, key := range []tea.KeyMsg{probeRunes("a"), probeRunes("A"), probeRunes("a"), tea.KeyMsg{Type: tea.KeyEnter}, probeRunes("d")} {
		var next tea.Cmd
		model, next = model.Update(key)
		if next != nil {
			t.Fatalf("key %v scheduled a command while applying", key)
		}
	}
	if recorder.count() != 1 {
		t.Fatalf("keys queued more commands while applying: %d calls", recorder.count())
	}
	if !strings.Contains(model.View(), "Applying firewall rules") {
		t.Fatalf("applying state left the screen:\n%s", model.View())
	}
	// Esc still quits while applying and executes nothing more.
	model, quit := model.Update(tea.KeyMsg{Type: tea.KeyEsc})
	msg := quit()
	if _, ok := msg.(tea.QuitMsg); !ok {
		t.Fatalf("Esc while applying returned %T, want tea.QuitMsg", msg)
	}
	if recorder.count() != 1 {
		t.Fatalf("Esc while applying executed more commands: %d", recorder.count())
	}
	if recorder.cancelled.Load() {
		t.Fatal("applying cancelled the pairing watcher")
	}
	_ = model
}

func TestApplyGateNoDiagnosisBindsNoKey(t *testing.T) {
	var recorder probeRecorder
	service := SetupService{
		Inspect: func(context.Context) (SetupState, error) { return SetupAwaitPairing, nil },
		Watch: func(ctx context.Context, _ func(string) bool) error {
			<-ctx.Done()
			return ctx.Err()
		},
		Apply: func(argv []string) tea.Cmd {
			recorder.record(argv)
			return nil
		},
	}
	model := probeReady(t, service)
	view := model.View()
	for _, banned := range []string{"d: diagnostics", "a: apply", "Net ", "LAN blocked"} {
		if strings.Contains(view, banned) {
			t.Fatalf("view without Diagnose shows %q:\n%s", banned, view)
		}
	}
	for _, key := range []string{"d", "a", "A"} {
		var cmd tea.Cmd
		model, cmd = model.Update(probeRunes(key))
		if cmd != nil || recorder.count() != 0 {
			t.Fatalf("key %q acted without Diagnose", key)
		}
	}
	if strings.Contains(model.View(), "Pairing diagnosis") {
		t.Fatal("detail opened without Diagnose")
	}
	_, quit := model.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if _, ok := quit().(tea.QuitMsg); !ok {
		t.Fatal("Esc no longer quits without Diagnose")
	}
}

func TestApplyGateArmingAndFunctionKeys(t *testing.T) {
	var recorder probeRecorder
	model := probeReady(t, probeService(func(context.Context) (PairingDiagnosis, error) { return probePlan(), nil }, &recorder))
	model, _ = model.Update(probeRunes("d"))
	model, _ = model.Update(probeRunes("a"))
	model, _ = model.Update(tea.KeyMsg{Type: tea.KeyF3})
	t.Logf("after F3 armed=%v", strings.Contains(model.View(), "confirm"))
	if !strings.Contains(model.View(), "confirm") {
		t.Log("F3 cleared the confirmation prompt")
	} else {
		t.Log("F3 kept the confirmation prompt armed")
	}
	_, cmd := model.Update(probeRunes("a"))
	if recorder.count() != 0 || cmd == nil {
		t.Logf("a after F3 executed %d commands", recorder.count())
	}
}

// El pie promete que cualquier otra tecla cancela la confirmación. Las teclas
// de idioma y tema se atienden antes del cambio de fase, así que también deben
// cancelarla; si no, `a`, F3, `a` ejecutaría mientras la pantalla aún dice
// "cancela".
func TestApplyGateFunctionKeysCancelConfirmation(t *testing.T) {
	var recorder probeRecorder
	model := probeReady(t, probeService(func(context.Context) (PairingDiagnosis, error) { return probePlan(), nil }, &recorder))
	model, _ = model.Update(probeRunes("d"))
	model, _ = model.Update(probeRunes("a"))
	if !strings.Contains(model.View(), "a: confirm") {
		t.Fatalf("first a must ask for confirmation:\n%s", model.View())
	}
	model, _ = model.Update(tea.KeyMsg{Type: tea.KeyF2})
	if strings.Contains(model.View(), "a: confirm") {
		t.Fatalf("F2 must cancel the confirmation:\n%s", model.View())
	}
	model, _ = model.Update(probeRunes("a"))
	if recorder.count() != 0 {
		t.Fatalf("a after F2 executed %d commands; want a confirmation only", recorder.count())
	}
	if !strings.Contains(model.View(), "a: confirm") {
		t.Fatalf("a after F2 must ask for confirmation again:\n%s", model.View())
	}
	model, _ = model.Update(tea.KeyMsg{Type: tea.KeyF3})
	if strings.Contains(model.View(), "a: confirm") {
		t.Fatalf("F3 must cancel the confirmation:\n%s", model.View())
	}
	model, _ = model.Update(probeRunes("a"))
	if recorder.count() != 0 {
		t.Fatalf("a after F3 executed %d commands; want a confirmation only", recorder.count())
	}
}
