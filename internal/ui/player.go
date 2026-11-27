package ui

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/dcaceresc/pulso/internal/config"
	"github.com/dcaceresc/pulso/internal/soloist"
	"github.com/dcaceresc/pulso/internal/spotify"
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
	playerNarrowWidth = 60 // Below this the table and bar keep only their essential columns.
	playerShortTable  = 20 // Below this the table keeps only its title column.
	playerSwitcherKey = 40 // Below this the library switcher uses the short key label.
	playerMinBodyRoom = 4  // Content wins over separator rules in short windows.
	playerSeekStepMS  = 10_000
	playerTickEvery   = time.Second
)

type playerViewState struct {
	state playerState
	ok    bool
}

// savePhase is the step of the save flow the chooser is showing. The panel owns
// the keyboard while it is not idle.
type savePhase uint8

const (
	saveIdle savePhase = iota
	saveChoosing
	// saveScopeOffered waits for the explicit approval an older read-only session
	// needs before it can change a playlist.
	saveScopeOffered
	saveAuthorization
	saveSaving
)

// saveNotice is the outcome of the last attempt: one quiet row that never
// carries a message Spotify sent.
type saveNotice uint8

const (
	saveNoticeNone saveNotice = iota
	saveNoticeSaved
	saveNoticeFailed
	saveNoticeNotEditable
	saveNoticeDeclined
)

// savePanel is the save-to-playlist action: the writer the catalog handed over,
// the chooser cursor, the playlist already chosen and the last notice.
type savePanel struct {
	writer spotify.PlaylistWriter
	phase  savePhase
	cursor int
	target libraryPlaylist

	authURL string
	// generation fences the answers of one attempt, so a late reply never
	// overwrites a newer one.
	generation uint64
	// retried records that the single retry after an approval was already used.
	retried bool
	// trackURI es la pista confirmada con Enter: el reintento la conserva.
	trackURI string
	work     context.Context
	cancel   context.CancelFunc
	events   <-chan saveAuthMsg

	notice     saveNotice
	noticeName string
}

// saveResultMsg carries the answer to one save request, fenced by generation.
type saveResultMsg struct {
	generation uint64
	name       string
	err        error
}

// saveAuthMsg reports the approval link and its eventual result, like the
// wizard's own login-link presentation.
type saveAuthMsg struct {
	generation uint64
	url        string
	writer     spotify.PlaylistWriter
	done       bool
}

// profileSource is the catalog read the top bar needs: the name of the account
// that owns the session. It stays optional so a catalog that cannot answer
// keeps the muted placeholder.
type profileSource interface {
	Profile(context.Context) (string, error)
}

// profileSourceOf accepts a catalog only when it can answer the account name; a
// typed nil catalog stays absent.
func profileSourceOf(api CatalogAPI) profileSource {
	if api == nil {
		return nil
	}
	source, ok := api.(profileSource)
	if !ok {
		return nil
	}
	if value := reflect.ValueOf(source); value.Kind() == reflect.Pointer && value.IsNil() {
		return nil
	}
	return source
}

// playerProfileMsg carries the once-per-catalog account name answer.
type playerProfileMsg struct {
	generation uint64
	name       string
	err        error
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
	ticking         bool
	wantShuffle     *bool
	wantRepeat      string
	modeSession     uint64

	// The top bar's account name is read once per installed catalog, so a
	// failed read never repeats on every event or keystroke. profileKnown marks
	// that an answer arrived; an empty name then leaves no label at all.
	profile           profileSource
	profileName       string
	profileKnown      bool
	profileAsked      bool
	profileGeneration uint64

	// Workspace focus and the Soloist queue cursor. The library keeps its own
	// per-panel cursors for playlists and their tracks; the search state keeps
	// the cursor of the result table.
	focus         playerPanel
	tableSelected int
	query         string
	library       *libraryState
	search        *searchState
	// help reports whether the full-screen help view owns the keyboard.
	help bool
	// clock is the source of the interpolated playhead; tests replace it.
	clock func() time.Time
	// save guarda la pista en reproducción en una lista elegida. Solo tiene
	// sentido con un catálogo que puede escribir; el resto del tiempo la acción
	// no se anuncia ni responde.
	saveTo savePanel
	// openBrowser presenta el enlace de aprobación; las pruebas lo reemplazan.
	openBrowser func(string) error
}

func NewPlayerModel(ctx context.Context, states <-chan soloist.State, commands chan<- soloist.Command, width, height int) tea.Model {
	return NewPlayerModelWithPreferences(ctx, states, commands, width, height, config.DefaultPreferences(), nil, "", nil)
}

// NewPlayerModelWithPreferences builds the playback workspace. The Spotify
// catalog is optional: without authorize, the library explains the connection
// instead, and playback still works.
func NewPlayerModelWithPreferences(ctx context.Context, states <-chan soloist.State, commands chan<- soloist.Command, width, height int, p config.Preferences, save func(config.Preferences) error, clientID string, authorize func(context.Context, string, func(string)) (CatalogAPI, error)) tea.Model {
	return NewPlayerModelWithCatalog(ctx, states, commands, width, height, p, save, nil, authorize, clientID)
}

func NewPlayerModelWithCatalog(ctx context.Context, states <-chan soloist.State, commands chan<- soloist.Command, width, height int, p config.Preferences, save func(config.Preferences) error, api CatalogAPI, authorize func(context.Context, string, func(string)) (CatalogAPI, error), knownClientID string) tea.Model {
	ctx, cancel := context.WithCancel(ctx)
	m := &playerModel{
		ctx: ctx, cancel: cancel, states: states, commands: commands,
		width: width, height: height, preferences: p.Normalized(), save: save,
		focus: playerFocusTable, clock: time.Now,
	}
	clientID := ""
	if authorize != nil {
		clientID = knownClientID
		if clientID == "" {
			clientID = spotify.LoadClientID()
		}
	}
	// The setup handoff passes the authorized catalog; when it can serve
	// library pages the sidebar, the table and the search all read it directly.
	m.library = newLibraryState(ctx, librarySourceOf(api), authorize, clientID)
	m.search = newSearchState(ctx, m.library.source)
	m.profile = profileSourceOf(api)
	m.saveTo.writer = playlistWriterOf(api)
	m.openBrowser = spotify.OpenBrowser
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
	// The account name is asked once, as soon as an authorized catalog exists.
	return playerCommands(state, library, m.fetchProfile())
}

// playerCommands joins the scheduled requests: no command stays a single
// command, so a view that only awaits state never sees an empty batch.
func playerCommands(commands ...tea.Cmd) tea.Cmd {
	kept := make([]tea.Cmd, 0, len(commands))
	for _, command := range commands {
		if command != nil {
			kept = append(kept, command)
		}
	}
	switch len(kept) {
	case 0:
		return nil
	case 1:
		return kept[0]
	}
	return tea.Batch(kept...)
}

// fetchProfile asks the catalog for the account name at most once per installed
// catalog, so a failure leaves the placeholder without repeating the request.
func (m *playerModel) fetchProfile() tea.Cmd {
	if m.profile == nil || m.profileAsked || m.ctx.Err() != nil {
		return nil
	}
	m.profileAsked = true
	ctx, generation, source := m.ctx, m.profileGeneration, m.profile
	return func() tea.Msg {
		if ctx.Err() != nil {
			return nil
		}
		name, err := source.Profile(ctx)
		return playerProfileMsg{generation: generation, name: name, err: err}
	}
}

// installProfile adopts the catalog that authorization just unlocked and asks
// for the account name again: a new Spotify session may be another account.
func (m *playerModel) installProfile(api CatalogAPI) tea.Cmd {
	source := profileSourceOf(api)
	if source == nil {
		return nil
	}
	m.profile, m.profileName, m.profileKnown, m.profileAsked = source, "", false, false
	m.profileGeneration++
	return m.fetchProfile()
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
		auth, isAuth := msg.(libraryAuthMsg)
		// Una autorización solo instala el catálogo si la generación sigue viva.
		accepted := isAuth && auth.done && auth.api != nil && m.library.current(auth.generation)
		cmd := m.library.update(msg)
		// Authorization can install the library source after startup.
		m.search.source = m.library.source
		// Una página nueva puede alargar la lista que muestra el selector.
		m.saveTo.cursor = playerIndex(m.saveTo.cursor, len(m.library.playlists))
		if accepted {
			// El catálogo aprobado también trae la mitad de escritura, si la tiene.
			m.saveTo.writer = playlistWriterOf(auth.api)
			return m, playerCommands(cmd, m.installProfile(auth.api))
		}
		return m, cmd
	case saveResultMsg, saveAuthMsg:
		return m, m.updateSave(msg)
	case playerProfileMsg:
		if m.quit || msg.generation != m.profileGeneration || m.ctx.Err() != nil {
			break
		}
		// Un fallo deja el marcador apagado y no se muestra nunca en crudo.
		if msg.err != nil {
			return m, nil
		}
		m.profileKnown = true
		m.profileName = playerText(msg.name)
		return m, nil
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
		// Un aviso nuevo de Soloist no queda escondido por el último guardado.
		if playerText(msg.state.Notice) != playerText(m.state.Notice) {
			m.saveTo.notice = saveNoticeNone
		}
		m.state = msg.state
		// Sin una pista en reproducción el selector se queda sin qué guardar.
		if m.saveTo.phase != saveIdle && !soloist.IsTrackURI(m.state.Track.URI) {
			m.closeSave()
		}
		m.reconcileModes()
		m.clampSelections()
		// A playing session needs a second of wall time to advance its playhead.
		return m, tea.Batch(m.awaitState(), m.armTick())
	case playerTickMsg:
		return m, m.tick()
	case tea.KeyMsg:
		// The help view swallows every key but its own: the panel underneath
		// never acts by accident.
		if m.help {
			m.handleHelpKey(msg)
			return m, nil
		}
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
	return m.preferences.Text(english, spanish)
}

func (m *playerModel) handleKey(key tea.KeyMsg) tea.Cmd {
	// F1 opens the help view even inside the text field; `?` is text there and
	// opens it from every other panel.
	if key.Type == tea.KeyF1 {
		m.help = true
		return nil
	}
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
	if key.String() == "?" {
		m.help = true
		return nil
	}
	// El selector de lista reclama sus teclas mientras está abierto.
	if cmd, handled := m.saveKey(key); handled {
		return cmd
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
		m.focus = playerFocusSidebar
		return nil
	case "a":
		return m.openSave()
	}
	// Shuffle, repeat and the bar's own seek keys come next; s and r reach
	// Soloist from every panel, while the arrows stay with the playing bar.
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

// handlePlayingKey owns shuffle and repeat from every panel, and the seek
// arrows from the playing bar, which is the only place they are advertised.
// It reports whether the key belonged to those bindings.
func (m *playerModel) handlePlayingKey(key tea.KeyMsg) bool {
	var command playerCommand
	switch {
	case key.Type == tea.KeyLeft, key.Type == tea.KeyRight:
		if m.focus != playerFocusPlaying {
			return false
		}
		delta := int64(playerSeekStepMS)
		if key.Type == tea.KeyLeft {
			delta = -delta
		}
		command = playerCommand{Name: "seek", PositionMS: playerSeekTarget(m.playhead(), delta, m.state.Track.DurationMS)}
	case key.String() == "s":
		command = playerCommand{Name: "set_shuffle", Shuffle: !m.shuffleWanted()}
	case key.String() == "r":
		command = playerCommand{Name: "set_repeat", Repeat: playerNextRepeat(m.repeatWanted())}
	default:
		return false
	}
	if m.commands == nil || !m.state.Connected {
		return true
	}
	if m.send(command) {
		switch command.Name {
		case "seek":
			// Move the bar at once; Soloist confirms with position_sync.
			m.state.PositionMS, m.state.AnchorAt = command.PositionMS, m.now()
		case "set_shuffle":
			// Remember the intent until Soloist reports matching options, and ask
			// for a fresh snapshot so a lost event cannot leave the toggle stuck.
			wanted := command.Shuffle
			m.wantShuffle = &wanted
			m.send(playerCommand{Name: "get_state"})
		case "set_repeat":
			m.wantRepeat = command.Repeat
			m.send(playerCommand{Name: "get_state"})
		}
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
	case key.String() == "S":
		if uri := m.playlistShuffleURI(); uri != "" && m.state.Connected && m.commands != nil {
			if m.send(playerCommand{Name: soloist.PlayPlaylistShuffle, URI: uri}) {
				m.wantShuffle = nil
			}
		}
		return nil, true
	case key.Type == tea.KeyEnter && m.focus == playerFocusSidebar:
		return m.activatePlaylist(), true
	case key.Type == tea.KeyEnter && m.focus == playerFocusTable && m.search.active:
		// The unauthorized row advertises Enter: connect, so Enter starts the
		// same authorization the sidebar offers.
		if m.library.source == nil {
			return m.activatePlaylist(), true
		}
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

func (m *playerModel) playlistShuffleURI() string {
	var uri string
	switch {
	case m.focus == playerFocusSidebar:
		if playlist, ok := m.library.selectedPlaylist(); ok {
			uri = playlist.URI
		}
	case m.focus == playerFocusTable && !m.search.active && m.library.open():
		uri = m.library.playlist.URI
	}
	if soloist.IsPlaylistURI(uri) {
		return uri
	}
	return ""
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

// saveAvailable reports whether the playing item can be saved right now: a
// catalog that can write, a playlist listing and the track URI Soloist reported.
func (m *playerModel) saveAvailable() bool {
	return m.saveTo.writer != nil && m.library != nil && m.library.source != nil &&
		m.state.Connected && soloist.IsTrackURI(m.state.Track.URI)
}

// saveKey owns the panel's keys while it is open. Ctrl+C still falls through so
// the workspace can always quit.
func (m *playerModel) saveKey(key tea.KeyMsg) (tea.Cmd, bool) {
	if m.saveTo.phase == saveIdle || key.Type == tea.KeyCtrlC {
		return nil, false
	}
	switch key.Type {
	case tea.KeyEsc:
		m.closeSave()
		return nil, true
	case tea.KeyEnter:
		return m.confirmSave(), true
	}
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
	if delta != 0 && m.saveTo.phase == saveChoosing {
		return m.moveSave(delta), true
	}
	return nil, true
}

// openSave shows the chooser for the playing track over the playlist page the
// library already loaded.
func (m *playerModel) openSave() tea.Cmd {
	if !m.saveAvailable() {
		return nil
	}
	m.saveTo.phase = saveChoosing
	m.saveTo.cursor = playerIndex(m.library.sidebarSelected, len(m.library.playlists))
	m.saveTo.target = libraryPlaylist{}
	m.saveTo.authURL = ""
	m.saveTo.notice, m.saveTo.noticeName = saveNoticeNone, ""
	m.saveTo.retried = false
	if len(m.library.playlists) == 0 && m.library.notice == libraryNoticeLoadFailed {
		return m.library.fetchPlaylists(0)
	}
	return nil
}

// moveSave moves the chooser cursor with the library's habits and pages in more
// playlists when the cursor passes the loaded rows.
func (m *playerModel) moveSave(delta int) tea.Cmd {
	library := m.library
	if len(library.playlists) == 0 {
		return nil
	}
	last := len(library.playlists) - 1
	if library.playlistMore {
		last++
	}
	m.saveTo.cursor = min(max(0, m.saveTo.cursor+delta), last)
	// Se pagina solo cuando la biblioteca no tiene una petición en curso: pedir
	// otra página cancelaría la carga de canciones que ya está esperando.
	if m.saveTo.cursor > len(library.playlists)-1 && library.playlistMore && !library.loadingPlaylists && !library.loadingTracks {
		return library.fetchPlaylists(library.playlistOffset)
	}
	return nil
}

// saveTargetPlaylist is the playlist under the chooser cursor.
func (m *playerModel) saveTargetPlaylist() (libraryPlaylist, bool) {
	if m.library == nil || len(m.library.playlists) == 0 {
		return libraryPlaylist{}, false
	}
	return m.library.playlists[playerIndex(m.saveTo.cursor, len(m.library.playlists))], true
}

// saveTargetName is the playlist the title and the notices name.
func (m *playerModel) saveTargetName() string {
	playlist := m.saveTo.target
	if m.saveTo.phase == saveChoosing {
		if current, ok := m.saveTargetPlaylist(); ok {
			playlist = current
		}
	}
	if name := playerText(playlist.Name); name != "" {
		return name
	}
	if playlist.URI != "" {
		return m.text("Untitled playlist", "Lista sin título")
	}
	return ""
}

// confirmSave chooses the playlist or starts the offered approval.
func (m *playerModel) confirmSave() tea.Cmd {
	switch m.saveTo.phase {
	case saveChoosing:
		playlist, ok := m.saveTargetPlaylist()
		if !ok {
			return nil
		}
		m.saveTo.target = playlist
		m.saveTo.trackURI = m.state.Track.URI
		return m.requestSave(m.saveTo.writer, playlist)
	case saveScopeOffered:
		return m.reauthorizeWrites()
	}
	return nil
}

// requestSave asks Spotify to append the playing track to the chosen playlist.
// Nothing reads as saved until the answer arrives.
func (m *playerModel) requestSave(writer spotify.PlaylistWriter, playlist libraryPlaylist) tea.Cmd {
	trackURI := m.saveTo.trackURI
	if writer == nil || m.ctx.Err() != nil || !soloist.IsTrackURI(trackURI) {
		return nil
	}
	m.saveTo.generation++
	m.saveTo.phase = saveSaving
	name := playerText(playlist.Name)
	if name == "" {
		name = m.text("Untitled playlist", "Lista sin título")
	}
	ctx, generation, uri := m.ctx, m.saveTo.generation, playlist.URI
	return func() tea.Msg {
		if ctx.Err() != nil {
			return nil
		}
		_, err := writer.AddToPlaylist(ctx, uri, trackURI)
		return saveResultMsg{generation: generation, name: name, err: err}
	}
}

// reauthorizeWrites repeats the approval with the write scopes the saved session
// lacks, using the wizard's browser-and-link presentation.
func (m *playerModel) reauthorizeWrites() tea.Cmd {
	writer := m.saveTo.writer
	if writer == nil || m.ctx.Err() != nil {
		return nil
	}
	ctx, cancel := context.WithCancel(m.ctx)
	m.saveTo.generation++
	generation := m.saveTo.generation
	events := make(chan saveAuthMsg, 2)
	m.saveTo.work, m.saveTo.cancel, m.saveTo.events = ctx, cancel, events
	m.saveTo.phase, m.saveTo.authURL = saveAuthorization, ""
	open := m.openBrowser
	return func() tea.Msg {
		if ctx.Err() != nil {
			return nil
		}
		go func() {
			var published sync.Once
			updated, err := writer.ReauthorizeWrites(ctx, func(raw string) {
				link := catalogBrowserURL(raw)
				if link == "" || ctx.Err() != nil {
					return
				}
				if open != nil {
					_ = open(link)
				}
				published.Do(func() { events <- saveAuthMsg{generation: generation, url: link} })
			})
			if err != nil {
				updated = nil
			}
			events <- saveAuthMsg{generation: generation, writer: updated, done: true}
		}()
		return saveAwaitAuth(ctx, events)()
	}
}

// saveAwaitAuth reads the next approval event without blocking the program.
func saveAwaitAuth(ctx context.Context, events <-chan saveAuthMsg) tea.Cmd {
	return func() tea.Msg {
		select {
		case <-ctx.Done():
			return nil
		case msg := <-events:
			return msg
		}
	}
}

// closeSave dismisses the panel. A save already in flight still reports its
// notice; a pending approval is abandoned.
func (m *playerModel) closeSave() {
	if m.saveTo.phase == saveAuthorization {
		if m.saveTo.cancel != nil {
			m.saveTo.cancel()
		}
		// Descarta la respuesta de una aprobación abandonada.
		m.saveTo.generation++
	}
	m.saveTo.work, m.saveTo.cancel, m.saveTo.events = nil, nil, nil
	m.saveTo.phase, m.saveTo.authURL = saveIdle, ""
}

// updateSave applies one fenced answer of the save flow. A failure never shows
// what Spotify answered and never touches playback.
func (m *playerModel) updateSave(msg tea.Msg) tea.Cmd {
	if m.quit || m.ctx.Err() != nil {
		return nil
	}
	switch msg := msg.(type) {
	case saveResultMsg:
		if msg.generation != m.saveTo.generation {
			return nil
		}
		m.saveTo.authURL = ""
		switch {
		case msg.err == nil:
			m.saveTo.phase, m.saveTo.notice, m.saveTo.noticeName = saveIdle, saveNoticeSaved, msg.name
		case errors.Is(msg.err, spotify.ErrPlaylistNotEditable):
			m.saveTo.phase, m.saveTo.notice, m.saveTo.noticeName = saveIdle, saveNoticeNotEditable, msg.name
		case errors.Is(msg.err, spotify.ErrSpotifyWriteScope) && !m.saveTo.retried && m.saveTo.target.URI != "":
			// Falta el permiso de escritura: se ofrece aprobarlo otra vez.
			m.saveTo.phase = saveScopeOffered
		default:
			m.saveTo.phase, m.saveTo.notice, m.saveTo.noticeName = saveIdle, saveNoticeFailed, msg.name
		}
		return nil
	case saveAuthMsg:
		if msg.generation != m.saveTo.generation {
			return nil
		}
		if !msg.done {
			m.saveTo.authURL = msg.url
			return saveAwaitAuth(m.saveTo.work, m.saveTo.events)
		}
		if m.saveTo.cancel != nil {
			m.saveTo.cancel()
		}
		m.saveTo.work, m.saveTo.cancel, m.saveTo.events, m.saveTo.authURL = nil, nil, nil, ""
		if msg.writer == nil {
			m.saveTo.phase, m.saveTo.notice = saveIdle, saveNoticeDeclined
			m.saveTo.noticeName = m.saveTargetName()
			return nil
		}
		return m.installApproved(msg.writer)
	}
	return nil
}

// installApproved adopta el catálogo aprobado: la lista y la cuenta lo usan, el
// reemplazado deja de guardar la sesión y el guardado confirmado se reintenta.
func (m *playerModel) installApproved(writer spotify.PlaylistWriter) tea.Cmd {
	for _, replaced := range []any{m.saveTo.writer, m.library.source} {
		if releaser, ok := replaced.(interface{ ReleaseSession() }); ok {
			releaser.ReleaseSession()
		}
	}
	m.saveTo.writer, m.saveTo.retried = writer, true
	retry := m.requestSave(writer, m.saveTo.target)
	api, ok := writer.(CatalogAPI)
	if !ok {
		return retry
	}
	if source := librarySourceOf(api); source != nil {
		m.library.source, m.search.source, m.library.started = source, source, true
	}
	return playerCommands(m.installProfile(api), retry)
}

// saveTitle names the action and the playlist that would receive the track, so
// the target is visible before confirming.
func (m *playerModel) saveTitle() string {
	title := m.text("Save to playlist", "Guardar en lista")
	if name := m.saveTargetName(); name != "" {
		title += " · " + name
	}
	return title
}

// saveRows renders the panel: the chooser, the offer to approve the missing
// permission, or the wait for Spotify.
func (m *playerModel) saveRows(styles playerStyles, room int) []playerRow {
	muted := func(text string) playerRow { return playerRow{text: text, style: styles.muted} }
	switch m.saveTo.phase {
	case saveSaving:
		return []playerRow{muted(playerMarked(playerMarkNotice, m.text("Saving…", "Guardando…")))}
	case saveAuthorization:
		rows := []playerRow{muted(playerMarked(playerMarkNotice, m.text("Waiting for Spotify…", "Esperando a Spotify…")))}
		if m.saveTo.authURL != "" {
			rows = append(rows, playerRow{text: "  " + m.saveTo.authURL, style: styles.muted})
		}
		return rows
	case saveScopeOffered:
		return []playerRow{{text: playerMarked(playerMarkFailure, m.text(
			"Spotify needs your approval to change playlists. Enter: authorize again.",
			"Spotify necesita tu aprobación para cambiar listas. Enter: autorizar otra vez.")), style: styles.failure}}
	}
	library := m.library
	switch {
	case library.notice == libraryNoticeLoadFailed && len(library.playlists) == 0:
		return []playerRow{{text: m.markError(), style: styles.failure}}
	case !library.loadedPlaylists && len(library.playlists) == 0:
		return []playerRow{muted(m.markLoading())}
	case len(library.playlists) == 0:
		return []playerRow{muted(m.markEmpty())}
	}
	start := max(0, m.saveTo.cursor-room+1)
	rows := make([]playerRow, 0, room)
	for i := start; i < len(library.playlists) && len(rows) < room; i++ {
		playlist := library.playlists[i]
		marker, style := "  ", lipgloss.NewStyle()
		if i == m.saveTo.cursor {
			marker, style = "> ", styles.accent
		}
		name := playerText(playlist.Name)
		if name == "" {
			name = m.text("Untitled playlist", "Lista sin título")
		}
		rows = append(rows, playerRow{text: marker + name, style: style})
	}
	if library.loadingPlaylists && len(rows) < room {
		rows = append(rows, playerRow{text: m.markLoading(), style: styles.muted})
	}
	return rows
}

// saveNoticeText is the last outcome, named for the playlist it touched.
func (m *playerModel) saveNoticeText() string {
	switch m.saveTo.notice {
	case saveNoticeSaved:
		return fmt.Sprintf(m.text("Saved to %s.", "Guardada en %s."), m.saveTo.noticeName)
	case saveNoticeFailed:
		return fmt.Sprintf(m.text("Could not save to %s.", "No se pudo guardar en %s."), m.saveTo.noticeName)
	case saveNoticeNotEditable:
		return fmt.Sprintf(m.text("Could not save to %s: this account cannot change that playlist.",
			"No se pudo guardar en %s: esta cuenta no puede cambiar esa lista."), m.saveTo.noticeName)
	case saveNoticeDeclined:
		return m.text("Spotify authorization did not complete; nothing was saved.",
			"La autorización de Spotify no se completó; no se guardó nada.")
	}
	return ""
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
	m.preferences = m.preferences.Normalized()
	m.saved = false
	if m.save != nil && m.save(m.preferences) == nil {
		m.saved = true
	}
	return nil
}

var playerLabel = lipgloss.NewStyle().Bold(true)

// playerText sanea los metadatos visibles: sin sintaxis de terminal, sin
// caracteres de control y solo con espacios simples.
func playerText(text string) string {
	return soloist.SanitizeText(text)
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

// armTick starts the single tick chain. Every Soloist event used to arm
// another one, so the screen was redrawn once per second per event.
func (m *playerModel) armTick() tea.Cmd {
	if m.ticking || m.quit || m.ended || !m.tickWanted() {
		return nil
	}
	m.ticking = true
	return m.nextTick()
}

// tick continues the armed chain, dropping it as soon as it is pointless.
func (m *playerModel) tick() tea.Cmd {
	if !m.ticking {
		return nil
	}
	if !m.tickWanted() {
		m.ticking = false
		return nil
	}
	return m.nextTick()
}

func (m *playerModel) tickWanted() bool {
	return m.ctx.Err() == nil && m.state.Connected && playerPlaying(m.state.Status)
}

func (m *playerModel) nextTick() tea.Cmd {
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
	// A paused session may still carry the last speed; only advance while playing.
	if anchor, speed := m.state.AnchorAt, m.state.Speed; !anchor.IsZero() && speed > 0 && playerPlaying(m.state.Status) {
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

// playerAccentSurface is the only style that paints a background: accent text
// on the accent colour. Muted text is never placed on it, so a state is never
// dim-on-accent.
func playerAccentSurface(palette themePalette) lipgloss.Style {
	return lipgloss.NewStyle().Bold(true).Foreground(palette.AccentText).Background(palette.Accent)
}

func (m *playerModel) styles() playerStyles {
	palette := m.preferences.Palette()
	return playerStyles{
		accent:  lipgloss.NewStyle().Bold(true).Foreground(palette.Accent),
		muted:   lipgloss.NewStyle().Foreground(palette.Muted),
		label:   lipgloss.NewStyle().Bold(true).Foreground(palette.Accent),
		failure: lipgloss.NewStyle().Foreground(palette.Error),
		banner:  playerAccentSurface(palette),
	}
}

// State rows pair a single-width glyph and a localized word with a colour, so
// no state depends on colour alone. There are no emoji.
const (
	playerMarkLoading = "…"
	playerMarkEmpty   = "○"
	playerMarkFailure = "!"
	playerMarkNotice  = "●"
)

func playerMarked(mark, text string) string { return mark + " " + text }

// The workspace reuses one sentence per concept in every panel, so the same
// state reads the same in the sidebar, the table and the search results.
func (m *playerModel) markLoading() string {
	return playerMarked(playerMarkLoading, m.text("Loading", "Cargando"))
}

func (m *playerModel) markEmpty() string {
	return playerMarked(playerMarkEmpty, m.text("Nothing here yet", "Aún no hay nada"))
}

func (m *playerModel) markError() string {
	return playerMarked(playerMarkFailure, m.text("Could not load Spotify data. Enter: retry.",
		"No se pudieron cargar los datos de Spotify. Enter: reintentar."))
}

func (m *playerModel) markUnauthorized() string {
	return playerMarked(playerMarkNotice, m.text("No Spotify. Enter: connect",
		"Sin Spotify. Enter: conectar"))
}

func (m *playerModel) markRestricted() string {
	return playerMarked(playerMarkFailure, m.text("Playlist contents unavailable. p: play the playlist.",
		"Contenido de la lista no disponible. p: reproducir la lista."))
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
	if m.help {
		return m.helpView()
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

// searchRow is the top bar: the label, the live buffer with its cursor, and the
// muted account label on the right.
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
	account := m.profileText()
	if account == "" {
		return left
	}
	// El nombre real se recorta al hueco que deja el campo de búsqueda.
	right := styles.muted.Render(playerFit(account, max(0, width-lipgloss.Width(left)-2)))
	if lipgloss.Width(left)+lipgloss.Width(right)+2 > width {
		return left
	}
	return left + strings.Repeat(" ", width-lipgloss.Width(left)-lipgloss.Width(right)) + right
}

// profileText es la etiqueta de cuenta de la barra superior: el marcador
// apagado hasta que llega el nombre, y luego `@nombre`.
func (m *playerModel) profileText() string {
	if m.profileName != "" {
		return "@" + m.profileName
	}
	if m.profileKnown {
		// La cuenta existe pero no publica nombre: no hay etiqueta que mostrar.
		return ""
	}
	return m.text("@user", "@usuario")
}

func (m *playerModel) playingRows(width int, styles playerStyles) []playerRow {
	glyph := "⏸"
	title := m.text("Nothing playing", "No hay nada en reproducción")
	if !m.loaded {
		title = m.text("Loading", "Cargando")
	} else if !m.state.Connected {
		title = m.text("Disconnected", "Sin conexión")
	} else if track := playerText(m.state.Track.Title); track != "" {
		title = track
	}
	if m.loaded && m.state.Connected && strings.EqualFold(strings.TrimSpace(m.state.Status), "playing") {
		glyph = "▶"
	}
	// The artist joins the title only when the bar has room for it; below the
	// narrow breakpoint the bar keeps the title and the volume.
	if artist := playerText(m.state.Track.Artist); artist != "" && width >= playerNarrowWidth && m.loaded && m.state.Connected {
		title += " · " + artist
	}
	marker := "  "
	if m.focus == playerFocusPlaying {
		marker = "> "
	}
	if playlist := playerText(m.state.PlaylistContext); m.loaded && m.state.Connected && playlist != "" {
		title += " · " + m.text("Playlist: ", "Lista: ") + playlist
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
	if device := playerRemoteDevice(m.state.Device); device != "" {
		volume += "  " + m.text("Device ", "Dispositivo ") + device
	}
	rows = append(rows, playerRow{text: playerSpread(progress, volume, width), style: styles.muted})
	return rows
}

// playerLocalDevice es el nombre que Soloist reporta para la reproducción
// local; la barra lo omite porque es la norma y solo hace ruido.
const playerLocalDevice = "Pulso"

// playerRemoteDevice devuelve el dispositivo solo cuando no es el local.
func playerRemoteDevice(device string) string {
	device = playerText(device)
	if strings.EqualFold(device, playerLocalDevice) {
		return ""
	}
	return device
}

// shuffleMode renders the shuffle state Soloist last reported.
func (m *playerModel) shuffleMode() string {
	if !m.state.OptionsKnown && m.wantShuffle == nil {
		return "—"
	}
	if m.shuffleWanted() {
		return m.text("on", "activado")
	}
	return m.text("off", "apagado")
}

// repeatMode renders off, all (context) or one (track).
func (m *playerModel) repeatMode() string {
	if !m.state.OptionsKnown && m.wantRepeat == "" {
		return "—"
	}
	switch m.repeatWanted() {
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
	if text := m.saveNoticeText(); text != "" {
		notice = text
	}
	switch m.state.PlaylistShuffle {
	case soloist.PlaylistShuffleWaiting:
		notice = m.text("Waiting for playlist confirmation…", "Esperando confirmación de la lista…")
	case soloist.PlaylistShuffleUnconfirmed:
		if m.state.Connected {
			notice = m.text("Playlist shuffle not applied.", "No se aplicó el aleatorio de la lista.")
		} else {
			notice = m.text("Playlist shuffle not applied. Retry after reconnecting.", "No se aplicó el aleatorio de la lista. Reinténtalo al reconectar.")
		}
	}
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
	hints := m.hintRows(width)
	if budget := m.hintBudget(width, styles, len(rows)); len(hints) > budget {
		hints = hints[:budget]
	}
	for _, hint := range hints {
		rows = append(rows, playerRow{text: hint, style: styles.muted})
	}
	return rows
}

// hintBudget keeps the footer inside the window: the frame, the playing bar,
// any notice row and one content row come first. One footer row is always kept
// so the keys stay discoverable.
func (m *playerModel) hintBudget(width int, styles playerStyles, noticeRows int) int {
	fixed := 2 + len(m.playingRows(width, styles)) + noticeRows
	return max(1, m.height-fixed-1)
}

// playerHint is one footer binding: the key as it is pressed and the localized
// action it performs. Labels stay one word so a pair parses as `Key label`.
type playerHint struct {
	key   string
	label string
}

// playerHintLines wraps bindings into sqlterm's flat footer shape: one leading
// space, `Key label` pairs, two spaces between pairs and one trailing space.
// Narrow windows wrap onto another row of the same shape instead of falling
// back to shortened labels.
func playerHintLines(hints []playerHint, width int) []string {
	if width <= 0 || len(hints) == 0 {
		return nil
	}
	lines := make([]string, 0, len(hints))
	line := ""
	flush := func() {
		if line != "" {
			lines = append(lines, line+" ")
			line = ""
		}
	}
	for _, hint := range hints {
		item := hint.key + " " + hint.label
		if line == "" {
			line = " " + item
			continue
		}
		// A pair that cannot fit even alone stays on its own row and is
		// truncated by the row renderer, never split across rows.
		if lipgloss.Width(line)+2+lipgloss.Width(item)+1 <= width {
			line += "  " + item
			continue
		}
		flush()
		line = " " + item
	}
	flush()
	return lines
}

// hintPairs follows the focused panel and never advertises a key that does
// nothing there. Shuffle and repeat work from every panel but the text field,
// so only the playing bar advertises the seek arrows.
func (m *playerModel) hintPairs() []playerHint {
	if m.focus == playerFocusSearch {
		return []playerHint{
			{"Enter", m.text("search", "buscar")},
			{"Esc", m.text("leave", "salir")},
			{"Backspace", m.text("delete", "borrar")},
			{"F1", m.text("help", "ayuda")},
			{"F2", m.text("language", "idioma")},
			{"F3", m.text("theme", "tema")},
		}
	}
	// El selector de lista sustituye al pie mientras está abierto.
	if m.saveTo.phase != saveIdle {
		hints := make([]playerHint, 0, 4)
		switch m.saveTo.phase {
		case saveChoosing:
			hints = append(hints,
				playerHint{"↑↓/jk", m.text("playlist", "lista")},
				playerHint{"Enter", m.text("save", "guardar")})
		case saveScopeOffered:
			hints = append(hints, playerHint{"Enter", m.text("authorize", "autorizar")})
		}
		return append(hints,
			playerHint{"Esc", m.text("cancel", "cancelar")},
			playerHint{"F1", m.text("help", "ayuda")})
	}
	hints := []playerHint{{"Tab", m.text("focus", "panel")}}
	switch m.focus {
	case playerFocusSidebar:
		hints = append(hints,
			playerHint{"↑↓/jk", m.text("library", "biblioteca")},
			playerHint{"Enter", m.text("open", "abrir")},
			playerHint{"Space", m.text("play/pause", "pausa")},
			playerHint{"s", m.text("shuffle", "aleatorio")},
			playerHint{"r", m.text("repeat", "repetir")},
			playerHint{"+/-", m.text("volume", "volumen")},
			playerHint{"/", m.text("search", "buscar")})
	case playerFocusPlaying:
		hints = append(hints,
			playerHint{"Space", m.text("play/pause", "pausa")},
			playerHint{"←→", m.text("seek", "salto")},
			playerHint{"n/p", m.text("track", "pista")},
			playerHint{"s", m.text("shuffle", "aleatorio")},
			playerHint{"r", m.text("repeat", "repetir")},
			playerHint{"+/-", m.text("volume", "volumen")},
			playerHint{"/", m.text("search", "buscar")},
			playerHint{"l", m.text("library", "biblioteca")})
	default:
		hints = append(hints,
			playerHint{"↑↓/jk", m.text("move", "mover")},
			playerHint{"Enter", m.text("play", "reproducir")},
			playerHint{"Space", m.text("play/pause", "pausa")},
			playerHint{"s", m.text("shuffle", "aleatorio")},
			playerHint{"r", m.text("repeat", "repetir")},
			playerHint{"+/-", m.text("volume", "volumen")},
			playerHint{"/", m.text("search", "buscar")},
			playerHint{"l", m.text("library", "biblioteca")})
	}
	if m.saveAvailable() {
		hints = append(hints, playerHint{"a", m.text("save", "guardar")})
	}
	if m.playlistShuffleURI() != "" {
		hints = append(hints, playerHint{"S", m.text("shuffle-list", "mezclar-lista")})
	}
	return append(hints,
		playerHint{"F1", m.text("help", "ayuda")},
		playerHint{"F2", m.text("language", "idioma")},
		playerHint{"F3", m.text("theme", "tema")},
		playerHint{"q", m.text("quit", "salir")},
		playerHint{"Esc", m.text("back/quit", "volver/salir")})
}

// hintRows is the footer: the focused panel's pairs in the shared flat format.
func (m *playerModel) hintRows(width int) []string {
	return playerHintLines(m.hintPairs(), width)
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

// stackedRows is the breakpoint layout: one library switcher row above the table.
func (m *playerModel) stackedRows(width int, styles playerStyles, room int) []playerRow {
	rows := []playerRow{{text: m.librarySwitcher(width), style: styles.label}}
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
	return m.markEmpty()
}

// librarySwitcher is the stacked one-line sidebar: it names the current
// selection and the key that changes it.
func (m *playerModel) librarySwitcher(width int) string {
	left := m.text("LIBRARY: ", "BIBLIOTECA: ") + m.librarySelection()
	change := m.text("Tab: library", "Tab: biblioteca")
	if width < playerSwitcherKey {
		change = "Tab"
	}
	return playerSpread(left, change, width)
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

// libraryStateRows explains an unavailable library with the same localized
// sentence the other panels use for that concept.
func (m *playerModel) libraryStateRows(styles playerStyles) []playerRow {
	library := m.library
	muted := func(text string) playerRow { return playerRow{text: text, style: styles.muted} }
	switch {
	case library.canAuthorize():
		switch {
		case library.authURL != "":
			return []playerRow{{text: "  " + library.authURL, style: styles.muted}}
		case library.authorizing():
			return []playerRow{muted(playerMarked(playerMarkNotice, m.text("Waiting for Spotify…", "Esperando a Spotify…")))}
		case library.notice == libraryNoticeAuthFailed:
			return []playerRow{{text: playerMarked(playerMarkFailure, m.text("Spotify unavailable; playback works. Enter: retry.",
				"Spotify no disponible; la reproducción funciona. Enter: reintentar.")), style: styles.failure}}
		case library.notice == libraryNoticeClientID:
			return []playerRow{{text: playerMarked(playerMarkFailure, m.text("Restart Pulso and complete the Spotify step.",
				"Reinicia Pulso y completa el paso de Spotify.")), style: styles.failure}}
		default:
			return []playerRow{muted(m.markUnauthorized())}
		}
	case library.source == nil:
		return []playerRow{muted(m.markEmpty())}
	case library.notice == libraryNoticeLoadFailed && !library.loadedPlaylists:
		return []playerRow{{text: m.markError(), style: styles.failure}}
	case !library.loadedPlaylists && len(library.playlists) == 0:
		return []playerRow{muted(m.markLoading())}
	case library.loadedPlaylists && len(library.playlists) == 0:
		return []playerRow{muted(m.markEmpty())}
	}
	return nil
}

func (m *playerModel) mainTitle() string {
	if m.saveTo.phase != saveIdle {
		return m.saveTitle()
	}
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
// value ever overflows the region. Below the narrow breakpoint the table keeps
// only the number, the title and a short duration column.
func (m *playerModel) tableColumns(width int) []playerColumn {
	if width <= 0 {
		return nil
	}
	title, artist := m.text("Title", "Título"), m.text("Artist", "Artista")
	duration, short := m.text("Duration", "Duración"), m.text("Dur.", "Dur.")
	durationWidth, shortWidth := lipgloss.Width(duration), lipgloss.Width(short)
	switch {
	case width < 12:
		return []playerColumn{{kind: playerColumnTitle, label: title, width: width}}
	case width < playerShortTable:
		return []playerColumn{
			{kind: playerColumnNumber, label: "#", width: 2},
			{kind: playerColumnTitle, label: title, width: width - 3},
		}
	case width < playerNarrowWidth:
		return []playerColumn{
			{kind: playerColumnNumber, label: "#", width: 2},
			{kind: playerColumnTitle, label: title, width: width - 4 - shortWidth},
			{kind: playerColumnDuration, label: short, width: shortWidth},
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
	if m.saveTo.phase != saveIdle {
		return m.saveRows(styles, room)
	}
	if m.search.active {
		return m.searchTableRows(width, styles, room)
	}
	if m.library.open() {
		return m.libraryTableRows(width, styles, room)
	}
	switch {
	case !m.loaded:
		return []playerRow{{text: m.markLoading(), style: styles.muted}}
	case !m.state.Connected:
		return []playerRow{{text: m.text("Queue unavailable", "Cola no disponible"), style: styles.muted}}
	case len(m.state.Queue) == 0:
		return []playerRow{{text: m.markEmpty(), style: styles.muted}}
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
		return []playerRow{{text: m.markRestricted(), style: styles.failure}}
	case library.notice == libraryNoticeLoadFailed:
		return []playerRow{{text: m.markError(), style: styles.failure}}
	case !library.loadedTracks || (library.loadingTracks && len(library.tracks) == 0):
		return []playerRow{{text: m.markLoading(), style: styles.muted}}
	case len(library.tracks) == 0:
		return []playerRow{{text: m.markEmpty(), style: styles.muted}}
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
		rows = append(rows, playerRow{text: m.markLoading(), style: styles.muted})
	}
	return rows
}

// searchTableRows renders the results, including one shared localized row for
// each loading, empty, error and unauthorized state.
func (m *playerModel) searchTableRows(width int, styles playerStyles, room int) []playerRow {
	search := m.search
	switch {
	case m.library.source == nil:
		return []playerRow{{text: m.markUnauthorized(), style: styles.muted}}
	case search.loading && len(search.tracks) == 0:
		return []playerRow{{text: m.markLoading(), style: styles.muted}}
	case search.err != nil && len(search.tracks) == 0:
		return []playerRow{{text: m.markError(), style: styles.failure}}
	case search.loaded && len(search.tracks) == 0:
		return []playerRow{{text: m.markEmpty(), style: styles.muted}}
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
			rows = append(rows, playerRow{text: m.markLoading(), style: styles.muted})
		case search.err != nil:
			rows = append(rows, playerRow{text: m.markError(), style: styles.failure})
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

// shuffleWanted and repeatWanted prefer the value the human just asked for, so
// a toggle never sticks while Soloist's event is in flight or missing.
func (m *playerModel) shuffleWanted() bool {
	if m.wantShuffle != nil {
		return *m.wantShuffle
	}
	return m.state.Shuffle
}

func (m *playerModel) repeatWanted() string {
	if m.wantRepeat != "" {
		return m.wantRepeat
	}
	return m.state.Repeat
}

// reconcileModes drops an intent once Soloist reports it, and drops both when
// the session changes or disconnects, because the intent no longer applies.
func (m *playerModel) reconcileModes() {
	if !m.state.Connected || m.state.SessionID != m.modeSession {
		m.wantShuffle, m.wantRepeat = nil, ""
		m.modeSession = m.state.SessionID
		return
	}
	if m.wantShuffle != nil && m.state.OptionsKnown && m.state.Shuffle == *m.wantShuffle {
		m.wantShuffle = nil
	}
	if m.wantRepeat != "" && m.state.OptionsKnown && m.state.Repeat == m.wantRepeat {
		m.wantRepeat = ""
	}
}
