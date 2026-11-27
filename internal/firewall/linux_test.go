//go:build linux

package firewall

import (
	"context"
	"errors"
	"net/netip"
	"testing"
)

func TestUFWActiveWithMDNSAllowed(t *testing.T) {
	root := t.TempDir()
	writeFixture(t, root, ufwBeforeRules, "*filter\n-A ufw-before-input -p udp -d 224.0.0.251 --dport 5353 -j ACCEPT\nCOMMIT\n")
	writeFixture(t, root, ufwUserRules, "*filter\n-A ufw-user-input -s 192.168.1.0/24 -j ACCEPT\nCOMMIT\n")
	fake := newFakeRunner(map[string]commandResponse{
		"systemctl is-active ufw": activeResponse(),
	})

	plan := Inspect(context.Background(), Options{
		Root:   root,
		Runner: fake.run,
		Prefix: netip.MustParsePrefix("192.168.1.0/24"),
	})

	want := Plan{
		Backend:  BackendUFW,
		Findings: []Finding{{Kind: Active, Backend: BackendUFW}},
	}
	assertPlan(t, plan, want)
	assertNeverExecuted(t, fake.calls)
}

func TestUFWActiveWithoutReadableRules(t *testing.T) {
	fake := newFakeRunner(map[string]commandResponse{
		"systemctl is-active ufw": activeResponse(),
	})

	plan := Inspect(context.Background(), Options{
		Root:   t.TempDir(),
		Runner: fake.run,
		Prefix: netip.MustParsePrefix("192.168.1.0/24"),
	})

	want := Plan{
		Backend: BackendUFW,
		Findings: []Finding{{
			Kind:    Unverifiable,
			Backend: BackendUFW,
			Path:    ufwBeforeRules,
		}},
	}
	assertPlan(t, plan, want)
	assertNeverExecuted(t, fake.calls)
}

func TestUFWActiveLackingMDNS(t *testing.T) {
	root := t.TempDir()
	writeFixture(t, root, ufwBeforeRules, "*filter\n-A ufw-before-input -p tcp --dport 22 -j ACCEPT\nCOMMIT\n")
	writeFixture(t, root, ufwUserRules, "*filter\n-A ufw-user-input -s 192.168.1.0/24 -j ACCEPT\nCOMMIT\n")
	fake := newFakeRunner(map[string]commandResponse{
		"systemctl is-active ufw": activeResponse(),
	})

	plan := Inspect(context.Background(), Options{
		Root:   root,
		Runner: fake.run,
		Prefix: netip.MustParsePrefix("192.168.1.0/24"),
	})

	want := Plan{
		Backend: BackendUFW,
		Findings: []Finding{
			{Kind: Active, Backend: BackendUFW},
			{Kind: MDNSMissing, Backend: BackendUFW, Port: 5353, Path: ufwBeforeRules},
		},
		Commands: []Command{{
			Finding: MDNSMissing,
			Argv:    []string{"sudo", "ufw", "allow", "from", "192.168.1.0/24", "to", "any", "port", "5353", "proto", "udp"},
		}},
	}
	assertPlan(t, plan, want)
	assertNeverExecuted(t, fake.calls)
}

func TestUFWIgnoresCommentedRules(t *testing.T) {
	root := t.TempDir()
	writeFixture(t, root, ufwBeforeRules, "*filter\n# -A ufw-before-input -p udp --dport 5353 -j ACCEPT\nCOMMIT\n")
	writeFixture(t, root, ufwUserRules, "*filter\n-A ufw-user-input -s 192.168.1.0/24 -j ACCEPT\nCOMMIT\n")
	fake := newFakeRunner(map[string]commandResponse{
		"systemctl is-active ufw": activeResponse(),
	})

	plan := Inspect(context.Background(), Options{
		Root:   root,
		Runner: fake.run,
		Prefix: netip.MustParsePrefix("192.168.1.0/24"),
	})

	want := Plan{
		Backend: BackendUFW,
		Findings: []Finding{
			{Kind: Active, Backend: BackendUFW},
			{Kind: MDNSMissing, Backend: BackendUFW, Port: 5353, Path: ufwBeforeRules},
		},
		Commands: []Command{{
			Finding: MDNSMissing,
			Argv:    []string{"sudo", "ufw", "allow", "from", "192.168.1.0/24", "to", "any", "port", "5353", "proto", "udp"},
		}},
	}
	assertPlan(t, plan, want)
}

func TestUFWActiveLackingLANAccess(t *testing.T) {
	root := t.TempDir()
	writeFixture(t, root, ufwBeforeRules, "*filter\n-A ufw-before-input -p udp -d 224.0.0.251 --dport 5353 -j ACCEPT\nCOMMIT\n")
	writeFixture(t, root, ufwUserRules, "*filter\n:ufw-user-input - [0:0]\nCOMMIT\n")
	fake := newFakeRunner(map[string]commandResponse{
		"systemctl is-active ufw": activeResponse(),
	})

	plan := Inspect(context.Background(), Options{
		Root:   root,
		Runner: fake.run,
		Prefix: netip.MustParsePrefix("192.168.1.0/24"),
		Port:   37200,
	})

	want := Plan{
		Backend: BackendUFW,
		Findings: []Finding{
			{Kind: Active, Backend: BackendUFW},
			{Kind: ServiceMissing, Backend: BackendUFW, Port: 37200, Path: ufwUserRules},
		},
		Commands: []Command{{
			Finding: ServiceMissing,
			Argv:    []string{"sudo", "ufw", "allow", "from", "192.168.1.0/24"},
		}},
	}
	assertPlan(t, plan, want)
	assertNeverExecuted(t, fake.calls)
}

func TestUFWEnabledFromConfiguration(t *testing.T) {
	root := t.TempDir()
	writeFixture(t, root, ufwConfigPath, "ENABLED=yes\n")
	fake := newFakeRunner(map[string]commandResponse{
		"systemctl is-active ufw": inactiveResponse(),
	})

	plan := Inspect(context.Background(), Options{
		Root:   root,
		Runner: fake.run,
		Prefix: netip.MustParsePrefix("192.168.1.0/24"),
	})

	want := Plan{
		Backend: BackendUFW,
		Findings: []Finding{{
			Kind:    Unverifiable,
			Backend: BackendUFW,
			Path:    ufwBeforeRules,
		}},
	}
	assertPlan(t, plan, want)
}

func TestNoFirewall(t *testing.T) {
	fake := newFakeRunner(map[string]commandResponse{
		"systemctl is-active ufw":       inactiveResponse(),
		"systemctl is-active firewalld": inactiveResponse(),
		"systemctl is-active nftables":  inactiveResponse(),
	})

	plan := Inspect(context.Background(), Options{Root: t.TempDir(), Runner: fake.run})

	want := Plan{
		Backend:  BackendNone,
		Findings: []Finding{{Kind: Inactive}},
	}
	assertPlan(t, plan, want)
	assertNeverExecuted(t, fake.calls)
}

func TestFirewalldActiveWithoutReadableRules(t *testing.T) {
	fake := newFakeRunner(map[string]commandResponse{
		"systemctl is-active ufw":       inactiveResponse(),
		"systemctl is-active firewalld": activeResponse(),
		"firewall-cmd --list-all":       {err: errors.New("access denied")},
	})

	plan := Inspect(context.Background(), Options{Root: t.TempDir(), Runner: fake.run})

	want := Plan{
		Backend: BackendFirewalld,
		Findings: []Finding{{
			Kind:    Unverifiable,
			Backend: BackendFirewalld,
		}},
	}
	assertPlan(t, plan, want)
	assertNeverExecuted(t, fake.calls)
}

func TestFirewalldActiveWithReadableRules(t *testing.T) {
	fake := newFakeRunner(map[string]commandResponse{
		"systemctl is-active ufw":       inactiveResponse(),
		"systemctl is-active firewalld": activeResponse(),
		"firewall-cmd --list-all":       {out: "public (active)\n  services: mdns\n  ports: 37200/tcp\n"},
	})

	plan := Inspect(context.Background(), Options{Root: t.TempDir(), Runner: fake.run, Port: 37200})

	want := Plan{
		Backend:  BackendFirewalld,
		Findings: []Finding{{Kind: Active, Backend: BackendFirewalld}},
	}
	assertPlan(t, plan, want)
	assertNeverExecuted(t, fake.calls)
}

func TestNFTablesConfigFixture(t *testing.T) {
	root := t.TempDir()
	writeFixture(t, root, nftablesConfig, "table inet filter {\n  chain input {\n    type filter hook input priority 0; policy drop;\n    udp dport 5353 accept\n    tcp dport 37200 accept\n  }\n}\n")
	fake := newFakeRunner(map[string]commandResponse{
		"systemctl is-active ufw":       inactiveResponse(),
		"systemctl is-active firewalld": inactiveResponse(),
		"systemctl is-active nftables":  activeResponse(),
	})

	plan := Inspect(context.Background(), Options{Root: root, Runner: fake.run, Port: 37200})

	want := Plan{
		Backend:  BackendNFTables,
		Findings: []Finding{{Kind: Active, Backend: BackendNFTables}},
	}
	assertPlan(t, plan, want)
	assertNeverExecuted(t, fake.calls)
}

func TestNFTablesConfigMissingRules(t *testing.T) {
	root := t.TempDir()
	writeFixture(t, root, nftablesConfig, "table inet filter {\n  chain input {\n    type filter hook input priority 0; policy drop;\n  }\n}\n")
	fake := newFakeRunner(map[string]commandResponse{
		"systemctl is-active ufw":       inactiveResponse(),
		"systemctl is-active firewalld": inactiveResponse(),
		"systemctl is-active nftables":  activeResponse(),
	})

	plan := Inspect(context.Background(), Options{Root: root, Runner: fake.run, Port: 37200})

	want := Plan{
		Backend: BackendNFTables,
		Findings: []Finding{
			{Kind: Active, Backend: BackendNFTables},
			{Kind: MDNSMissing, Backend: BackendNFTables, Port: 5353, Path: nftablesConfig},
			{Kind: ServiceMissing, Backend: BackendNFTables, Port: 37200, Path: nftablesConfig},
		},
	}
	assertPlan(t, plan, want)
	assertNeverExecuted(t, fake.calls)
}

func TestNFTablesConfigUnreadable(t *testing.T) {
	fake := newFakeRunner(map[string]commandResponse{
		"systemctl is-active ufw":       inactiveResponse(),
		"systemctl is-active firewalld": inactiveResponse(),
		"systemctl is-active nftables":  activeResponse(),
	})

	plan := Inspect(context.Background(), Options{Root: t.TempDir(), Runner: fake.run})

	want := Plan{
		Backend: BackendNFTables,
		Findings: []Finding{{
			Kind:    Unverifiable,
			Backend: BackendNFTables,
			Path:    nftablesConfig,
		}},
	}
	assertPlan(t, plan, want)
}

func TestUFWMultiportRuleIsNotALANAllow(t *testing.T) {
	prefix := netip.MustParsePrefix("192.168.1.0/24")
	portSpecific := []string{
		"-A ufw-user-input -p udp -d 172.17.0.1 --dport 53 -s 172.16.0.0/12 -j ACCEPT",
		"-A ufw-user-input -p tcp --dport 47984 -s 192.168.1.0/24 -j ACCEPT",
		"-A ufw-user-input -p udp -m multiport --dports 47998:48000 -s 192.168.1.0/24 -j ACCEPT",
		"-A ufw-user-limit-accept -j ACCEPT",
	}
	if ufwAllowsLAN(portSpecific, prefix, 0) {
		t.Fatal("una regla con puertos concretos no cubre cualquier puerto de la LAN")
	}
	if !ufwAllowsLAN([]string{"-A ufw-user-input -p tcp -s 192.168.1.0/24 -j ACCEPT"}, prefix, 0) {
		t.Fatal("una regla de la LAN sin puerto debe contar como permiso")
	}
	if !ufwAllowsLAN([]string{"-A ufw-before-input -p tcp -s 192.168.1.0/24 -j ACCEPT"}, prefix, 0) {
		t.Fatal("una regla de ufw-before-input debe contar como permiso")
	}
}

func TestUFWRulesScopedToInterfaceOrProtocolDoNotGrantLANAccess(t *testing.T) {
	prefix := netip.MustParsePrefix("192.168.1.0/24")
	for _, line := range []string{
		"-A ufw-before-input -i lo -j ACCEPT",
		"-A ufw-before-input -p icmp --icmp-type echo-request -j ACCEPT",
		"-A ufw-before-input -p udp -j ACCEPT",
	} {
		if ufwAllowsLAN([]string{line}, prefix, 0) {
			t.Fatalf("la regla %q no prueba acceso TCP desde la LAN", line)
		}
	}
	if !ufwAllowsLAN([]string{"-A ufw-before-input -p tcp -j ACCEPT"}, prefix, 0) {
		t.Fatal("un permiso TCP sin origen debe contar como acceso")
	}
}
