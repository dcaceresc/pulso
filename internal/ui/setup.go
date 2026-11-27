package ui

import (
	"context"
	"errors"
	"io/fs"
	"strings"
	"sync"
	"time"
	"unicode"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/dcaceresc/pulso/internal/config"
	"github.com/dcaceresc/pulso/internal/soloist"
	"github.com/dcaceresc/pulso/internal/spotify"
)

type SetupState uint8

const (
	SetupMissing SetupState = iota
	SetupNeedsKey
	SetupAwaitPairing
	SetupReady
)

type setupSpotifyState uint8

const (
	setupSpotifyClientID setupSpotifyState = iota
	setupSpotifyPending
	setupSpotifyFailed
)

// spotifyRestoreTimeout bounds the silent session restore so a slow or missing
// session falls back to the interactive step quickly.
const spotifyRestoreTimeout = 5 * time.Second

type setupSpotifyMsg struct {
	attempt uint64
	url     string
	api     CatalogAPI
	err     error
	done    bool
}

// setupSpotifyRestored reports whether a saved session was usable. It carries
// no error: any failure simply continues with the interactive step.
type setupSpotifyRestored struct {
	api CatalogAPI
	ok  bool
}

type SetupService struct {
	Inspect               func(context.Context) (SetupState, error)
	Watch                 func(context.Context, func(string) bool) error
	Install               func(context.Context) error
	Start                 func(context.Context, string) error
	Player                func(width, height int) tea.Model
	PlayerWithPreferences func(width, height int, p config.Preferences, api CatalogAPI) tea.Model
	Authorize             func(context.Context, string, func(string)) (CatalogAPI, error)
	Restore               func(context.Context) (CatalogAPI, bool)
	ClientID              string
}

type setupPhase uint8

const (
	setupChecking setupPhase = iota
	setupCheckFailed
	setupConfirmInstall
	setupInstalling
	setupInstallFailed
	setupEnterKey
	setupStarting
	setupStartFailed
	setupPairing
	setupPairingChecking
	setupPairingStopped
	setupRestoringSpotify
	setupDone
	setupSpotify
	setupQuit
)

type setupInspection struct {
	state SetupState
	err   error
}

type setupInstalled uint8

const (
	setupInstallOK setupInstalled = iota
	setupInstallOtherError
	setupInstallPermission
	setupInstallTimeout
)

func setupInstallResult(err error) setupInstalled {
	switch {
	case err == nil:
		return setupInstallOK
	case errors.Is(err, fs.ErrPermission):
		return setupInstallPermission
	case errors.Is(err, context.DeadlineExceeded):
		return setupInstallTimeout
	default:
		return setupInstallOtherError
	}
}

type setupStarted bool

type setupModel struct {
	ctx            context.Context
	service        SetupService
	phase          setupPhase
	progress       SetupState // Last confirmed inspection, retained while rechecking.
	installIssue   setupInstalled
	key            []rune
	pairing        *setupPairingSession
	pairingDone    <-chan struct{}
	pairingState   setupPairingStatus
	deviceName     string
	preferences    preferences
	spotify        setupSpotifyState
	spotifyURL     string
	spotifyAPI     CatalogAPI
	spotifyDone    bool
	spotifyEvents  chan setupSpotifyMsg
	spotifyAttempt uint64
	cancelAuth     context.CancelFunc
	clientID       []rune
	clientIssue    bool
	save           func(preferences) error
	saveFailed     bool
	width          int
	height         int
}

func NewSetupModel(ctx context.Context, service SetupService) tea.Model {
	return NewSetupModelWithPreferences(ctx, service, config.DefaultPreferences(), nil)
}

func NewSetupModelWithPreferences(ctx context.Context, service SetupService, p config.Preferences, save func(config.Preferences) error) tea.Model {
	return &setupModel{ctx: ctx, service: service, phase: setupChecking, width: 80, height: 24, preferences: p.Normalized(), save: save}
}

func (m *setupModel) Init() tea.Cmd {
	inspect, ctx := m.service.Inspect, m.ctx
	return func() tea.Msg {
		state, err := inspect(ctx)
		return setupInspection{state, err}
	}
}

func (m *setupModel) recheck() tea.Cmd {
	m.phase = setupChecking
	return m.Init()
}

func (m *setupModel) beginPairing(recovering bool) tea.Cmd {
	m.phase = setupPairing
	m.pairingState, m.deviceName = setupPairingWaiting, ""
	if recovering {
		m.pairingState = setupPairingReconnecting
	}
	if m.service.Watch == nil {
		return nil
	}
	ctx, cancel := context.WithCancel(m.ctx)
	session := &setupPairingSession{cancel, make(chan setupPairingUpdate), make(chan struct{})}
	go session.run(ctx, m.pairingDone, m.service, recovering)
	m.pairing, m.pairingDone = session, session.done
	return nextSetupPairingUpdate(session)
}

func nextSetupPairingUpdate(session *setupPairingSession) tea.Cmd {
	return func() tea.Msg {
		if update, ok := <-session.updates; ok {
			return update
		}
		return nil
	}
}

func (m *setupModel) cancelPairing() {
	if m.pairing != nil {
		m.pairing.cancel()
		m.pairing = nil
	}
}

// backToKey vuelve al paso de la clave para reemplazar una clave mal escrita.
// El campo empieza vacío: la clave que Soloist ya rechazó no se conserva. El
// reinicio ocurre cuando se envía la clave nueva.
func (m *setupModel) backToKey() {
	m.cancelPairing()
	m.clearKey()
	m.pairingState, m.deviceName = setupPairingWaiting, ""
	m.progress, m.phase = SetupNeedsKey, setupEnterKey
}

func (m *setupModel) openPlayer() (tea.Model, tea.Cmd) {
	m.cancelPairing()
	m.progress, m.phase = SetupReady, setupDone
	if m.service.PlayerWithPreferences != nil {
		player := m.service.PlayerWithPreferences(m.width, m.height, m.preferences, m.spotifyAPI)
		return player, player.Init()
	}
	if m.service.Player != nil {
		player := m.service.Player(m.width, m.height)
		return player, player.Init()
	}
	return m, nil
}

// afterSoloist offers the Spotify catalog sign-in once Soloist is ready. A
// saved session restores silently; search and playlists need their own
// authorization, and playback does not.
func (m *setupModel) afterSoloist() (tea.Model, tea.Cmd) {
	m.progress = SetupReady
	if m.spotifyDone {
		return m.openPlayer()
	}
	if m.service.Restore != nil {
		return m.restoreSpotify()
	}
	return m.beginSpotify()
}

// restoreSpotify asks the service for a saved session behind a short deadline
// so a slow or missing file never holds the wizard.
func (m *setupModel) restoreSpotify() (tea.Model, tea.Cmd) {
	m.phase = setupRestoringSpotify
	restore, ctx := m.service.Restore, m.ctx
	return m, func() tea.Msg {
		ctx, cancel := context.WithTimeout(ctx, spotifyRestoreTimeout)
		defer cancel()
		api, ok := restore(ctx)
		return setupSpotifyRestored{api: api, ok: ok}
	}
}

// beginSpotify opens the Spotify step. A remembered Client ID only needs the
// browser approval, so the instructions appear only when nothing is stored.
func (m *setupModel) beginSpotify() (tea.Model, tea.Cmd) {
	if m.service.Authorize == nil {
		return m.openPlayer()
	}
	// The field never starts with a stored value: showing it as if typed was
	// confusing and could authorize with a stale placeholder.
	m.phase = setupSpotify
	m.spotify = setupSpotifyClientID
	m.clientID = nil
	return m, nil
}

func (m *setupModel) startSpotify() tea.Cmd {
	m.cancelSpotify()
	m.spotify, m.spotifyURL = setupSpotifyPending, ""
	ctx, cancel := context.WithCancel(m.ctx)
	m.cancelAuth, m.spotifyAttempt = cancel, m.spotifyAttempt+1
	authorize, clientID, attempt := m.service.Authorize, strings.TrimSpace(m.service.ClientID), m.spotifyAttempt
	events := make(chan setupSpotifyMsg, 2)
	m.spotifyEvents = events
	return func() tea.Msg {
		go func() {
			var once sync.Once
			api, err := authorize(ctx, clientID, func(url string) {
				once.Do(func() { events <- setupSpotifyMsg{attempt: attempt, url: url} })
			})
			if err != nil {
				api = nil
			}
			events <- setupSpotifyMsg{attempt: attempt, api: api, err: err, done: true}
		}()
		return <-events
	}
}

// cancelSpotify ends an in-flight authorization attempt and fences off its
// late messages, so editing the Client ID or skipping releases the loopback port.
func (m *setupModel) cancelSpotify() {
	if m.cancelAuth != nil {
		m.cancelAuth()
		m.cancelAuth = nil
		m.spotifyAttempt++
	}
	m.spotifyEvents = nil
}

func awaitSpotify(events <-chan setupSpotifyMsg) tea.Cmd {
	return func() tea.Msg {
		if msg, ok := <-events; ok {
			return msg
		}
		return nil
	}
}

func (m *setupModel) skipSpotify() (tea.Model, tea.Cmd) {
	m.cancelSpotify()
	m.spotifyDone = true
	return m.openPlayer()
}

// changePreferences toggles language or cycles the theme and persists the result.
func (m *setupModel) changePreferences(language bool) {
	if language {
		m.preferences.Language = map[string]string{"en": "es", "es": "en"}[m.preferences.Language]
	} else {
		next := map[string]string{"standard": "dark", "dark": "light", "light": "standard"}
		m.preferences.Theme = next[m.preferences.Theme]
	}
	m.preferences = m.preferences.Normalized()
	m.saveFailed = false
	if m.save != nil && m.save(m.preferences) != nil {
		m.saveFailed = true
	}
}

func (m *setupModel) clearKey() {
	clear(m.key)
	m.key = nil
}

func (m *setupModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
	case setupInspection:
		switch m.phase {
		case setupPairingChecking:
			switch {
			case setupDaemonStopped(msg.state, msg.err):
				m.phase = setupPairingStopped
			case msg.err == nil && msg.state == SetupReady:
				return m.afterSoloist()
			default:
				return m, m.beginPairing(msg.err != nil)
			}
		case setupChecking:
			m.phase = setupCheckFailed
			if msg.err == nil {
				m.progress = msg.state
				switch msg.state {
				case SetupMissing:
					m.phase = setupConfirmInstall
				case SetupNeedsKey:
					m.phase = setupEnterKey
				case SetupAwaitPairing:
					return m, m.beginPairing(false)
				case SetupReady:
					return m.afterSoloist()
				}
			}
		}
	case setupPairingUpdate:
		if m.phase == setupPairing && m.pairing == msg.session {
			switch msg.status {
			case setupPairingReady:
				return m.afterSoloist()
			case setupPairingDaemonStopped:
				m.cancelPairing()
				m.phase = setupPairingStopped
			default:
				m.pairingState, m.deviceName = msg.status, msg.device
				return m, nextSetupPairingUpdate(msg.session)
			}
		}
	case setupInstalled:
		if m.phase == setupInstalling {
			if msg == setupInstallOK {
				return m, m.recheck()
			}
			m.installIssue = msg
			m.phase = setupInstallFailed
		}
	case setupStarted:
		if m.phase == setupStarting {
			if msg {
				return m, m.recheck()
			}
			m.phase = setupStartFailed
		}
	case setupSpotifyRestored:
		if m.phase != setupRestoringSpotify {
			break
		}
		if msg.ok && msg.api != nil {
			m.spotifyAPI, m.spotifyDone = msg.api, true
			return m.openPlayer()
		}
		return m.beginSpotify()
	case setupSpotifyMsg:
		if m.phase != setupSpotify || msg.attempt != m.spotifyAttempt {
			break
		}
		if !msg.done {
			m.spotifyURL = msg.url
			return m, awaitSpotify(m.spotifyEvents)
		}
		m.cancelSpotify()
		if msg.api != nil {
			m.spotifyAPI, m.spotifyDone = msg.api, true
			return m.openPlayer()
		}
		if errors.Is(msg.err, spotify.ErrClientIDRejected) {
			// A rejected ID must not be retried from the stored copy.
			m.service.ClientID = ""
			m.clientID = nil
			_ = spotify.ClearClientID()
			m.spotify = setupSpotifyClientID
			m.clientIssue = true
			return m, nil
		}
		m.spotify = setupSpotifyFailed
	case tea.KeyMsg:
		if m.phase == setupSpotify && m.spotify != setupSpotifyClientID && msg.Type == tea.KeyRunes && !msg.Paste && len(msg.Runes) == 1 {
			switch msg.Runes[0] {
			case 's', 'S':
				return m.skipSpotify()
			case 'e', 'E':
				m.cancelSpotify()
				m.spotify, m.clientIssue, m.spotifyURL = setupSpotifyClientID, false, ""
				// Editing starts from an empty field; the saved ID stays a hint.
				m.clientID = nil
				return m, nil
			}
		}
		return m, m.handleKey(msg)
	}
	return m, nil
}

func (m *setupModel) handleKey(msg tea.KeyMsg) tea.Cmd {
	switch msg.Type {
	case tea.KeyF2:
		m.changePreferences(true)
		return nil
	case tea.KeyF3:
		m.changePreferences(false)
		return nil
	}
	if msg.Type == tea.KeyEsc || msg.Type == tea.KeyCtrlC {
		m.clearKey()
		m.cancelPairing()
		m.phase = setupQuit
		return tea.Quit
	}

	switch m.phase {
	case setupCheckFailed:
		if msg.Type == tea.KeyEnter {
			return m.recheck()
		}
	case setupSpotify:
		if m.spotify == setupSpotifyClientID {
			switch msg.Type {
			case tea.KeyRunes:
				for _, r := range msg.Runes {
					if unicode.IsPrint(r) && !unicode.IsSpace(r) {
						m.clientID = append(m.clientID, r)
					}
				}
				m.clientIssue = false
			case tea.KeyBackspace:
				if n := len(m.clientID); n != 0 {
					m.clientID = m.clientID[:n-1]
				}
			case tea.KeyEnter:
				typed := strings.TrimSpace(string(m.clientID))
				if typed == "" {
					// Reuse a stored ID only on an explicit Enter, never silently.
					typed = strings.TrimSpace(m.service.ClientID)
				}
				id, ok := spotify.NormalizeClientID(typed)
				if !ok {
					m.clientIssue = true
					return nil
				}
				m.clientIssue = false
				m.clientID, m.service.ClientID = nil, id
				_ = spotify.SaveClientID(id)
				return m.startSpotify()
			}
			return nil
		}
		if msg.Type == tea.KeyEnter {
			return m.startSpotify()
		}
	case setupPairing, setupPairingStopped:
		if msg.Type == tea.KeyEnter {
			m.cancelPairing()
			m.phase = setupPairingChecking
			return m.Init()
		}
		if msg.Type == tea.KeyRunes && !msg.Paste && len(msg.Runes) == 1 &&
			(msg.Runes[0] == 'b' || msg.Runes[0] == 'B') {
			m.backToKey()
			return nil
		}
	case setupConfirmInstall, setupInstallFailed:
		if m.phase == setupInstallFailed && msg.Type == tea.KeyEnter {
			return m.recheck()
		}
		if msg.Type == tea.KeyRunes && !msg.Paste && len(msg.Runes) == 1 &&
			(msg.Runes[0] == 'y' || msg.Runes[0] == 'Y') {
			m.phase = setupInstalling
			install, ctx := m.service.Install, m.ctx
			return func() tea.Msg { return setupInstallResult(install(ctx)) }
		}
	case setupEnterKey, setupStartFailed:
		switch msg.Type {
		case tea.KeyRunes:
			for _, r := range msg.Runes {
				if unicode.IsPrint(r) && !unicode.IsSpace(r) {
					m.key = append(m.key, r)
				}
			}
		case tea.KeyBackspace:
			if n := len(m.key); n != 0 {
				m.key[n-1] = 0
				m.key = m.key[:n-1]
			}
		case tea.KeyEnter:
			if len(m.key) != 0 {
				key := string(m.key)
				m.clearKey()
				m.phase = setupStarting
				start, ctx := m.service.Start, m.ctx
				// Only the command receives the submitted key; its result is sanitized.
				return func() tea.Msg { return setupStarted(start(ctx, key) == nil) }
			}
		}
	}
	return nil
}

type setupScreen struct {
	action, link, status, reason, device string
	hints                                []string
	keyPrompt, clientPrompt              bool
}

func (m *setupModel) screen() setupScreen {
	var screen setupScreen
	switch m.phase {
	case setupChecking, setupPairingChecking:
		screen.action, screen.status = m.text("Checking Soloist...", "Comprobando Soloist..."), m.text("Working", "Trabajando")
	case setupRestoringSpotify:
		screen.action, screen.status = m.text("Restoring your Spotify session...", "Restaurando tu sesión de Spotify..."), m.text("Working", "Trabajando")
	case setupCheckFailed:
		screen.action, screen.status = m.text("Could not check Soloist. Try again.", "No se pudo comprobar Soloist. Inténtalo de nuevo."), m.text("Retry needed", "Reintento necesario")
		screen.hints = []string{m.text("Enter: recheck", "Enter: volver a comprobar")}
	case setupConfirmInstall:
		screen.action, screen.status = m.text("Soloist is not installed. Install it now?", "Soloist no está instalado. ¿Quieres instalarlo ahora?"), m.text("Waiting for confirmation", "Esperando confirmación")
		screen.hints = []string{m.text("Y: install", "Y: instalar")}
	case setupInstalling:
		screen.action, screen.status = m.text("Installing Soloist...", "Instalando Soloist..."), m.text("Working", "Trabajando")
	case setupInstallFailed:
		screen.action = m.text("Install Soloist manually from the official downloads.\nPut the executable on your PATH, then recheck.",
			"Instala Soloist manualmente desde las descargas oficiales.\nColoca el ejecutable en tu PATH y vuelve a comprobar.")
		screen.link, screen.status = soloist.DownloadsURL, m.text("Retry needed", "Reintento necesario")
		screen.hints = []string{m.text("Y: retry install", "Y: reintentar instalación"), m.text("Enter: recheck", "Enter: volver a comprobar")}
		switch m.installIssue {
		case setupInstallPermission:
			screen.reason = m.text("Cannot write to the install location.", "No se puede escribir en la ubicación de instalación.")
			screen.action += m.text("\nCheck write permissions before retrying the automatic install.",
				"\nRevisa los permisos de escritura antes de reintentar la instalación automática.")
		case setupInstallTimeout:
			screen.reason = m.text("Soloist download timed out.", "Se agotó el tiempo de descarga de Soloist.")
			screen.action += m.text("\nCheck your connection before retrying the automatic install.",
				"\nRevisa tu conexión antes de reintentar la instalación automática.")
		default:
			screen.reason = m.text("Automatic install failed.", "Error en la instalación automática.")
		}
	case setupEnterKey, setupStartFailed:
		screen.action = m.text("Soloist is installed. To create your Soloist key:\n"+
			"1. Open https://developer.spotify.com/dashboard/soloist\n"+
			"2. Sign in with your Spotify Premium account.\n"+
			"3. Create a Soloist key and paste it below.",
			"Soloist está instalado. Para crear tu clave de Soloist:\n"+
				"1. Abre https://developer.spotify.com/dashboard/soloist\n"+
				"2. Inicia sesión con tu cuenta de Spotify Premium.\n"+
				"3. Crea una clave de Soloist y pégala aquí.")
		screen.status = m.text("Waiting for key", "Esperando la clave")
		if m.phase == setupStartFailed {
			screen.status = m.text("Retry needed", "Reintento necesario")
			screen.action += m.text("\nCould not start Soloist. Enter your key to retry.",
				"\nNo se pudo iniciar Soloist. Introduce tu clave para reintentar.")
		}
		screen.keyPrompt = true
		screen.hints = []string{m.text("Enter: start", "Enter: iniciar")}
	case setupStarting:
		screen.action, screen.status = m.text("Starting Soloist...", "Iniciando Soloist..."), m.text("Working", "Trabajando")
	case setupPairing:
		deviceStep := m.text("4. Select Pulso or your existing Soloist device name.\n",
			"4. Selecciona Pulso o el nombre de tu dispositivo Soloist.\n")
		if m.deviceName != "" {
			deviceStep = m.text("4. Select the device shown below.\n", "4. Selecciona el dispositivo que aparece abajo.\n")
			screen.device = m.text("Device: ", "Dispositivo: ") + m.deviceName
		}
		screen.action = m.text("1. Leave Pulso open. On your phone, open Spotify with the same account.\n"+
			"2. Connect both devices to the same local network.\n"+
			"3. Play a song and tap Connect to a device.\n"+deviceStep+
			"5. Pulso opens Now Playing when Spotify connects.\n"+
			"Device missing? Check Wi-Fi, guest network isolation, and VPN settings.",
			"1. Deja Pulso abierto. En tu teléfono, abre Spotify con la misma cuenta.\n"+
				"2. Conecta ambos dispositivos a la misma red local.\n"+
				"3. En reproducción, toca Conectar a un dispositivo.\n"+deviceStep+
				"5. Pulso abre Ahora suena cuando Spotify se conecte.\n"+
				"¿No aparece el dispositivo? Revisa el Wi-Fi, el aislamiento de invitados y la VPN.")
		screen.status = m.text("Waiting for pairing", "Esperando el emparejamiento")
		if m.pairingState == setupPairingReconnecting {
			screen.status = m.text("Reconnecting to Soloist", "Reconectando con Soloist")
			screen.reason = m.text("Run pulso doctor if it does not reconnect.", "Ejecuta pulso doctor si no se reconecta.")
		}
		screen.hints = []string{m.text("Enter: recheck", "Enter: volver a comprobar"), m.text("b: back to the key", "b: volver a la clave")}
	case setupPairingStopped:
		screen.action = m.text("Soloist stopped. Start it again, then press Enter to recheck.",
			"Soloist se detuvo. Inícialo de nuevo y pulsa Enter para volver a comprobar.")
		screen.status = m.text("Soloist stopped", "Soloist detenido")
		screen.hints = []string{m.text("Enter: recheck", "Enter: volver a comprobar"), m.text("b: back to the key", "b: volver a la clave")}
	case setupDone:
		screen.action, screen.status = m.text("Soloist is ready.", "Soloist está listo."), m.text("Setup complete", "Configuración completa")
	case setupSpotify:
		switch m.spotify {
		case setupSpotifyClientID:
			screen.action = m.text("Connect Spotify to search tracks and see your playlists:\n"+
				"1. Create an app at https://developer.spotify.com/dashboard\n"+
				"2. Add "+spotify.RedirectHint()+" as a redirect URI; the port must match exactly.\n"+
				"3. Paste its Client ID below. Only the public Client ID is saved.",
				"Conecta Spotify para buscar canciones y ver tus listas:\n"+
					"1. Crea una app en https://developer.spotify.com/dashboard\n"+
					"2. Añade "+spotify.RedirectHint()+" como URI de redirección; el puerto debe coincidir.\n"+
					"3. Pega abajo su Client ID. Solo se guarda el Client ID público.")
			screen.status = m.text("Waiting for the Client ID", "Esperando el Client ID")
			if m.clientIssue {
				screen.reason = m.text("A Spotify Client ID has 32 hex characters (0-9, a-f); the Soloist key is different.",
					"El Client ID de Spotify tiene 32 caracteres hexadecimales (0-9, a-f); la clave de Soloist es distinta.")
			}
			screen.clientPrompt = true
			screen.hints = []string{m.text("Enter: connect", "Enter: conectar")}
		case setupSpotifyFailed:
			screen.action = m.text("Spotify authorization did not finish. Retry, or continue without search and playlists.",
				"La autorización de Spotify no se completó. Reintenta o continúa sin búsqueda ni listas.")
			screen.status = m.text("Retry needed", "Reintento necesario")
			screen.hints = []string{m.text("Enter: retry", "Enter: reintentar"), m.text("e: edit Client ID", "e: cambiar Client ID"), m.text("s: skip", "s: omitir")}
		default:
			screen.action = m.text("Open this URL in your browser and approve access. Playback works without it.",
				"Abre esta URL en tu navegador y aprueba el acceso. La reproducción funciona sin esto.")
			screen.link = m.spotifyURL
			screen.status = m.text("Waiting for Spotify authorization", "Esperando autorización de Spotify")
			screen.hints = []string{m.text("s: skip", "s: omitir"), m.text("e: edit Client ID", "e: cambiar Client ID")}
		}
	}
	if m.saveFailed {
		screen.reason = m.text("Could not save preferences.", "No se pudieron guardar las preferencias.")
	}
	screen.hints = append(screen.hints, m.text("Esc: quit", "Esc: salir"))
	return screen
}

var (
	setupBanner = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("0")).Background(lipgloss.Color("14"))
	setupLabel  = lipgloss.NewStyle().Bold(true)
)

func (m *setupModel) text(english, spanish string) string {
	return m.preferences.Text(english, spanish)
}

func (m *setupModel) checklist() []string {
	steps := []string{"Soloist", m.text("Key", "Clave"), m.text("Pairing", "Emparejamiento")}
	if m.service.Authorize != nil {
		steps = append(steps, "Spotify")
	}
	for i, step := range steps {
		mark := "[ ] "
		switch {
		case i < int(m.progress):
			mark = "[x] "
		case i == int(m.progress):
			mark = "[>] "
		}
		steps[i] = mark + step
	}
	return steps
}

// setupRows lays out static ASCII copy. Keep whole groups together when they fit;
// split long words (usually URLs) rather than clipping them at the right edge.
func setupRows(groups []string, separator string, width int) []string {
	var rows []string
	line := ""
	for _, group := range groups {
		if line != "" && len(line)+len(separator)+len(group) > width {
			rows = append(rows, line)
			line = ""
		}
		for len(group) > width {
			rows = append(rows, group[:width])
			group = group[width:]
		}
		if line != "" {
			line += separator
		}
		line += group
	}
	if line != "" {
		rows = append(rows, line)
	}
	return rows
}

func setupActionRows(label, action string, width int) []string {
	rows := []string{setupLabel.Render(label[:min(width, len(label))])}
	for _, paragraph := range strings.Split(action, "\n") {
		rows = append(rows, setupRows(strings.Fields(paragraph), " ", width)...)
	}
	return rows
}

func (m *setupModel) View() string {
	if m.phase == setupQuit || m.width < 1 || m.height < 1 {
		return ""
	}
	inset := 0
	if m.width >= 20 {
		inset = 1
	}
	width := m.width - 2*inset
	indent := strings.Repeat(" ", inset)
	screen, steps := m.screen(), m.checklist()

	title := m.text("Welcome to Pulso", "Bienvenido a Pulso")
	palette := m.preferences.Palette()
	banner := lipgloss.NewStyle().Bold(true).Foreground(palette.AccentText).Background(palette.Accent)
	header := []string{banner.Width(m.width).Render(indent + title[:min(len(title), width)])}
	if m.height >= 12 && width >= 25 {
		header = append(header, indent+setupLabel.Render("[ SETUP ]")+"  /  Soloist")
		if m.height >= 18 {
			header = append(header, "")
		}
	}

	status := []string{m.text("STATUS  ", "ESTADO  ") + screen.status}
	if screen.reason != "" {
		status = append(status, screen.reason)
	}
	footer := setupRows(status, "  /  ", width)
	for i, row := range footer {
		footer[i] = setupLabel.Render(row)
	}
	if screen.device != "" {
		footer = append([]string{playerFit(screen.device, width)}, footer...)
	}
	themeLabel := map[string]string{"standard": "Standard", "dark": "Dark", "light": "Light"}[m.preferences.Theme]
	hints := append([]string{}, screen.hints...)
	if width >= 60 {
		hints = append(hints,
			m.text("F2: language", "F2: idioma")+" ("+m.text("English", "Español")+")",
			m.text("F3: theme", "F3: tema")+" ("+m.text(themeLabel,
				map[string]string{"standard": "Estándar", "dark": "Oscuro", "light": "Claro"}[m.preferences.Theme])+")")
	}
	footer = append(footer, setupRows(hints, " | ", width)...)
	footer = footer[max(0, len(footer)-(m.height-1)):]
	room := m.height - len(header) - len(footer)

	var body []string
	if m.width >= 95 && m.height >= 27 {
		const sidebarWidth = 25
		actionWidth := width - sidebarWidth - 3
		actionRows := setupActionRows(m.text("ACTION", "ACCIÓN"), screen.action, actionWidth)
		checklist := append([]string{setupLabel.Render(m.text("PROGRESS", "PROGRESO"))}, steps...)
		for i := 0; i < max(len(actionRows), len(checklist)); i++ {
			left, right := "", ""
			if i < len(actionRows) {
				left = actionRows[i]
			}
			if i < len(checklist) {
				right = checklist[i]
			}
			body = append(body, left+strings.Repeat(" ", actionWidth-lipgloss.Width(left))+" │ "+right)
		}
	} else {
		body = setupRows(steps, "  ", width)
		if m.height >= 18 {
			body = append(body, "")
		}
		body = append(body, setupActionRows(m.text("ACTION", "ACCIÓN"), screen.action, width)...)
	}

	var essential []string
	if screen.link != "" {
		essential = setupRows([]string{screen.link}, "", width)
	}
	if screen.keyPrompt {
		prompt := m.text("Key: ", "Clave: ")
		essential = append(essential, prompt[:min(width, len(prompt))]+strings.Repeat("*", min(len(m.key), 64, max(0, width-len(prompt)))))
	}
	if screen.clientPrompt {
		prompt := "Client ID: "
		line := prompt + string(m.clientID) + "_"
		// A stored ID is a hint, never editable content, and the field stays empty.
		if stored := strings.TrimSpace(m.service.ClientID); stored != "" && len(stored) > 8 && len(m.clientID) == 0 {
			essential = append(essential, playerFit(m.text("Saved Client ID: ", "Client ID guardado: ")+stored[:4]+"…"+stored[len(stored)-4:], width))
		}
		essential = append(essential, playerFit(line, width))
	}
	if room > 0 {
		// Give the key field and the official fallback link priority over copy
		// when the terminal is short. Use spare space to pin the footer.
		essential = essential[:min(len(essential), room)]
		actionRoom := room - len(essential)
		const resizeHint = "Resize for details."
		if len(body) > actionRoom && actionRoom > 0 && width >= len(resizeHint) {
			body = append(body[:actionRoom-1], resizeHint)
		} else {
			body = body[:min(len(body), actionRoom)]
		}
		body = append(body, essential...)
		for _, row := range body {
			header = append(header, indent+row)
		}
		for len(header)+len(footer) < m.height {
			header = append(header, "")
		}
	}
	for _, row := range footer {
		header = append(header, indent+row)
	}
	return strings.Join(header, "\n")
}
