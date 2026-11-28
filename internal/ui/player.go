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

// playerPanel es una zona enfocable del espacio de trabajo. Cada zona tiene su
// propia letra —`l` biblioteca, `/` búsqueda, `c` cola— y `Esc` vuelve a la
// tabla: no hay ciclo de foco. La caja del reproductor no es una zona, así que
// sus teclas de transporte valen desde cualquier zona.
type playerPanel uint8

const (
	playerFocusSidebar playerPanel = iota
	playerFocusTable
	playerFocusSearch
)

const (
	playerWideWidth   = 100 // La caja del reproductor usa su banda alta desde este ancho.
	playerNarrowWidth = 60  // Below this the table and bar keep only their essential columns.
	playerShortTable  = 20  // Below this the table keeps only its title column.
	playerSwitcherKey = 40  // Below this the library switcher uses the short key label.
	playerMinBodyRoom = 4   // Content wins over separator rules in short windows.
	playerSeekStepMS  = 10_000
	playerTickEvery   = time.Second
)

// Las bandas de la matriz de tamaños. La primera que cumple manda: la mínima
// recoge además las ventanas cuyo alto no alcanza para la banda de su ancho.
const (
	playerBandInsufficient = iota // Solo el mensaje de tamaño.
	playerBandMinimum             // Fila de contexto y reproductor.
	playerBandCompact             // Selector de biblioteca y tabla.
	playerBandMedium              // Biblioteca y tabla.
	playerBandWide                // Biblioteca, tabla y cola con su contexto.
)

// Las condiciones de la matriz, con un umbral de ancho acumulativo: un alto
// que no llega a la banda de su ancho cae en la siguiente forma más rica que
// quepa, nunca en la más pobre.
const (
	playerMinWidth      = 40 // Por debajo de este ancho solo cabe el mensaje.
	playerMinHeight     = 12
	playerCompactWidth  = 80
	playerCompactHeight = 20
	playerMediumWidth   = 100
	playerMediumHeight  = 26
	playerFullWidth     = 120
	playerFullHeight    = 30
	// La banda mínima conserva la tabla cuando el cuerpo tiene la fila de
	// contexto, el encabezado de la tabla y su regla.
	playerMinimumTableBody = 3
)

// Las zonas con cajas: la biblioteca y la cola tienen ancho fijo y la tabla se
// queda el resto del ancho de la ventana.
const (
	playerLibraryZone  = 25 // Ancho de la caja de la biblioteca en la banda amplia.
	playerLibraryMid   = 24 // Ancho de la caja de la biblioteca en la banda media.
	playerQueueZone    = 30 // Ancho de la caja A CONTINUACIÓN.
	playerZoneGap      = 1  // Columna de separación entre dos cajas.
	playerZoneMinTable = 4  // Por debajo de este ancho la composición no cabe.
)

// La caja del reproductor ocupa el borde inferior: su línea superior lleva el
// estado y dentro van la carátula —o sus iniciales cuando no hay imagen—, el
// título, el álbum con el artista y el tiempo con su barra. La altura interior
// depende del ancho, para que la tabla sea siempre la que cede filas.
const (
	playerBoxWide    = 10 // Filas interiores desde 100 columnas.
	playerBoxCompact = 8  // Filas interiores desde 80 columnas.
	playerBoxMinimum = 6  // Filas interiores por debajo de 80 columnas.
	// El ancho de la ranura es el que el catálogo usa para elegir la imagen.
	playerArtWidth   = spotify.CoverWidth
	playerArtRows    = 5
	playerBoxGutter  = 80 // Por debajo de este ancho la caja no gasta carátula.
	playerBoxMinRoom = 8  // Por debajo de este ancho no hay caja que dibujar.
)

type playerViewState struct {
	state playerState
	ok    bool
}

// savePhase is the step of the playlist edit flow the panel is showing. The
// panel owns the keyboard while it is not idle.
type savePhase uint8

const (
	saveIdle savePhase = iota
	saveChoosing
	// saveConfirming waits for the explicit confirmation a removal needs.
	saveConfirming
	// saveScopeOffered waits for the explicit approval an older read-only session
	// needs before it can change a playlist, and for the same single approval when
	// Spotify rejected the change without naming a playlist this account cannot
	// edit.
	saveScopeOffered
	saveAuthorization
	saveSaving
)

// saveAction is the edit the panel does on the chosen playlist: add the playing
// track or remove one occurrence of a browsed row. The confirmation, the retry
// and the notice wording follow it.
type saveAction uint8

const (
	saveActionAdd saveAction = iota
	saveActionRemove
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
	// saveNoticeNeedsPlaylist explica por qué `x` no tiene nada que quitar.
	saveNoticeNeedsPlaylist
)

// savePanel is the playlist edit panel: the writer the catalog handed over, the
// edit it is doing, the chooser cursor, the playlist already chosen and the last
// notice.
type savePanel struct {
	writer spotify.PlaylistWriter
	phase  savePhase
	action saveAction
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
	// removeTrackURI, removeTrackName y removeSnapshot son la fila confirmada
	// para quitar: la pista, su nombre visible y el snapshot de la lectura donde
	// se eligió. Sin snapshot no hay retirada.
	removeTrackURI, removeTrackName, removeSnapshot string
	// reloading marca que un snapshot viejo obligó a releer la lista: mientras
	// tanto la retirada espera el snapshot nuevo en vez de adivinar.
	reloading bool
	work      context.Context
	cancel    context.CancelFunc
	events    <-chan saveAuthMsg

	notice      saveNotice
	noticeName  string
	noticeTrack string
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
	// covers guarda el mapa URI → URL que anuncian las páginas ya cargadas, la
	// consulta de respaldo y la imagen decodificada: la caja dibuja la carátula
	// cuando llega y, mientras tanto, sus iniciales.
	covers *coverArt
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
	m.covers = newCoverArt(coverSourceOf(api))
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
		// Una ventana que crece hasta la ranura de la carátula pide lo que antes
		// no cabía; mientras no quepa, la petición no se hace.
		return m, m.coverRequest()
	case libraryAuthMsg, libraryPageMsg:
		auth, isAuth := msg.(libraryAuthMsg)
		// Una autorización solo instala el catálogo si la generación sigue viva.
		accepted := isAuth && auth.done && auth.api != nil && m.library.current(auth.generation)
		cmd := m.library.update(msg)
		// Authorization can install the library source after startup.
		m.search.source = m.library.source
		// Una página nueva puede alargar la lista que muestra el selector.
		m.saveTo.cursor = playerIndex(m.saveTo.cursor, len(m.library.playlists))
		// Una lista releída tras un snapshot viejo vuelve a armar la confirmación.
		m.reloadRemoval(msg)
		// Cada página cargada anuncia la carátula de sus pistas, así que puede ser
		// la primera vez que la pista en reproducción tiene imagen.
		m.covers.remember(m.library.tracks)
		if accepted {
			// El catálogo aprobado también trae la mitad de escritura, si la tiene.
			m.saveTo.writer = playlistWriterOf(auth.api)
			// Y con él la consulta de carátula que antes no podía hacerse.
			m.covers.installSource(coverSourceOf(auth.api))
			return m, playerCommands(cmd, m.installProfile(auth.api), m.coverRequest())
		}
		return m, playerCommands(cmd, m.coverRequest())
	case saveResultMsg, saveAuthMsg:
		return m, m.updateSave(msg)
	case coverURLMsg:
		// La respuesta de la consulta de respaldo: con URL se sigue con la
		// descarga; sin ella el intento ya quedó recordado.
		if m.quit || m.ctx.Err() != nil {
			break
		}
		if msg.err == nil && msg.url != "" {
			m.covers.urls[msg.uri] = msg.url
		}
		return m, m.coverRequest()
	case coverBytesMsg:
		// Una descarga fallida deja las iniciales: no hay aviso ni fila de error.
		m.covers.land(msg)
		return m, nil
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
		cmd := m.search.update(msg)
		// Los resultados de la búsqueda también anuncian carátulas.
		m.covers.remember(m.search.tracks)
		return m, playerCommands(cmd, m.coverRequest())
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
		// Sin una pista en reproducción el selector de guardado se queda sin qué
		// guardar; quitar una fila de la lista abierta no depende de la reproducción.
		if m.saveTo.phase != saveIdle && m.saveTo.action == saveActionAdd && !soloist.IsTrackURI(m.state.Track.URI) {
			m.closeSave()
		}
		m.reconcileModes()
		m.clampSelections()
		// A playing session needs a second of wall time to advance its playhead.
		return m, playerCommands(m.awaitState(), m.armTick(), m.coverRequest())
	case playerTickMsg:
		return m, m.tick()
	case tea.KeyMsg:
		// La ayuda se queda con toda tecla, salvo las de salir: `?` y `Esc` la
		// cierran y las globales de salida siguen funcionando.
		if m.help {
			return m, m.handleHelpKey(msg)
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

// handleKey aplica el modelo de una tecla y una acción: primero lo global —
// salir, ayuda, preferencias y transporte—, después las letras de zona y al
// final las teclas de la zona enfocada.
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
	// Un campo de texto enfocado se queda con toda tecla imprimible: `q`, `s`,
	// `r`, `+`, `-` y `?` son texto ahí y nunca un atajo.
	if m.focus == playerFocusSearch {
		return m.handleSearchKey(key)
	}
	// Ctrl+C sale siempre y `q` sale cuando ningún campo de texto tiene el foco.
	if key.Type == tea.KeyCtrlC || key.String() == "q" {
		return m.quitApp()
	}
	// `?` es la única ayuda y la única referencia de teclas.
	if key.String() == "?" {
		m.help = true
		return nil
	}
	// El selector de lista reclama sus teclas mientras está abierto.
	if cmd, handled := m.saveKey(key); handled {
		return cmd
	}
	// Esc nunca sale: vuelve a la tabla o cancela lo enfocado.
	if key.Type == tea.KeyEsc {
		m.backToTable()
		return nil
	}
	if m.ctx.Err() != nil {
		return nil
	}
	// Una letra por zona: la biblioteca, la búsqueda y la cola de la tabla.
	switch key.String() {
	case "l":
		m.focus = playerFocusSidebar
		return nil
	case "/":
		m.focus = playerFocusSearch
		return nil
	case "c":
		m.showQueue()
		return nil
	case "a":
		return m.openSave()
	case "x":
		if !m.removeAvailable() {
			m.explainRemove()
			return nil
		}
		return m.openRemove()
	}
	// El transporte es global: salto, aleatorio y repetición valen desde
	// cualquier zona; después va el cursor de la zona y al final la fila.
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
	case "b":
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

// handlePlayingKey owns las teclas globales del transporte —las flechas de
// salto, `s` y `r`—, que valen desde cualquier zona porque la caja del
// reproductor no es una zona. `s` está acotada por zona: en la biblioteca mezcla
// la lista seleccionada o abierta y en cualquier otra zona alterna el aleatorio
// de la reproducción. Informa si la tecla pertenecía a esos enlaces.
func (m *playerModel) handlePlayingKey(key tea.KeyMsg) bool {
	var command playerCommand
	switch {
	case key.Type == tea.KeyLeft, key.Type == tea.KeyRight:
		delta := int64(playerSeekStepMS)
		if key.Type == tea.KeyLeft {
			delta = -delta
		}
		command = playerCommand{Name: "seek", PositionMS: playerSeekTarget(m.playhead(), delta, m.state.Track.DurationMS)}
	case key.String() == "s":
		if uri := m.playlistShuffleURI(); uri != "" {
			m.playPlaylistShuffle(uri)
			return true
		}
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

// playPlaylistShuffle mezcla la lista que nombra la zona enfocada y olvida el
// aleatorio pendiente: la confirmación llega con el estado de Soloist.
func (m *playerModel) playPlaylistShuffle(uri string) {
	if m.commands == nil || !m.state.Connected {
		return
	}
	if m.send(playerCommand{Name: soloist.PlayPlaylistShuffle, URI: uri}) {
		m.wantShuffle = nil
	}
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

// handleSearchKey edita el buffer de búsqueda. Enter envía una consulta no
// vacía y deja el campo por la tabla; Esc lo desenfoca y cierra los resultados
// mostrados. Tab ya no mueve el foco: las zonas se alcanzan con su propia letra.
func (m *playerModel) handleSearchKey(key tea.KeyMsg) tea.Cmd {
	switch key.Type {
	case tea.KeyEsc:
		m.search.close()
		m.focus = playerFocusTable
		return nil
	case tea.KeyEnter:
		return m.submitSearch()
	case tea.KeyCtrlC:
		return m.quitApp()
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

// activateSelection interpreta Enter en la biblioteca y en la tabla, e informa
// si la tecla pertenecía a esos paneles.
func (m *playerModel) activateSelection(key tea.KeyMsg) (tea.Cmd, bool) {
	if key.Paste {
		return nil, false
	}
	switch {
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
		// La lista restringida se reproduce con el mismo Enter que abriría su
		// contenido: no hay filas que reproducir y es la única acción que queda.
		if m.library.notice == libraryNoticeUnavailable {
			m.playURI(m.library.playlist.URI)
			return nil, true
		}
		if m.library.notice == libraryNoticeLoadFailed && !m.library.loadedTracks {
			return m.library.fetchTracks(0), true
		}
		if track, ok := m.library.selectedTrack(); ok {
			m.playURI(track.URI)
		}
		return nil, true
	}
	return nil, false
}

// playlistShuffleURI nombra la lista que `s` mezcla desde la biblioteca. Fuera
// de la biblioteca devuelve vacío y `s` alterna el aleatorio de la reproducción:
// la lista abierta en la tabla no cambia el significado de la tecla.
func (m *playerModel) playlistShuffleURI() string {
	if m.focus != playerFocusSidebar {
		return ""
	}
	playlist, ok := m.library.selectedPlaylist()
	if !ok || !soloist.IsPlaylistURI(playlist.URI) {
		return ""
	}
	return playlist.URI
}

// activatePlaylist abre la lista seleccionada en la biblioteca o inicia la
// autorización. Una lista que ya se sabe restringida se reproduce con el mismo
// Enter: sus filas nunca van a llegar.
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
	if playlist, ok := library.selectedPlaylist(); ok && library.restricted && library.open() && playlist.URI == library.playlist.URI {
		m.playURI(playlist.URI)
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
	m.saveTo.action = saveActionAdd
	m.saveTo.cursor = playerIndex(m.library.sidebarSelected, len(m.library.playlists))
	m.saveTo.target = libraryPlaylist{}
	m.saveTo.authURL = ""
	m.saveTo.removeTrackURI, m.saveTo.removeTrackName, m.saveTo.removeSnapshot = "", "", ""
	m.saveTo.reloading = false
	m.saveTo.notice, m.saveTo.noticeName, m.saveTo.noticeTrack = saveNoticeNone, "", ""
	m.saveTo.retried = false
	if len(m.library.playlists) == 0 && m.library.notice == libraryNoticeLoadFailed {
		return m.library.fetchPlaylists(0)
	}
	return nil
}

// explainRemove dice qué falta cuando `x` no puede actuar, en lugar de que la
// tecla parezca rota: hace falta una lista abierta, una fila de pista y un
// catálogo que pueda escribir en ella.
func (m *playerModel) explainRemove() {
	m.saveTo.action = saveActionRemove
	m.saveTo.notice, m.saveTo.noticeName = saveNoticeNeedsPlaylist, ""
	if m.saveTo.writer == nil {
		m.saveTo.noticeName = m.text("no write permission", "sin permiso de escritura")
	}
}

// openRemove asks to remove the highlighted row of the playlist being browsed.
// Sin snapshot y sin URI de pista la acción no existe.
func (m *playerModel) openRemove() tea.Cmd {
	track, ok := m.removeRow()
	if !ok {
		return nil
	}
	m.saveTo.phase = saveConfirming
	m.saveTo.action = saveActionRemove
	m.saveTo.target = m.library.playlist
	m.saveTo.removeTrackURI, m.saveTo.removeTrackName = track.URI, playerText(track.Name)
	m.saveTo.removeSnapshot, m.saveTo.reloading = track.Snapshot, false
	m.saveTo.authURL = ""
	m.saveTo.notice, m.saveTo.noticeName = saveNoticeNone, ""
	m.saveTo.noticeTrack = m.saveTo.removeTrackName
	m.saveTo.retried = false
	return nil
}

// removeRow is the highlighted track of the browsed playlist with the snapshot
// of the listing it came from. Search results never carry one, so they can only
// be played.
func (m *playerModel) removeRow() (libraryTrack, bool) {
	if m.library == nil || !m.library.open() || m.search.active || m.saveTo.writer == nil {
		return libraryTrack{}, false
	}
	track, ok := m.library.selectedTrack()
	if !ok || !soloist.IsTrackURI(track.URI) || track.Snapshot == "" {
		return libraryTrack{}, false
	}
	return track, true
}

// removeAvailable reports whether the highlighted row can be removed right now,
// so the footer only advertises a key that works.
func (m *playerModel) removeAvailable() bool {
	_, ok := m.removeRow()
	return ok
}

// removePrompt names the track and the playlist, so the confirmation states
// exactly what would be removed.
func (m *playerModel) removePrompt() string {
	return fmt.Sprintf(m.text("Remove %s from %s?", "¿Quitar %s de %s?"), m.removeTrackLabel(), m.saveTargetName())
}

// removeTrackLabel is the visible name of the track being removed.
func (m *playerModel) removeTrackLabel() string {
	if name := m.saveTo.removeTrackName; name != "" {
		return name
	}
	return m.text("this track", "esta pista")
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

// confirmSave chooses the playlist, confirms a removal or starts the offered
// approval.
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
	case saveConfirming:
		return m.requestRemove(m.saveTo.writer)
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

// requestRemove asks Spotify to remove the confirmed occurrence from the
// browsed playlist. Sin snapshot y sin URI de pista no hay petición: la retirada
// nombra la lectura exacta donde se eligió la fila.
func (m *playerModel) requestRemove(writer spotify.PlaylistWriter) tea.Cmd {
	trackURI, snapshot, playlist := m.saveTo.removeTrackURI, m.saveTo.removeSnapshot, m.saveTo.target
	if writer == nil || m.ctx.Err() != nil || playlist.URI == "" ||
		!soloist.IsTrackURI(trackURI) || snapshot == "" {
		return nil
	}
	name := m.saveTargetName()
	m.saveTo.generation++
	m.saveTo.phase = saveSaving
	m.saveTo.reloading = false
	ctx, generation := m.ctx, m.saveTo.generation
	return func() tea.Msg {
		if ctx.Err() != nil {
			return nil
		}
		err := writer.RemoveFromPlaylist(ctx, playlist.URI, trackURI, snapshot)
		return saveResultMsg{generation: generation, name: name, err: err}
	}
}

// afterRemove reloads the browsed playlist so the removed row disappears.
func (m *playerModel) afterRemove() tea.Cmd {
	if m.library == nil || !m.library.open() {
		return nil
	}
	return m.library.fetchTracks(0)
}

// reloadRemoval re-arms the confirmation with the snapshot of the page that just
// replaced the browsed listing: un snapshot viejo se responde releyendo la lista
// y preguntando otra vez, nunca adivinando qué aparición quitar.
func (m *playerModel) reloadRemoval(msg tea.Msg) {
	if m.saveTo.phase != saveConfirming || m.saveTo.action != saveActionRemove || !m.saveTo.reloading {
		return
	}
	page, ok := msg.(libraryPageMsg)
	if !ok || page.playlistPage || page.offset != 0 || m.library.loadingTracks {
		return
	}
	m.saveTo.reloading = false
	snapshot, name := "", ""
	for _, track := range page.tracks {
		if track.URI == m.saveTo.removeTrackURI && track.Snapshot != "" {
			snapshot, name = track.Snapshot, playerText(track.Name)
			break
		}
	}
	if page.err != nil || snapshot == "" {
		// La lista releída no ofrece esa fila: se informa y no se quita nada.
		m.saveTo.phase, m.saveTo.notice = saveIdle, saveNoticeFailed
		m.saveTo.noticeName, m.saveTo.noticeTrack = m.saveTargetName(), m.removeTrackLabel()
		return
	}
	m.saveTo.removeSnapshot = snapshot
	if name != "" {
		m.saveTo.removeTrackName = name
	}
	m.saveTo.noticeTrack = m.removeTrackLabel()
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
	m.saveTo.reloading = false
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
		removing := m.saveTo.action == saveActionRemove
		switch {
		case msg.err == nil:
			m.saveTo.phase, m.saveTo.notice, m.saveTo.noticeName = saveIdle, saveNoticeSaved, msg.name
			// Cualquier escritura aceptada cambia el snapshot de la lista: se relee
			// para que las filas queden con la versión que Spotify ya conoce. En una
			// retirada además hace desaparecer la fila.
			m.saveTo.removeSnapshot = ""
			return m.afterRemove()
		case errors.Is(msg.err, spotify.ErrPlaylistSnapshotConflict) && removing:
			// La lista cambió desde que se leyó: se relee y se pregunta otra vez.
			m.saveTo.phase, m.saveTo.reloading = saveConfirming, true
			m.saveTo.removeSnapshot = ""
			return m.library.fetchTracks(0)
		case errors.Is(msg.err, spotify.ErrPlaylistNotEditable):
			// Solo un rechazo que nombra la falta de permiso sobre esa lista llega
			// aquí: la noticia afirma que esta cuenta no puede cambiarla.
			m.saveTo.phase, m.saveTo.notice, m.saveTo.noticeName = saveIdle, saveNoticeNotEditable, msg.name
		case errors.Is(msg.err, spotify.ErrSpotifyWriteScope) && !m.saveTo.retried && m.saveTo.target.URI != "":
			// Falta el permiso de escritura, o Spotify rechazó el cambio sin motivo
			// clasificable: se ofrece aprobarlo otra vez, una sola vez.
			m.saveTo.phase = saveScopeOffered
		default:
			// Cualquier otra causa se reporta por su nombre y sin inventar un motivo.
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
			m.saveTo.noticeName, m.saveTo.noticeTrack = m.saveTargetName(), m.removeTrackLabel()
			return nil
		}
		return m.installApproved(msg.writer)
	}
	return nil
}

// installApproved adopta el catálogo aprobado: la lista y la cuenta lo usan, el
// reemplazado deja de guardar la sesión y la edición confirmada se reintenta,
// una sola vez y con la misma fila.
func (m *playerModel) installApproved(writer spotify.PlaylistWriter) tea.Cmd {
	for _, replaced := range []any{m.saveTo.writer, m.library.source} {
		if releaser, ok := replaced.(interface{ ReleaseSession() }); ok {
			releaser.ReleaseSession()
		}
	}
	m.saveTo.writer, m.saveTo.retried = writer, true
	removing := m.saveTo.action == saveActionRemove
	retry := m.requestSave(writer, m.saveTo.target)
	if removing {
		retry = m.requestRemove(writer)
	}
	if retry == nil {
		m.saveTo.phase, m.saveTo.notice = saveIdle, saveNoticeFailed
		m.saveTo.noticeName = m.saveTargetName()
	}
	api, ok := writer.(CatalogAPI)
	if !ok {
		return retry
	}
	if source := librarySourceOf(api); source != nil {
		m.library.source, m.search.source, m.library.started = source, source, true
	}
	return playerCommands(m.installProfile(api), retry)
}

// saveTitle names the edit and the playlist it would change, so the target is
// visible before confirming.
func (m *playerModel) saveTitle() string {
	title := m.text("Save to playlist", "Guardar en lista")
	if m.saveTo.action == saveActionRemove {
		title = m.text("Remove from playlist", "Quitar de la lista")
	}
	if name := m.saveTargetName(); name != "" {
		title += " · " + name
	}
	return title
}

// saveRows renders the panel: the chooser, the removal confirmation, the offer to
// approve the missing permission, or the wait for Spotify.
func (m *playerModel) saveRows(styles playerStyles, room int) []playerRow {
	state := func(text string, style lipgloss.Style) playerRow {
		return playerRow{text: text, style: style, state: true}
	}
	muted := func(text string) playerRow { return state(text, styles.muted) }
	switch m.saveTo.phase {
	case saveSaving:
		return []playerRow{muted(playerMarked(playerMarkNotice, m.text("Saving…", "Guardando…")))}
	case saveAuthorization:
		rows := []playerRow{muted(playerMarked(playerMarkNotice, m.text("Waiting for Spotify…", "Esperando a Spotify…")))}
		if m.saveTo.authURL != "" {
			// El enlace no es una frase: se queda en la caja, recortado al ancho.
			rows = append(rows, playerRow{text: "  " + m.saveTo.authURL, style: styles.muted})
		}
		return rows
	case saveConfirming:
		rows := []playerRow{state(m.removePrompt(), lipgloss.NewStyle())}
		if m.saveTo.reloading {
			rows = append(rows, muted(playerMarked(playerMarkNotice, m.text("Reading the playlist again…", "Leyendo la lista otra vez…"))))
		}
		return rows
	case saveScopeOffered:
		return []playerRow{state(playerMarked(playerMarkFailure, m.text(
			"Spotify needs your approval to change playlists. Enter: authorize again.",
			"Spotify necesita tu aprobación para cambiar listas. Enter: autorizar otra vez.")), styles.failure)}
	}
	library := m.library
	switch {
	case library.notice == libraryNoticeLoadFailed && len(library.playlists) == 0:
		return []playerRow{state(m.markError(), styles.failure)}
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

// saveNoticeText is the last outcome, named for the track and the playlist it
// touched.
func (m *playerModel) saveNoticeText() string {
	removed := m.saveTo.action == saveActionRemove
	name, track := m.saveTo.noticeName, m.removeTrackLabel()
	switch m.saveTo.notice {
	case saveNoticeSaved:
		if removed {
			return fmt.Sprintf(m.text("Removed %s from %s.", "Se quitó %s de %s."), track, name)
		}
		return fmt.Sprintf(m.text("Saved to %s.", "Guardada en %s."), name)
	case saveNoticeFailed:
		if removed {
			return fmt.Sprintf(m.text("Could not remove %s from %s.", "No se pudo quitar %s de %s."), track, name)
		}
		return fmt.Sprintf(m.text("Could not save to %s.", "No se pudo guardar en %s."), name)
	case saveNoticeNotEditable:
		if removed {
			return fmt.Sprintf(m.text("Could not remove %s from %s: this account cannot change that playlist.",
				"No se pudo quitar %s de %s: esta cuenta no puede cambiar esa lista."), track, name)
		}
		return fmt.Sprintf(m.text("Could not save to %s: this account cannot change that playlist.",
			"No se pudo guardar en %s: esta cuenta no puede cambiar esa lista."), name)
	case saveNoticeDeclined:
		if removed {
			return m.text("Spotify authorization did not complete; nothing was removed.",
				"La autorización de Spotify no se completó; no se quitó nada.")
		}
		return m.text("Spotify authorization did not complete; nothing was saved.",
			"La autorización de Spotify no se completó; no se guardó nada.")
	case saveNoticeNeedsPlaylist:
		if m.saveTo.noticeName != "" {
			return fmt.Sprintf(m.text("Could not remove: %s.", "No se pudo quitar: %s."), m.saveTo.noticeName)
		}
		return m.text("Open one of your playlists with Enter to remove tracks.",
			"Abrí una de tus listas con Enter para quitar canciones.")
	}
	return ""
}

func (m *playerModel) clampSelections() {
	m.tableSelected = playerIndex(m.tableSelected, len(m.state.Queue))
	m.library.clamp()
	m.search.clamp()
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
	return playerMarked(playerMarkFailure, m.text("Playlist contents unavailable. Enter: play the playlist.",
		"Contenido de la lista no disponible. Enter: reproducir la lista."))
}

type playerRow struct {
	text  string
	style lipgloss.Style
	// state marca una frase de estado de la sesión: si no cabe en su zona se
	// dibuja una sola vez a todo el ancho, no recortada dentro de la caja.
	state bool
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

// playerBand elige la forma de la pantalla: la primera banda de la escalera
// cuyo ancho y alto cumplen manda, así un alto que no llega al umbral de su
// ancho cae en la forma siguiente y no en la más pobre.
func playerBand(width, height int) int {
	switch {
	case width < playerMinWidth || height < playerMinHeight:
		return playerBandInsufficient
	case width >= playerFullWidth && height >= playerFullHeight:
		return playerBandWide
	case width >= playerMediumWidth && height >= playerMediumHeight:
		return playerBandMedium
	case width >= playerCompactWidth && height >= playerCompactHeight:
		return playerBandCompact
	}
	return playerBandMinimum
}

// playerBodyMinimum son las filas que la composición necesita para leerse: la
// tabla conserva encabezado y una pista, o el cuerpo deja de pedir reglas. La
// banda mínima pide solo su fila de contexto: la tabla entra después, cuando
// las filas alcanzan para su encabezado (ver playerMinimumTableBody).
func playerBodyMinimum(band int) int {
	switch band {
	case playerBandWide, playerBandMedium:
		return 5 // Borde, encabezado, regla, una pista y borde.
	case playerBandCompact:
		return playerMinBodyRoom
	}
	return 1
}

// playerSizeMessage es la banda insuficiente: una sola fila centrada que nombra
// el tamaño mínimo y el actual, sin cajas.
func (m *playerModel) playerSizeMessage(width int) string {
	format := m.text("Minimum %d×%d · now %d×%d", "Mínimo %d×%d · ahora %d×%d")
	message := fmt.Sprintf(format, playerMinWidth, playerMinHeight, m.width, m.height)
	text := playerEllipsis(message, width)
	left := max(0, (width-lipgloss.Width(text))/2)
	return strings.Repeat(" ", left) + playerPad(text, width-left)
}

func (m *playerModel) View() string {
	if m.quit || m.width < 1 || m.height < 1 {
		return ""
	}
	if m.help {
		return m.helpView()
	}
	styles := m.styles()
	width, height := m.width, m.height
	band := playerBand(width, height)
	if band == playerBandInsufficient {
		return m.playerSizeMessage(width)
	}
	rule := styles.muted.Render(strings.Repeat("─", width))

	play, status := m.playerBoxRows(width, styles), m.statusRows()
	// Las frases de estado que no caben en su caja ocupan filas reservadas a
	// todo el ancho, directamente sobre el reproductor.
	overflow := playerSentenceRows(m.playerOverflowRows(styles), width)
	// El reparto cede en este orden: la caja del reproductor, la fila de
	// contexto, las frases de estado y las filas de la tabla. Las reglas también
	// ceden antes que el cuerpo mínimo. Sin pie, sus filas vuelven a la tabla.
	rules := 3
	room := height - 2 - len(play) - len(status) - len(overflow) - rules
	for room < playerBodyMinimum(band) && (rules > 0 || len(status) > 0) {
		if rules > 0 {
			rules--
		} else {
			status = status[:len(status)-1]
		}
		room = height - 2 - len(play) - len(status) - len(overflow) - rules
	}
	// En la banda mínima la tabla entra antes que el aviso de la sesión: si sus
	// filas dejan la tabla sin encabezado, el aviso cede. Si tampoco así alcanza
	// (el caso de 40×12), el cuerpo se queda con su fila de contexto.
	noticeRows := 0
	if m.playerNoticeText() != "" {
		noticeRows = 1
	}
	for band == playerBandMinimum && room < playerMinimumTableBody && len(status) > noticeRows {
		status = status[:len(status)-1]
		room = height - 2 - len(play) - len(status) - len(overflow) - rules
	}
	room = max(0, room)
	body, _ := m.bodyRows(width, styles, room)
	if len(body) > room {
		body = body[:room]
	}

	rows := make([]string, 0, height)
	rows = append(rows, playerRenderRow(playerRow{text: playerSpread("Pulso", m.statusWord(), width), style: styles.banner}, width))
	rows = append(rows, playerRenderRow(playerRow{text: m.searchRow(width, styles)}, width))
	// Una regla bajo el encabezado, otra antes de la caja y otra debajo: cada
	// una solo se dibuja si su vecino existe.
	if rules >= 3 && len(body) > 0 {
		rows = append(rows, rule)
	}
	for _, row := range body {
		rows = append(rows, playerRenderRow(row, width))
	}
	// Las filas que el cuerpo no usa se dibujan en blanco: el reproductor queda
	// siempre al pie de la ventana, como en la matriz de tamaños.
	for i := len(body); i < room; i++ {
		rows = append(rows, playerRenderRow(playerRow{}, width))
	}
	// Never stack the region rule on top of a table rule.
	if rules >= 1 && len(body) > 0 && !playerLastRowIsRule(rows, width) {
		rows = append(rows, rule)
	}
	// El hueco que dejó el pie retirado se dibuja en blanco sobre la caja y
	// debajo de la frase promovida: el marco llena la ventana y el reproductor
	// sigue al pie, sin separar la frase de la caja.
	tail := len(play) + len(status) + len(overflow)
	if rules >= 2 && len(status) > 0 {
		tail++
	}
	for fill := height - len(rows) - tail; fill > 0; fill-- {
		rows = append(rows, playerRenderRow(playerRow{}, width))
	}
	// La frase promovida va una sola vez, a todo el ancho y pegada a la caja.
	for _, row := range overflow {
		rows = append(rows, playerRenderRow(row, width))
	}
	// La caja ya viene dibujada y ajustada a la ventana.
	rows = append(rows, play...)
	if rules >= 2 && len(status) > 0 {
		rows = append(rows, rule)
	}
	for _, row := range status {
		rows = append(rows, playerRenderRow(row, width))
	}
	if len(rows) > height {
		rows = rows[:height]
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

// playerBoxBand elige la altura interior por ancho: la banda de la matriz.
func playerBoxBand(width int) int {
	switch {
	case width >= playerWideWidth:
		return playerBoxWide
	case width >= playerBoxGutter:
		return playerBoxCompact
	}
	return playerBoxMinimum
}

// playerBoxInterior recorta la banda cuando la ventana es baja: la caja deja
// sitio al encabezado, una fila de cuerpo y una fila de estado antes de crecer.
func playerBoxInterior(width, height int) int {
	interior := playerBoxBand(width)
	if limit := height - 2 - 1 - playerMinBodyRoom - 1 - 2; interior > limit {
		interior = max(3, limit)
	}
	return interior
}

// playerBoxHeight cuenta el borde superior, el interior y el inferior.
func playerBoxHeight(width, height int) int {
	if width < playerBoxMinRoom {
		return 0
	}
	return playerBoxInterior(width, height) + 2
}

// playerArtSlot es la única regla de la ranura de la carátula: dibujarla y
// pedirla usan la misma, así una ventana estrecha no descarga una imagen que
// nunca podría pintar.
func playerArtSlot(width, interior int) bool {
	return width >= playerBoxGutter && interior >= playerArtRows+3
}

// playerBoxLayout reparte las filas interiores de la caja. Las filas que no
// caben quedan en -1 y no se dibujan.
type playerBoxLayout struct {
	progress int
	context  int
}

func playerBoxLayoutFor(interior int) playerBoxLayout {
	switch {
	case interior >= playerBoxWide:
		return playerBoxLayout{progress: 6, context: 8}
	case interior >= playerBoxCompact:
		return playerBoxLayout{progress: 5, context: 7}
	case interior >= playerBoxMinimum:
		return playerBoxLayout{progress: 4, context: 5}
	case interior == 5:
		return playerBoxLayout{progress: 4, context: -1}
	case interior == 4:
		return playerBoxLayout{progress: 3, context: -1}
	}
	return playerBoxLayout{progress: 2, context: -1}
}

// Lo que la caja puede afirmar de la sesión: una pista real, una carga en
// curso o una sesión caída. Es una sola decisión para la línea de estado y para
// el interior, así no muestran cosas distintas.
const (
	playerBoxLive = iota
	playerBoxLoading
	playerBoxDisconnected
)

func (m *playerModel) playerBoxCondition() int {
	switch {
	case !m.loaded:
		return playerBoxLoading
	case !m.state.Connected:
		return playerBoxDisconnected
	}
	return playerBoxLive
}

// playerBoxText devuelve el título, el artista y el álbum que corresponden al
// estado actual. Un título guardado no sobrevive a una desconexión.
func (m *playerModel) playerBoxText() (title, artist, album string) {
	switch m.playerBoxCondition() {
	case playerBoxLoading:
		return m.text("Loading", "Cargando"), "", ""
	case playerBoxDisconnected:
		return m.text("Disconnected", "Sin conexión"), "", ""
	}
	title = playerText(m.state.Track.Title)
	if title == "" {
		title = m.text("Nothing playing", "No hay nada en reproducción")
	}
	return title, playerText(m.state.Track.Artist), playerText(m.state.Track.Album)
}

// playerEllipsis recorta con puntos suspensivos, que es lo único que cabe en
// una celda cuando no queda sitio.
func playerEllipsis(text string, width int) string {
	if width <= 0 {
		return ""
	}
	if lipgloss.Width(text) <= width {
		return text
	}
	if width == 1 {
		return "…"
	}
	return ansi.Truncate(text, width-1, "") + "…"
}

// playerBoxState es la línea de estado del borde superior: reproducción,
// aleatorio, repetición y volumen. No hay dispositivo porque Pulso reproduce en
// esta misma consola; solo aparece el remoto, y va en la línea de contexto.
func (m *playerModel) playerBoxState(width int) string {
	glyph := "⏸"
	word := m.text("Paused", "En pausa")
	switch m.playerBoxCondition() {
	case playerBoxLoading:
		word = m.text("Loading", "Cargando")
	case playerBoxDisconnected:
		word = m.text("Disconnected", "Sin conexión")
	default:
		if strings.EqualFold(strings.TrimSpace(m.state.Status), "playing") {
			glyph, word = "▶", m.text("Playing", "Reproduciendo")
		}
	}
	shuffle := "⇄ " + m.shuffleMode()
	repeat := "↻ " + m.repeatMode()
	volume := playerVolume(m.state.Volume)
	volumeWord := fmt.Sprintf(m.text("Volume %d%%", "Volumen %d%%"), volume)
	// De la forma más holgada a la más corta: primero se pierden los espacios,
	// después la palabra del estado y al final la etiqueta del volumen. El
	// porcentaje suelto sigue siendo alcanzable —en español «Volumen 37%» no cabe
	// en la banda mínima—, porque el valor del volumen nunca debe desaparecer.
	options := []string{
		glyph + " " + word + "  ▸  " + shuffle + "  ▸  " + repeat + "  ▸  " + volumeWord,
		glyph + " " + word + " ▸ " + shuffle + " ▸ " + repeat + " ▸ " + volumeWord,
		glyph + " ▸ " + shuffle + " ▸ " + repeat + " ▸ " + volumeWord,
		glyph + " ▸ " + shuffle + " ▸ " + repeat + fmt.Sprintf(" ▸ %d%%", volume),
	}
	for _, line := range options {
		if lipgloss.Width(line) <= width {
			return line
		}
	}
	return playerEllipsis(options[len(options)-1], width)
}

// playerBoxArtLines devuelve la ranura de la carátula: la imagen ya decodificada
// cuando llegó y, mientras no esté, el bloque de iniciales. El segundo valor dice
// si las filas ya traen su color, porque la carátula usa 24 bits por celda.
func (m *playerModel) playerBoxArtLines() ([]string, bool) {
	if rows, ok := m.covers.rows(playerText(m.state.Track.URI)); ok {
		return rows, true
	}
	return m.playerBoxArt(), false
}

// coverRequest pide lo que falte para dibujar la carátula de la pista en
// reproducción. Se llama al llegar un estado, una página o un tamaño, nunca al
// dibujar: así la primera pintura muestra las iniciales sin esperar la imagen, y
// una ventana que no tiene ranura no descarga ni pregunta nada.
func (m *playerModel) coverRequest() tea.Cmd {
	if m.covers == nil || m.quit || m.playerBoxCondition() != playerBoxLive {
		return nil
	}
	if !playerArtSlot(m.width, playerBoxInterior(m.width, m.height)) {
		return nil
	}
	return m.covers.request(m.ctx, playerText(m.state.Track.URI))
}

// playerBoxArt devuelve el bloque de iniciales de la carátula: dos letras del
// título mientras no haya imagen que dibujar. playerArtRows es la fuente de la
// altura y cada cadena mide playerArtWidth celdas.
func (m *playerModel) playerBoxArt() []string {
	initials := "♪"
	if title := playerText(m.state.Track.Title); title != "" {
		letters := make([]rune, 0, 2)
		for _, word := range strings.Fields(title) {
			runes := []rune(word)
			// Las palabras cortas (“de”, “la”) no aportan inicial.
			if len(runes) < 3 {
				continue
			}
			letters = append(letters, runes[0])
			if len(letters) == 2 {
				break
			}
		}
		if len(letters) == 0 {
			letters = append(letters, []rune(title)[0])
		}
		initials = strings.ToUpper(string(letters))
	}
	inner := playerArtWidth - 2
	pad := max(0, inner-lipgloss.Width(initials))
	left := pad / 2
	middle := strings.Repeat(" ", left) + initials + strings.Repeat(" ", pad-left)
	blank := "│" + strings.Repeat(" ", inner) + "│"
	rows := make([]string, 0, playerArtRows)
	for row := 0; row < playerArtRows; row++ {
		switch {
		case row == 0:
			rows = append(rows, "┌"+strings.Repeat("─", inner)+"┐")
		case row == playerArtRows-1:
			rows = append(rows, "└"+strings.Repeat("─", inner)+"┘")
		case row == playerArtRows/2:
			rows = append(rows, "│"+middle+"│")
		default:
			rows = append(rows, blank)
		}
	}
	return rows
}

// playerBoxContext es la última línea interior: la lista de la reproducción y,
// solo cuando hace falta, el dispositivo remoto.
func (m *playerModel) playerBoxContext() string {
	parts := make([]string, 0, 2)
	if playlist := playerText(m.state.PlaylistContext); playlist != "" && m.loaded && m.state.Connected {
		parts = append(parts, m.text("Playlist: ", "Lista: ")+playlist)
	}
	if device := playerRemoteDevice(m.state.Device); device != "" {
		parts = append(parts, m.text("Device ", "Dispositivo ")+device)
	}
	return strings.Join(parts, " · ")
}

// playerBoxRows dibuja la caja completa: la línea de estado en el borde
// superior, el interior y el borde inferior. Cada fila mide exactamente width.
func (m *playerModel) playerBoxRows(width int, styles playerStyles) []string {
	if width < playerBoxMinRoom {
		return nil
	}
	interior := playerBoxInterior(width, m.height)
	inner := width - 2
	edge := styles.muted
	rows := make([]string, 0, interior+2)

	state := m.playerBoxState(max(1, inner-4))
	border := "─ " + state + " "
	border += strings.Repeat("─", max(0, inner-lipgloss.Width(border)))
	rows = append(rows, edge.Render("├")+styles.label.Render(playerFit(border, inner))+edge.Render("┤"))

	hasArt := playerArtSlot(width, interior) && m.playerBoxCondition() == playerBoxLive
	art := []string{}
	artColored := false
	if hasArt {
		art, artColored = m.playerBoxArtLines()
	}
	left := "  "
	if hasArt {
		left = "  " + strings.Repeat(" ", playerArtWidth) + "  "
	}
	textWidth := max(1, inner-lipgloss.Width(left)-2)

	title, artist, album := m.playerBoxText()
	layout := playerBoxLayoutFor(interior)
	// Una caja que no está viva no afirma nada de la sesión, y el tiempo es
	// parte de esa afirmación: el total en cero y la posición en cero, para que
	// la línea no deje una última posición junto a un total que ya no existe.
	var position, duration int64
	if m.playerBoxCondition() == playerBoxLive {
		position, duration = m.playhead(), m.state.Track.DurationMS
	}
	progress := playerProgress(position, duration, textWidth)

	// Las filas se reparten por prioridad: el título y el tiempo sobreviven a
	// cualquier altura; el artista y el álbum solo cuando hay sitio para ellos.
	showArtist := interior >= 4
	showAlbum := interior >= 5
	for row := 0; row < interior; row++ {
		text, style := "", lipgloss.Style{}
		switch {
		case row == 1:
			text, style = title, styles.accent
		case row == 2 && showArtist:
			text, style = artist, styles.muted
		case row == 3 && showAlbum:
			text, style = album, styles.muted
		case row == layout.progress:
			text, style = progress, styles.muted
		case row == layout.context:
			text, style = m.playerBoxContext(), styles.muted
		}
		line := left
		if hasArt && row-1 >= 0 && row-1 < len(art) {
			// La carátula trae su color de 24 bits por celda; las iniciales van
			// con el color atenuado del tema.
			cell := art[row-1]
			if !artColored {
				cell = styles.muted.Render(cell)
			}
			line = "  " + cell + "  "
		}
		line += playerPad(style.Render(playerEllipsis(text, textWidth)), textWidth)
		rows = append(rows, edge.Render("│")+playerPad(line, inner)+edge.Render("│"))
	}
	rows = append(rows, edge.Render("└")+edge.Render(strings.Repeat("─", inner))+edge.Render("┘"))
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

// playerNoticeText es el aviso de la sesión: la única fila que queda bajo la caja
// del reproductor, y la última que cede antes de quedarse sin sitio.
func (m *playerModel) playerNoticeText() string {
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
	return notice
}

// statusRows es la única fila que queda bajo la caja del reproductor: el aviso
// de la sesión. La línea de pistas del pie se retiró en E3 y `?` es la única
// referencia de teclas.
func (m *playerModel) statusRows() []playerRow {
	if notice := m.playerNoticeText(); notice != "" {
		return []playerRow{{text: notice}}
	}
	return nil
}

// quitApp sale de la aplicación: `q` sin campo de texto y `Ctrl+C` siempre.
func (m *playerModel) quitApp() tea.Cmd {
	m.quit = true
	m.cancel()
	return tea.Quit
}

// backToTable es el único trabajo de Esc: vuelve a la tabla y, si la tabla
// muestra resultados, los cancela y devuelve la lista que había debajo. Nunca
// sale de la aplicación y nunca cierra la lista abierta; para eso está `c`.
func (m *playerModel) backToTable() {
	if m.search.active {
		m.search.close()
	}
	m.focus = playerFocusTable
}

// showQueue devuelve la tabla a la cola: cierra la búsqueda mostrada y la lista
// abierta, que es lo único que decide qué dibuja la tabla.
func (m *playerModel) showQueue() {
	m.search.close()
	m.library.closePlaylist()
	m.focus = playerFocusTable
}

// closePlaylist devuelve la tabla a la cola: olvida la lista abierta con sus
// filas y cancela la página que estuviera en camino. El aviso de la propia
// biblioteca, que llega antes de abrir nada, se conserva.
func (l *libraryState) closePlaylist() {
	if l == nil {
		return
	}
	l.stopWork()
	if l.loadedTracks {
		l.notice, l.err = libraryNoticeNone, nil
	}
	l.playlist, l.tracks = libraryPlaylist{}, nil
	l.trackTotal, l.trackMore, l.trackOffset = 0, false, 0
	l.tableSelected, l.loadingTracks, l.loadedTracks, l.restricted = 0, false, false, false
}

func (m *playerModel) bodyRows(width int, styles playerStyles, room int) ([]playerRow, []playerRow) {
	if room <= 0 || width <= 0 {
		return nil, nil
	}
	switch playerBand(width, m.height) {
	case playerBandWide, playerBandMedium:
		return m.zoneRows(width, styles, room, playerBand(width, m.height) == playerBandWide)
	case playerBandCompact:
		return m.stackedRows(width, styles, room), nil
	}
	// La banda mínima: la fila que nombra la biblioteca y, cuando las filas
	// alcanzan para el encabezado de la tabla y su regla, la tabla también.
	rows := []playerRow{{text: m.librarySwitcher(width), style: styles.label}}
	if room < playerMinimumTableBody {
		return rows, nil
	}
	return append(rows, m.tableZoneRows(width, styles, room-1)...), nil
}

// playerZoneWidths reparte el ancho de la banda entre las cajas: la biblioteca
// tiene ancho fijo, la cola también cuando existe, y la tabla se queda el resto.
func playerZoneWidths(width int, queue bool) (library, table, upcoming int) {
	library = playerLibraryMid
	if queue {
		library, upcoming = playerLibraryZone, playerQueueZone
	}
	table = width - library - playerZoneGap
	if queue {
		table -= upcoming + playerZoneGap
	}
	return library, table, upcoming
}

// zoneRows es la composición de cajas de las bandas amplia y media: la
// biblioteca a la izquierda, la tabla flexible y, solo en la banda amplia, la
// cola con su bloque de contexto. Las tres cajas comparten alto y cada fila
// mide exactamente width, así que solo la tabla cede filas. Devuelve además las
// frases de estado que no caben en su caja y se dibujan a todo el ancho.
func (m *playerModel) zoneRows(width int, styles playerStyles, room int, queue bool) ([]playerRow, []playerRow) {
	libraryWidth, tableWidth, queueWidth := playerZoneWidths(width, queue)
	if room < 2 || tableWidth < playerZoneMinTable {
		return nil, nil
	}
	libraryInterior := libraryWidth - 2
	libraryContent, overflow := playerZoneState(m.libraryRows(styles, libraryInterior), libraryInterior)
	tableInterior := tableWidth - 2
	tableContent, sentences := playerZoneState(m.tableZoneRows(tableInterior, styles, room-2), tableInterior)
	library := playerZoneRows(libraryWidth, room, m.text("LIBRARY", "BIBLIOTECA"),
		m.zoneEdge(playerFocusSidebar, styles), styles.label, libraryContent)
	table := playerZoneRows(tableWidth, room, m.mainTitle(),
		m.zoneEdge(playerFocusTable, styles), styles.label, tableContent)
	var upcoming []string
	if queue {
		upcoming = playerZoneRows(queueWidth, room, m.text("UP NEXT", "A CONTINUACIÓN"),
			m.queueEdge(styles), styles.label, m.queueRows(queueWidth-2, styles, room-2))
	}
	gap := strings.Repeat(" ", playerZoneGap)
	rows := make([]playerRow, 0, room)
	for i := 0; i < room; i++ {
		line := library[i] + gap + table[i]
		if queue {
			line += gap + upcoming[i]
		}
		rows = append(rows, playerRow{text: line})
	}
	return rows, append(overflow, sentences...)
}

// playerZoneState separa las frases de estado de una zona: las que caben se
// quedan dentro y las que no se promueven a la fila a todo el ancho. La
// decisión lee la marca de estado de la fila, nunca su texto: cualquier frase de
// sesión que no quepa se promueve, empiece con el glifo que empiece. La caja
// conserva la marca corta de las frases que la traen.
func playerZoneState(rows []playerRow, interior int) ([]playerRow, []playerRow) {
	kept := make([]playerRow, 0, len(rows))
	var overflow []playerRow
	for _, row := range rows {
		if !row.state || lipgloss.Width(row.text) <= interior {
			kept = append(kept, row)
			continue
		}
		overflow = append(overflow, row)
		kept = append(kept, playerRow{text: playerStateMark(row.text), style: row.style, state: true})
	}
	return kept, overflow
}

// playerStateMark es la marca con la que empieza una frase de estado, o nada
// cuando la fila no trae una. Solo decora la fila que queda en la zona: no
// decide si la frase se promueve.
func playerStateMark(text string) string {
	for _, mark := range []string{playerMarkLoading, playerMarkEmpty, playerMarkFailure, playerMarkNotice} {
		if strings.HasPrefix(text, mark+" ") {
			return mark
		}
	}
	return ""
}

// playerOverflowRows son las frases de estado que no caben en su caja. Se
// calculan antes del reparto porque su fila a todo el ancho va reservada.
func (m *playerModel) playerOverflowRows(styles playerStyles) []playerRow {
	band := playerBand(m.width, m.height)
	if band != playerBandWide && band != playerBandMedium {
		return nil
	}
	library, table, _ := playerZoneWidths(m.width, band == playerBandWide)
	libraryInterior, tableInterior := library-2, table-2
	_, overflow := playerZoneState(m.libraryRows(styles, libraryInterior), libraryInterior)
	_, sentences := playerZoneState(m.tableRows(tableInterior, styles, 0), tableInterior)
	return append(overflow, sentences...)
}

// playerSentenceRows dibuja las frases promovidas a todo el ancho, envueltas en
// varias filas cuando siguen sin caber. Cada fila mide exactamente width.
func playerSentenceRows(rows []playerRow, width int) []playerRow {
	var lines []playerRow
	for _, row := range rows {
		for _, line := range playerWrap(row.text, width) {
			lines = append(lines, playerRow{text: line, style: row.style})
		}
	}
	return lines
}

// playerWrap corta una frase por palabras dentro del ancho; una palabra más
// larga que la ventana se parte para no perder ninguna letra.
func playerWrap(text string, width int) []string {
	if width <= 0 {
		return nil
	}
	var lines []string
	line := ""
	flush := func() {
		if line != "" {
			lines = append(lines, line)
			line = ""
		}
	}
	for _, word := range strings.Fields(text) {
		switch {
		case line == "":
			line = word
		case lipgloss.Width(line)+1+lipgloss.Width(word) <= width:
			line += " " + word
		default:
			flush()
			line = word
		}
		for lipgloss.Width(line) > width {
			head := ansi.Truncate(line, width, "")
			lines = append(lines, head)
			line = strings.TrimSpace(strings.TrimPrefix(line, head))
		}
	}
	flush()
	return lines
}

// playerZoneRows dibuja una caja de zona: el título vive en el borde superior,
// las filas que no caben se recortan y la caja devuelve exactamente height
// filas de exactamente width celdas.
func playerZoneRows(width, height int, title string, edge, heading lipgloss.Style, rows []playerRow) []string {
	if width < 2 || height < 2 {
		return nil
	}
	inner := width - 2
	label := "─ " + playerEllipsis(title, max(0, inner-3)) + " "
	label += strings.Repeat("─", max(0, inner-lipgloss.Width(label)))
	label = playerFit(label, inner)
	lines := make([]string, 0, height)
	lines = append(lines, edge.Render("┌")+heading.Render(label)+edge.Render("┐"))
	for i := 0; i < height-2; i++ {
		lines = append(lines, edge.Render("│")+playerCell(playerRowAt(rows, i), inner)+edge.Render("│"))
	}
	lines = append(lines, edge.Render("└")+edge.Render(strings.Repeat("─", inner))+edge.Render("┘"))
	return lines
}

// zoneEdge acentúa el borde de la zona que tiene el foco y apaga las demás.
func (m *playerModel) zoneEdge(panel playerPanel, styles playerStyles) lipgloss.Style {
	if m.focus == panel {
		return styles.accent
	}
	return styles.muted
}

// queueEdge acentúa la cola cuando es el contenido de la tabla con el foco: no
// hay lista abierta, ni búsqueda, ni selector de guardado por delante.
func (m *playerModel) queueEdge(styles playerStyles) lipgloss.Style {
	if m.focus == playerFocusTable && m.saveTo.phase == saveIdle && !m.search.active && !m.library.open() {
		return styles.accent
	}
	return styles.muted
}

// queueRows es el interior de la caja A CONTINUACIÓN: las pistas que siguen,
// cada una con su artista en la fila de abajo, y el bloque de contexto anclado
// al fondo de la caja.
func (m *playerModel) queueRows(width int, styles playerStyles, room int) []playerRow {
	if width <= 0 || room <= 0 {
		return nil
	}
	context := []playerRow{
		{text: m.text("CONTEXT", "CONTEXTO"), style: styles.label},
		{text: m.queuePlaylist(), style: styles.muted},
		{text: "⇄ " + m.shuffleMode() + " · ↻ " + m.repeatMode(), style: styles.muted},
	}
	if len(context) > room {
		context = context[:room]
	}
	rows := make([]playerRow, 0, room)
	if state, ok := m.queueStateRow(styles); ok {
		rows = append(rows, state)
	} else {
		for i, track := range m.state.Queue {
			if len(rows)+2+len(context) > room {
				break
			}
			rows = append(rows, playerRow{text: fmt.Sprintf("%d ", i+1) + playerText(track.Title)})
			rows = append(rows, playerRow{text: "  " + playerText(track.Artist), style: styles.muted})
		}
	}
	for len(rows)+len(context) < room {
		rows = append(rows, playerRow{})
	}
	return append(rows, context...)
}

// queuePlaylist nombra la lista que suena: la que informa Soloist y, cuando no
// hay ninguna, la lista abierta en la biblioteca.
func (m *playerModel) queuePlaylist() string {
	if m.loaded && m.state.Connected {
		if name := playerText(m.state.PlaylistContext); name != "" {
			return name
		}
	}
	if m.library.open() {
		if name := playerText(m.library.playlist.Name); name != "" {
			return name
		}
	}
	return m.text("No playlist", "Sin lista")
}

// stackedRows is the compact breakpoint layout: one library switcher row above
// the table.
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
// selection and the zone key that changes it.
func (m *playerModel) librarySwitcher(width int) string {
	left := m.text("LIBRARY: ", "BIBLIOTECA: ") + m.librarySelection()
	change := m.text("l library", "l biblioteca")
	if width < playerSwitcherKey {
		change = "l"
	}
	return playerSpread(left, change, width)
}

// sidebarRows lista una barra lateral que ninguna banda dibuja ya: solo la usan
// las pruebas, como fuente del título, de las filas de listas y de las filas de
// estado. La banda compacta no la usa: dibuja su selector con librarySwitcher, y
// libraryRows alimenta únicamente la caja de biblioteca de las bandas amplia y
// media.
func (m *playerModel) sidebarRows(styles playerStyles, width int) []playerRow {
	rows := []playerRow{{text: m.text("LIBRARY", "BIBLIOTECA"), style: styles.label}}
	return append(rows, m.libraryRows(styles, width)...)
}

// libraryRows lists playlists and ends in the state rows: loading, empty, error
// and unauthorized each read differently. La caja escribe el título en su
// borde, así que éstas son sus únicas filas interiores.
func (m *playerModel) libraryRows(styles playerStyles, width int) []playerRow {
	var rows []playerRow
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
// sentence the other panels use for that concept. Cada fila es una frase de
// estado: la caja la dibuja entera o la promueve a todo el ancho.
func (m *playerModel) libraryStateRows(styles playerStyles) []playerRow {
	library := m.library
	state := func(text string, style lipgloss.Style) playerRow {
		return playerRow{text: text, style: style, state: true}
	}
	muted := func(text string) playerRow { return state(text, styles.muted) }
	switch {
	case library.canAuthorize():
		switch {
		case library.authURL != "":
			// El enlace es un dato, no una frase: se queda en la caja, recortado
			// como hasta ahora, para no mostrar su consulta a todo el ancho.
			return []playerRow{{text: "  " + library.authURL, style: styles.muted}}
		case library.authorizing():
			return []playerRow{muted(playerMarked(playerMarkNotice, m.text("Waiting for Spotify…", "Esperando a Spotify…")))}
		case library.notice == libraryNoticeAuthFailed:
			return []playerRow{state(playerMarked(playerMarkFailure, m.text("Spotify unavailable; playback works. Enter: retry.",
				"Spotify no disponible; la reproducción funciona. Enter: reintentar.")), styles.failure)}
		case library.notice == libraryNoticeClientID:
			return []playerRow{state(playerMarked(playerMarkFailure, m.text("Restart Pulso and complete the Spotify step.",
				"Reinicia Pulso y completa el paso de Spotify.")), styles.failure)}
		default:
			return []playerRow{muted(m.markUnauthorized())}
		}
	case library.source == nil:
		return []playerRow{muted(m.markEmpty())}
	case library.notice == libraryNoticeLoadFailed && !library.loadedPlaylists:
		return []playerRow{state(m.markError(), styles.failure)}
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
	rows := []playerRow{{text: m.mainTitle(), style: styles.label}}
	return append(rows, m.tableZoneRows(width, styles, max(1, room-1))...)
}

// tableZoneRows son las filas interiores de la caja de la tabla: su encabezado,
// su propia regla y las pistas que quepan. El nombre de la lista vive en el
// borde de la caja, así que aquí no se repite.
func (m *playerModel) tableZoneRows(width int, styles playerStyles, room int) []playerRow {
	if width <= 0 || room <= 0 {
		return nil
	}
	rows := []playerRow{
		{text: m.tableHeader(width), style: playerLabel},
		{text: strings.Repeat("─", width), style: styles.muted},
	}
	return append(rows, m.tableRows(width, styles, max(0, room-2))...)
}

func (m *playerModel) tableHeader(width int) string {
	columns := m.tableColumns(width)
	cells := make([]string, len(columns))
	for i, column := range columns {
		cells[i] = playerPad(column.label, column.width)
	}
	return strings.Join(cells, " ")
}

// queueStateRow explica por qué la cola no tiene filas que listar: la misma
// frase en la tabla y en la caja de la cola.
func (m *playerModel) queueStateRow(styles playerStyles) (playerRow, bool) {
	switch {
	case !m.loaded:
		return playerRow{text: m.markLoading(), style: styles.muted}, true
	case !m.state.Connected:
		return playerRow{text: m.text("Queue unavailable", "Cola no disponible"), style: styles.muted}, true
	case len(m.state.Queue) == 0:
		return playerRow{text: m.markEmpty(), style: styles.muted}, true
	}
	return playerRow{}, false
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
	if state, ok := m.queueStateRow(styles); ok {
		return []playerRow{state}
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
		// La frase nombra `Enter` y no cabe en todas las cajas: se promueve como
		// cualquier otra frase de estado.
		return []playerRow{{text: m.markRestricted(), style: styles.failure, state: true}}
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
