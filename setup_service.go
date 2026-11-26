package main

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
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/coder/websocket"
)

const (
	soloistProbeTimeout   = time.Second
	soloistStartupTimeout = 10 * time.Second
	soloistStartupPoll    = 50 * time.Millisecond
	soloistMarkerLimit    = 128
	soloistEventLimit     = 4 << 10
)

var (
	errSoloistRuntime        = errors.New("Soloist runtime is unreachable or invalid. Run pulso doctor.")
	errSoloistDataDir        = errors.New("Could not locate Soloist's default data directory. Run pulso doctor.")
	errSoloistDiscovery      = errors.New("Could not locate Soloist. Run pulso doctor.")
	errSoloistStart          = errors.New("Could not start Soloist. Run pulso doctor.")
	errSoloistExited         = errors.New("Soloist exited before it was ready. Run pulso doctor.")
	errSoloistStartupTimeout = errors.New("Soloist did not become ready. Run pulso doctor.")
)

type localSetup struct {
	find           func() (string, error)
	dataDir        func() (string, error)
	command        func(string, ...string) *exec.Cmd
	startupTimeout time.Duration
	startMu        sync.Mutex
}

func runSetup(stdout io.Writer) error {
	local := &localSetup{
		find:           findSoloist,
		dataDir:        func() (string, error) { return soloistDataDir(runtime.GOOS, os.Getenv) },
		command:        exec.Command,
		startupTimeout: soloistStartupTimeout,
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer func() {
		cancel()
		// Bubble Tea commands run asynchronously. Let an in-flight startup
		// kill and reap its own child before main exits.
		local.startMu.Lock()
		local.startMu.Unlock()
	}()
	model := newSetupModel(ctx, setupService{
		inspect: local.inspect,
		install: installManagedSoloist,
		start:   local.start,
		player: func(width, height int) tea.Model {
			states, commands := newPlayerStream(ctx, local.dataDir)
			return newPlayerModel(ctx, states, commands, width, height)
		},
	})
	_, err := tea.NewProgram(model, tea.WithOutput(stdout), tea.WithAltScreen()).Run()
	return err
}

func installManagedSoloist(ctx context.Context) error {
	path, err := managedSoloistPath()
	if err != nil {
		return err
	}
	return installSoloist(ctx, path)
}

func soloistDataDir(goos string, getenv func(string) string) (string, error) {
	var dir string
	switch goos {
	case "windows":
		if base := getenv("LOCALAPPDATA"); filepath.IsAbs(base) {
			dir = filepath.Join(base, "soloist", "data")
		}
	case "linux":
		switch {
		case getenv("STATE_DIRECTORY") != "":
			dir = getenv("STATE_DIRECTORY")
		case getenv("XDG_DATA_HOME") != "":
			dir = filepath.Join(getenv("XDG_DATA_HOME"), "soloist")
		case getenv("HOME") != "":
			dir = filepath.Join(getenv("HOME"), ".local", "share", "soloist")
		}
	}
	if !filepath.IsAbs(dir) {
		return "", errSoloistDataDir
	}
	return dir, nil
}

func (s *localSetup) inspect(ctx context.Context) (setupState, error) {
	dir, err := s.dataDir()
	if err != nil {
		return setupMissing, err
	}
	state, err := soloistRuntimeState(ctx, dir)
	if err != nil || state != setupNeedsKey {
		return state, err
	}
	path, err := s.find()
	if err != nil {
		return setupMissing, errSoloistDiscovery
	}
	if path == "" {
		return setupMissing, nil
	}
	return setupNeedsKey, nil
}

func soloistRuntimeState(ctx context.Context, dir string) (setupState, error) {
	addr, hasAddr, addrErr := readSoloistMarker(filepath.Join(dir, "ws.addr"))
	port, hasPort, portErr := readSoloistMarker(filepath.Join(dir, "ws.port"))
	if addrErr != nil || portErr != nil || hasAddr != hasPort {
		return setupNeedsKey, errSoloistRuntime
	}
	if !hasAddr {
		for _, name := range []string{"pid", "soloist.pid"} {
			if _, err := os.Lstat(filepath.Join(dir, name)); !os.IsNotExist(err) {
				return setupNeedsKey, errSoloistRuntime
			}
		}
		return setupNeedsKey, nil
	}
	ip, ipErr := netip.ParseAddr(addr)
	number, portErr := strconv.ParseUint(port, 10, 16)
	if ipErr != nil || !ip.IsLoopback() || ip.Zone() != "" || portErr != nil || number == 0 {
		return setupNeedsKey, errSoloistRuntime
	}
	endpoint := url.URL{Scheme: "ws", Host: net.JoinHostPort(ip.String(), strconv.FormatUint(number, 10)), Path: "/"}
	return soloistAuthState(ctx, endpoint.String())
}

func readSoloistMarker(path string) (string, bool, error) {
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return "", false, nil
	}
	if err != nil || !info.Mode().IsRegular() || info.Size() > soloistMarkerLimit {
		return "", true, errSoloistRuntime
	}
	file, err := os.Open(path)
	if err != nil {
		return "", true, errSoloistRuntime
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, soloistMarkerLimit+1))
	if err != nil || len(data) > soloistMarkerLimit {
		return "", true, errSoloistRuntime
	}
	return strings.TrimSpace(string(data)), true, nil
}

func soloistAuthState(ctx context.Context, endpoint string) (setupState, error) {
	ctx, cancel := context.WithTimeout(ctx, soloistProbeTimeout)
	defer cancel()
	transport := &http.Transport{MaxResponseHeaderBytes: 8 << 10, DisableCompression: true}
	defer transport.CloseIdleConnections()
	client := &http.Client{
		Transport:     transport, // No proxy lookup, including from the environment.
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	conn, _, err := websocket.Dial(ctx, endpoint, &websocket.DialOptions{HTTPClient: client})
	if err != nil {
		return setupNeedsKey, errSoloistRuntime
	}
	defer conn.CloseNow()
	conn.SetReadLimit(soloistEventLimit)
	kind, data, err := conn.Read(ctx)
	var message struct {
		Type     string `json:"type"`
		LoggedIn *bool  `json:"logged_in"`
	}
	if err != nil || kind != websocket.MessageText || json.Unmarshal(data, &message) != nil || message.Type != "auth_state" || message.LoggedIn == nil {
		return setupNeedsKey, errSoloistRuntime
	}
	if *message.LoggedIn {
		return setupReady, nil
	}
	return setupAwaitPairing, nil
}

func (s *localSetup) start(ctx context.Context, key string) error {
	s.startMu.Lock()
	defer s.startMu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	dir, err := s.dataDir()
	if err != nil {
		return err
	}
	// Inspect again after key entry: Soloist might have started in the meantime.
	if state, err := soloistRuntimeState(ctx, dir); err != nil || state != setupNeedsKey {
		return err
	}
	path, err := s.find()
	if err != nil || path == "" {
		return errSoloistDiscovery
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	cmd := s.command(path, "--device-name", "Pulso", "--api-key", key, "--ws", "127.0.0.1:0", "--data-dir", dir)
	// Don't attach the daemon's output to the terminal or include it in errors.
	if err := cmd.Start(); err != nil {
		return errSoloistStart
	}
	return waitForSoloist(ctx, cmd, dir, s.startupTimeout)
}

func waitForSoloist(ctx context.Context, cmd *exec.Cmd, dir string, timeout time.Duration) error {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	done := make(chan struct{})
	go func() {
		_ = cmd.Wait()
		close(done)
		cancel()
	}()
	ready := false
	defer func() {
		if !ready {
			_ = cmd.Process.Kill()
			<-done
		}
	}()
	ticker := time.NewTicker(soloistStartupPoll)
	defer ticker.Stop()
	for {
		state, err := soloistRuntimeState(ctx, dir)
		select {
		case <-done:
			return errSoloistExited
		default:
		}
		if ctx.Err() != nil {
			return errSoloistStartupTimeout
		}
		if err == nil && state != setupNeedsKey {
			ready = true
			return nil
		}
		select {
		case <-ctx.Done():
		case <-ticker.C:
		}
	}
}
