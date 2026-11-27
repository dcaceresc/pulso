package firewall

import (
	"context"
	"strings"
)

// La dirección se proporciona como IPv4 validada. ActiveStore contiene la
// política resultante, no solo las preferencias locales del administrador.
const windowsPolicyScript = `
$ErrorActionPreference = 'Stop'
$ip = '%s'
$interfaces = @(Get-NetIPAddress -AddressFamily IPv4 -IPAddress $ip)
if ($interfaces.Count -ne 1) { return }
$nic = $interfaces[0]
if (-not $nic.InterfaceAlias -or -not $nic.InterfaceIndex) { return }
$networks = @(Get-NetConnectionProfile -InterfaceIndex $nic.InterfaceIndex)
if ($networks.Count -ne 1) { return }
switch ([string]$networks[0].NetworkCategory) {
    'DomainAuthenticated' { $name = 'Domain' }
    'Private' { $name = 'Private' }
    'Public' { $name = 'Public' }
    default { return }
}
$profiles = @(Get-NetFirewallProfile -PolicyStore ActiveStore -Name $name)
if ($profiles.Count -ne 1) { return }
$profile = $profiles[0]
switch ([string]$profile.Enabled) {
    'False' { 'inactive'; return }
    'True' {}
    default { return }
}
foreach ($alias in $profile.DisabledInterfaceAliases) {
    if ($alias -eq 'NotConfigured') { continue }
    if ($nic.InterfaceAlias -eq $alias) { 'inactive'; return }
    if ($nic.InterfaceAlias -like $alias) { return }
}
if ([string]$profile.AllowInboundRules -eq 'False' -and
    [string]$profile.DefaultInboundAction -eq 'Block' -and
    $null -ne $profile.DisabledInterfaceAliases) {
    'inbound-blocked'; return
}
'active'
`

// Solo un bloqueo general de tráfico entrante no solicitado permite concluir
// sin conocer las reglas de cada aplicación. Una excepción nueva no lo elude.
func inspectWindows(ctx context.Context, opts Options) Plan {
	var state string
	if opts.LocalAddr.Is4() {
		script := strings.Replace(windowsPolicyScript, "%s", opts.LocalAddr.String(), 1)
		out, err := opts.Runner(ctx, "powershell", "-NoProfile", "-NonInteractive", "-Command", script)
		if err == nil {
			state = strings.TrimSpace(string(out))
		}
	}
	status, udp, tcp := Unverifiable, MDNSUnverified, ServiceUnverified
	port := opts.Port
	if port < 1 || port > 65535 {
		port = 0
	}
	switch state {
	case "inactive":
		status = Inactive
	case "active":
		status = Active
	case "inbound-blocked":
		status, udp = Active, MDNSMissing
		if port != 0 {
			tcp = ServiceMissing
		}
	}
	return Plan{Backend: BackendWindows, Findings: []Finding{
		{Kind: status, Backend: BackendWindows},
		{Kind: udp, Backend: BackendWindows, Port: 5353},
		{Kind: tcp, Backend: BackendWindows, Port: port},
	}}
}
