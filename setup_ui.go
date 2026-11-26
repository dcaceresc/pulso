package main

import (
	"context"
	"errors"
	"io/fs"
	"strings"
	"unicode"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

type setupState uint8

const (
	setupMissing setupState = iota
	setupNeedsKey
	setupAwaitPairing
	setupReady
)

type setupService struct {
	inspect               func(context.Context) (setupState, error)
	watch                 func(context.Context, func(string) bool) error
	install               func(context.Context) error
	start                 func(context.Context, string) error
	player                func(width, height int) tea.Model
	playerWithPreferences func(width, height int, p preferences) tea.Model
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
	setupDone
	setupQuit
)

type setupInspection struct {
	state setupState
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
	ctx          context.Context
	service      setupService
	phase        setupPhase
	progress     setupState // Last confirmed inspection, retained while rechecking.
	installIssue setupInstalled
	key          []rune
	pairing      *setupPairingSession
	pairingDone  <-chan struct{}
	pairingState setupPairingStatus
	deviceName   string
	preferences  preferences
	save         func(preferences) error
	saveFailed   bool
	width        int
	height       int
}

func newSetupModel(ctx context.Context, service setupService) tea.Model {
	return newSetupModelWithPreferences(ctx, service, defaultPreferences(), nil)
}

func newSetupModelWithPreferences(ctx context.Context, service setupService, p preferences, save func(preferences) error) tea.Model {
	return &setupModel{ctx: ctx, service: service, phase: setupChecking, width: 80, height: 24, preferences: p.normalized(), save: save}
}

func (m *setupModel) Init() tea.Cmd {
	inspect, ctx := m.service.inspect, m.ctx
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
	if m.service.watch == nil {
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

func (m *setupModel) openPlayer() (tea.Model, tea.Cmd) {
	m.cancelPairing()
	m.progress, m.phase = setupReady, setupDone
	if m.service.playerWithPreferences != nil {
		player := m.service.playerWithPreferences(m.width, m.height, m.preferences)
		return player, player.Init()
	}
	if m.service.player != nil {
		player := m.service.player(m.width, m.height)
		return player, player.Init()
	}
	return m, nil
}

// changePreferences toggles language or cycles the theme and persists the result.
func (m *setupModel) changePreferences(language bool) {
	if language {
		m.preferences.Language = map[string]string{"en": "es", "es": "en"}[m.preferences.Language]
	} else {
		next := map[string]string{"standard": "dark", "dark": "light", "light": "standard"}
		m.preferences.Theme = next[m.preferences.Theme]
	}
	m.preferences = m.preferences.normalized()
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
			case msg.err == nil && msg.state == setupReady:
				return m.openPlayer()
			default:
				return m, m.beginPairing(msg.err != nil)
			}
		case setupChecking:
			m.phase = setupCheckFailed
			if msg.err == nil {
				m.progress = msg.state
				switch msg.state {
				case setupMissing:
					m.phase = setupConfirmInstall
				case setupNeedsKey:
					m.phase = setupEnterKey
				case setupAwaitPairing:
					return m, m.beginPairing(false)
				case setupReady:
					return m.openPlayer()
				}
			}
		}
	case setupPairingUpdate:
		if m.phase == setupPairing && m.pairing == msg.session {
			switch msg.status {
			case setupPairingReady:
				return m.openPlayer()
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
	case tea.KeyMsg:
		return m, m.handleKey(msg)
	}
	return m, nil
}

func (m *setupModel) handleKey(msg tea.KeyMsg) tea.Cmd {
	switch msg.Type {
	case tea.KeyF2, tea.KeyCtrlL:
		m.changePreferences(true)
		return nil
	case tea.KeyF3, tea.KeyCtrlT:
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
	case setupPairing, setupPairingStopped:
		if msg.Type == tea.KeyEnter {
			m.cancelPairing()
			m.phase = setupPairingChecking
			return m.Init()
		}
	case setupConfirmInstall, setupInstallFailed:
		if m.phase == setupInstallFailed && msg.Type == tea.KeyEnter {
			return m.recheck()
		}
		if msg.Type == tea.KeyRunes && !msg.Paste && len(msg.Runes) == 1 &&
			(msg.Runes[0] == 'y' || msg.Runes[0] == 'Y') {
			m.phase = setupInstalling
			install, ctx := m.service.install, m.ctx
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
				start, ctx := m.service.start, m.ctx
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
	keyPrompt                            bool
}

func (m *setupModel) screen() setupScreen {
	var screen setupScreen
	switch m.phase {
	case setupChecking, setupPairingChecking:
		screen.action, screen.status = m.text("Checking Soloist...", "Comprobando Soloist..."), m.text("Working", "Trabajando")
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
		screen.link, screen.status = downloads, m.text("Retry needed", "Reintento necesario")
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
		screen.hints = []string{m.text("Enter: recheck", "Enter: volver a comprobar")}
	case setupPairingStopped:
		screen.action = m.text("Soloist stopped. Start it again, then press Enter to recheck.",
			"Soloist se detuvo. Inícialo de nuevo y pulsa Enter para volver a comprobar.")
		screen.status = m.text("Soloist stopped", "Soloist detenido")
		screen.hints = []string{m.text("Enter: recheck", "Enter: volver a comprobar")}
	case setupDone:
		screen.action, screen.status = m.text("Soloist is ready.", "Soloist está listo."), m.text("Setup complete", "Configuración completa")
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
	return m.preferences.text(english, spanish)
}

func (m *setupModel) checklist() []string {
	steps := []string{"Soloist", m.text("Key", "Clave"), m.text("Pairing", "Emparejamiento")}
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
	palette := m.preferences.palette()
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
