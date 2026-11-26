package soloist

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"
	"unicode"

	"github.com/charmbracelet/x/ansi"
	"github.com/coder/websocket"
)

const (
	probeTimeout = time.Second
	markerLimit  = 128
	eventLimit   = 4 << 10
)

var (
	// ErrRuntime informa de un runtime de Soloist inalcanzable o que no sigue
	// el contrato documentado de marcadores y handshake.
	ErrRuntime = errors.New("Soloist runtime is unreachable or invalid. Run pulso doctor.")
	// ErrNotListening es el mismo diagnóstico; el emparejamiento puede distinguir un socket rechazado.
	ErrNotListening = errors.New(ErrRuntime.Error())
)

// Status es el estado local de Soloist que necesita el asistente de
// configuración. Nunca describe una cuenta remota: solo los marcadores del
// daemon y el primer evento de autenticación.
type Status uint8

const (
	StatusMissing Status = iota
	StatusNeedsKey
	StatusAwaitPairing
	StatusReady
)

// Inspect clasifica el directorio de datos sin iniciar Soloist.
func Inspect(ctx context.Context, dir string) (Status, error) {
	endpoint, err := runtimeEndpoint(dir)
	if err != nil || endpoint == "" {
		return StatusNeedsKey, err
	}
	return authState(ctx, endpoint)
}

// runtimeEndpoint lee los marcadores ws.addr y ws.port del daemon. Un endpoint
// vacío sin error significa que no hay ningún daemon en ejecución.
func runtimeEndpoint(dir string) (string, error) {
	addr, hasAddr, addrErr := readMarker(filepath.Join(dir, "ws.addr"))
	port, hasPort, portErr := readMarker(filepath.Join(dir, "ws.port"))
	if addrErr != nil || portErr != nil || hasAddr != hasPort {
		return "", ErrRuntime
	}
	if !hasAddr {
		for _, name := range []string{"pid", "soloist.pid"} {
			if _, err := os.Lstat(filepath.Join(dir, name)); !os.IsNotExist(err) {
				return "", ErrRuntime
			}
		}
		return "", nil
	}
	ip, ipErr := netip.ParseAddr(addr)
	number, portErr := strconv.ParseUint(port, 10, 16)
	if ipErr != nil || !ip.IsLoopback() || ip.Zone() != "" || portErr != nil || number == 0 {
		return "", ErrRuntime
	}
	endpoint := url.URL{Scheme: "ws", Host: net.JoinHostPort(ip.String(), strconv.FormatUint(number, 10)), Path: "/"}
	return endpoint.String(), nil
}

// readMarker lee un único marcador regular y acotado. El bool indica si el
// marcador existía.
func readMarker(path string) (string, bool, error) {
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return "", false, nil
	}
	if err != nil || !info.Mode().IsRegular() || info.Size() > markerLimit {
		return "", true, ErrRuntime
	}
	file, err := os.Open(path)
	if err != nil {
		return "", true, ErrRuntime
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, markerLimit+1))
	if err != nil || len(data) > markerLimit {
		return "", true, ErrRuntime
	}
	return strings.TrimSpace(string(data)), true, nil
}

type authEvent struct {
	Type       string          `json:"type"`
	LoggedIn   *bool           `json:"logged_in"`
	DeviceName json.RawMessage `json:"device_name"`
}

func readAuthEvent(ctx context.Context, conn *websocket.Conn) (authEvent, error) {
	kind, data, err := conn.Read(ctx)
	var message authEvent
	if err != nil || kind != websocket.MessageText || json.Unmarshal(data, &message) != nil {
		return authEvent{}, ErrRuntime
	}
	return message, nil
}

// openAuth acota juntos el handshake y el primer evento. Si funciona, quien
// llama posee un socket vivo cuyas lecturas posteriores usan su contexto.
func openAuth(ctx context.Context, endpoint string) (*websocket.Conn, authEvent, error) {
	ctx, cancel := context.WithTimeout(ctx, probeTimeout)
	defer cancel()
	transport := &http.Transport{MaxResponseHeaderBytes: 8 << 10, DisableCompression: true}
	defer transport.CloseIdleConnections()
	client := &http.Client{
		Transport:     transport, // No proxy lookup, including from the environment.
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	conn, _, err := websocket.Dial(ctx, endpoint, &websocket.DialOptions{HTTPClient: client})
	if err != nil {
		const wsaConnRefused = syscall.Errno(10061)
		if errors.Is(err, syscall.ECONNREFUSED) || runtime.GOOS == "windows" && errors.Is(err, wsaConnRefused) {
			return nil, authEvent{}, ErrNotListening
		}
		return nil, authEvent{}, ErrRuntime
	}
	conn.SetReadLimit(eventLimit)
	message, err := readAuthEvent(ctx, conn)
	if err != nil || message.Type != "auth_state" || message.LoggedIn == nil {
		_ = conn.CloseNow()
		return nil, authEvent{}, ErrRuntime
	}
	return conn, message, nil
}

func authState(ctx context.Context, endpoint string) (Status, error) {
	conn, message, err := openAuth(ctx, endpoint)
	if err != nil {
		return StatusNeedsKey, err
	}
	defer conn.CloseNow()
	if *message.LoggedIn {
		return StatusReady, nil
	}
	return StatusAwaitPairing, nil
}

// WatchPairing mantiene un socket hasta que un auth_state confirme el inicio de
// sesión, falle el socket o se cancele el contexto de emparejamiento. Nunca
// envía comandos a Soloist. report recibe el nombre de dispositivo saneado que
// anuncia Soloist.
func WatchPairing(ctx context.Context, dir string, report func(string) bool) error {
	endpoint, err := runtimeEndpoint(dir)
	if err != nil || endpoint == "" {
		return ErrRuntime
	}
	conn, event, err := openAuth(ctx, endpoint)
	if err != nil {
		return err
	}
	defer conn.CloseNow()

	var name string
	waiting := false
	for {
		switch event.Type {
		case "auth_state", "logout", "logged_out":
			if event.Type == "auth_state" {
				if event.LoggedIn == nil {
					return ErrRuntime
				}
				if *event.LoggedIn {
					return nil
				}
			}
			var advertised string
			if json.Unmarshal(event.DeviceName, &advertised) == nil {
				advertised = SanitizeText(advertised)
			}
			if !waiting || (advertised != "" && advertised != name) {
				name, waiting = advertised, true
				if !report(name) {
					return ctx.Err()
				}
			}
		}
		event, err = readAuthEvent(ctx, conn)
		if err != nil {
			return err
		}
	}
}

// SanitizeText elimina secuencias de escape de terminal y caracteres de control
// y colapsa los espacios. Los nombres de dispositivo y los metadatos son datos
// de visualización, nunca sintaxis de terminal.
func SanitizeText(text string) string {
	return strings.Join(strings.Fields(strings.Map(func(r rune) rune {
		switch {
		case unicode.IsSpace(r):
			return ' '
		case unicode.IsControl(r), unicode.Is(unicode.Cf, r) && r != '\u200d':
			return -1
		default:
			return r
		}
	}, ansi.Strip(text))), " ")
}
