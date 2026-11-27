//go:build linux

package firewall

import (
	"context"
	"net/netip"
	"strconv"
	"strings"
)

const (
	ufwConfigPath  = "/etc/ufw/ufw.conf"
	ufwBeforeRules = "/etc/ufw/before.rules"
	ufwUserRules   = "/etc/ufw/user.rules"
	nftablesConfig = "/etc/nftables.conf"
)

// inspectPlatform delega en la detección de Linux.
func inspectPlatform(ctx context.Context, opts Options) Plan {
	return inspectLinux(ctx, opts)
}

// inspectLinux elige el backend activo. ufw tiene prioridad porque es el que
// suele administrar las reglas cuando está habilitado.
func inspectLinux(ctx context.Context, opts Options) Plan {
	if plan, ok := inspectUFW(ctx, opts); ok {
		return plan
	}
	if plan, ok := inspectFirewalld(ctx, opts); ok {
		return plan
	}
	if plan, ok := inspectNFTables(ctx, opts); ok {
		return plan
	}
	return inactivePlan()
}

// serviceActive consulta systemd sin exigir privilegios.
func serviceActive(ctx context.Context, opts Options, name string) bool {
	out, _ := opts.Runner(ctx, "systemctl", "is-active", name)
	return strings.TrimSpace(string(out)) == "active"
}

func inspectUFW(ctx context.Context, opts Options) (Plan, bool) {
	if !serviceActive(ctx, opts, "ufw") && !ufwEnabledFromFile(opts.Root) {
		return Plan{}, false
	}

	lines, readable := readRules(opts.Root, ufwBeforeRules, ufwUserRules)
	if !readable {
		return unverifiablePlan(BackendUFW, ufwBeforeRules), true
	}

	plan := Plan{Backend: BackendUFW, Findings: []Finding{{Kind: Active, Backend: BackendUFW}}}
	prefix := lanPrefix(opts.Prefix, localAddress)

	if !anyRule(lines, mdnsRule) {
		plan.Findings = append(plan.Findings, Finding{
			Kind:    MDNSMissing,
			Backend: BackendUFW,
			Port:    5353,
			Path:    ufwBeforeRules,
		})
		if prefix.IsValid() {
			plan.addCommand(MDNSMissing,
				"sudo", "ufw", "allow", "from", prefix.String(),
				"to", "any", "port", "5353", "proto", "udp",
			)
		}
	}

	if !ufwAllowsLAN(lines, prefix, opts.Port) {
		plan.Findings = append(plan.Findings, Finding{
			Kind:    ServiceMissing,
			Backend: BackendUFW,
			Port:    opts.Port,
			Path:    ufwUserRules,
		})
		if prefix.IsValid() {
			plan.addCommand(ServiceMissing, "sudo", "ufw", "allow", "from", prefix.String())
		}
	}

	return plan, true
}

// ufwEnabledFromFile lee el indicador persistente de ufw.
func ufwEnabledFromFile(root string) bool {
	data, err := readRoot(root, ufwConfigPath)
	if err != nil {
		return false
	}
	for _, line := range splitLines(data) {
		key, value, ok := strings.Cut(line, "=")
		if ok && strings.TrimSpace(key) == "ENABLED" && strings.EqualFold(strings.TrimSpace(value), "yes") {
			return true
		}
	}
	return false
}

// readRules acumula las líneas de los archivos que se hayan podido leer.
func readRules(root string, names ...string) ([]string, bool) {
	var lines []string
	readable := false
	for _, name := range names {
		data, err := readRoot(root, name)
		if err != nil {
			continue
		}
		readable = true
		lines = append(lines, splitLines(data)...)
	}
	return lines, readable
}

func anyRule(lines []string, match func([]string) bool) bool {
	for _, line := range lines {
		fields := ruleFields(line)
		if len(fields) > 0 && match(fields) {
			return true
		}
	}
	return false
}

// ruleFields descarta los comentarios de una línea de reglas antes de
// analizarla.
func ruleFields(line string) []string {
	if index := strings.IndexByte(line, '#'); index >= 0 {
		line = line[:index]
	}
	return strings.Fields(line)
}

// mdnsRule reconoce una regla que acepta el descubrimiento por UDP 5353.
func mdnsRule(fields []string) bool {
	return allows(fields) && hasUDP(fields) && hasPort(fields, 5353)
}

func allows(fields []string) bool {
	for _, field := range fields {
		if strings.EqualFold(field, "accept") {
			return true
		}
	}
	return false
}

func hasToken(fields []string, token string) bool {
	for _, field := range fields {
		if strings.EqualFold(field, token) {
			return true
		}
	}
	return false
}

func hasUDP(fields []string) bool { return hasToken(fields, "udp") }

func hasTCP(fields []string) bool { return hasToken(fields, "tcp") }

// hasProto detecta cualquier protocolo explícito, no solo TCP o UDP.
func hasProto(fields []string) bool {
	if hasTCP(fields) || hasUDP(fields) {
		return true
	}
	for i, field := range fields {
		lower := strings.ToLower(field)
		if lower == "-p" || lower == "--protocol" {
			return i+1 < len(fields)
		}
		if strings.HasPrefix(lower, "--protocol=") {
			return true
		}
	}
	return false
}

// hasDport informa si la regla restringe el puerto de destino.
// hasDport detecta cualquier restricción de puerto de destino, incluida la
// forma multiport (--dports), para no confundir una regla concreta con un
// permiso para cualquier puerto.
func hasDport(fields []string) bool {
	for _, field := range fields {
		lower := strings.ToLower(field)
		switch {
		case lower == "--dport", lower == "dport", lower == "--dports", lower == "dports":
			return true
		case strings.HasPrefix(lower, "--dport="), strings.HasPrefix(lower, "dport="),
			strings.HasPrefix(lower, "--dports="), strings.HasPrefix(lower, "dports="):
			return true
		}
	}
	return false
}

// hasPort compara el puerto de destino con el valor pedido.
func hasPort(fields []string, port int) bool {
	want := strconv.Itoa(port)
	for i, field := range fields {
		lower := strings.ToLower(field)
		switch {
		case lower == "--dport", lower == "dport":
			if i+1 < len(fields) && strings.TrimSpace(fields[i+1]) == want {
				return true
			}
		case strings.HasPrefix(lower, "--dport="):
			if strings.TrimPrefix(lower, "--dport=") == want {
				return true
			}
		case strings.HasPrefix(lower, "dport="):
			if strings.TrimPrefix(lower, "dport=") == want {
				return true
			}
		}
	}
	return false
}

func hasConntrack(fields []string) bool {
	for _, field := range fields {
		switch strings.ToLower(field) {
		case "conntrack", "ctstate", "state":
			return true
		}
	}
	return false
}

// sourcePrefix lee el origen de una regla cuando lo declara.
func sourcePrefix(fields []string) (netip.Prefix, bool) {
	for i, field := range fields {
		lower := strings.ToLower(field)
		var value string
		switch {
		case lower == "-s", lower == "--source":
			if i+1 < len(fields) {
				value = fields[i+1]
			}
		case strings.HasPrefix(lower, "--source="):
			value = field[len("--source="):]
		case strings.HasPrefix(lower, "-s="):
			value = field[len("-s="):]
		}
		if value == "" {
			continue
		}
		prefix, err := netip.ParsePrefix(value)
		if err != nil {
			addr, addrErr := netip.ParseAddr(value)
			if addrErr != nil {
				continue
			}
			prefix = netip.PrefixFrom(addr, addr.BitLen())
		}
		return prefix.Masked(), true
	}
	return netip.Prefix{}, false
}

// ufwAllowsLAN decide si alguna regla acepta tráfico de la LAN hacia el puerto
// de control de Soloist. Las reglas con estado no cuentan: solo reflejan
// conexiones ya establecidas.
// hasIface detecta reglas atadas a una interfaz concreta, como -i lo.
func hasIface(fields []string) bool {
	for _, field := range fields {
		lower := strings.ToLower(field)
		if lower == "-i" || lower == "--in-interface" || strings.HasPrefix(lower, "--in-interface=") {
			return true
		}
	}
	return false
}

// inputChain informa si la regla pertenece a una cadena de entrada. Las demás
// cadenas, como ufw-user-limit-accept, solo reciben tráfico que ya pasó otro
// filtro y no abren puertos por sí solas.
func inputChain(fields []string) bool {
	for i, field := range fields {
		if !strings.EqualFold(field, "-A") && !strings.EqualFold(field, "--append") {
			continue
		}
		if i+1 >= len(fields) {
			return false
		}
		switch fields[i+1] {
		case "ufw-user-input", "ufw-before-input", "ufw-after-input":
			return true
		}
		return false
	}
	return false
}

func ufwAllowsLAN(lines []string, prefix netip.Prefix, port int) bool {
	for _, line := range lines {
		fields := ruleFields(line)
		if len(fields) == 0 {
			continue
		}
		if !inputChain(fields) {
			continue
		}
		if !allows(fields) || hasConntrack(fields) {
			continue
		}
		source, hasSource := sourcePrefix(fields)
		sourceCovers := hasSource && prefix.IsValid() && source.Overlaps(prefix)

		if port > 0 && hasProto(fields) && !hasTCP(fields) {
			continue
		}
		if port > 0 && hasPort(fields, port) {
			if !hasSource || sourceCovers {
				return true
			}
			continue
		}
		if hasDport(fields) {
			continue
		}
		// Una regla atada a una interfaz o a un protocolo distinto de TCP no
		// prueba que el puerto de Soloist sea alcanzable desde la red local.
		if hasIface(fields) || hasProto(fields) && !hasTCP(fields) {
			continue
		}
		if sourceCovers {
			return true
		}
		if hasSource {
			continue
		}
		if port > 0 {
			return !hasProto(fields) || hasTCP(fields)
		}
		return true
	}
	return false
}

func inspectFirewalld(ctx context.Context, opts Options) (Plan, bool) {
	if !serviceActive(ctx, opts, "firewalld") {
		return Plan{}, false
	}
	out, err := opts.Runner(ctx, "firewall-cmd", "--list-all")
	if err != nil {
		return unverifiablePlan(BackendFirewalld, ""), true
	}

	text := string(out)
	plan := Plan{Backend: BackendFirewalld, Findings: []Finding{{Kind: Active, Backend: BackendFirewalld}}}
	if !strings.Contains(text, "5353/udp") && !serviceListed(text, "mdns") {
		plan.Findings = append(plan.Findings, Finding{
			Kind:    MDNSMissing,
			Backend: BackendFirewalld,
			Port:    5353,
		})
	}
	if opts.Port > 0 && !strings.Contains(text, strconv.Itoa(opts.Port)+"/tcp") {
		plan.Findings = append(plan.Findings, Finding{
			Kind:    ServiceMissing,
			Backend: BackendFirewalld,
			Port:    opts.Port,
		})
	}
	return plan, true
}

// serviceListed busca un servicio dentro de la línea "services:" de
// firewall-cmd --list-all.
func serviceListed(text, service string) bool {
	for _, line := range splitLines([]byte(text)) {
		key, value, ok := strings.Cut(line, ":")
		if !ok || strings.TrimSpace(strings.ToLower(key)) != "services" {
			continue
		}
		for _, name := range strings.Fields(value) {
			if name == service {
				return true
			}
		}
	}
	return false
}

func inspectNFTables(ctx context.Context, opts Options) (Plan, bool) {
	if !serviceActive(ctx, opts, "nftables") {
		return Plan{}, false
	}
	data, err := readRoot(opts.Root, nftablesConfig)
	if err != nil {
		return unverifiablePlan(BackendNFTables, nftablesConfig), true
	}

	lines := splitLines(data)
	plan := Plan{Backend: BackendNFTables, Findings: []Finding{{Kind: Active, Backend: BackendNFTables}}}
	if !anyRule(lines, mdnsRule) {
		plan.Findings = append(plan.Findings, Finding{
			Kind:    MDNSMissing,
			Backend: BackendNFTables,
			Port:    5353,
			Path:    nftablesConfig,
		})
	}
	if opts.Port > 0 && !anyRule(lines, func(fields []string) bool {
		return allows(fields) && hasTCP(fields) && hasPort(fields, opts.Port)
	}) {
		plan.Findings = append(plan.Findings, Finding{
			Kind:    ServiceMissing,
			Backend: BackendNFTables,
			Port:    opts.Port,
			Path:    nftablesConfig,
		})
	}
	return plan, true
}
