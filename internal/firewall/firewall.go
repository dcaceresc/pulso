// Package firewall detecta el estado del cortafuegos del equipo y propone los
// comandos que una persona puede ejecutar. Nunca ejecuta esos comandos, nunca
// modifica reglas y nunca necesita privilegios de administrador.
package firewall

import (
	"context"
	"net"
	"net/netip"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// Kind identifica un hallazgo. No contiene texto para el usuario: la interfaz
// lo traduce.
type Kind uint8

const (
	Inactive       Kind = iota // no se encontró un cortafuegos activo
	Active                     // hay un cortafuegos activo
	MDNSMissing                // el descubrimiento (UDP 5353) no está permitido
	ServiceMissing             // el tráfico de la LAN hacia este equipo no está permitido
	Unverifiable               // hay un cortafuegos activo pero sus reglas no se pueden leer
)

// Backend identifica la implementación del cortafuegos.
type Backend uint8

const (
	BackendNone Backend = iota
	BackendUFW
	BackendFirewalld
	BackendNFTables
	BackendWindows
	BackendUnknown
)

// Finding es un hallazgo del diagnóstico.
type Finding struct {
	Kind    Kind
	Backend Backend
	Port    int    // 0 cuando el hallazgo no se refiere a un puerto
	Path    string // archivo o nombre de regla implicado; vacío cuando no aplica
}

// Command es un paso ejecutable que se muestra a la persona y solo se ejecuta
// después de un consentimiento explícito.
type Command struct {
	Argv    []string // nunca una cadena de shell
	Finding Kind     // hallazgo que resuelve este comando
}

// Plan es el resultado completo del diagnóstico.
type Plan struct {
	Backend  Backend
	Findings []Finding
	Commands []Command
}

// Runner ejecuta un proceso. Las pruebas inyectan uno falso para no tocar el
// cortafuegos real.
type Runner func(ctx context.Context, name string, args ...string) ([]byte, error)

// Options ajusta el diagnóstico.
type Options struct {
	Root   string       // raíz del sistema de archivos para las reglas; "" significa "/"
	Runner Runner       // "" significa el ejecutor real de exec.CommandContext
	Prefix netip.Prefix // prefijo de la LAN; si no es válido se enmascara la dirección a /24
	Port   int          // puerto TCP de Soloist Connect cuando se conoce; 0 si no
}

// Inspect describe el cortafuegos del equipo y propone los comandos que faltan.
func Inspect(ctx context.Context, opts Options) Plan {
	return inspectPlatform(ctx, opts.withDefaults())
}

func (o Options) withDefaults() Options {
	if o.Root == "" {
		o.Root = "/"
	}
	if o.Runner == nil {
		o.Runner = execRunner
	}
	if o.Port < 0 {
		o.Port = 0
	}
	return o
}

func execRunner(ctx context.Context, name string, args ...string) ([]byte, error) {
	return exec.CommandContext(ctx, name, args...).Output()
}

// readRoot lee un archivo de reglas relativo a la raíz configurada.
func readRoot(root, name string) ([]byte, error) {
	return os.ReadFile(filepath.Join(root, filepath.FromSlash(strings.TrimPrefix(name, "/"))))
}

// splitLines separa un archivo de reglas en líneas no vacías.
func splitLines(data []byte) []string {
	var lines []string
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			lines = append(lines, line)
		}
	}
	return lines
}

// inactivePlan devuelve el único hallazgo posible cuando no hay cortafuegos.
func inactivePlan() Plan {
	return Plan{Backend: BackendNone, Findings: []Finding{{Kind: Inactive}}}
}

// unverifiablePlan devuelve el hallazgo de un cortafuegos activo cuyas reglas
// no se pueden leer.
func unverifiablePlan(backend Backend, path string) Plan {
	return Plan{
		Backend:  backend,
		Findings: []Finding{{Kind: Unverifiable, Backend: backend, Path: path}},
	}
}

// lanPrefix devuelve el prefijo de la LAN para las reglas propuestas. Si no se
// conoce, usa la dirección de salida enmascarada a /24.
func lanPrefix(given netip.Prefix, fallback func() netip.Addr) netip.Prefix {
	if given.IsValid() {
		return given.Masked()
	}
	addr := fallback()
	if !addr.IsValid() {
		return netip.Prefix{}
	}
	return netip.PrefixFrom(addr, 24).Masked()
}

// localAddress obtiene la dirección de salida sin enviar paquetes: el núcleo
// elige la ruta hacia el bloque TEST-NET-1.
func localAddress() netip.Addr {
	conn, err := net.Dial("udp4", "192.0.2.1:9")
	if err != nil {
		return netip.Addr{}
	}
	defer func() { _ = conn.Close() }()
	local, ok := conn.LocalAddr().(*net.UDPAddr)
	if !ok {
		return netip.Addr{}
	}
	return local.AddrPort().Addr().Unmap()
}

func (p *Plan) addCommand(kind Kind, argv ...string) {
	p.Commands = append(p.Commands, Command{Argv: append([]string(nil), argv...), Finding: kind})
}
