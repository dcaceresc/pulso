package app

import (
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/dcaceresc/pulso/internal/config"
	"github.com/dcaceresc/pulso/internal/soloist"
	"github.com/dcaceresc/pulso/internal/spotify"
	"github.com/dcaceresc/pulso/internal/ui"
)

const (
	soloistStartupTimeout = 10 * time.Second
	soloistStartupPoll    = 50 * time.Millisecond
)

var (
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
	// child es el daemon que arrancó esta sesión. Volver por una clave nueva lo
	// reemplaza, porque Soloist conserva la clave con la que se inició.
	child     *exec.Cmd
	childDone chan struct{}
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
	model := ui.NewSetupModelWithPreferences(ctx, ui.SetupService{
		Inspect: local.inspect,
		Watch:   local.watchPairing,
		Install: installManagedSoloist,
		Start:   local.start,
		Authorize: func(ctx context.Context, clientID string, showURL func(string)) (ui.CatalogAPI, error) {
			return authorizeSpotifyCatalog(ctx, clientID, openAndShow(showURL))
		},
		ClientID: firstNonEmpty(os.Getenv("PULSO_SPOTIFY_CLIENT_ID"), spotify.LoadClientID()),
		// A saved session means the Spotify step never appears; any failure
		// falls through to the interactive flow.
		Restore: restoreSpotifyCatalog,
		PlayerWithPreferences: func(width, height int, p config.Preferences, api ui.CatalogAPI) tea.Model {
			states, commands := soloist.NewStream(ctx, local.dataDir)
			return ui.NewPlayerModelWithCatalog(ctx, states, commands, width, height, p, config.Save, api,
				func(ctx context.Context, clientID string, showURL func(string)) (ui.CatalogAPI, error) {
					return authorizeSpotifyCatalog(ctx, clientID, openAndShow(showURL))
				}, firstNonEmpty(os.Getenv("PULSO_SPOTIFY_CLIENT_ID"), spotify.LoadClientID()))
		},
	}, config.Load(), config.Save)
	_, err := tea.NewProgram(model, tea.WithOutput(stdout), tea.WithAltScreen()).Run()
	if err == nil {
		soloist.Pause(context.Background(), local.dataDir)
	}
	return err
}

// authorizeSpotifyCatalog adapta el flujo OAuth al punto de unión de la UI sin
// entregar nunca al reproductor un catálogo nil con tipo.
func authorizeSpotifyCatalog(ctx context.Context, clientID string, showURL func(string)) (ui.CatalogAPI, error) {
	catalog, err := spotify.Authorize(ctx, clientID, showURL)
	if catalog == nil {
		return nil, err
	}
	return catalog, err
}

// restoreSpotifyCatalog adapta la sesión guardada al hook del asistente. La
// variable de entorno tiene prioridad, igual que en el flujo interactivo.
func restoreSpotifyCatalog(ctx context.Context) (ui.CatalogAPI, bool) {
	catalog, ok := spotify.Restore(ctx, firstNonEmpty(os.Getenv("PULSO_SPOTIFY_CLIENT_ID"), spotify.LoadClientID()))
	if !ok || catalog == nil {
		return nil, false
	}
	return catalog, true
}

func installManagedSoloist(ctx context.Context) error {
	path, err := managedSoloistPath()
	if err != nil {
		return err
	}
	return soloist.Install(ctx, path)
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

func (s *localSetup) inspect(ctx context.Context) (ui.SetupState, error) {
	dir, err := s.dataDir()
	if err != nil {
		return ui.SetupMissing, err
	}
	status, err := soloist.Inspect(ctx, dir)
	state := setupStateOf(status)
	if err != nil || state != ui.SetupNeedsKey {
		return state, err
	}
	path, err := s.find()
	if err != nil {
		return ui.SetupMissing, errSoloistDiscovery
	}
	if path == "" {
		return ui.SetupMissing, nil
	}
	return ui.SetupNeedsKey, nil
}

// setupStateOf traduce el estado del runtime de Soloist al checklist del
// asistente.
func setupStateOf(status soloist.Status) ui.SetupState {
	switch status {
	case soloist.StatusReady:
		return ui.SetupReady
	case soloist.StatusAwaitPairing:
		return ui.SetupAwaitPairing
	case soloist.StatusNeedsKey:
		return ui.SetupNeedsKey
	default:
		return ui.SetupMissing
	}
}

// watchPairing mantiene el socket de emparejamiento del asistente. El protocolo
// vive con el daemon de Soloist; esto solo aporta su directorio de datos.
func (s *localSetup) watchPairing(ctx context.Context, report func(string) bool) error {
	dir, err := s.dataDir()
	if err != nil {
		return err
	}
	return soloist.WatchPairing(ctx, dir, report)
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
	// Una clave nueva necesita un proceso nuevo: Soloist conserva la clave con la
	// que se inició. La instancia detenida deja marcadores obsoletos, así que la
	// reinspección no puede vetar el reemplazo que acaba de hacer necesario.
	restarting := s.stopChildLocked()
	if !restarting {
		// Inspect again after key entry: Soloist might have started in the meantime.
		if status, err := soloist.Inspect(ctx, dir); err != nil || status != soloist.StatusNeedsKey {
			return err
		}
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
	done := make(chan struct{})
	if err := waitForSoloist(ctx, cmd, dir, s.startupTimeout, done); err != nil {
		return err
	}
	s.child, s.childDone = cmd, done
	return nil
}

// stopChildLocked termina el daemon que arrancó esta sesión y espera a que se
// recoja. Indica si había alguno que detener.
func (s *localSetup) stopChildLocked() bool {
	if s.child == nil {
		return false
	}
	_ = s.child.Process.Kill()
	<-s.childDone
	s.child, s.childDone = nil, nil
	return true
}

func waitForSoloist(ctx context.Context, cmd *exec.Cmd, dir string, timeout time.Duration, done chan struct{}) error {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
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
		status, err := soloist.Inspect(ctx, dir)
		select {
		case <-done:
			return errSoloistExited
		default:
		}
		if ctx.Err() != nil {
			return errSoloistStartupTimeout
		}
		if err == nil && status != soloist.StatusNeedsKey {
			ready = true
			return nil
		}
		select {
		case <-ctx.Done():
		case <-ticker.C:
		}
	}
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			return value
		}
	}
	return ""
}

// openAndShow opens the authorization page and still reports the URL in the UI.
func openAndShow(showURL func(string)) func(string) {
	return func(raw string) {
		_ = spotify.OpenBrowser(raw)
		showURL(raw)
	}
}
