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
	preferences     preferences
	save            func(preferences) error
	catalog         *catalogModel
	saved           bool
	pending, loaded bool
	ended, quit     bool
	busy            bool
}

func newPlayerModel(ctx context.Context, states <-chan playerState, commands chan<- playerCommand, width, height int) tea.Model {
	return newPlayerModelWithPreferences(ctx, states, commands, width, height, defaultPreferences(), nil, "", nil)
}

// newPlayerModelWithPreferences builds the playback workspace. The catalog is
// optional: without authorize, the overlay stays absent and playback still works.
func newPlayerModelWithPreferences(ctx context.Context, states <-chan playerState, commands chan<- playerCommand, width, height int, p preferences, save func(preferences) error, clientID string, authorize func(context.Context, string, func(string)) (catalogAPI, error)) tea.Model {
	ctx, cancel := context.WithCancel(ctx)
	m := &playerModel{
		ctx: ctx, cancel: cancel, states: states, commands: commands,
		width: width, height: height, preferences: p.normalized(), save: save,
	}
	if authorize != nil {
		if clientID == "" {
			clientID = loadClientID()
		}
		m.catalog = newCatalogModel(ctx, m.preferences, width, height, clientID, authorize, m.playURI)
		m.catalog.saveClientID = func(id string) {
			_ = saveClientID(id)
		}
	}
	return m
}

// playURI sends a catalog selection to the session currently on screen.
func (m *playerModel) playURI(uri string) {
	if m.commands == nil || m.ctx.Err() != nil || !m.state.Connected || uri == "" {
		return
	}
	command := playerCommand{Name: "play", URI: uri, SessionID: m.state.SessionID}
	select {
	case <-m.ctx.Done():
	case m.commands <- command:
	default:
		m.busy = true
	}
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
		if m.catalog != nil {
			_, cmd := m.catalog.Update(msg)
			return m, cmd
		}
	case catalogAuthMsg, catalogPageMsg:
		if m.catalog != nil {
			_, cmd := m.catalog.Update(msg)
			return m, cmd
		}
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
		switch msg.Type {
		case tea.KeyF2, tea.KeyCtrlL:
			m.changePreferences(true)
			return m, nil
		case tea.KeyF3, tea.KeyCtrlT:
			m.changePreferences(false)
			return m, nil
		}
		if m.catalog != nil && !m.catalog.Closed() {
			_, cmd := m.catalog.Update(msg)
			return m, cmd
		}
		return m, m.handleKey(msg)
	}
	return m, nil
}

func (m *playerModel) text(english, spanish string) string {
	return m.preferences.text(english, spanish)
}

func (m *playerModel) handleKey(key tea.KeyMsg) tea.Cmd {
	switch key.Type {
	case tea.KeyF2, tea.KeyCtrlL:
		return m.changePreferences(true)
	case tea.KeyF3, tea.KeyCtrlT:
		return m.changePreferences(false)
	}
	if key.Paste || key.Alt {
		return nil
	}
	if key.Type == tea.KeyEsc || key.Type == tea.KeyCtrlC || key.String() == "q" {
		m.quit = true
		m.cancel()
		return tea.Quit
	}
	if m.ctx.Err() != nil {
		return nil
	}
	if m.catalog != nil {
		switch key.String() {
		case "s", "l":
			m.catalog.width, m.catalog.height = m.width, m.height
			m.catalog.preferences = m.preferences
			return m.catalog.Open(map[string]string{"s": "search", "l": "playlists"}[key.String()])
		}
	}
	if m.quit || !m.state.Connected || m.commands == nil {
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

	// Bind the keypress to the session visible when it was made.
	command.SessionID = m.state.SessionID
	// A bounded try-send preserves key order without queuing goroutines.
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

// changePreferences toggles language or cycles the theme and persists the result.
func (m *playerModel) changePreferences(language bool) tea.Cmd {
	if language {
		m.preferences.Language = map[string]string{"en": "es", "es": "en"}[m.preferences.Language]
	} else {
		next := map[string]string{"standard": "dark", "dark": "light", "light": "standard"}
		m.preferences.Theme = next[m.preferences.Theme]
	}
	m.preferences = m.preferences.normalized()
	if m.catalog != nil {
		m.catalog.preferences = m.preferences
	}
	m.saved = false
	if m.save != nil && m.save(m.preferences) == nil {
		m.saved = true
	}
	return nil
}

var playerLabel = lipgloss.NewStyle().Bold(true)

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
	rows := []string{m.text("NOW PLAYING", "EN REPRODUCCIÓN")}
	if !m.loaded {
		return append(rows, m.text("Loading Soloist playback...", "Cargando la reproducción de Soloist..."))
	}
	if !m.state.Connected {
		return append(rows, m.text("Disconnected from Soloist", "Sin conexión con Soloist"))
	}
	track := m.state.Track
	title, artist, album := playerText(track.Title), playerText(track.Artist), playerText(track.Album)
	if title == "" && artist == "" && album == "" && track.DurationMS <= 0 {
		rows = append(rows, m.text("Nothing playing", "No hay nada en reproducción"))
	} else {
		if title == "" {
			title = m.text("Unknown track", "Pista desconocida")
		}
		rows = append(rows, title)
		if artist != "" {
			rows = append(rows, m.text("Artist: ", "Artista: ")+artist)
		}
		if album != "" {
			rows = append(rows, m.text("Album: ", "Álbum: ")+album)
		}
		rows = append(rows, playerProgress(m.state.PositionMS, track.DurationMS, width))
	}
	return append(rows, fmt.Sprintf(m.text("Volume %d%%", "Volumen %d%%"), playerVolume(m.state.Volume)))
}

func (m *playerModel) queueRows(room int) []string {
	rows := []string{m.text("QUEUE", "COLA")}
	if !m.state.Connected {
		return append(rows, m.text("Queue unavailable", "Cola no disponible"))
	}
	if len(m.state.Queue) == 0 {
		return append(rows, m.text("Queue is empty", "La cola está vacía"))
	}
	for i, track := range m.state.Queue[:min(len(m.state.Queue), max(0, room-1))] {
		title := playerText(track.Title)
		if title == "" {
			title = m.text("Unknown track", "Pista desconocida")
		}
		if artist := playerText(track.Artist); artist != "" {
			title += " — " + artist
		}
		rows = append(rows, fmt.Sprintf("%d. %s", i+1, title))
	}
	return rows
}

func (m *playerModel) footer(width, height int) []string {
	status := m.text("Loading", "Cargando")
	if m.loaded {
		status = m.text("Disconnected", "Sin conexión")
		if m.state.Connected {
			status = playerText(m.state.Status)
			if status == "" {
				status = m.text("Connected", "Conectado")
			}
			if device := playerText(m.state.Device); device != "" {
				status += " / " + device
			}
		}
	}
	label := m.text("STATUS ", "ESTADO ")
	hints := []string{
		m.text("Esc/q: quit | Space: play/pause | n/p: next/previous | +/-: volume",
			"Esc/q: salir | Espacio: reproducir/pausar | n/p: siguiente/anterior | +/-: volumen"),
		m.text("s: search | l: playlists | ", "s: buscar | l: listas | ") +
			m.text("F2: language", "F2: idioma") + " | " + m.text("F3: theme", "F3: tema"),
	}
	rows := []string{playerLabel.Render(playerFit(label+status, width))}
	if height >= 9 {
		notice := playerText(m.state.Notice)
		if m.busy {
			notice = m.text("Controls busy; try again.", "Controles ocupados; inténtalo de nuevo.")
		}
		if m.saved {
			notice = m.text("Preferences saved.", "Preferencias guardadas.")
		}
		if notice != "" {
			rows = append(rows, playerFit(notice, width))
		}
	}
	for i, hint := range hints {
		if i > 0 && width < 40 {
			break
		}
		rows = append(rows, playerFit(hint, width))
	}
	return rows
}

func (m *playerModel) View() string {
	if m.quit || m.width < 1 || m.height < 1 {
		return ""
	}
	if m.catalog != nil && !m.catalog.Closed() {
		return m.catalog.View()
	}
	// Match the setup workspace: a full-width banner, a flexible body, and
	// pinned status and shortcut rows. Very small windows lose the inset.
	palette := m.preferences.palette()
	banner := lipgloss.NewStyle().Bold(true).Foreground(palette.AccentText).Background(palette.Accent)
	inset := 0
	if m.width >= 20 {
		inset = 1
	}
	indent := strings.Repeat(" ", inset)
	width := m.width - 2*inset
	rows := []string{banner.Width(m.width).Render(indent + playerFit(m.text("Pulso / Playback", "Pulso / Reproducción"), width))}
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
