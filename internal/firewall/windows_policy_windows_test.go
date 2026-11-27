package firewall

import (
	"context"
	"net/netip"
	"testing"
	"time"
)

func TestWindowsPolicyScriptWithPowerShell(t *testing.T) {
	const cmdlets = `
function Get-NetIPAddress($AddressFamily, $IPAddress) {
    if ($AddressFamily -ne 'IPv4' -or $IPAddress -ne '192.0.2.15') { throw 'Wrong LAN address' }
    [pscustomobject]@{ InterfaceIndex = 9; InterfaceAlias = 'Ethernet [LAN]' }
}
function Get-NetConnectionProfile($InterfaceIndex) {
    if ($InterfaceIndex -ne 9) { throw 'Wrong LAN interface' }
    [pscustomobject]@{ NetworkCategory = 'DomainAuthenticated' }
}
function Get-NetFirewallProfile($PolicyStore, $Name) {
    if ($PolicyStore -ne 'ActiveStore' -or $Name -ne 'Domain') { throw 'Wrong effective profile' }
    [pscustomobject]@{
        Enabled = 'True'; DefaultInboundAction = 'Block';
        AllowInboundRules = $inbound; DisabledInterfaceAliases = $excluded
    }
}
`
	for _, tc := range []struct {
		name, policy string
		status, want Kind
	}{
		{"default block alone is not proof", "$inbound = 'True'; $excluded = 'NotConfigured'", Active, MDNSUnverified},
		{"confirmed blanket block", "$inbound = 'False'; $excluded = @('NotConfigured')", Active, MDNSMissing},
		{"missing exclusions cannot prove a block", "$inbound = 'False'; $excluded = $null", Active, MDNSUnverified},
		{"literal alias with brackets is excluded", "$inbound = 'False'; $excluded = 'Ethernet [LAN]'", Inactive, MDNSUnverified},
		{"ambiguous alias pattern is unverified", "$inbound = 'False'; $excluded = 'Ethernet *'", Unverifiable, MDNSUnverified},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
			defer cancel()
			runner := func(ctx context.Context, name string, args ...string) ([]byte, error) {
				args[len(args)-1] = tc.policy + "\n" + cmdlets + args[len(args)-1]
				out, err := execRunner(ctx, name, args...)
				if err != nil {
					t.Fatalf("PowerShell policy script: %v", err)
				}
				return out, err
			}
			plan := Inspect(ctx, Options{Runner: runner, LocalAddr: netip.MustParseAddr("192.0.2.15")})
			assertPlan(t, plan, Plan{Backend: BackendWindows, Findings: []Finding{
				{Kind: tc.status, Backend: BackendWindows},
				{Kind: tc.want, Backend: BackendWindows, Port: 5353},
				{Kind: ServiceUnverified, Backend: BackendWindows},
			}})
		})
	}
}
