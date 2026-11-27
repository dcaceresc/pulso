package firewall

import (
	"context"
	"errors"
	"net/netip"
	"strings"
	"testing"
)

func TestWindowsFirewallPolicy(t *testing.T) {
	for _, tc := range []struct {
		name, out        string
		err              error
		port             int
		status, udp, tcp Kind
	}{
		{name: "active is not evidence of blocked discovery", out: "active\r\n", status: Active, udp: MDNSUnverified, tcp: ServiceUnverified},
		{name: "active with a known TCP port", out: "active", port: 37200, status: Active, udp: MDNSUnverified, tcp: ServiceUnverified},
		{name: "blanket block with a known TCP port", out: "inbound-blocked", port: 37200, status: Active, udp: MDNSMissing, tcp: ServiceMissing},
		{name: "blanket block without a Connect port", out: "inbound-blocked", status: Active, udp: MDNSMissing, tcp: ServiceUnverified},
		{name: "disabled profile", out: "inactive", status: Inactive, udp: MDNSUnverified, tcp: ServiceUnverified},
		{name: "no unique interface or profile", status: Unverifiable, udp: MDNSUnverified, tcp: ServiceUnverified},
		{name: "query denied", out: "inbound-blocked", err: errors.New("access denied"), status: Unverifiable, udp: MDNSUnverified, tcp: ServiceUnverified},
	} {
		t.Run(tc.name, func(t *testing.T) {
			addr := netip.MustParseAddr("192.0.2.15")
			script := strings.Replace(windowsPolicyScript, "%s", addr.String(), 1)
			fake := newFakeRunner(map[string]commandResponse{
				"powershell -NoProfile -NonInteractive -Command " + script: {out: tc.out, err: tc.err},
			})
			plan := inspectWindows(t.Context(), Options{Runner: fake.run, LocalAddr: addr, Port: tc.port})
			assertPlan(t, plan, Plan{Backend: BackendWindows, Findings: []Finding{
				{Kind: tc.status, Backend: BackendWindows},
				{Kind: tc.udp, Backend: BackendWindows, Port: 5353},
				{Kind: tc.tcp, Backend: BackendWindows, Port: tc.port},
			}})
			if len(fake.calls) != 1 {
				t.Fatalf("queries = %v, want one policy query", fake.calls)
			}
			assertNeverExecuted(t, fake.calls)
		})
	}
}

func TestWindowsFirewallWithoutLocalAddressDoesNotGuessProfile(t *testing.T) {
	fake := newFakeRunner(nil)
	plan := inspectWindows(context.Background(), Options{Runner: fake.run})
	assertPlan(t, plan, Plan{Backend: BackendWindows, Findings: []Finding{
		{Kind: Unverifiable, Backend: BackendWindows},
		{Kind: MDNSUnverified, Backend: BackendWindows, Port: 5353},
		{Kind: ServiceUnverified, Backend: BackendWindows},
	}})
	if len(fake.calls) != 0 {
		t.Fatalf("queried without a local address: %v", fake.calls)
	}
}
