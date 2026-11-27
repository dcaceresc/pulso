package ui

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
)

// searchTracksFixture serves one page and then a second one at the shared page
// size, so moving past the loaded rows reaches a real request.
func searchTracksFixture(total int) func(context.Context, string, int) ([]libraryTrack, int, bool, error) {
	return func(_ context.Context, _ string, offset int) ([]libraryTrack, int, bool, error) {
		if offset == 0 {
			return []libraryTrack{
				{Name: "Tren al Sur", Artist: "Los Prisioneros", DurationMS: 336000, URI: "spotify:track:one"},
				{Name: "Estrechez de Corazón", Artist: "Los Prisioneros", DurationMS: 381000, URI: "spotify:track:two"},
				{Name: "Llueve sobre la Ciudad", Artist: "Los Bunkers", DurationMS: 236000, URI: "spotify:track:three"},
			}, total, true, nil
		}
		return []libraryTrack{{Name: "Página dos", Artist: "Artista", DurationMS: 200000, URI: "spotify:track:page2"}}, total, false, nil
	}
}

// searchWorkspace builds a 120x40 workspace whose table and search share one
// fake Spotify source, and settles the startup playlist page.
func searchWorkspace(t *testing.T, p preferences, state playerState, source *libraryFake) (tea.Model, chan playerCommand) {
	t.Helper()
	m, out, _ := libraryNewPlayer(t, 120, 40, p, state, source, nil, "")
	return m, out
}

// searchSubmit types a query into the top bar and settles the first page.
func searchSubmit(t *testing.T, m tea.Model, query string) tea.Model {
	t.Helper()
	m, _ = m.Update(playerViewKey("/"))
	m, _ = m.Update(playerViewKey(query))
	m, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("submitting a search scheduled no fetch")
	}
	return librarySettle(t, m, cmd)
}

func TestSearchFirstPageReplacesTable(t *testing.T) {
	source := &libraryFake{playlists: libraryPlaylistsFixture(), items: libraryTracksFixture(42), search: searchTracksFixture(42)}
	m, out := searchWorkspace(t, preferences{}, playerState{Connected: true, SessionID: 7}, source)
	m, cmd := libraryOpenFirst(t, m)
	m = librarySettle(t, m, cmd)
	playerViewText(t, m, "Rock Chileno · 42 tracks", "Tren al Sur")

	m, _ = m.Update(playerViewKey("/"))
	playerViewText(t, m, "Search: _")
	m, _ = m.Update(playerViewKey("tren"))
	m, cmd = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("Enter on a non-empty query did not fetch the first page")
	}
	// The loading state is one localized row before the page lands.
	playerViewText(t, m, "Search", "Loading")
	m = librarySettle(t, m, cmd)

	queries, offsets := source.searchRequests()
	if len(queries) != 1 || queries[0] != "tren" || len(offsets) != 1 || offsets[0] != 0 {
		t.Fatalf("search requests = %q %v, want the first page of tren", queries, offsets)
	}
	view := playerViewText(t, m, "Search: tren · 42 tracks", ">1 Tren al Sur", "5:36", "6:21", "3:56")
	if strings.Contains(view, "Rock Chileno · 42") {
		t.Fatalf("the playlist table survived the search:\n%s", view)
	}
	if len(out) != 0 {
		t.Fatal("fetching the first search page played something")
	}
}

func TestSearchSecondPageLoadsPastCursor(t *testing.T) {
	source := &libraryFake{playlists: libraryPlaylistsFixture(), items: libraryTracksFixture(42), search: searchTracksFixture(42)}
	m, _ := searchWorkspace(t, preferences{}, playerState{Connected: true, SessionID: 3}, source)
	m = searchSubmit(t, m, "tren")
	playerViewText(t, m, ">1 Tren al Sur")

	for range 3 {
		var cmd tea.Cmd
		m, cmd = m.Update(playerViewKey("j"))
		if cmd != nil {
			m = librarySettle(t, m, cmd)
		}
	}
	_, offsets := source.searchRequests()
	if len(offsets) != 2 || offsets[1] != spotifyCatalogPageSize {
		t.Fatalf("search offsets = %v, want a second page at the shared page size", offsets)
	}
	playerViewText(t, m, ">4 Página dos")
}

func TestSearchEnterPlaysTheBoundSession(t *testing.T) {
	source := &libraryFake{playlists: libraryPlaylistsFixture(), items: libraryTracksFixture(42), search: searchTracksFixture(42)}
	m, out := searchWorkspace(t, preferences{}, playerState{Connected: true, SessionID: 21}, source)
	m = searchSubmit(t, m, "tren")
	m, _ = m.Update(playerViewKey("j"))
	playerViewText(t, m, ">2 Estrechez de Corazón")
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd != nil {
		t.Fatal("playing a result scheduled work")
	}
	select {
	case got := <-out:
		if want := (playerCommand{Name: "play", URI: "spotify:track:two", SessionID: 21}); got != want {
			t.Fatalf("got %+v, want %+v", got, want)
		}
	default:
		t.Fatal("Enter on a search result did not play it")
	}
}

func TestSearchSendsNothingWhenDisconnected(t *testing.T) {
	source := &libraryFake{playlists: libraryPlaylistsFixture(), items: libraryTracksFixture(42), search: searchTracksFixture(42)}
	m, out := searchWorkspace(t, preferences{}, playerState{SessionID: 4}, source)
	m = searchSubmit(t, m, "tren")
	// The results still load while Soloist is away.
	playerViewText(t, m, "Tren al Sur")
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if len(out) != 0 {
		t.Fatal("Enter sent a command while disconnected")
	}
}

func TestSearchEscapeRestoresThePlaylistTable(t *testing.T) {
	source := &libraryFake{playlists: libraryPlaylistsFixture(), items: libraryTracksFixture(42), search: searchTracksFixture(42)}
	m, _ := searchWorkspace(t, preferences{}, playerState{Connected: true, SessionID: 7}, source)
	m, cmd := libraryOpenFirst(t, m)
	m = librarySettle(t, m, cmd)
	m, _ = m.Update(playerViewKey("j"))
	playerViewText(t, m, ">2 Estrechez de Corazón")

	m = searchSubmit(t, m, "tren")
	playerViewText(t, m, ">1 Tren al Sur")
	// The first Esc leaves the results, the second one behaves as today.
	m, cmd = m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if cmd != nil {
		t.Fatal("leaving the search results scheduled work")
	}
	view := playerViewText(t, m, "Rock Chileno · 42 tracks", ">2 Estrechez de Corazón")
	if strings.Contains(view, ">1 Tren al Sur") {
		t.Fatalf("the search results survived Esc:\n%s", view)
	}
	m, quit := m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if _, ok := playerViewRun(t, quit).(tea.QuitMsg); !ok {
		t.Fatal("a second Esc did not quit")
	}
}

func TestSearchEscapeBlursTheField(t *testing.T) {
	source := &libraryFake{playlists: libraryPlaylistsFixture(), search: searchTracksFixture(42)}
	m, out := searchWorkspace(t, preferences{}, playerState{Connected: true, SessionID: 7}, source)
	m, _ = m.Update(playerViewKey("/"))
	m, _ = m.Update(playerViewKey("zzr"))
	m, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if cmd != nil {
		t.Fatal("Esc in a search field with no results quit the workspace")
	}
	view := playerViewText(t, m, "Search: zzr")
	if strings.Contains(view, "Search: zzr_") {
		t.Fatalf("the blurred field kept its cursor:\n%s", view)
	}
	if len(out) != 0 {
		t.Fatal("blurring the search field sent a command")
	}
}

func TestSearchEmptyQueryNeverFetches(t *testing.T) {
	source := &libraryFake{playlists: libraryPlaylistsFixture(), search: searchTracksFixture(42)}
	m, out := searchWorkspace(t, preferences{}, playerState{Connected: true}, source)
	m, _ = m.Update(playerViewKey("/"))
	if _, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter}); cmd != nil {
		t.Fatal("Enter with an empty query scheduled a fetch")
	}
	m, _ = m.Update(playerViewKey("   "))
	if _, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter}); cmd != nil {
		t.Fatal("Enter with a blank query scheduled a fetch")
	}
	if queries, offsets := source.searchRequests(); len(queries) != 0 || len(offsets) != 0 {
		t.Fatalf("an empty query reached the source: %q %v", queries, offsets)
	}
	if player := m.(*playerModel); player.focus != playerFocusSearch {
		t.Fatal("an empty query left the search field")
	}
	if view := ansi.Strip(m.View()); strings.Contains(view, "Loading") {
		t.Fatalf("an empty query started a search:\n%s", view)
	}
	if len(out) != 0 {
		t.Fatal("an empty query sent a command")
	}
}

func TestSearchErrorRowAndRetry(t *testing.T) {
	failing := func(context.Context, string, int) ([]libraryTrack, int, bool, error) {
		return nil, 0, false, errSpotifyCatalog
	}
	source := &libraryFake{playlists: libraryPlaylistsFixture(), search: failing}
	m, _ := searchWorkspace(t, preferences{}, playerState{Connected: true, SessionID: 1}, source)
	m = searchSubmit(t, m, "tren")
	view := playerViewText(t, m, "Could not load Spotify data. Enter: retry.")
	if strings.Contains(view, "Loading") {
		t.Fatalf("a failed search stayed loading:\n%s", view)
	}
	// Enter on the failed table retries the first page.
	m, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("Enter did not retry the failed search")
	}
	m = librarySettle(t, m, cmd)
	if queries, offsets := source.searchRequests(); len(queries) != 2 || offsets[1] != 0 {
		t.Fatalf("retry requests = %q %v, want the first page again", queries, offsets)
	}

	localized := &libraryFake{playlists: libraryPlaylistsFixture(), search: failing}
	es, _ := searchWorkspace(t, preferences{Language: "es"}, playerState{Connected: true}, localized)
	es = searchSubmit(t, es, "tren")
	playerViewText(t, es, "No se pudieron cargar los datos de Spotify")
}

func TestSearchEmptyResultsRow(t *testing.T) {
	source := &libraryFake{playlists: libraryPlaylistsFixture(), search: func(context.Context, string, int) ([]libraryTrack, int, bool, error) {
		return nil, 0, false, nil
	}}
	m, _ := searchWorkspace(t, preferences{}, playerState{Connected: true}, source)
	m = searchSubmit(t, m, "tren")
	playerViewText(t, m, "Nothing here yet")

	localized := &libraryFake{playlists: libraryPlaylistsFixture(), search: func(context.Context, string, int) ([]libraryTrack, int, bool, error) {
		return nil, 0, false, nil
	}}
	es, _ := searchWorkspace(t, preferences{Language: "es"}, playerState{Connected: true}, localized)
	es = searchSubmit(t, es, "tren")
	playerViewText(t, es, "Aún no hay nada")
}

func TestSearchUnauthorizedRow(t *testing.T) {
	authorize := func(context.Context, string, func(string)) (CatalogAPI, error) {
		return nil, errors.New("offline fixture")
	}
	for _, tt := range []struct {
		language string
		want     string
	}{
		{"en", "● No Spotify. Enter: connect"},
		{"es", "● Sin Spotify. Enter: conectar"},
	} {
		t.Run(tt.language, func(t *testing.T) {
			m, out, _ := libraryNewPlayer(t, 120, 40, preferences{Language: tt.language}, playerState{Connected: true, SessionID: 3}, nil, authorize, "fixture-client")
			m, _ = m.Update(playerViewKey("/"))
			m, _ = m.Update(playerViewKey("tren"))
			m, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
			if cmd != nil {
				t.Fatal("an unauthorized search scheduled a fetch")
			}
			playerViewText(t, m, tt.want)
			if len(out) != 0 {
				t.Fatal("an unauthorized search sent a command")
			}
		})
	}
}

func TestSearchStaleGenerationDropped(t *testing.T) {
	source := &libraryFake{playlists: libraryPlaylistsFixture(), search: func(_ context.Context, query string, _ int) ([]libraryTrack, int, bool, error) {
		if query == "first" {
			return []libraryTrack{{Name: "Stale result", URI: "spotify:track:stale"}}, 1, false, nil
		}
		return []libraryTrack{{Name: "Fresh result", URI: "spotify:track:fresh"}}, 1, false, nil
	}}
	m, _ := searchWorkspace(t, preferences{}, playerState{Connected: true, SessionID: 5}, source)
	m, _ = m.Update(playerViewKey("/"))
	m, _ = m.Update(playerViewKey("first"))
	m, stale := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if stale == nil {
		t.Fatal("the first search scheduled no fetch")
	}

	// A newer submit supersedes the first request.
	m, _ = m.Update(playerViewKey("/"))
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyCtrlU})
	m, _ = m.Update(playerViewKey("second"))
	m, fresh := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = librarySettle(t, m, fresh)
	playerViewText(t, m, "Fresh result")

	m, _ = m.Update(playerViewRun(t, stale))
	if view := ansi.Strip(m.View()); strings.Contains(view, "Stale result") {
		t.Fatalf("a stale search page reached the table:\n%s", view)
	}
}

func TestSearchFieldReachableFromEveryPanel(t *testing.T) {
	for _, tt := range []struct {
		name string
		tabs int
	}{
		{"table", 0},
		{"search", 1},
		{"playing bar", 2},
		{"sidebar", 3},
	} {
		t.Run(tt.name, func(t *testing.T) {
			m, out := playerViewModel(t, 120, 40, playerState{Connected: true})
			for range tt.tabs {
				m, _ = m.Update(tea.KeyMsg{Type: tea.KeyTab})
			}
			m, _ = m.Update(playerViewKey("/"))
			if player := m.(*playerModel); player.focus != playerFocusSearch {
				t.Fatalf("/ did not focus the search field from the %s", tt.name)
			}
			playerViewText(t, m, "Search:")
			if len(out) != 0 {
				t.Fatal("/ sent a playback command")
			}
		})
	}
}

func TestSearchBounds(t *testing.T) {
	source := &libraryFake{playlists: libraryPlaylistsFixture(), items: libraryTracksFixture(42), search: searchTracksFixture(42)}
	m, _ := searchWorkspace(t, preferences{}, playerState{
		Connected: true, Status: "playing", Volume: 37, PositionMS: 62000,
		Track: playerTrack{Title: "Estrechez de Corazón", Artist: "Los Prisioneros", DurationMS: 381000},
	}, source)
	m = searchSubmit(t, m, "tren")
	for _, size := range []struct{ width, height int }{
		{120, 40}, {100, 30}, {80, 24}, {40, 12}, {20, 8}, {12, 5}, {1, 1},
	} {
		t.Run(fmt.Sprintf("%dx%d", size.width, size.height), func(t *testing.T) {
			playerViewRows(t, m, size.width, size.height)
		})
	}
}
