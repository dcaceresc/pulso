package firewall

import (
	"context"
	"errors"
	"net/netip"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// commandResponse es la respuesta inyectada para un proceso concreto.
type commandResponse struct {
	out string
	err error
}

// fakeRunner registra cada llamada y devuelve la respuesta preparada. Nunca
// toca el cortafuegos real.
type fakeRunner struct {
	responses map[string]commandResponse
	calls     [][]string
}

func newFakeRunner(responses map[string]commandResponse) *fakeRunner {
	return &fakeRunner{responses: responses}
}

func (f *fakeRunner) run(_ context.Context, name string, args ...string) ([]byte, error) {
	call := append([]string{name}, args...)
	f.calls = append(f.calls, call)
	response, ok := f.responses[strings.Join(call, " ")]
	if !ok {
		return nil, errors.New("unexpected command: " + strings.Join(call, " "))
	}
	return []byte(response.out), response.err
}

func activeResponse() commandResponse { return commandResponse{out: "active\n"} }

func inactiveResponse() commandResponse {
	return commandResponse{out: "inactive\n", err: errors.New("exit status 3")}
}

func writeFixture(t *testing.T, root, name, content string) {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(strings.TrimPrefix(name, "/")))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir fixture: %v", err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
}

// assertNeverExecuted comprueba que el diagnóstico solo ejecutó consultas y
// nunca los comandos que se proponen al humano.
func assertNeverExecuted(t *testing.T, calls [][]string) {
	t.Helper()
	for _, call := range calls {
		if len(call) == 0 {
			continue
		}
		switch call[0] {
		case "sudo", "netsh":
			t.Fatalf("a proposed command was executed: %v", call)
		}
		if call[0] == "powershell" && strings.Contains(strings.Join(call, " "), "New-NetFirewallRule") {
			t.Fatalf("a proposed command was executed: %v", call)
		}
	}
}

func assertPlan(t *testing.T, got, want Plan) {
	t.Helper()
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("plan = %#v; want %#v", got, want)
	}
}

func TestWindowsProfilesActive(t *testing.T) {
	fake := newFakeRunner(map[string]commandResponse{
		"powershell -NoProfile -Command " + windowsProfilesScript: {
			out: "Domain=True\r\nPrivate=False\r\nPublic=True\r\n",
		},
	})

	plan := inspectWindows(context.Background(), Options{Runner: fake.run, Port: 37200}.withDefaults())

	want := Plan{
		Backend: BackendWindows,
		Findings: []Finding{
			{Kind: Active, Backend: BackendWindows},
			{Kind: MDNSMissing, Backend: BackendWindows, Port: 5353, Path: windowsRuleName},
			{Kind: ServiceMissing, Backend: BackendWindows, Port: 37200, Path: windowsRuleName},
		},
		Commands: []Command{
			{
				Finding: MDNSMissing,
				Argv:    []string{"netsh", "advfirewall", "firewall", "add", "rule", "name=Pulso (Soloist)", "dir=in", "action=allow", "protocol=UDP", "localport=5353"},
			},
			{
				Finding: ServiceMissing,
				Argv:    []string{"netsh", "advfirewall", "firewall", "add", "rule", "name=Pulso (Soloist)", "dir=in", "action=allow", "protocol=TCP", "localport=37200"},
			},
		},
	}
	assertPlan(t, plan, want)
	assertNeverExecuted(t, fake.calls)
}

func TestWindowsProfilesDisabled(t *testing.T) {
	fake := newFakeRunner(map[string]commandResponse{
		"powershell -NoProfile -Command " + windowsProfilesScript: {
			out: "Domain=False\r\nPrivate=False\r\nPublic=False\r\n",
		},
	})

	plan := inspectWindows(context.Background(), Options{Runner: fake.run}.withDefaults())

	want := Plan{
		Backend:  BackendNone,
		Findings: []Finding{{Kind: Inactive}},
	}
	assertPlan(t, plan, want)
}

func TestWindowsProfilesUnreadable(t *testing.T) {
	fake := newFakeRunner(map[string]commandResponse{
		"powershell -NoProfile -Command " + windowsProfilesScript: {err: errors.New("denied")},
	})

	plan := inspectWindows(context.Background(), Options{Runner: fake.run}.withDefaults())

	want := Plan{
		Backend: BackendWindows,
		Findings: []Finding{{
			Kind:    Unverifiable,
			Backend: BackendWindows,
		}},
	}
	assertPlan(t, plan, want)
}

func TestLanPrefix(t *testing.T) {
	for _, tc := range []struct {
		name  string
		given netip.Prefix
		fall  netip.Addr
		want  netip.Prefix
	}{
		{
			name:  "valid prefix is masked",
			given: netip.MustParsePrefix("192.168.1.120/24"),
			want:  netip.MustParsePrefix("192.168.1.0/24"),
		},
		{
			name: "invalid prefix masks the LAN address",
			fall: netip.MustParseAddr("192.168.1.120"),
			want: netip.MustParsePrefix("192.168.1.0/24"),
		},
		{
			name: "missing LAN address",
			want: netip.Prefix{},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := lanPrefix(tc.given, func() netip.Addr { return tc.fall })
			if got != tc.want {
				t.Fatalf("lanPrefix = %v; want %v", got, tc.want)
			}
		})
	}
}
