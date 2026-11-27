package netcheck

import (
	"errors"
	"net"
	"net/netip"
	"reflect"
	"testing"
)

func i(name, address string) Interface {
	return Interface{Name: name, Address: netip.MustParseAddr(address), Virtual: isVirtual(name)}
}

func TestIsVirtual(t *testing.T) {
	for _, tc := range []struct {
		name string
		want bool
	}{
		{"enp7s0", false},
		{"wlp6s0", false},
		{"eth0", false},
		{"br-060d8248859d", true},
		{"docker0", true},
		{"veth772af54", true},
		{"virbr0", true},
		{"tailscale0", true},
		{"wg0", true},
		{"tun0", true},
		{"tap0", true},
		{"lxcbr0", true},
		{"incusbr0", true},
		{"podman0", true},
		{"cni0", true},
		{"flannel.1", true},
		{"cali123", true},
		{"ztabc", true},
	} {
		if got := isVirtual(tc.name); got != tc.want {
			t.Errorf("isVirtual(%q) = %v; want %v", tc.name, got, tc.want)
		}
	}
}

func TestReport(t *testing.T) {
	lan := netip.MustParseAddr("192.168.1.120")
	for _, tc := range []struct {
		name        string
		candidates  []Interface
		wantLAN     string
		wantVirtual []string
		ambiguous   bool
	}{
		{name: "single physical interface", candidates: []Interface{i("enp7s0", "192.168.1.120")}, wantLAN: "192.168.1.120"},
		{
			name:        "docker bridge beside the network",
			candidates:  []Interface{i("br-060d8248859d", "172.18.0.1"), i("enp7s0", "192.168.1.120")},
			wantLAN:     "192.168.1.120",
			wantVirtual: []string{"br-060d8248859d"},
			ambiguous:   true,
		},
		{
			name:        "vpn beside the network",
			candidates:  []Interface{i("enp7s0", "192.168.1.120"), i("wg0", "10.8.0.2")},
			wantLAN:     "192.168.1.120",
			wantVirtual: []string{"wg0"},
			ambiguous:   true,
		},
		{
			name:       "two physical interfaces",
			candidates: []Interface{i("enp7s0", "192.168.1.120"), i("wlp6s0", "192.168.1.130")},
			wantLAN:    "192.168.1.120",
			ambiguous:  true,
		},
		{name: "no candidate", wantLAN: "192.168.1.120"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := report(tc.candidates, lan)
			if got.LAN != lan {
				t.Fatalf("LAN = %v; want %v", got.LAN, lan)
			}
			if tc.wantLAN != got.LAN.String() {
				t.Fatalf("unexpected LAN %v", got.LAN)
			}
			var names []string
			for _, candidate := range got.Virtual {
				names = append(names, candidate.Name)
			}
			if len(names)+len(tc.wantVirtual) != 0 && !reflect.DeepEqual(names, tc.wantVirtual) {
				t.Fatalf("virtual = %v; want %v", names, tc.wantVirtual)
			}
			if got.Ambiguous() != tc.ambiguous {
				t.Fatalf("Ambiguous() = %v; want %v", got.Ambiguous(), tc.ambiguous)
			}
		})
	}
}

func TestReportKeepsEveryCandidate(t *testing.T) {
	candidates := []Interface{i("enp7s0", "192.168.1.120"), i("br-060d8248859d", "172.18.0.1")}
	got := report(candidates, netip.Addr{})
	if !reflect.DeepEqual(got.Candidates, candidates) {
		t.Fatalf("Candidates = %v; want %v", got.Candidates, candidates)
	}
	if !got.LAN.IsValid() {
		return
	}
	t.Fatal("missing LAN must stay invalid")
}

func TestLanAddress(t *testing.T) {
	want := netip.MustParseAddr("192.168.1.120")
	if got := lanAddress(func() (netip.Addr, error) { return want, nil }); got != want {
		t.Fatalf("lanAddress = %v; want %v", got, want)
	}
	if got := lanAddress(func() (netip.Addr, error) { return netip.Addr{}, errors.New("no route") }); got.IsValid() {
		t.Fatalf("lanAddress on error = %v; want invalid", got)
	}
}

func TestPrefixAddr(t *testing.T) {
	got, ok := prefixAddr(&net.IPNet{IP: net.ParseIP("192.168.1.120")})
	if !ok || got != netip.MustParseAddr("192.168.1.120") {
		t.Fatalf("IPNet = %v, %v", got, ok)
	}
	got, ok = prefixAddr(&net.IPAddr{IP: net.ParseIP("::ffff:192.168.1.120")})
	if !ok || got.String() != "192.168.1.120" {
		t.Fatalf("mapped IPAddr = %v, %v", got, ok)
	}
	if _, ok := prefixAddr(stubStringer{}); ok {
		t.Fatal("unexpected address type accepted")
	}
}

type stubStringer struct{}

func (stubStringer) Network() string { return "stub" }
func (stubStringer) String() string  { return "192.168.1.120" }
