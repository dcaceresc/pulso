package ui

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/dcaceresc/pulso/internal/spotify"
)

// libraryFixtureCatalog builds a real Spotify catalog around a fixture
// transport, so the workspace is exercised through the production type.
func libraryFixtureCatalog(roundTrip func(*http.Request) (*http.Response, error)) *spotify.Catalog {
	return spotify.NewCatalog(&http.Client{Transport: fixtureTransport(roundTrip), CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}}, "fixture-client", "fixture-access", "fixture-refresh", time.Now().Add(time.Hour), "")
}

type fixtureTransport func(*http.Request) (*http.Response, error)

func (f fixtureTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func libraryFixtureResponse(code int, body string) *http.Response {
	return &http.Response{StatusCode: code, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body))}
}

// libraryFake serves fixture pages and records what the workspace requested. It
// also implements CatalogAPI so it can travel the production setup handoff.
type libraryFake struct {
	mu            sync.Mutex
	playlistCalls []int
	trackCalls    []int
	trackURIs     []string
	searchCalls   []int
	searchQueries []string
	playlists     func(context.Context, int) ([]libraryPlaylist, int, bool, error)
	items         func(context.Context, string, int) (spotify.PlaylistItemsPage, error)
	search        func(context.Context, string, int) ([]libraryTrack, int, bool, error)
}

func (f *libraryFake) PlaylistPage(ctx context.Context, offset int) ([]libraryPlaylist, int, bool, error) {
	f.mu.Lock()
	f.playlistCalls = append(f.playlistCalls, offset)
	f.mu.Unlock()
	if f.playlists == nil {
		return nil, 0, false, nil
	}
	return f.playlists(ctx, offset)
}

// PlaylistItems sirve una página de la lista abierta con el snapshot de la
// lectura que la produjo, igual que la fuente real.
func (f *libraryFake) PlaylistItems(ctx context.Context, uri string, offset int) (spotify.PlaylistItemsPage, error) {
	f.mu.Lock()
	f.trackCalls = append(f.trackCalls, offset)
	f.trackURIs = append(f.trackURIs, uri)
	f.mu.Unlock()
	if f.items == nil {
		return spotify.PlaylistItemsPage{}, nil
	}
	return f.items(ctx, uri, offset)
}

// SearchPage records one search request per call, so a test can prove the
// query and the offset that reached the source.
func (f *libraryFake) SearchPage(ctx context.Context, query string, offset int) ([]libraryTrack, int, bool, error) {
	f.mu.Lock()
	f.searchCalls = append(f.searchCalls, offset)
	f.searchQueries = append(f.searchQueries, query)
	f.mu.Unlock()
	if f.search == nil {
		return nil, 0, false, nil
	}
	return f.search(ctx, query, offset)
}

func (f *libraryFake) Search(context.Context, string, int) ([]catalogItem, bool, error) {
	return nil, false, nil
}

func (f *libraryFake) Playlists(context.Context, int) ([]catalogItem, bool, error) {
	return nil, false, nil
}

func (f *libraryFake) PlaylistTracks(context.Context, string, int) ([]catalogItem, bool, error) {
	return nil, false, nil
}

func (f *libraryFake) requests() (playlists, tracks []int, uris []string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]int(nil), f.playlistCalls...), append([]int(nil), f.trackCalls...), append([]string(nil), f.trackURIs...)
}

func (f *libraryFake) searchRequests() (queries []string, offsets []int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.searchQueries...), append([]int(nil), f.searchCalls...)
}

func libraryPlaylistsFixture() func(context.Context, int) ([]libraryPlaylist, int, bool, error) {
	return func(_ context.Context, offset int) ([]libraryPlaylist, int, bool, error) {
		if offset != 0 {
			return nil, 2, false, nil
		}
		return []libraryPlaylist{
			{Name: "Rock Chileno", Owner: "ana", URI: "spotify:playlist:rock", Total: 42},
			{Name: "Jazz", Owner: "beto", URI: "spotify:playlist:jazz", Total: 12},
		}, 2, false, nil
	}
}

func libraryTracksFor(uri string) []libraryTrack {
	if uri == "spotify:playlist:jazz" {
		return []libraryTrack{{Name: "Take Five", Artist: "Dave", DurationMS: 324000, URI: "spotify:track:jazz1"}}
	}
	return []libraryTrack{
		{Name: "Tren al Sur", Artist: "Los Prisioneros", DurationMS: 336000, URI: "spotify:track:one"},
		{Name: "Estrechez de Corazón", Artist: "Los Prisioneros", DurationMS: 381000, URI: "spotify:track:two"},
		{Name: "Llueve sobre la Ciudad", Artist: "Los Bunkers", DurationMS: 236000, URI: "spotify:track:three"},
	}
}

// librarySnapshotFor is the listing snapshot each fixture page reports, so a
// removal names the page it was chosen from.
func librarySnapshotFor(uri string) string {
	if uri == "spotify:playlist:jazz" {
		return "snapshot-jazz"
	}
	return "snapshot-rock"
}

// libraryTracksFixture serves one page and then a second one at the shared page
// size, so paging past the loaded rows reaches a real request.
func libraryTracksFixture(total int) func(context.Context, string, int) (spotify.PlaylistItemsPage, error) {
	return func(_ context.Context, uri string, offset int) (spotify.PlaylistItemsPage, error) {
		if offset == 0 {
			return spotify.PlaylistItemsPage{Tracks: libraryTracksFor(uri), Total: total, More: true, Snapshot: librarySnapshotFor(uri)}, nil
		}
		return spotify.PlaylistItemsPage{
			Tracks: []libraryTrack{{Name: "Página dos", Artist: "Artista", DurationMS: 200000, URI: "spotify:track:page2"}},
			Total:  total,
		}, nil
	}
}

// libraryNewPlayer builds the workspace and settles the startup library page.
func libraryNewPlayer(t *testing.T, width, height int, p preferences, state playerState, source librarySource, authorize func(context.Context, string, func(string)) (CatalogAPI, error), clientID string) (tea.Model, chan playerCommand, context.CancelFunc) {
	t.Helper()
	m, out, cancel := libraryNewPlayerPending(t, width, height, p, state, source, authorize, clientID)
	return librarySettle(t, m, m.Init()), out, cancel
}

// libraryNewPlayerPending leaves the startup requests unrun, so a test can
// observe a panel that has not received its first page yet.
func libraryNewPlayerPending(t *testing.T, width, height int, p preferences, state playerState, source librarySource, authorize func(context.Context, string, func(string)) (CatalogAPI, error), clientID string) (tea.Model, chan playerCommand, context.CancelFunc) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	states, out := make(chan playerState, 1), make(chan playerCommand, 4)
	states <- state
	var api CatalogAPI
	if source != nil {
		api = source.(CatalogAPI)
	}
	return NewPlayerModelWithCatalog(ctx, states, out, width, height, p, nil, api, authorize, clientID), out, cancel
}

// librarySettle runs every command the library schedules. Player state reads
// stay pending, exactly as a live Bubble Tea program leaves them.
func librarySettle(t *testing.T, m tea.Model, cmd tea.Cmd) tea.Model {
	t.Helper()
	for steps := 0; cmd != nil; steps++ {
		if steps > 32 {
			t.Fatal("library commands did not settle")
		}
		msg := playerViewRun(t, cmd)
		cmd = nil
		switch value := msg.(type) {
		case nil:
		case tea.BatchMsg:
			for _, inner := range value {
				m = librarySettle(t, m, inner)
			}
		default:
			var next tea.Cmd
			m, next = m.Update(value)
			if _, scheduled := value.(playerViewState); !scheduled {
				cmd = next
			}
		}
	}
	return m
}

// libraryFocusSidebar alcanza la biblioteca con su propia letra de zona: `Tab`
// ya no mueve el foco.
func libraryFocusSidebar(t *testing.T, m tea.Model) tea.Model {
	t.Helper()
	m, _ = m.Update(playerViewKey("l"))
	return m
}

// libraryOpenFirst moves focus to the sidebar and opens the selected playlist.
func libraryOpenFirst(t *testing.T, m tea.Model) (tea.Model, tea.Cmd) {
	t.Helper()
	return libraryFocusSidebar(t, m).Update(tea.KeyMsg{Type: tea.KeyEnter})
}

func TestLibraryStartupLoadsPlaylists(t *testing.T) {
	source := &libraryFake{playlists: libraryPlaylistsFixture()}
	m, out, _ := libraryNewPlayer(t, 120, 40, preferences{}, playerState{Connected: true, SessionID: 7}, source, nil, "")
	if playlists, _, _ := source.requests(); len(playlists) != 1 || playlists[0] != 0 {
		t.Fatalf("startup requested %v, want one first playlist page", playlists)
	}
	view := ansi.Strip(m.View())
	for _, want := range []string{"LIBRARY", "> Rock Chileno", "ana", "Jazz", "beto"} {
		if !strings.Contains(view, want) {
			t.Fatalf("missing %q in the sidebar:\n%s", want, view)
		}
	}
	if len(out) != 0 {
		t.Fatal("loading the library sent a playback command")
	}

	// Only the selected row carries the palette accent.
	m = libraryFocusSidebar(t, m)
	player, ok := m.(*playerModel)
	if !ok {
		t.Fatalf("unexpected model %T", m)
	}
	styles := player.styles()
	var selectedRow, otherRow playerRow
	for _, row := range player.sidebarRows(styles, 30) {
		switch {
		case strings.Contains(row.text, "Rock Chileno"):
			selectedRow = row
		case strings.Contains(row.text, "Jazz"):
			otherRow = row
		}
	}
	if selectedRow.style.GetForeground() != styles.accent.GetForeground() {
		t.Fatalf("the selected playlist is not accented: %+v", selectedRow)
	}
	if _, plain := otherRow.style.GetForeground().(lipgloss.NoColor); !plain {
		t.Fatalf("an unselected playlist is accented: %+v", otherRow.style.GetForeground())
	}
}

func TestLibrarySidebarDropsOwnerWhenNarrow(t *testing.T) {
	source := &libraryFake{playlists: func(context.Context, int) ([]libraryPlaylist, int, bool, error) {
		return []libraryPlaylist{{Name: "Rock Chileno Clasico", Owner: "owner", URI: "spotify:playlist:rock", Total: 1}}, 1, false, nil
	}}
	m, _, _ := libraryNewPlayer(t, 100, 30, preferences{}, playerState{Connected: true}, source, nil, "")
	view := ansi.Strip(m.View())
	if !strings.Contains(view, "Rock Chileno Clasico") {
		t.Fatalf("missing the playlist name:\n%s", view)
	}
	if strings.Contains(view, "owner") {
		t.Fatalf("the owner did not fit the sidebar:\n%s", view)
	}
}

func TestLibraryPlaylistSelectionLoadsTracks(t *testing.T) {
	source := &libraryFake{playlists: libraryPlaylistsFixture(), items: func(_ context.Context, uri string, offset int) (spotify.PlaylistItemsPage, error) {
		return spotify.PlaylistItemsPage{Tracks: libraryTracksFor(uri), Total: 42, Snapshot: librarySnapshotFor(uri)}, nil
	}}
	m, out, _ := libraryNewPlayer(t, 120, 40, preferences{Language: "es", Theme: "standard"}, playerState{Connected: true, SessionID: 9}, source, nil, "")
	m, cmd := libraryOpenFirst(t, m)
	if cmd == nil {
		t.Fatal("opening a playlist did not load its first track page")
	}
	m = librarySettle(t, m, cmd)
	if _, tracks, uris := source.requests(); len(tracks) != 1 || tracks[0] != 0 || uris[0] != "spotify:playlist:rock" {
		t.Fatalf("track request = %v %v, want the first page of rock", tracks, uris)
	}
	view := ansi.Strip(m.View())
	for _, want := range []string{"Rock Chileno · 42 canciones", "#", "Título", "Artista", "Duración", ">1", "Tren al Sur", "5:36", "6:21", "3:56"} {
		if !strings.Contains(view, want) {
			t.Fatalf("missing %q in the table:\n%s", want, view)
		}
	}
	if len(out) != 0 {
		t.Fatal("opening a playlist played something")
	}
}

func TestLibraryPagesPastLoadedRows(t *testing.T) {
	source := &libraryFake{playlists: libraryPlaylistsFixture(), items: libraryTracksFixture(42)}
	m, _, _ := libraryNewPlayer(t, 120, 40, preferences{}, playerState{Connected: true, SessionID: 3}, source, nil, "")
	m, cmd := libraryOpenFirst(t, m)
	m = librarySettle(t, m, cmd)
	playerViewText(t, m, ">1", "Tren al Sur", "5:36")

	// Three rows are loaded, so moving past the last one asks for the next page.
	for range 3 {
		var next tea.Cmd
		m, next = m.Update(playerViewKey("j"))
		if next != nil {
			m = librarySettle(t, m, next)
		}
	}
	if _, tracks, _ := source.requests(); len(tracks) != 2 || tracks[1] != spotifyCatalogPageSize {
		t.Fatalf("track requests = %v, want a second page at the shared page size", tracks)
	}
	playerViewText(t, m, ">4", "Página dos")
}

func TestLibraryRestrictedPlaylist(t *testing.T) {
	for _, tt := range []struct {
		name string
		p    preferences
		want []string
	}{
		{"english", preferences{}, []string{"unavailable", "Enter: play the playlist"}},
		{"spanish", preferences{Language: "es"}, []string{"no disponible", "Enter: reproducir la lista"}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			source := &libraryFake{playlists: libraryPlaylistsFixture(), items: func(context.Context, string, int) (spotify.PlaylistItemsPage, error) {
				return spotify.PlaylistItemsPage{}, errPlaylistUnavailable
			}}
			m, out, _ := libraryNewPlayer(t, 120, 40, tt.p, playerState{Connected: true, SessionID: 11}, source, nil, "")
			m, cmd := libraryOpenFirst(t, m)
			m = librarySettle(t, m, cmd)
			view := ansi.Strip(m.View())
			for _, want := range tt.want {
				if !strings.Contains(view, want) {
					t.Fatalf("missing %q for a restricted playlist:\n%s", want, view)
				}
			}
			// La lista restringida se reproduce con Enter, la única tecla que queda.
			m, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
			select {
			case got := <-out:
				if want := (playerCommand{Name: "play", URI: "spotify:playlist:rock", SessionID: 11}); got != want {
					t.Fatalf("got %+v, want %+v", got, want)
				}
			default:
				t.Fatal("a restricted playlist did not play by URI")
			}
		})
	}
}

func TestLibraryPlaysPlaylistAndTrackForSession(t *testing.T) {
	source := &libraryFake{playlists: libraryPlaylistsFixture(), items: func(_ context.Context, uri string, offset int) (spotify.PlaylistItemsPage, error) {
		return spotify.PlaylistItemsPage{Tracks: libraryTracksFor(uri), Total: 42, Snapshot: librarySnapshotFor(uri)}, nil
	}}
	m, out, _ := libraryNewPlayer(t, 120, 40, preferences{}, playerState{Connected: true, SessionID: 21}, source, nil, "")

	// Enter en la biblioteca abre la lista seleccionada.
	m = libraryFocusSidebar(t, m)
	m, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = librarySettle(t, m, cmd)
	if view := ansi.Strip(m.View()); !strings.Contains(view, "Tren al Sur") {
		t.Fatalf("Enter on the sidebar did not open the playlist:\n%s", view)
	}
	if len(out) != 0 {
		t.Fatal("opening a playlist sent a playback command")
	}

	// Enter on a row plays the open playlist: the context keeps `n` inside it.
	m, _ = m.Update(playerViewKey("j"))
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	select {
	case got := <-out:
		if want := (playerCommand{Name: "play", URI: "spotify:playlist:rock", SessionID: 21}); got != want {
			t.Fatalf("got %+v, want %+v", got, want)
		}
	default:
		t.Fatal("Enter on a track did not play it")
	}
}

// TestE3ShuffleIsScopedByZoneOnly fija el único significado de `s`, tal como lo
// declara la tabla de controles: en la biblioteca mezcla la lista seleccionada y
// en cualquier otra zona —también en la tabla con una lista abierta— alterna el
// aleatorio de la reproducción. `S` ya no existe y `p` tampoco.
func TestE3ShuffleIsScopedByZoneOnly(t *testing.T) {
	source := &libraryFake{playlists: libraryPlaylistsFixture(), items: func(context.Context, string, int) (spotify.PlaylistItemsPage, error) {
		return spotify.PlaylistItemsPage{}, errPlaylistUnavailable
	}}
	m, out, _ := libraryNewPlayer(t, 120, 40, preferences{}, playerState{Connected: true, SessionID: 21}, source, nil, "")
	check := func(key string, want ...playerCommand) {
		t.Helper()
		m, _ = m.Update(playerViewKey(key))
		for _, expected := range want {
			expected.SessionID = 21
			select {
			case got := <-out:
				if got != expected {
					t.Fatalf("%q sent %+v, want %+v", key, got, expected)
				}
			default:
				t.Fatalf("%q did not send %+v", key, expected)
			}
		}
		select {
		case got := <-out:
			t.Fatalf("%q sent unexpected %+v", key, got)
		default:
		}
	}
	// En la tabla `s` es el aleatorio de la reproducción.
	check("s", playerCommand{Name: "set_shuffle", Shuffle: true}, playerCommand{Name: "get_state"})
	// En la biblioteca `s` mezcla la lista seleccionada.
	m, _ = m.Update(playerViewKey("l"))
	check("s", playerCommand{Name: "play_playlist_shuffle", URI: "spotify:playlist:rock"})
	// `S` ya no es un enlace y `p` tampoco.
	check("S")
	check("p")
	// Con la lista abierta y el foco en la tabla, `s` vuelve a la reproducción:
	// la lista abierta no le da un tercer significado a la tecla.
	m, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = librarySettle(t, m, cmd)
	check("s", playerCommand{Name: "set_shuffle", Shuffle: true}, playerCommand{Name: "get_state"})
	// La misma tecla en la biblioteca sigue mezclando la lista abierta.
	m, _ = m.Update(playerViewKey("l"))
	check("s", playerCommand{Name: "play_playlist_shuffle", URI: "spotify:playlist:rock"})
	// Y la tabla con la cola a la vista no cambia nada: sigue siendo reproducción.
	check("c")
	check("s", playerCommand{Name: "set_shuffle", Shuffle: true}, playerCommand{Name: "get_state"})
}

func TestLibraryKeepsQueueWhenNoPlaylistOpen(t *testing.T) {
	source := &libraryFake{playlists: libraryPlaylistsFixture(), items: libraryTracksFixture(42)}
	queue := []playerTrack{{Title: "Queued Song", Artist: "Queued Artist", DurationMS: 100000}}
	m, _, _ := libraryNewPlayer(t, 120, 40, preferences{}, playerState{Connected: true, SessionID: 5, Queue: queue}, source, nil, "")
	playerViewText(t, m, "Queued Song", "Queued Artist", "1:40", "Rock Chileno")
}

func TestLibrarySendsNothingWhenDisconnected(t *testing.T) {
	source := &libraryFake{playlists: libraryPlaylistsFixture(), items: func(_ context.Context, uri string, offset int) (spotify.PlaylistItemsPage, error) {
		return spotify.PlaylistItemsPage{Tracks: libraryTracksFor(uri), Total: 42, Snapshot: librarySnapshotFor(uri)}, nil
	}}
	m, out, _ := libraryNewPlayer(t, 120, 40, preferences{}, playerState{SessionID: 4}, source, nil, "")
	m = libraryFocusSidebar(t, m)
	m, _ = m.Update(playerViewKey("s"))
	if len(out) != 0 {
		t.Fatal("s sent a command while disconnected")
	}
	m, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = librarySettle(t, m, cmd)
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if len(out) != 0 {
		t.Fatal("Enter sent a track command while disconnected")
	}
	// The library still loads while Soloist is away.
	playerViewText(t, m, "Tren al Sur")
}

func TestLibraryAuthorizeOnDemand(t *testing.T) {
	const url = "https://accounts.spotify.com/authorize?client_id=fixture-client&response_type=code"
	fake := &libraryFake{playlists: libraryPlaylistsFixture(), items: libraryTracksFixture(42)}
	requested := make(chan string, 1)
	authorize := func(_ context.Context, clientID string, showURL func(string)) (CatalogAPI, error) {
		requested <- clientID
		showURL(url)
		return fake, nil
	}
	m, out, _ := libraryNewPlayer(t, 120, 40, preferences{}, playerState{Connected: true, SessionID: 2}, nil, authorize, "fixture-client")
	playerViewText(t, m, "No Spotify. Enter: connect")
	if len(out) != 0 {
		t.Fatal("the call to action sent a playback command")
	}
	// The stacked layout keeps a localized call to action in its summary row.
	playerViewRows(t, m, 80, 24)
	playerViewText(t, m, "LIBRARY: ● No Spotify. Enter: connect")

	m = libraryFocusSidebar(t, m)
	m, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("Enter on the sidebar did not request authorization")
	}
	msg := playerViewRun(t, cmd)
	select {
	case got := <-requested:
		if got != "fixture-client" {
			t.Fatalf("authorized with %q", got)
		}
	default:
		t.Fatal("authorization was not requested")
	}
	m, cmd = m.Update(msg)
	notice := ansi.Strip(m.View())
	if !strings.Contains(notice, "https://accounts.spotify.co") {
		t.Fatalf("the authorization URL is not shown:\n%s", notice)
	}
	if strings.Contains(notice, "response_type=code") {
		t.Fatalf("the authorization URL was not trimmed to width:\n%s", notice)
	}
	m = librarySettle(t, m, cmd)
	if !strings.Contains(ansi.Strip(m.View()), "Rock Chileno") {
		t.Fatalf("an authorized library did not load:\n%s", m.View())
	}
}

func TestLibraryMissingClientIDNotice(t *testing.T) {
	authorized := make(chan struct{}, 1)
	authorize := func(context.Context, string, func(string)) (CatalogAPI, error) {
		authorized <- struct{}{}
		return nil, errors.New("offline fixture")
	}
	for _, tt := range []struct {
		language string
		short    string
		want     string
	}{
		{"en", "Restart Pulso", "LIBRARY: ! Restart Pulso and complete the Spotify step."},
		{"es", "Reinicia Pulso", "BIBLIOTECA: ! Reinicia Pulso y completa el paso de Spotify."},
	} {
		t.Run(tt.language, func(t *testing.T) {
			m, _, _ := libraryNewPlayer(t, 120, 40, preferences{Language: tt.language}, playerState{Connected: true}, nil, authorize, "fixture-client")
			player := m.(*playerModel)
			// No remembered Client ID means the wizard never ran on this profile.
			player.library.clientID = ""
			m = libraryFocusSidebar(t, m)
			m, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
			if cmd != nil {
				t.Fatal("authorization started without a Client ID")
			}
			select {
			case <-authorized:
				t.Fatal("the authorize seam ran without a Client ID")
			default:
			}
			// The sidebar truncates; the stacked summary carries the whole notice.
			playerViewText(t, m, tt.short)
			playerViewRows(t, m, 80, 24)
			playerViewText(t, m, tt.want)
		})
	}
}

func TestLibraryAuthorizeFailureKeepsPlayback(t *testing.T) {
	authorize := func(context.Context, string, func(string)) (CatalogAPI, error) {
		return nil, errors.New("offline fixture")
	}
	m, out, _ := libraryNewPlayer(t, 120, 40, preferences{}, playerState{Connected: true, Status: "paused", Volume: 40}, nil, authorize, "fixture-client")
	m = libraryFocusSidebar(t, m)
	m, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = librarySettle(t, m, cmd)
	view := ansi.Strip(m.View())
	if !strings.Contains(view, "Spotify unavailable") {
		t.Fatalf("missing a safe message after a failed authorization:\n%s", view)
	}
	if strings.Contains(view, "offline fixture") {
		t.Fatalf("raw failure text reached the view:\n%s", view)
	}
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeySpace})
	select {
	case got := <-out:
		if got.Name != "play" {
			t.Fatalf("got %+v, want a resumed play command", got)
		}
	default:
		t.Fatal("a failed authorization disabled playback")
	}
}

func TestLibraryStaleGenerationDropped(t *testing.T) {
	source := &libraryFake{playlists: libraryPlaylistsFixture(), items: func(_ context.Context, uri string, offset int) (spotify.PlaylistItemsPage, error) {
		return spotify.PlaylistItemsPage{Tracks: libraryTracksFor(uri), Total: 1, Snapshot: librarySnapshotFor(uri)}, nil
	}}
	m, _, _ := libraryNewPlayer(t, 120, 40, preferences{}, playerState{Connected: true, SessionID: 5}, source, nil, "")
	m, stale := libraryOpenFirst(t, m)
	if stale == nil {
		t.Fatal("opening a playlist did not schedule a fetch")
	}
	staleMsg := playerViewRun(t, stale)

	// A second selection supersedes the first request.
	m = libraryFocusSidebar(t, m)
	m, _ = m.Update(playerViewKey("j"))
	var fresh tea.Cmd
	m, fresh = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m, _ = m.Update(playerViewRun(t, fresh))
	playerViewText(t, m, "Take Five")

	m, _ = m.Update(staleMsg)
	view := playerViewText(t, m, "Take Five")
	if strings.Contains(view, "Tren al Sur") {
		t.Fatalf("a stale page reached the table:\n%s", view)
	}
}

func TestLibraryFetchCancelsWithTheModel(t *testing.T) {
	blocked := make(chan struct{})
	source := &libraryFake{playlists: libraryPlaylistsFixture(), items: func(ctx context.Context, _ string, _ int) (spotify.PlaylistItemsPage, error) {
		close(blocked)
		<-ctx.Done()
		return spotify.PlaylistItemsPage{}, ctx.Err()
	}}
	m, _, cancel := libraryNewPlayer(t, 120, 40, preferences{}, playerState{Connected: true, SessionID: 1}, source, nil, "")
	m, cmd := libraryOpenFirst(t, m)
	if cmd == nil {
		t.Fatal("opening a playlist did not schedule a fetch")
	}
	done := make(chan tea.Msg, 1)
	go func() { done <- cmd() }()
	<-blocked
	cancel()
	var msg tea.Msg
	select {
	case msg = <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("a canceled fetch did not return")
	}
	m, _ = m.Update(msg)
	if view := ansi.Strip(m.View()); strings.Contains(view, "Tren al Sur") {
		t.Fatalf("a canceled fetch reached the table:\n%s", view)
	}
}

func TestLibraryStatesAreNeverBlank(t *testing.T) {
	failingTracks := func(context.Context, string, int) (spotify.PlaylistItemsPage, error) {
		return spotify.PlaylistItemsPage{}, errSpotifyCatalog
	}
	cases := []struct {
		name             string
		p                preferences
		source           *libraryFake
		open             bool
		startup, opening bool // settle the startup page and the playlist page
		want             string
	}{
		{"playlists loading", preferences{}, &libraryFake{playlists: libraryPlaylistsFixture()}, false, false, false, "Loading"},
		{"playlists empty", preferences{}, &libraryFake{playlists: func(context.Context, int) ([]libraryPlaylist, int, bool, error) {
			return nil, 0, false, nil
		}}, false, true, false, "Nothing here yet"},
		{"playlists failed", preferences{}, &libraryFake{playlists: func(context.Context, int) ([]libraryPlaylist, int, bool, error) {
			return nil, 0, false, errSpotifyCatalog
		}}, false, true, false, "Could not load Spotify data"},
		{"tracks empty", preferences{}, &libraryFake{playlists: libraryPlaylistsFixture(), items: func(context.Context, string, int) (spotify.PlaylistItemsPage, error) {
			return spotify.PlaylistItemsPage{}, nil
		}}, true, true, true, "Nothing here yet"},
		{"tracks failed", preferences{Language: "es"}, &libraryFake{playlists: libraryPlaylistsFixture(), items: failingTracks}, true, true, true, "No se pudieron cargar los datos de Spotify"},
		{"tracks loading", preferences{}, &libraryFake{playlists: libraryPlaylistsFixture(), items: libraryTracksForProbe}, true, true, false, "Loading"},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			m, _, _ := libraryNewPlayerPending(t, 120, 40, tt.p, playerState{Connected: true}, tt.source, nil, "")
			cmd := m.Init()
			if tt.startup {
				m = librarySettle(t, m, cmd)
			}
			if tt.open {
				m, cmd = libraryOpenFirst(t, m)
				if tt.opening {
					m = librarySettle(t, m, cmd)
				}
			}
			if view := ansi.Strip(m.View()); !strings.Contains(view, tt.want) {
				t.Fatalf("missing %q:\n%s", tt.want, view)
			}
		})
	}
}

func libraryTracksForProbe(_ context.Context, uri string, _ int) (spotify.PlaylistItemsPage, error) {
	return spotify.PlaylistItemsPage{Tracks: libraryTracksFor(uri), Total: 42, More: true, Snapshot: librarySnapshotFor(uri)}, nil
}

func TestLibraryUsesTheAuthorizedCatalog(t *testing.T) {
	catalog := libraryFixtureCatalog(func(r *http.Request) (*http.Response, error) {
		switch r.URL.Path {
		case "/v1/me/playlists":
			return libraryFixtureResponse(200, `{"total":1,"items":[{"name":"Rock","owner":{"display_name":"ana"},"uri":"spotify:playlist:rock","tracks":{"total":1}}],"next":null}`), nil
		case "/v1/playlists/rock/items":
			return libraryFixtureResponse(200, `{"total":1,"items":[{"item":{"name":"Tren al Sur","artists":[{"name":"Los Prisioneros"}],"duration_ms":336000,"uri":"spotify:track:one"}}],"next":null}`), nil
		case "/v1/playlists/rock":
			// El snapshot vive en la lista, no en su página de elementos.
			if got := r.URL.RawQuery; got != "fields=snapshot_id" {
				t.Fatalf("snapshot query = %q, want fields=snapshot_id", got)
			}
			return libraryFixtureResponse(200, `{"snapshot_id":"snapshot-rock"}`), nil
		case "/v1/me":
			// El catálogo autorizado también alimenta la etiqueta de cuenta.
			return libraryFixtureResponse(200, `{"display_name":"ana"}`), nil
		}
		t.Fatalf("unexpected request %s", r.URL.Path)
		return nil, nil
	})
	m, out, _ := libraryNewPlayer(t, 120, 40, preferences{}, playerState{Connected: true, SessionID: 31}, catalog, nil, "")
	playerViewText(t, m, "Rock", "ana")
	m, cmd := libraryOpenFirst(t, m)
	m = librarySettle(t, m, cmd)
	playerViewText(t, m, "Rock · 1 tracks", "Tren al Sur", "5:36")
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	select {
	case got := <-out:
		if want := (playerCommand{Name: "play", URI: "spotify:playlist:rock", SessionID: 31}); got != want {
			t.Fatalf("got %+v, want %+v", got, want)
		}
	default:
		t.Fatal("the authorized catalog did not play a track")
	}
}

func TestLibraryBounds(t *testing.T) {
	source := &libraryFake{playlists: libraryPlaylistsFixture(), items: libraryTracksFixture(42)}
	m, _, _ := libraryNewPlayer(t, 120, 40, preferences{}, playerState{
		Connected: true, Status: "playing", Volume: 37, PositionMS: 62000,
		Track: playerTrack{Title: "Estrechez de Corazón", Artist: "Los Prisioneros", DurationMS: 381000},
	}, source, nil, "")
	var cmd tea.Cmd
	m, cmd = libraryOpenFirst(t, m)
	m = librarySettle(t, m, cmd)
	for _, size := range []struct{ width, height int }{
		{120, 40}, {100, 30}, {80, 24}, {40, 12}, {20, 8}, {12, 5}, {1, 1},
	} {
		t.Run(fmt.Sprintf("%dx%d", size.width, size.height), func(t *testing.T) {
			playerViewRows(t, m, size.width, size.height)
		})
	}
}
