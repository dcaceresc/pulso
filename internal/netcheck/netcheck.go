// Package netcheck describe la red local para explicar por qué un dispositivo
// Spotify Connect puede no aparecer en el teléfono.
package netcheck

import (
	"errors"
	"net"
	"net/netip"
	"strings"
)

// probeAddress pertenece al bloque TEST-NET-1: sirve para que el núcleo elija
// la dirección de salida sin enviar un solo paquete.
const probeAddress = "192.0.2.1:9"

// virtualPrefixes nombra las interfaces que no son la red del usuario: puentes
// de contenedores, VPN y túneles. Soloist elige una única dirección al
// arrancar, así que una de estas puede quedarse con el anuncio.
var virtualPrefixes = []string{
	"br-", "docker", "podman", "veth", "virbr", "vnet", "lxc", "incus",
	"cni", "flannel", "cali",
	"tap", "tun", "wg", "tailscale", "zt",
}

// Interface es una dirección IPv4 activa con el nombre que la publica.
type Interface struct {
	Name    string
	Address netip.Addr
	Virtual bool
}

// Report es el inventario que el asistente muestra durante el emparejamiento.
type Report struct {
	LAN        netip.Addr // dirección con la que el equipo sale a la red
	Candidates []Interface
	Virtual    []Interface
}

// Ambiguous informa si más de una interfaz puede recibir el único anuncio de
// Soloist. Un cero en Virtual no basta: dos redes físicas también compiten.
func (r Report) Ambiguous() bool {
	if len(r.Candidates) < 2 {
		return false
	}
	return true
}

// Inspect describe el equipo real.
func Inspect() (Report, error) {
	candidates, err := interfaces()
	if err != nil {
		return Report{}, err
	}
	return report(candidates, lanAddress(routeCandidate)), nil
}

func report(candidates []Interface, lan netip.Addr) Report {
	result := Report{LAN: lan, Candidates: candidates}
	for _, candidate := range candidates {
		if candidate.Virtual {
			result.Virtual = append(result.Virtual, candidate)
		}
	}
	return result
}

func interfaces() ([]Interface, error) {
	list, err := net.Interfaces()
	if err != nil {
		return nil, err
	}
	var found []Interface
	for _, iface := range list {
		if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, err := iface.Addrs()
		if err != nil {
			continue
		}
		for _, addr := range addrs {
			ip, ok := prefixAddr(addr)
			if !ok || !ip.Is4() || ip.IsLoopback() || ip.IsLinkLocalUnicast() {
				continue
			}
			found = append(found, Interface{Name: iface.Name, Address: ip, Virtual: isVirtual(iface.Name)})
		}
	}
	return found, nil
}

func prefixAddr(addr net.Addr) (netip.Addr, bool) {
	var raw net.IP
	switch value := addr.(type) {
	case *net.IPNet:
		raw = value.IP
	case *net.IPAddr:
		raw = value.IP
	default:
		return netip.Addr{}, false
	}
	parsed, ok := netip.AddrFromSlice(raw)
	if !ok {
		return netip.Addr{}, false
	}
	return parsed.Unmap(), true
}

func isVirtual(name string) bool {
	for _, prefix := range virtualPrefixes {
		if strings.HasPrefix(name, prefix) {
			return true
		}
	}
	return false
}

func lanAddress(dial func() (netip.Addr, error)) netip.Addr {
	addr, err := dial()
	if err != nil {
		return netip.Addr{}
	}
	return addr
}

func routeCandidate() (netip.Addr, error) {
	conn, err := net.Dial("udp4", probeAddress)
	if err != nil {
		return netip.Addr{}, err
	}
	defer func() { _ = conn.Close() }()
	local, ok := conn.LocalAddr().(*net.UDPAddr)
	if !ok {
		return netip.Addr{}, errors.New("unexpected local address")
	}
	return local.AddrPort().Addr().Unmap(), nil
}
