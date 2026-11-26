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
	inspect func(context.Context) (setupState, error)
	install func(context.Context) error
	start   func(context.Context, string) error
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
	setupDone
	setupQuit
)

type setupInspection struct {
	state setupState
	ok    bool
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
	width        int
	height       int
}

func newSetupModel(ctx context.Context, service setupService) tea.Model {
	return &setupModel{ctx: ctx, service: service, phase: setupChecking, width: 80, height: 24}
}

func (m *setupModel) Init() tea.Cmd {
	inspect, ctx := m.service.inspect, m.ctx
	return func() tea.Msg {
		state, err := inspect(ctx)
		return setupInspection{state, err == nil}
	}
}

func (m *setupModel) recheck() tea.Cmd {
	m.phase = setupChecking
	return m.Init()
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
		if m.phase == setupChecking {
			m.phase = setupCheckFailed
			if msg.ok {
				m.progress = msg.state
				switch msg.state {
				case setupMissing:
					m.phase = setupConfirmInstall
				case setupNeedsKey:
					m.phase = setupEnterKey
				case setupAwaitPairing:
					m.phase = setupPairing
				case setupReady:
					m.phase = setupDone
				}
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
	if msg.Type == tea.KeyEsc || msg.Type == tea.KeyCtrlC {
		m.clearKey()
		m.phase = setupQuit
		return tea.Quit
	}

	switch m.phase {
	case setupCheckFailed, setupPairing:
		if msg.Type == tea.KeyEnter {
			return m.recheck()
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
	action, link, status, reason string
	hints                        []string
	keyPrompt                    bool
}

func (m *setupModel) screen() setupScreen {
	var screen setupScreen
	switch m.phase {
	case setupChecking:
		screen.action, screen.status = "Checking Soloist...", "Working"
	case setupCheckFailed:
		screen.action, screen.status = "Could not check Soloist. Try again.", "Retry needed"
		screen.hints = []string{"Enter: recheck"}
	case setupConfirmInstall:
		screen.action, screen.status = "Soloist is not installed. Install it now?", "Waiting for confirmation"
		screen.hints = []string{"Y: install"}
	case setupInstalling:
		screen.action, screen.status = "Installing Soloist...", "Working"
	case setupInstallFailed:
		screen.action = "Install Soloist manually from the official downloads.\nPut the executable on your PATH, then recheck."
		screen.link, screen.status = downloads, "Retry needed"
		screen.hints = []string{"Y: retry install", "Enter: recheck"}
		switch m.installIssue {
		case setupInstallPermission:
			screen.reason = "Cannot write to the install location."
			screen.action += "\nCheck write permissions before retrying the automatic install."
		case setupInstallTimeout:
			screen.reason = "Soloist download timed out."
			screen.action += "\nCheck your connection before retrying the automatic install."
		default:
			screen.reason = "Automatic install failed."
		}
	case setupEnterKey, setupStartFailed:
		screen.action = "Soloist is installed. To create your Soloist key:\n" +
			"1. Open https://developer.spotify.com/dashboard/soloist\n" +
			"2. Sign in with your Spotify Premium account.\n" +
			"3. Create a Soloist key and paste it below."
		screen.status = "Waiting for key"
		if m.phase == setupStartFailed {
			screen.status = "Retry needed"
			screen.action += "\nCould not start Soloist. Enter your key to retry."
		}
		screen.keyPrompt = true
		screen.hints = []string{"Enter: start"}
	case setupStarting:
		screen.action, screen.status = "Starting Soloist...", "Working"
	case setupPairing:
		screen.action = "1. Leave Pulso open. On your phone, open Spotify with the same account.\n" +
			"2. Connect both devices to the same local network.\n" +
			"3. Play a song and tap Connect to a device.\n" +
			"4. Select Pulso or your existing Soloist device name.\n" +
			"5. When audio plays here, return to Pulso and press Enter.\n" +
			"Device missing? Check Wi-Fi, guest network isolation, and VPN settings."
		screen.status = "Waiting for pairing"
		screen.hints = []string{"Enter: recheck"}
	case setupDone:
		screen.action, screen.status = "Soloist is ready.", "Setup complete"
	}
	screen.hints = append(screen.hints, "Esc: quit")
	return screen
}

var (
	setupBanner = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("0")).Background(lipgloss.Color("14"))
	setupLabel  = lipgloss.NewStyle().Bold(true)
)

func (m *setupModel) checklist() []string {
	steps := []string{"Soloist", "Key", "Pairing"}
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

func setupActionRows(action string, width int) []string {
	rows := []string{setupLabel.Render("ACTION"[:min(width, len("ACTION"))])}
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

	title := "Welcome to Pulso"
	header := []string{setupBanner.Width(m.width).Render(indent + title[:min(len(title), width)])}
	if m.height >= 12 && width >= 25 {
		header = append(header, indent+setupLabel.Render("[ SETUP ]")+"  /  Soloist")
		if m.height >= 18 {
			header = append(header, "")
		}
	}

	status := []string{"STATUS  " + screen.status}
	if screen.reason != "" {
		status = append(status, screen.reason)
	}
	footer := setupRows(status, "  /  ", width)
	for i, row := range footer {
		footer[i] = setupLabel.Render(row)
	}
	footer = append(footer, setupRows(screen.hints, " | ", width)...)
	// Keep the title and the key hints in unusually short terminals.
	footer = footer[max(0, len(footer)-(m.height-1)):]
	room := m.height - len(header) - len(footer)

	var body []string
	if m.width >= 95 && m.height >= 27 {
		const sidebarWidth = 25
		actionWidth := width - sidebarWidth - 3
		actionRows := setupActionRows(screen.action, actionWidth)
		checklist := append([]string{setupLabel.Render("PROGRESS")}, steps...)
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
		body = append(body, setupActionRows(screen.action, width)...)
	}

	var essential []string
	if screen.link != "" {
		essential = setupRows([]string{screen.link}, "", width)
	}
	if screen.keyPrompt {
		prompt := "Key: "
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
