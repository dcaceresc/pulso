package main

import (
	"context"
	"strings"
	"unicode"

	tea "github.com/charmbracelet/bubbletea"
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

type setupInstalled bool
type setupStarted bool

type setupModel struct {
	ctx     context.Context
	service setupService
	phase   setupPhase
	key     []rune
}

func newSetupModel(ctx context.Context, service setupService) tea.Model {
	return &setupModel{ctx: ctx, service: service, phase: setupChecking}
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
	case setupInspection:
		if m.phase == setupChecking {
			m.phase = setupCheckFailed
			if msg.ok {
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
			if msg {
				return m, m.recheck()
			}
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
		if msg.Type == tea.KeyRunes && !msg.Paste && len(msg.Runes) == 1 &&
			(msg.Runes[0] == 'y' || msg.Runes[0] == 'Y') {
			m.phase = setupInstalling
			install, ctx := m.service.install, m.ctx
			return func() tea.Msg { return setupInstalled(install(ctx) == nil) }
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

func (m *setupModel) View() string {
	body, hints := "", "Esc: quit"
	switch m.phase {
	case setupChecking:
		body = "Checking Soloist..."
	case setupCheckFailed:
		body = "Could not check Soloist. Try again."
		hints = "Enter: recheck | Esc: quit"
	case setupConfirmInstall:
		body = "Soloist is not installed. Install it now?"
		hints = "Y: install | Esc: quit"
	case setupInstalling:
		body = "Installing Soloist..."
	case setupInstallFailed:
		body = "Could not install Soloist. Try again?"
		hints = "Y: retry install | Esc: quit"
	case setupEnterKey, setupStartFailed:
		body = "Soloist is installed. To create your Soloist key:\n" +
			"1. Open https://developer.spotify.com/dashboard/soloist\n" +
			"2. Sign in with your Spotify Premium account.\n" +
			"3. Create a Soloist key and paste it below.\n\n"
		if m.phase == setupStartFailed {
			body += "Could not start Soloist. Enter your key to retry.\n"
		}
		body += "Key: " + strings.Repeat("*", min(len(m.key), 64))
		hints = "Enter: start | Esc: quit"
	case setupStarting:
		body = "Starting Soloist..."
	case setupPairing:
		body = "Pair Soloist in the Spotify app using the same account.\n" +
			"In the device picker, choose Pulso or your existing Soloist device name.\nThen recheck here."
		hints = "Enter: recheck | Esc: quit"
	case setupDone:
		body = "Soloist is ready."
	case setupQuit:
		return ""
	}
	return "Welcome to Pulso\n\n" + body + "\n\n" + hints
}
