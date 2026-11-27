package firewall

import (
	"context"
	"strconv"
	"strings"
)

const (
	// windowsProfilesScript imprime una línea "perfil=estado" por perfil.
	windowsProfilesScript = "Get-NetFirewallProfile | ForEach-Object { $_.Name + '=' + $_.Enabled }"
	windowsRuleName       = "Pulso (Soloist)"
)

// inspectWindows consulta los perfiles del Firewall de Defender. Vive sin
// etiqueta de compilación para que la suite pueda ejercitarlo con un Runner
// inyectado desde cualquier sistema, sin ejecutar nada real.
func inspectWindows(ctx context.Context, opts Options) Plan {
	out, err := opts.Runner(ctx, "powershell", "-NoProfile", "-Command", windowsProfilesScript)
	if err != nil {
		return unverifiablePlan(BackendWindows, "")
	}
	enabled, known := parseWindowsProfiles(out)
	if !known {
		return unverifiablePlan(BackendWindows, "")
	}
	if !enabled {
		return inactivePlan()
	}

	plan := Plan{Backend: BackendWindows, Findings: []Finding{
		{Kind: Active, Backend: BackendWindows},
		{Kind: MDNSMissing, Backend: BackendWindows, Port: 5353, Path: windowsRuleName},
		{Kind: ServiceMissing, Backend: BackendWindows, Port: opts.Port, Path: windowsRuleName},
	}}
	plan.addCommand(MDNSMissing, netshRule("UDP", 5353)...)
	if opts.Port > 0 {
		plan.addCommand(ServiceMissing, netshRule("TCP", opts.Port)...)
	}
	return plan
}

// parseWindowsProfiles interpreta las líneas "perfil=True|False". known es
// falso cuando ninguna línea trae un estado reconocible.
func parseWindowsProfiles(data []byte) (enabled, known bool) {
	for _, line := range splitLines(data) {
		_, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		switch strings.ToLower(strings.TrimSpace(value)) {
		case "true", "1", "yes", "enabled":
			enabled = true
			known = true
		case "false", "0", "no", "disabled":
			known = true
		}
	}
	return enabled, known
}

// netshRule arma una regla entrante que solo añade una excepción. El nombre con
// espacios y paréntesis viaja como un único argumento, sin comillas de shell.
func netshRule(protocol string, port int) []string {
	return []string{
		"netsh", "advfirewall", "firewall", "add", "rule",
		"name=" + windowsRuleName,
		"dir=in",
		"action=allow",
		"protocol=" + protocol,
		"localport=" + strconv.Itoa(port),
	}
}
