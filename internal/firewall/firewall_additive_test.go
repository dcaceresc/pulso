package firewall

import (
	"context"
	"crypto/sha256"
	"fmt"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Independent verifier probes for S23/T33: the package may only propose
// additive allow rules, never removals, and it must not need privileges.

type probeUnix struct {
	name string
	args []string
}

func probeWrite(t *testing.T, root, rel, content string) {
	t.Helper()
	full := filepath.Join(root, filepath.FromSlash(strings.TrimPrefix(rel, "/")))
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func probeHashTree(t *testing.T, root string) string {
	t.Helper()
	h := sha256.New()
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		fmt.Fprintf(h, "%s %d %d\n", path, info.Mode(), info.Size())
		if !info.IsDir() {
			data, readErr := os.ReadFile(path)
			if readErr != nil {
				return readErr
			}
			h.Write(data)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return fmt.Sprintf("%x", h.Sum(nil))
}

func probeRunner(calls *[]probeUnix, active map[string]bool) Runner {
	return func(_ context.Context, name string, args ...string) ([]byte, error) {
		*calls = append(*calls, probeUnix{name, append([]string(nil), args...)})
		if name == "systemctl" && len(args) >= 2 {
			if active[args[1]] {
				return []byte("active\n"), nil
			}
			return []byte("inactive\n"), nil
		}
		return nil, fmt.Errorf("probe: %s not available", name)
	}
}

func TestAdditiveOnlyArgvIsOnlyAdditive(t *testing.T) {
	forbidden := []string{"-D", "--delete", "delete", "--remove", "remove", "flush", "-I", "insert",
		"deny", "drop", "reject", "disable", "reload", "reset", "set-default", "stop",
		"sh", "bash", "/bin/sh", "-c", ";"}

	cases := []struct {
		name   string
		rules  map[string]string
		opts   Options
		active map[string]bool
	}{
		{
			name: "ufw enabled, LAN blocked",
			rules: map[string]string{
				"/etc/ufw/ufw.conf":     "ENABLED=yes\n",
				"/etc/ufw/user.rules":   "*filter\n:ufw-user-input - [0:0]\n-A ufw-user-input -p tcp --dport 8080 -j ACCEPT\nCOMMIT\n",
				"/etc/ufw/before.rules": "*filter\n:ufw-before-input - [0:0]\n-A ufw-before-input -p udp --dport 5353 -d 224.0.0.251 -j ACCEPT\nCOMMIT\n",
			},
			opts:   Options{Port: 37200},
			active: map[string]bool{"ufw": true},
		},
		{
			name: "ufw enabled, no prefix, no port",
			rules: map[string]string{
				"/etc/ufw/ufw.conf":   "ENABLED=yes\n",
				"/etc/ufw/user.rules": "*filter\n:ufw-user-input - [0:0]\nCOMMIT\n",
			},
			active: map[string]bool{"ufw": true},
		},
		{
			name: "ufw foreign source prefix",
			rules: map[string]string{
				"/etc/ufw/ufw.conf":   "ENABLED=yes\n",
				"/etc/ufw/user.rules": "*filter\n:ufw-user-input - [0:0]\n-A ufw-user-input -s 10.0.0.0/8 -j DROP\nCOMMIT\n",
			},
			opts:   Options{Port: 65535, Prefix: netip.MustParsePrefix("10.0.0.0/8")},
			active: map[string]bool{"ufw": true},
		},
		{
			name:   "firewalld active",
			rules:  map[string]string{},
			opts:   Options{Port: 37200},
			active: map[string]bool{"firewalld": true},
		},
		{
			name:   "nftables active, unreadable",
			rules:  map[string]string{},
			opts:   Options{},
			active: map[string]bool{"nftables": true},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			for rel, content := range tc.rules {
				probeWrite(t, root, rel, content)
			}
			before := probeHashTree(t, root)
			var calls []probeUnix
			opts := tc.opts
			opts.Root, opts.Runner = root, probeRunner(&calls, tc.active)
			plan := Inspect(context.Background(), opts)
			if after := probeHashTree(t, root); after != before {
				t.Fatalf("inspection modified the rule fixtures")
			}
			for _, call := range calls {
				if call.name == "sh" || call.name == "bash" || call.name == "sudo" {
					t.Fatalf("inspection used a shell or sudo: %v", call)
				}
			}
			for _, command := range plan.Commands {
				if len(command.Argv) == 0 {
					t.Fatal("empty argv")
				}
				if command.Argv[0] != "sudo" {
					t.Fatalf("argv[0] = %q, want sudo", command.Argv[0])
				}
				joined := strings.Join(command.Argv, " ")
				if !strings.Contains(joined, "allow") {
					t.Fatalf("argv is not an allow rule: %v", command.Argv)
				}
				for _, argv := range command.Argv {
					lower := strings.ToLower(argv)
					for _, banned := range forbidden {
						if lower == banned {
							t.Fatalf("argv contains %q: %v", banned, command.Argv)
						}
					}
					for _, banned := range []string{"delete", "--remove", "flush", "deny", "drop"} {
						if strings.Contains(lower, banned) {
							t.Fatalf("argv contains %q: %v", banned, command.Argv)
						}
					}
				}
				if !strings.HasPrefix(joined, "sudo ufw allow from ") {
					t.Fatalf("proposed command is not a prefix-scoped allow: %v", command.Argv)
				}
			}
		})
	}
}

func TestAdditiveOnlyWindowsArgvIsOnlyAdditive(t *testing.T) {
	var calls []probeUnix
	runner := func(_ context.Context, name string, args ...string) ([]byte, error) {
		calls = append(calls, probeUnix{name, append([]string(nil), args...)})
		if name == "powershell" {
			return []byte("Domain=True\nPrivate=True\nPublic=True\n"), nil
		}
		return nil, fmt.Errorf("probe: unexpected %s", name)
	}
	plan := inspectWindows(context.Background(), Options{Runner: runner, Port: 37200})
	if len(plan.Commands) != 2 {
		t.Fatalf("commands = %v", plan.Commands)
	}
	for _, command := range plan.Commands {
		joined := strings.Join(command.Argv, " ")
		if command.Argv[0] != "netsh" {
			t.Fatalf("argv[0] = %q, want netsh", command.Argv[0])
		}
		for _, required := range []string{"advfirewall", "firewall", "add", "rule", "dir=in", "action=allow"} {
			if !strings.Contains(joined, required) {
				t.Fatalf("argv %v misses %q", command.Argv, required)
			}
		}
		for _, banned := range []string{"delete", "remove", "flush", "block", "disable"} {
			if strings.Contains(joined, banned) {
				t.Fatalf("argv %v contains %q", command.Argv, banned)
			}
		}
	}
	for _, call := range calls {
		if call.name != "powershell" {
			t.Fatalf("window inspection ran %v", call)
		}
	}
}

func TestAdditiveOnlyNoFirewallKeepsEmptyPlan(t *testing.T) {
	var calls []probeUnix
	plan := Inspect(context.Background(), Options{Root: t.TempDir(), Runner: probeRunner(&calls, nil)})
	if plan.Backend != BackendNone || len(plan.Commands) != 0 {
		t.Fatalf("plan = %+v", plan)
	}
}
