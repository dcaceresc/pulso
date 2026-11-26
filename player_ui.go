package main

import (
	"context"
	"fmt"
	"strings"
	"unicode"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

type playerViewState struct {
	state playerState
	ok    bool
}

type playerModel struct {
	ctx             context.Context
	cancel          context.CancelFunc
	states          <-chan playerState
	commands        chan<- playerCommand
	state           playerState
	width, height   int
	pending, loaded bool
	ended, quit     bool
	busy            bool
}

func newPlayerModel(ctx context.Context, states <-chan playerState, commands chan<- playerCommand, width, height int) tea.Model {
	ctx, cancel := context.WithCancel(ctx)
	return &playerModel{ctx: ctx, cancel: cancel, states: states, commands: commands, width: width, height: height}
}

func (m *playerModel) Init() tea.Cmd { return m.awaitState() }

func (m *playerModel) awaitState() tea.Cmd {
	if m.pending || m.ended || m.quit {
		return nil
	}
	m.pending = true
	ctx, states := m.ctx, m.states
	return func() tea.Msg {
		if states == nil || ctx.Err() != nil {
			return playerViewState{}
		}
		select {
		case <-ctx.Done():
			return playerViewState{}
		case state, ok := <-states:
			return playerViewState{state: state, ok: ok && ctx.Err() == nil}
		}
	}
}

func (m *playerModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
	case playerViewState:
		if m.quit || !m.pending {
			break
		}
		m.pending, m.loaded, m.busy = false, true, false
		if !msg.ok || m.ctx.Err() != nil {
			m.state = playerState{}
			m.ended = true
			m.cancel()
			break
		}
		m.state = msg.state
		return m, m.awaitState()
	case tea.KeyMsg:
		return m, m.handleKey(msg)
	}
	return m, nil
}

func (m *playerModel) handleKey(key tea.KeyMsg) tea.Cmd {
	if key.Paste || key.Alt {
		return nil
	}
	if key.Type == tea.KeyEsc || key.Type == tea.KeyCtrlC || key.String() == "q" {
		m.quit = true
		m.cancel()
		return tea.Quit
	}
	if m.quit || !m.state.Connected || m.ctx.Err() != nil || m.commands == nil {
		return nil
	}
	var command playerCommand
	switch key.String() {
	case " ":
		command.Name = "play"
		if strings.EqualFold(strings.TrimSpace(m.state.Status), "playing") {
			command.Name = "pause"
		}
	case "n":
		command.Name = "skip_next"
	case "p":
		command.Name = "skip_prev"
	case "+", "-":
		step := 5
		if key.String() == "-" {
			step = -step
		}
		command = playerCommand{Name: "set_volume", Volume: playerVolume(playerVolume(m.state.Volume) + step)}
	default:
		return nil
	}

	// A bounded try-send preserves key order without queuing goroutines or
	// delivering an old keypress after a disconnect.
	select {
	case <-m.ctx.Done():
	case m.commands <- command:
		m.busy = false
		if command.Name == "set_volume" {
			m.state.Volume = command.Volume
		}
	default:
		m.busy = true
	}
	return nil
}

var (
	playerBanner = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("0")).Background(lipgloss.Color("14"))
	playerLabel  = lipgloss.NewStyle().Bold(true)
)

// Metadata is never rendered as terminal syntax or allowed to create rows.
func playerText(text string) string {
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

func playerFit(text string, width int) string {
	return ansi.Truncate(text, max(0, width), "")
}

func playerVolume(value int) int { return min(100, max(0, value)) }

func playerElapsed(ms int64) string {
	seconds := max(0, ms) / 1000
	if seconds >= 3600 {
		return fmt.Sprintf("%d:%02d:%02d", seconds/3600, seconds/60%60, seconds%60)
	}
	return fmt.Sprintf("%d:%02d", seconds/60, seconds%60)
}

func playerProgress(position, duration int64, width int) string {
	if duration <= 0 {
		return playerElapsed(position) + " / --:--"
	}
	position = min(duration, max(0, position))
	times := playerElapsed(position) + " / " + playerElapsed(duration)
	if cells := min(20, width-lipgloss.Width(times)-2); cells >= 4 {
		filled := int(float64(position) / float64(duration) * float64(cells))
		times += "  " + strings.Repeat("━", filled) + strings.Repeat("─", cells-filled)
	}
	return times
}

func (m *playerModel) trackRows(width int) []string {
	rows := []string{"NOW PLAYING"}
	if !m.loaded {
		return append(rows, "Loading Soloist playback...")
	}
	if !m.state.Connected {
		return append(rows, "Disconnected from Soloist")
	}
	track := m.state.Track
	title, artist, album := playerText(track.Title), playerText(track.Artist), playerText(track.Album)
	if title == "" && artist == "" && album == "" && track.DurationMS <= 0 {
		rows = append(rows, "Nothing playing")
	} else {
		if title == "" {
			title = "Unknown track"
		}
		rows = append(rows, title)
		if artist != "" {
			rows = append(rows, "Artist: "+artist)
		}
		if album != "" {
			rows = append(rows, "Album: "+album)
		}
		rows = append(rows, playerProgress(m.state.PositionMS, track.DurationMS, width))
	}
	return append(rows, fmt.Sprintf("Volume %d%%", playerVolume(m.state.Volume)))
}

func (m *playerModel) queueRows(room int) []string {
	rows := []string{"QUEUE"}
	if !m.state.Connected {
		return append(rows, "Queue unavailable")
	}
	if len(m.state.Queue) == 0 {
		return append(rows, "Queue is empty")
	}
	for i, track := range m.state.Queue[:min(len(m.state.Queue), max(0, room-1))] {
		title := playerText(track.Title)
		if title == "" {
			title = "Unknown track"
		}
		if artist := playerText(track.Artist); artist != "" {
			title += " — " + artist
		}
		rows = append(rows, fmt.Sprintf("%d. %s", i+1, title))
	}
	return rows
}

func (m *playerModel) footer(width, height int) []string {
	status := "Loading"
	if m.loaded {
		status = "Disconnected"
		if m.state.Connected {
			status = playerText(m.state.Status)
			if status == "" {
				status = "Connected"
			}
			if device := playerText(m.state.Device); device != "" {
				status += " / " + device
			}
		}
	}
	hints := "Esc/q: quit | Space: play/pause | n/p: next/previous | +/-: volume"
	if width < len(hints) {
		hints = "Esc/q: quit | Space n/p +/-"
	}
	rows := []string{playerLabel.Render(playerFit("STATUS "+status, width))}
	if height >= 9 {
		notice := playerText(m.state.Notice)
		if m.busy {
			notice = "Controls busy; try again."
		}
		if notice != "" {
			rows = append(rows, playerFit(notice, width))
		}
	}
	return append(rows, playerFit(hints, width))
}

func (m *playerModel) View() string {
	if m.quit || m.width < 1 || m.height < 1 {
		return ""
	}
	// Match the setup workspace: a full-width banner, a flexible body, and
	// pinned status and shortcut rows. Very small windows lose the inset.
	inset := 0
	if m.width >= 20 {
		inset = 1
	}
	indent := strings.Repeat(" ", inset)
	width := m.width - 2*inset
	rows := []string{playerBanner.Width(m.width).Render(indent + playerFit("Pulso / Playback", width))}
	footer := m.footer(width, m.height)
	footer = footer[max(0, len(footer)-(m.height-1)):]
	room := m.height - 1 - len(footer)
	if width >= 78 {
		leftWidth := (width - 3) * 3 / 5
		rightWidth := width - 3 - leftWidth
		left, right := m.trackRows(leftWidth), m.queueRows(room)
		for i := 0; i < min(room, max(len(left), len(right))); i++ {
			a, b := playerPanelRow(left, i, leftWidth), playerPanelRow(right, i, rightWidth)
			rows = append(rows, indent+a+strings.Repeat(" ", leftWidth-lipgloss.Width(a))+" │ "+b)
		}
	} else {
		left := m.trackRows(width)
		right := m.queueRows(room - len(left))
		for _, panel := range [][]string{left, right} {
			count := min(len(panel), room)
			for i := 0; i < count; i++ {
				rows = append(rows, indent+playerPanelRow(panel, i, width))
			}
			room -= count
		}
	}
	for len(rows)+len(footer) < m.height {
		rows = append(rows, "")
	}
	for _, row := range footer {
		rows = append(rows, indent+row)
	}
	return strings.Join(rows, "\n")
}

func playerPanelRow(panel []string, i, width int) string {
	if i >= len(panel) {
		return ""
	}
	row := playerFit(panel[i], width)
	if i == 0 {
		return playerLabel.Render(row)
	}
	return row
}
