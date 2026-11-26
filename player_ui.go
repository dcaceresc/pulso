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

// playerPanel is one focusable region of the workspace. Tab and Shift+Tab
// walk these in order and wrap.
type playerPanel uint8

const (
	playerFocusSidebar playerPanel = iota
	playerFocusTable
	playerFocusSearch
	playerFocusPlaying
	playerFocusCount
)

const (
	playerWideWidth   = 100 // Three columns start here.
	playerWideHeight  = 30
	playerNarrowWidth = 60 // Below this the playing bar keeps only title and volume.
	playerHintsWidth  = 80 // Two hint rows start here.
	playerHintsHeight = 14
	playerMinBodyRoom = 4 // Content wins over separator rules in short windows.
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

	// Workspace focus and in-memory selections. No library source is wired
	// yet, so the selections simply clamp inside what is on screen.
	focus                          playerPanel
	sidebarSelected, tableSelected int
	query                          string
}

func newPlayerModel(ctx context.Context, states <-chan playerState, commands chan<- playerCommand, width, height int) tea.Model {
	return newPlayerModelWithPreferences(ctx, states, commands, width, height, defaultPreferences(), nil, "", nil)
}

// newPlayerModelWithPreferences builds the playback workspace. The catalog is
// optional: without authorize, the overlay stays absent and playback still works.
func newPlayerModelWithPreferences(ctx context.Context, states <-chan playerState, commands chan<- playerCommand, width, height int, p preferences, save func(preferences) error, clientID string, authorize func(context.Context, string, func(string)) (catalogAPI, error)) tea.Model {
	return newPlayerModelWithCatalog(ctx, states, commands, width, height, p, save, nil, authorize, clientID)
}

func newPlayerModelWithCatalog(ctx context.Context, states <-chan playerState, commands chan<- playerCommand, width, height int, p preferences, save func(preferences) error, api catalogAPI, authorize func(context.Context, string, func(string)) (catalogAPI, error), knownClientID string) tea.Model {
	ctx, cancel := context.WithCancel(ctx)
	m := &playerModel{
		ctx: ctx, cancel: cancel, states: states, commands: commands,
		width: width, height: height, preferences: p.normalized(), save: save,
		focus: playerFocusTable,
	}
	if authorize != nil {
		clientID := knownClientID
		if clientID == "" {
			clientID = loadClientID()
		}
		m.catalog = newCatalogModel(ctx, m.preferences, width, height, clientID, authorize, m.playURI)
		m.catalog.api = api
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
		m.clampSelections()
		return m, m.awaitState()
	case tea.KeyMsg:
		if msg.Type == tea.KeyF2 {
			m.changePreferences(true)
			return m, nil
		}
		if msg.Type == tea.KeyF3 {
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
	case tea.KeyF2:
		return m.changePreferences(true)
	case tea.KeyF3:
		return m.changePreferences(false)
	}
	if key.Paste || key.Alt {
		return nil
	}
	// A focused text field owns every printable key; `q`, `s`, `r`, `+` and
	// `-` are text there instead of shortcuts.
	if m.focus == playerFocusSearch {
		return m.handleSearchKey(key)
	}
	// Esc from any panel that is not the search input quits: nothing else
	// claims it once the catalog overlay has had its turn.
	if key.Type == tea.KeyEsc || key.Type == tea.KeyCtrlC || key.String() == "q" {
		m.quit = true
		m.cancel()
		return tea.Quit
	}
	if m.ctx.Err() != nil {
		return nil
	}
	switch key.Type {
	case tea.KeyTab:
		m.focus = playerStep(m.focus, 1)
		return nil
	case tea.KeyShiftTab:
		m.focus = playerStep(m.focus, -1)
		return nil
	}
	if key.String() == "/" {
		m.focus = playerFocusSearch
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
	if m.moveSelection(key) {
		return nil
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

// handleSearchKey edits the query buffer. Esc and Enter leave the field
// without quitting and without fetching anything yet.
func (m *playerModel) handleSearchKey(key tea.KeyMsg) tea.Cmd {
	switch key.Type {
	case tea.KeyEsc, tea.KeyEnter:
		m.focus = playerFocusTable
		return nil
	case tea.KeyTab:
		m.focus = playerFocusPlaying
		return nil
	case tea.KeyShiftTab:
		m.focus = playerFocusTable
		return nil
	case tea.KeyCtrlC:
		m.quit = true
		m.cancel()
		return tea.Quit
	}
	m.query = catalogEdit(m.query, key)
	return nil
}

func (m *playerModel) moveSelection(key tea.KeyMsg) bool {
	delta := 0
	switch key.Type {
	case tea.KeyUp:
		delta = -1
	case tea.KeyDown:
		delta = 1
	case tea.KeyRunes:
		switch string(key.Runes) {
		case "k":
			delta = -1
		case "j":
			delta = 1
		}
	}
	if delta == 0 {
		return false
	}
	switch m.focus {
	case playerFocusSidebar:
		m.sidebarSelected = playerIndex(m.sidebarSelected+delta, len(m.libraryItems()))
	case playerFocusTable:
		m.tableSelected = playerIndex(m.tableSelected+delta, len(m.state.Queue))
	default:
		return false
	}
	return true
}

func (m *playerModel) clampSelections() {
	m.sidebarSelected = playerIndex(m.sidebarSelected, len(m.libraryItems()))
	m.tableSelected = playerIndex(m.tableSelected, len(m.state.Queue))
}

func playerStep(value playerPanel, delta int) playerPanel {
	count := int(playerFocusCount)
	return playerPanel((int(value) + delta + count) % count)
}

// playerIndex keeps a selection inside a list; empty lists keep index 0.
func playerIndex(index, count int) int {
	if count <= 0 {
		return 0
	}
	return min(max(0, index), count-1)
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

// playerProgress renders elapsed time, a playhead bar, and total time.
func playerProgress(position, duration int64, width int) string {
	start := playerElapsed(max(0, position))
	if duration <= 0 {
		return start + " / --:--"
	}
	position = min(duration, max(0, position))
	end := playerElapsed(duration)
	cells := min(20, width-lipgloss.Width(start)-lipgloss.Width(end)-2)
	if cells < 4 {
		return start + " / " + end
	}
	filled := min(cells-1, int(float64(position)/float64(duration)*float64(cells)))
	bar := strings.Repeat("━", filled) + "╸" + strings.Repeat("─", cells-filled-1)
	return start + " " + bar + " " + end
}

// playerPad fits plain text into exactly width cells.
func playerPad(text string, width int) string {
	if width <= 0 {
		return ""
	}
	text = playerFit(text, width)
	return text + strings.Repeat(" ", max(0, width-lipgloss.Width(text)))
}

// playerSpread pins left and right inside width with at least one space.
func playerSpread(left, right string, width int) string {
	if width <= 0 {
		return ""
	}
	right = playerFit(right, width)
	leftWidth := width - lipgloss.Width(right)
	if leftWidth <= 0 {
		return right
	}
	left = playerFit(left, leftWidth-1)
	return left + strings.Repeat(" ", max(1, width-lipgloss.Width(left)-lipgloss.Width(right))) + right
}

type playerStyles struct {
	accent, muted, label, failure, banner lipgloss.Style
}

func (m *playerModel) styles() playerStyles {
	palette := m.preferences.palette()
	return playerStyles{
		accent:  lipgloss.NewStyle().Bold(true).Foreground(palette.Accent),
		muted:   lipgloss.NewStyle().Foreground(palette.Muted),
		label:   lipgloss.NewStyle().Bold(true).Foreground(palette.Accent),
		failure: lipgloss.NewStyle().Foreground(palette.Error),
		banner:  lipgloss.NewStyle().Bold(true).Foreground(palette.AccentText).Background(palette.Accent),
	}
}

type playerRow struct {
	text  string
	style lipgloss.Style
}

func playerRowAt(rows []playerRow, i int) playerRow {
	if i >= 0 && i < len(rows) {
		return rows[i]
	}
	return playerRow{}
}

// playerRenderRow fits a row to the width, pads it, then applies its style so
// backgrounds span the terminal.
func playerRenderRow(row playerRow, width int) string {
	if width <= 0 {
		return ""
	}
	text := row.text
	if lipgloss.Width(text) > width {
		text = playerFit(text, width)
	}
	if pad := width - lipgloss.Width(text); pad > 0 {
		text += strings.Repeat(" ", pad)
	}
	return row.style.Render(text)
}

func (m *playerModel) View() string {
	if m.quit || m.width < 1 || m.height < 1 {
		return ""
	}
	if m.catalog != nil && !m.catalog.Closed() {
		return m.catalog.View()
	}
	styles := m.styles()
	width := m.width
	rule := styles.muted.Render(strings.Repeat("─", width))

	play, status := m.playingRows(width, styles), m.statusRows(width, styles)
	rules := 3
	room := m.height - 2 - len(play) - len(status) - rules
	for room < playerMinBodyRoom && rules > 0 {
		rules--
		room = m.height - 2 - len(play) - len(status) - rules
	}
	room = max(0, room)
	body := m.bodyRows(width, styles, room)
	if len(body) > room {
		body = body[:room]
	}

	rows := make([]string, 0, m.height)
	rows = append(rows, playerRenderRow(playerRow{text: playerSpread("Pulso", m.statusWord(), width), style: styles.banner}, width))
	rows = append(rows, playerRenderRow(playerRow{text: m.searchRow(width, styles)}, width))
	if rules >= 3 {
		rows = append(rows, rule)
	}
	for _, row := range body {
		rows = append(rows, playerRenderRow(row, width))
	}
	// Never stack the region rule on top of a table rule.
	if rules >= 1 && !playerLastRowIsRule(rows, width) {
		rows = append(rows, rule)
	}
	for _, row := range play {
		rows = append(rows, playerRenderRow(row, width))
	}
	if rules >= 2 {
		rows = append(rows, rule)
	}
	for _, row := range status {
		rows = append(rows, playerRenderRow(row, width))
	}
	if len(rows) > m.height {
		rows = rows[:m.height]
		if len(rows) > 1 && ansi.Strip(rows[len(rows)-1]) == strings.Repeat("─", width) {
			rows = rows[:len(rows)-1]
		}
	}
	return strings.Join(rows, "\n")
}

func playerLastRowIsRule(rows []string, width int) bool {
	return width > 0 && len(rows) > 0 && ansi.Strip(rows[len(rows)-1]) == strings.Repeat("─", width)
}

func (m *playerModel) statusWord() string {
	status := m.text("Loading", "Cargando")
	if m.loaded {
		status = m.text("Disconnected", "Sin conexión")
		if m.state.Connected {
			status = playerText(m.state.Status)
			if status == "" {
				status = m.text("Connected", "Conectado")
			}
		}
	}
	return status
}

// searchRow is the top bar: the label, the live buffer with its cursor, and a
// muted user placeholder on the right.
func (m *playerModel) searchRow(width int, styles playerStyles) string {
	if width <= 0 {
		return ""
	}
	label := m.text("Search: ", "Buscar: ")
	focused := m.focus == playerFocusSearch
	field, fieldStyle := m.query, lipgloss.NewStyle()
	switch {
	case focused:
		field += "_"
	case field == "":
		field, fieldStyle = m.text("type to search", "escribe para buscar"), styles.muted
	}
	left := styles.label.Render(label) + fieldStyle.Render(playerFit(field, max(0, width-lipgloss.Width(label))))
	right := styles.muted.Render(m.text("@user", "@usuario"))
	if lipgloss.Width(left)+lipgloss.Width(right)+2 > width {
		return left
	}
	return left + strings.Repeat(" ", width-lipgloss.Width(left)-lipgloss.Width(right)) + right
}

func (m *playerModel) playingRows(width int, styles playerStyles) []playerRow {
	glyph := "⏸"
	var title string
	switch {
	case !m.loaded:
		title = m.text("Loading", "Cargando")
	case !m.state.Connected:
		title = m.text("Disconnected", "Sin conexión")
	default:
		title = playerText(m.state.Track.Title)
		if title == "" {
			title = m.text("Nothing playing", "No hay nada en reproducción")
		}
		if artist := playerText(m.state.Track.Artist); artist != "" {
			title += " · " + artist
		}
		if strings.EqualFold(strings.TrimSpace(m.state.Status), "playing") {
			glyph = "▶"
		}
	}
	marker := "  "
	if m.focus == playerFocusPlaying {
		marker = "> "
	}
	line := marker + glyph + " " + title
	volume := fmt.Sprintf(m.text("Volume %d%%", "Volumen %d%%"), playerVolume(m.state.Volume))
	if width < playerNarrowWidth {
		return []playerRow{{text: playerSpread(line, volume, width), style: playerLabel}}
	}
	modes := "⇄ " + m.text("off", "apagado") + "  ↻ " + m.text("off", "apagado")
	rows := []playerRow{{text: playerSpread(line, modes, width), style: playerLabel}}
	progress := playerProgress(m.state.PositionMS, m.state.Track.DurationMS, width)
	if album := playerText(m.state.Track.Album); album != "" && width >= 90 {
		progress = album + "  " + progress
	}
	if device := playerText(m.state.Device); device != "" {
		volume += "  " + m.text("Device ", "Dispositivo ") + device
	}
	rows = append(rows, playerRow{text: playerSpread(progress, volume, width), style: styles.muted})
	return rows
}

func (m *playerModel) statusRows(width int, styles playerStyles) []playerRow {
	notice := playerText(m.state.Notice)
	if m.busy {
		notice = m.text("Controls busy; try again.", "Controles ocupados; inténtalo de nuevo.")
	}
	if m.saved {
		notice = m.text("Preferences saved.", "Preferencias guardadas.")
	}
	var rows []playerRow
	if notice != "" {
		rows = append(rows, playerRow{text: notice})
	}
	for _, hint := range m.hintRows(width) {
		rows = append(rows, playerRow{text: hint, style: styles.muted})
	}
	return rows
}

// hintRows follows the focused panel; narrow windows use short labels.
func (m *playerModel) hintRows(width int) []string {
	if m.focus == playerFocusSearch {
		if width < playerHintsWidth {
			return []string{m.text("Enter: keep | Esc: leave", "Enter: mantener | Esc: salir")}
		}
		return []string{m.text("Type: edit | Backspace: delete | Enter: keep | Esc: leave",
			"Escribe: editar | Retroceso: borrar | Enter: mantener | Esc: salir")}
	}
	if width < playerHintsWidth {
		return []string{m.text("Tab | ↑↓/jk | /: search | q: quit", "Tab | ↑↓/jk | /: buscar | q: salir")}
	}
	primary := m.text("Tab: panel | ↑↓/jk: move | /: search | Esc/q: quit",
		"Tab: panel | ↑↓/jk: mover | /: buscar | Esc/q: salir")
	switch m.focus {
	case playerFocusSidebar:
		primary = m.text("Tab: panel | ↑↓/jk: library | /: search | Esc/q: quit",
			"Tab: panel | ↑↓/jk: biblioteca | /: buscar | Esc/q: salir")
	case playerFocusPlaying:
		primary = m.text("Tab: panel | Space: pause | n/p: track | +/-: volume | Esc/q: quit",
			"Tab: panel | Espacio: pausa | n/p: pista | +/-: volumen | Esc/q: salir")
	}
	rows := []string{primary}
	if m.height >= playerHintsHeight {
		rows = append(rows, m.text("s: search | l: playlists | +/-: volume | F2: language | F3: theme",
			"s: buscar | l: listas | +/-: volumen | F2: idioma | F3: tema"))
	}
	return rows
}

func (m *playerModel) bodyRows(width int, styles playerStyles, room int) []playerRow {
	if room <= 0 || width <= 0 {
		return nil
	}
	if width >= playerWideWidth && m.height >= playerWideHeight {
		return m.columnRows(width, styles, room)
	}
	return m.stackedRows(width, styles, room)
}

// columnRows is the three-region layout: sidebar, a column rule, and the table.
func (m *playerModel) columnRows(width int, styles playerStyles, room int) []playerRow {
	sidebarWidth := min(32, max(26, width/4))
	mainWidth := width - sidebarWidth - 1
	if mainWidth < 1 {
		return nil
	}
	side, main := m.sidebarRows(styles), m.mainRows(mainWidth, styles, room)
	separator := styles.muted.Render("│")
	rows := make([]playerRow, 0, room)
	for i := 0; i < room; i++ {
		left := playerCell(playerRowAt(side, i), sidebarWidth)
		rows = append(rows, playerRow{text: left + separator + playerCell(playerRowAt(main, i), mainWidth)})
	}
	return rows
}

// stackedRows is the breakpoint layout: one library summary row above the table.
func (m *playerModel) stackedRows(width int, styles playerStyles, room int) []playerRow {
	rows := []playerRow{{text: m.librarySummary(), style: styles.label}}
	return append(rows, m.mainRows(width, styles, max(1, room-1))...)
}

func playerCell(row playerRow, width int) string {
	if width <= 0 {
		return ""
	}
	text := playerFit(row.text, width)
	text += strings.Repeat(" ", max(0, width-lipgloss.Width(text)))
	return row.style.Render(text)
}

// libraryItems are the selectable sidebar rows. U1 has no playlist source yet,
// so the Playlists section is the only row; U2 replaces it with real entries.
func (m *playerModel) libraryItems() []string {
	return []string{m.text("Playlists", "Listas")}
}

func (m *playerModel) librarySelection() string {
	items := m.libraryItems()
	if len(items) == 0 {
		return m.text("Playlists", "Listas")
	}
	return items[playerIndex(m.sidebarSelected, len(items))]
}

func (m *playerModel) librarySummary() string {
	return m.text("LIBRARY: ", "BIBLIOTECA: ") + m.librarySelection()
}

func (m *playerModel) sidebarRows(styles playerStyles) []playerRow {
	rows := []playerRow{{text: m.text("LIBRARY", "BIBLIOTECA"), style: styles.label}}
	for i, item := range m.libraryItems() {
		marker, style := "  ", lipgloss.NewStyle()
		if i == m.sidebarSelected {
			marker = "> "
			if m.focus == playerFocusSidebar {
				style = styles.accent
			}
		}
		rows = append(rows, playerRow{text: marker + item, style: style})
	}
	// The section is empty until the library slice lands.
	rows = append(rows, playerRow{text: "  " + m.text("No playlists yet", "Aún no hay listas"), style: styles.muted})
	return rows
}

func (m *playerModel) mainTitle() string {
	title := m.text("Playback", "Reproducción")
	if m.loaded && m.state.Connected {
		title += " · " + fmt.Sprintf(m.text("%d tracks", "%d canciones"), len(m.state.Queue))
	}
	return title
}

type playerColumn struct {
	kind  int
	label string
	width int
}

const (
	playerColumnNumber = iota
	playerColumnTitle
	playerColumnArtist
	playerColumnDuration
)

// tableColumns drops whole columns instead of squeezing them, so no header or
// value ever overflows the region.
func (m *playerModel) tableColumns(width int) []playerColumn {
	if width <= 0 {
		return nil
	}
	title, artist := m.text("Title", "Título"), m.text("Artist", "Artista")
	duration := m.text("Duration", "Duración")
	durationWidth := lipgloss.Width(duration)
	switch {
	case width < 12:
		return []playerColumn{{kind: playerColumnTitle, label: title, width: width}}
	case width < 26:
		return []playerColumn{
			{kind: playerColumnNumber, label: "#", width: 2},
			{kind: playerColumnTitle, label: title, width: width - 3},
		}
	case width < 40:
		return []playerColumn{
			{kind: playerColumnNumber, label: "#", width: 2},
			{kind: playerColumnTitle, label: title, width: width - 4 - durationWidth},
			{kind: playerColumnDuration, label: duration, width: durationWidth},
		}
	default:
		rest := width - 5 - durationWidth
		return []playerColumn{
			{kind: playerColumnNumber, label: "#", width: 2},
			{kind: playerColumnTitle, label: title, width: rest / 2},
			{kind: playerColumnArtist, label: artist, width: rest - rest/2},
			{kind: playerColumnDuration, label: duration, width: durationWidth},
		}
	}
}

func (m *playerModel) mainRows(width int, styles playerStyles, room int) []playerRow {
	if width <= 0 {
		return nil
	}
	rows := []playerRow{
		{text: m.mainTitle(), style: styles.label},
		{text: m.tableHeader(width), style: playerLabel},
		{text: strings.Repeat("─", width), style: styles.muted},
	}
	return append(rows, m.tableRows(width, styles, max(1, room-3))...)
}

func (m *playerModel) tableHeader(width int) string {
	columns := m.tableColumns(width)
	cells := make([]string, len(columns))
	for i, column := range columns {
		cells[i] = playerPad(column.label, column.width)
	}
	return strings.Join(cells, " ")
}

func (m *playerModel) tableRows(width int, styles playerStyles, room int) []playerRow {
	switch {
	case !m.loaded:
		return []playerRow{{text: m.text("Loading Soloist playback...", "Cargando la reproducción de Soloist..."), style: styles.muted}}
	case !m.state.Connected:
		return []playerRow{{text: m.text("Queue unavailable", "Cola no disponible"), style: styles.muted}}
	case len(m.state.Queue) == 0:
		return []playerRow{{text: m.text("Queue is empty", "La cola está vacía"), style: styles.muted}}
	}
	columns := m.tableColumns(width)
	start := max(0, m.tableSelected-room+1)
	rows := make([]playerRow, 0, room)
	for i := start; i < len(m.state.Queue) && len(rows) < room; i++ {
		rows = append(rows, m.tableRow(columns, i, m.state.Queue[i], styles))
	}
	return rows
}

func (m *playerModel) tableRow(columns []playerColumn, index int, track playerTrack, styles playerStyles) playerRow {
	number := fmt.Sprintf(" %d", index+1)
	if index == m.tableSelected {
		number = fmt.Sprintf(">%d", index+1)
	}
	title := playerText(track.Title)
	if title == "" {
		title = m.text("Unknown track", "Pista desconocida")
	}
	duration := ""
	if track.DurationMS > 0 {
		duration = playerElapsed(track.DurationMS)
	}
	cells := make([]string, len(columns))
	for i, column := range columns {
		value := ""
		switch column.kind {
		case playerColumnNumber:
			value = number
		case playerColumnTitle:
			value = title
		case playerColumnArtist:
			value = playerText(track.Artist)
		case playerColumnDuration:
			value = duration
		}
		cells[i] = playerPad(value, column.width)
	}
	style := lipgloss.NewStyle()
	if index == m.tableSelected && m.focus == playerFocusTable {
		style = styles.accent
	}
	return playerRow{text: strings.Join(cells, " "), style: style}
}
