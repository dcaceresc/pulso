package main

import (
	"context"
	"fmt"
	"strings"
	"time"
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
	playerSeekStepMS  = 10_000
	playerTickEvery   = time.Second
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
	saved           bool
	pending, loaded bool
	ended, quit     bool
	busy            bool

	// Workspace focus and the Soloist queue cursor. The library keeps its own
	// per-panel cursors for playlists and their tracks; the search state keeps
	// the cursor of the result table.
	focus         playerPanel
	tableSelected int
	query         string
	library       *libraryState
	search        *searchState
	// clock is the source of the interpolated playhead; tests replace it.
	clock func() time.Time
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
		focus: playerFocusTable, clock: time.Now,
	}
	clientID := ""
	if authorize != nil {
		clientID = knownClientID
		if clientID == "" {
			clientID = loadClientID()
		}
	}
	// The setup handoff passes the authorized catalog; when it can serve
	// library pages the sidebar, the table and the search all read it directly.
	m.library = newLibraryState(ctx, librarySourceOf(api), authorize, clientID)
	m.search = newSearchState(ctx, m.library.source)
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

func (m *playerModel) Init() tea.Cmd {
	state := m.awaitState()
	// A handoff that already carries a library loads its first page at once.
	library := m.library.init()
	switch {
	case library == nil:
		return state
	case state == nil:
		return library
	default:
		return tea.Batch(state, library)
	}
}

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
	case libraryAuthMsg, libraryPageMsg:
		cmd := m.library.update(msg)
		// Authorization can install the library source after startup.
		m.search.source = m.library.source
		return m, cmd
	case searchPageMsg:
		return m, m.search.update(msg)
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
		// A playing session needs a second of wall time to advance its playhead.
		return m, tea.Batch(m.awaitState(), m.tick())
	case playerTickMsg:
		return m, m.tick()
	case tea.KeyMsg:
		if msg.Type == tea.KeyF2 {
			m.changePreferences(true)
			return m, nil
		}
		if msg.Type == tea.KeyF3 {
			m.changePreferences(false)
			return m, nil
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
	// Esc from any panel leaves shown search results first; the next one quits.
	if key.Type == tea.KeyEsc && m.search.active {
		m.search.close()
		m.focus = playerFocusTable
		return nil
	}
	// Otherwise Esc from any panel that is not the search input quits.
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
	switch key.String() {
	case "/":
		m.focus = playerFocusSearch
		return nil
	case "l":
		// l moves to the sidebar. The catalog overlay is gone; s is free
		// outside the playing bar.
		m.focus = playerFocusSidebar
		return nil
	}
	// The playing bar owns seek, shuffle and repeat.
	if m.handlePlayingKey(key) {
		return nil
	}
	if handled, cmd := m.moveSelection(key); handled {
		return cmd
	}
	if cmd, handled := m.activateSelection(key); handled {
		return cmd
	}
	if m.quit || !m.state.Connected || m.commands == nil {
		return nil
	}
	var command playerCommand
	switch key.String() {
	case " ":
		command.Name = "play"
		if playerPlaying(m.state.Status) {
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
	if m.send(command) && command.Name == "set_volume" {
		m.state.Volume = command.Volume
	}
	return nil
}

// handlePlayingKey owns the keys of the playing bar: seek, shuffle and repeat.
// It reports whether the key belonged to that panel.
func (m *playerModel) handlePlayingKey(key tea.KeyMsg) bool {
	if m.focus != playerFocusPlaying {
		return false
	}
	var command playerCommand
	switch {
	case key.Type == tea.KeyLeft, key.Type == tea.KeyRight:
		delta := int64(playerSeekStepMS)
		if key.Type == tea.KeyLeft {
			delta = -delta
		}
		command = playerCommand{Name: "seek", PositionMS: playerSeekTarget(m.playhead(), delta, m.state.Track.DurationMS)}
	case key.String() == "s":
		command = playerCommand{Name: "set_shuffle", Shuffle: !m.state.Shuffle}
	case key.String() == "r":
		command = playerCommand{Name: "set_repeat", Repeat: playerNextRepeat(m.state.Repeat)}
	default:
		return false
	}
	if m.commands == nil || !m.state.Connected {
		return true
	}
	if m.send(command) && command.Name == "seek" {
		// Move the bar at once; Soloist confirms with position_sync.
		m.state.PositionMS, m.state.AnchorAt = command.PositionMS, m.now()
	}
	return true
}

// send binds a command to the session on screen and reports whether it fit
// through the bounded command channel. There is no retry: the bar shows busy.
func (m *playerModel) send(command playerCommand) bool {
	command.SessionID = m.state.SessionID
	select {
	case <-m.ctx.Done():
	case m.commands <- command:
		m.busy = false
		return true
	default:
		m.busy = true
	}
	return false
}

// handleSearchKey edits the query buffer. Enter submits a non-empty query and
// leaves the field for the table; Esc blurs it, and closes shown results.
func (m *playerModel) handleSearchKey(key tea.KeyMsg) tea.Cmd {
	switch key.Type {
	case tea.KeyEsc:
		m.search.close()
		m.focus = playerFocusTable
		return nil
	case tea.KeyEnter:
		if cmd := m.submitSearch(); cmd != nil {
			return cmd
		}
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

// submitSearch fetches the first page of the typed query and moves the focus
// to the table. A blank query stays in the field and reaches no source.
func (m *playerModel) submitSearch() tea.Cmd {
	query := strings.TrimSpace(m.query)
	if query == "" {
		return nil
	}
	m.search.source = m.library.source
	m.focus = playerFocusTable
	return m.search.submit(query)
}

func (m *playerModel) moveSelection(key tea.KeyMsg) (bool, tea.Cmd) {
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
		return false, nil
	}
	switch m.focus {
	case playerFocusSidebar:
		return true, m.library.moveSidebar(delta)
	case playerFocusTable:
		if m.search.active {
			return true, m.search.move(delta)
		}
		if m.library.open() {
			return true, m.library.moveTracks(delta)
		}
		m.tableSelected = playerIndex(m.tableSelected+delta, len(m.state.Queue))
		return true, nil
	}
	return false, nil
}

// activateSelection handles Enter and the library's own play key. It reports
// whether the key belonged to the library panels.
func (m *playerModel) activateSelection(key tea.KeyMsg) (tea.Cmd, bool) {
	if key.Paste {
		return nil, false
	}
	switch {
	case key.Type == tea.KeyEnter && m.focus == playerFocusSidebar:
		return m.activatePlaylist(), true
	case key.Type == tea.KeyEnter && m.focus == playerFocusTable && m.search.active:
		// Enter on the empty error row asks for the same page again.
		if m.search.err != nil && len(m.search.tracks) == 0 {
			return m.search.retry(), true
		}
		if track, ok := m.search.selected(); ok {
			m.playURI(track.URI)
		}
		return nil, true
	case key.Type == tea.KeyEnter && m.focus == playerFocusTable && m.library.open():
		if m.library.notice == libraryNoticeLoadFailed && !m.library.loadedTracks {
			return m.library.fetchTracks(0), true
		}
		if track, ok := m.library.selectedTrack(); ok {
			m.playURI(track.URI)
		}
		return nil, true
	case key.String() == "p" && m.focus == playerFocusSidebar:
		if playlist, ok := m.library.selectedPlaylist(); ok {
			m.playURI(playlist.URI)
			return nil, true
		}
	case key.String() == "p" && m.focus == playerFocusTable && m.library.open() && m.library.restricted:
		m.playURI(m.library.playlist.URI)
		return nil, true
	}
	return nil, false
}

// activatePlaylist opens the sidebar selection or starts authorization.
func (m *playerModel) activatePlaylist() tea.Cmd {
	library := m.library
	switch {
	case library.canAuthorize():
		// A second Enter must not cancel the flow already waiting on the
		// browser and its loopback listener.
		if library.authorizing() {
			return nil
		}
		return library.startAuth()
	case library.source == nil:
		return nil
	case library.notice == libraryNoticeLoadFailed && !library.loadedPlaylists:
		return library.fetchPlaylists(0)
	case len(library.playlists) == 0:
		return nil
	}
	m.focus = playerFocusTable
	// Opening a playlist replaces the result table on purpose.
	m.search.close()
	return library.openPlaylist()
}

func (m *playerModel) clampSelections() {
	m.tableSelected = playerIndex(m.tableSelected, len(m.state.Queue))
	m.library.clamp()
	m.search.clamp()
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

// playerPlaying reports whether Soloist is advancing the playhead.
func playerPlaying(status string) bool {
	return strings.EqualFold(strings.TrimSpace(status), "playing")
}

// playerTickMsg re-renders the interpolated playhead.
type playerTickMsg struct{}

// tick keeps one second of wall time flowing into the bar while a session plays.
func (m *playerModel) tick() tea.Cmd {
	if m.quit || m.ended || m.ctx.Err() != nil || !m.state.Connected || !playerPlaying(m.state.Status) {
		return nil
	}
	ctx := m.ctx
	return tea.Tick(playerTickEvery, func(time.Time) tea.Msg {
		if ctx.Err() != nil {
			return nil
		}
		return playerTickMsg{}
	})
}

func (m *playerModel) now() time.Time {
	if m.clock == nil {
		return time.Now()
	}
	return m.clock()
}

// playhead interpolates from the reported position: the wall time since the
// anchor at Soloist's speed, capped at the track duration.
func (m *playerModel) playhead() int64 {
	position := m.state.PositionMS
	if anchor, speed := m.state.AnchorAt, m.state.Speed; !anchor.IsZero() && speed > 0 {
		if elapsed := m.now().Sub(anchor); elapsed > 0 {
			position += int64(float64(elapsed) / float64(time.Millisecond) * speed)
		}
	}
	if duration := m.state.Track.DurationMS; duration > 0 {
		position = min(duration, position)
	}
	return max(0, position)
}

// playerSeekTarget moves the playhead inside [0, duration]. A track without a
// known duration keeps only the lower bound.
func playerSeekTarget(position, delta, duration int64) int64 {
	target := max(0, position+delta)
	if duration > 0 {
		target = min(duration, target)
	}
	return target
}

// playerNextRepeat cycles repeat off → context → track. An unreported mode
// counts as off, which is what the bar shows.
func playerNextRepeat(mode string) string {
	switch mode {
	case "context":
		return "track"
	case "track":
		return "off"
	}
	return "context"
}

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
	modes := "⇄ " + m.shuffleMode() + "  ↻ " + m.repeatMode()
	rows := []playerRow{{text: playerSpread(line, modes, width), style: playerLabel}}
	progress := playerProgress(m.playhead(), m.state.Track.DurationMS, width)
	if album := playerText(m.state.Track.Album); album != "" && width >= 90 {
		progress = album + "  " + progress
	}
	if device := playerText(m.state.Device); device != "" {
		volume += "  " + m.text("Device ", "Dispositivo ") + device
	}
	rows = append(rows, playerRow{text: playerSpread(progress, volume, width), style: styles.muted})
	return rows
}

// shuffleMode renders the shuffle state Soloist last reported.
func (m *playerModel) shuffleMode() string {
	if m.state.Shuffle {
		return m.text("on", "activado")
	}
	return m.text("off", "apagado")
}

// repeatMode renders off, all (context) or one (track).
func (m *playerModel) repeatMode() string {
	switch m.state.Repeat {
	case "context":
		return m.text("all", "todo")
	case "track":
		return m.text("one", "una")
	default:
		return m.text("off", "apagado")
	}
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
			return []string{m.text("Enter: search | Esc: leave", "Enter: buscar | Esc: salir")}
		}
		return []string{m.text("Type: edit | Backspace: delete | Enter: search | Esc: leave",
			"Escribe: editar | Retroceso: borrar | Enter: buscar | Esc: salir")}
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
		primary = m.text("Tab: panel | Space: pause | ←→: seek | n/p: track | s: shuffle | r: repeat | +/-: volume | Esc/q: quit",
			"Tab: panel | Espacio: pausa | ←→: salto | n/p: pista | s: aleatorio | r: repetir | +/-: volumen | Esc/q: salir")
	}
	rows := []string{primary}
	if m.height >= playerHintsHeight {
		secondary := m.text("l: library | +/-: volume | F2: language | F3: theme",
			"l: biblioteca | +/-: volumen | F2: idioma | F3: tema")
		if m.focus == playerFocusPlaying {
			secondary = m.text("s: shuffle | r: repeat | +/-: volume | F2: language | F3: theme",
				"s: aleatorio | r: repetir | +/-: volumen | F2: idioma | F3: tema")
		}
		rows = append(rows, secondary)
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
	side, main := m.sidebarRows(styles, sidebarWidth), m.mainRows(mainWidth, styles, room)
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

// librarySelection is the playlist under the sidebar cursor, or the state row
// that stands in for an unavailable library in the stacked summary.
func (m *playerModel) librarySelection() string {
	if playlist, ok := m.library.selectedPlaylist(); ok {
		if name := playerText(playlist.Name); name != "" {
			return name
		}
	}
	if rows := m.libraryStateRows(m.styles()); len(rows) > 0 {
		return strings.TrimSpace(rows[0].text)
	}
	return libraryEmptyLabel(m.preferences)
}

func libraryEmptyLabel(p preferences) string {
	return p.text("No playlists yet", "Aún no hay listas")
}

func (m *playerModel) librarySummary() string {
	return m.text("LIBRARY: ", "BIBLIOTECA: ") + m.librarySelection()
}

// sidebarRows lists playlists and ends in the state rows, so the panel is never
// blank: loading, empty, error and unauthorized each read differently.
func (m *playerModel) sidebarRows(styles playerStyles, width int) []playerRow {
	rows := []playerRow{{text: m.text("LIBRARY", "BIBLIOTECA"), style: styles.label}}
	library := m.library
	for i, playlist := range library.playlists {
		marker, style := "  ", lipgloss.NewStyle()
		if i == library.sidebarSelected {
			marker = "> "
			if m.focus == playerFocusSidebar {
				style = styles.accent
			}
		}
		name := playerText(playlist.Name)
		if name == "" {
			name = m.text("Untitled playlist", "Lista sin título")
		}
		text := marker + name
		if owner := playerText(playlist.Owner); owner != "" && lipgloss.Width(text)+lipgloss.Width(owner)+3 <= width {
			text += " · " + owner
		}
		rows = append(rows, playerRow{text: text, style: style})
	}
	return append(rows, m.libraryStateRows(styles)...)
}

// libraryStateRows explains an unavailable library; the stacked layout reuses
// its first row as the summary.
func (m *playerModel) libraryStateRows(styles playerStyles) []playerRow {
	library := m.library
	muted := func(text string) playerRow { return playerRow{text: "  " + text, style: styles.muted} }
	switch {
	case library.canAuthorize():
		switch {
		case library.authURL != "":
			return []playerRow{{text: "  " + library.authURL, style: styles.muted}}
		case library.authorizing():
			return []playerRow{muted(m.text("Waiting for Spotify…", "Esperando a Spotify…"))}
		case library.notice == libraryNoticeAuthFailed:
			return []playerRow{
				{text: "  " + m.text("Spotify unavailable; playback works.", "Spotify no disponible; la reproducción funciona."), style: styles.failure},
				muted(m.text("Enter: retry Spotify", "Enter: reintentar Spotify")),
			}
		case library.notice == libraryNoticeClientID:
			return []playerRow{{text: "  " + m.text("Restart Pulso and complete the Spotify step.", "Reinicia Pulso y completa el paso de Spotify."), style: styles.failure}}
		default:
			return []playerRow{muted(m.text("Enter: connect Spotify", "Enter: conectar Spotify"))}
		}
	case library.source == nil:
		return []playerRow{muted(libraryEmptyLabel(m.preferences))}
	case library.loadingPlaylists && len(library.playlists) == 0:
		return []playerRow{muted(m.text("Loading…", "Cargando…"))}
	case library.notice == libraryNoticeLoadFailed && !library.loadedPlaylists:
		return []playerRow{
			{text: "  " + m.text("Could not load playlists.", "No se pudieron cargar las listas."), style: styles.failure},
			muted(m.text("Enter: retry", "Enter: reintentar")),
		}
	case library.loadedPlaylists && len(library.playlists) == 0:
		return []playerRow{muted(libraryEmptyLabel(m.preferences))}
	}
	return nil
}

func (m *playerModel) mainTitle() string {
	if m.search.active {
		return m.searchTitle()
	}
	if m.library.open() {
		title := playerText(m.library.playlist.Name)
		if title == "" {
			title = m.text("Playlist", "Lista")
		}
		total := max(m.library.trackTotal, len(m.library.tracks))
		if total > 0 {
			title += " · " + fmt.Sprintf(m.text("%d tracks", "%d canciones"), total)
		}
		return title
	}
	title := m.text("Playback", "Reproducción")
	if m.loaded && m.state.Connected {
		title += " · " + fmt.Sprintf(m.text("%d tracks", "%d canciones"), len(m.state.Queue))
	}
	return title
}

// searchTitle names the submitted query and its total, so the table never
// shows an unlabelled list of results.
func (m *playerModel) searchTitle() string {
	title := m.text("Search", "Búsqueda") + ": " + playerText(m.search.query)
	total := max(m.search.total, len(m.search.tracks))
	if total > 0 {
		title += " · " + fmt.Sprintf(m.text("%d tracks", "%d canciones"), total)
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
	if m.search.active {
		return m.searchTableRows(width, styles, room)
	}
	if m.library.open() {
		return m.libraryTableRows(width, styles, room)
	}
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
		rows = append(rows, m.tableRow(columns, i, i == m.tableSelected, m.state.Queue[i], styles))
	}
	return rows
}

// libraryTableRows renders the open playlist, including its own states.
func (m *playerModel) libraryTableRows(width int, styles playerStyles, room int) []playerRow {
	library := m.library
	switch {
	case library.notice == libraryNoticeUnavailable:
		return []playerRow{{text: m.text("Playlist contents unavailable. p: play the playlist.", "Contenido de la lista no disponible. p: reproducir la lista."), style: styles.failure}}
	case library.notice == libraryNoticeLoadFailed:
		return []playerRow{{text: m.text("Could not load the tracks. Enter: retry", "No se pudieron cargar las canciones. Enter: reintentar"), style: styles.failure}}
	case !library.loadedTracks || (library.loadingTracks && len(library.tracks) == 0):
		return []playerRow{{text: m.text("Loading tracks…", "Cargando canciones…"), style: styles.muted}}
	case len(library.tracks) == 0:
		return []playerRow{{text: m.text("No tracks in this playlist", "No hay canciones en esta lista"), style: styles.muted}}
	}
	columns := m.tableColumns(width)
	start := max(0, library.tableSelected-room+1)
	rows := make([]playerRow, 0, room)
	for i := start; i < len(library.tracks) && len(rows) < room; i++ {
		track := library.tracks[i]
		rows = append(rows, m.tableRow(columns, i, i == library.tableSelected,
			playerTrack{Title: track.Name, Artist: track.Artist, DurationMS: track.DurationMS}, styles))
	}
	if library.loadingTracks && len(rows) < room {
		rows = append(rows, playerRow{text: m.text("Loading more…", "Cargando más…"), style: styles.muted})
	}
	return rows
}

// searchTableRows renders the results, including one localized row for each
// loading, empty, error and unauthorized state.
func (m *playerModel) searchTableRows(width int, styles playerStyles, room int) []playerRow {
	search := m.search
	loading := m.text("Searching…", "Buscando…")
	failed := m.text("Could not load search results. Enter: retry", "No se pudieron cargar los resultados. Enter: reintentar")
	empty := m.text("No results for this search", "No hay resultados para esta búsqueda")
	switch {
	case m.library.source == nil:
		return []playerRow{{text: m.text("Spotify search is not connected", "La búsqueda de Spotify no está conectada"), style: styles.muted}}
	case search.loading && len(search.tracks) == 0:
		return []playerRow{{text: loading, style: styles.muted}}
	case search.err != nil && len(search.tracks) == 0:
		return []playerRow{{text: failed, style: styles.failure}}
	case search.loaded && len(search.tracks) == 0:
		return []playerRow{{text: empty, style: styles.muted}}
	}
	columns := m.tableColumns(width)
	start := max(0, search.cursor-room+1)
	rows := make([]playerRow, 0, room)
	for i := start; i < len(search.tracks) && len(rows) < room; i++ {
		track := search.tracks[i]
		rows = append(rows, m.tableRow(columns, i, i == search.cursor,
			playerTrack{Title: track.Name, Artist: track.Artist, DurationMS: track.DurationMS}, styles))
	}
	if len(rows) < room {
		switch {
		case search.loading:
			rows = append(rows, playerRow{text: m.text("Loading more…", "Cargando más…"), style: styles.muted})
		case search.err != nil:
			rows = append(rows, playerRow{text: failed, style: styles.failure})
		}
	}
	return rows
}

func (m *playerModel) tableRow(columns []playerColumn, index int, selected bool, track playerTrack, styles playerStyles) playerRow {
	number := fmt.Sprintf(" %d", index+1)
	if selected {
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
	if selected && m.focus == playerFocusTable {
		style = styles.accent
	}
	return playerRow{text: strings.Join(cells, " "), style: style}
}
