package ui

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/dcaceresc/pulso/internal/spotify"
)

// saveFixtureLink es el enlace de aprobación que el catálogo ficticio publica;
// usa la misma forma que el que construye el flujo real.
const saveFixtureLink = "https://accounts.spotify.com/authorize?client_id=fixture-client&response_type=code&redirect_uri=http%3A%2F%2F127.0.0.1%3A8888%2Fcallback&scope=playlist-modify-private&state=fixture&code_challenge=fixture&code_challenge_method=S256"

// saveCall es una petición que llegó al catálogo ficticio de escritura.
type saveCall struct {
	playlistURI, trackURI string
}

// saveFake es el catálogo escribible de las pruebas: pagina listas como la
// biblioteca, registra cada guardado y puede conceder el permiso una vez, para
// que cada paso del flujo sea observable sin red.
type saveFake struct {
	*libraryFake
	mu     sync.Mutex
	calls  []saveCall
	add    func(call int, playlistURI, trackURI string) (string, error)
	grant  func(showURL func(string)) (spotify.PlaylistWriter, error)
	links  []string
	grants int

	account  string
	released bool
}

func newSaveFake(playlists func(context.Context, int) ([]libraryPlaylist, int, bool, error)) *saveFake {
	return &saveFake{libraryFake: &libraryFake{playlists: playlists, tracks: libraryTracksFixture(2)}}
}

func (f *saveFake) AddToPlaylist(_ context.Context, playlistURI, trackURI string) (string, error) {
	f.mu.Lock()
	f.calls = append(f.calls, saveCall{playlistURI: playlistURI, trackURI: trackURI})
	call, add := len(f.calls), f.add
	f.mu.Unlock()
	if add == nil {
		return "snapshot-fixture", nil
	}
	return add(call, playlistURI, trackURI)
}

func (f *saveFake) ReauthorizeWrites(_ context.Context, showURL func(string)) (spotify.PlaylistWriter, error) {
	f.mu.Lock()
	f.grants++
	grant := f.grant
	f.mu.Unlock()
	if grant == nil {
		return nil, spotify.ErrSpotifyApprovalDeclined
	}
	return grant(func(raw string) {
		f.mu.Lock()
		f.links = append(f.links, raw)
		f.mu.Unlock()
		showURL(raw)
	})
}

func (f *saveFake) saved() []saveCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]saveCall(nil), f.calls...)
}

// Profile responde como la cuenta de este catálogo y deja ver cuál la sirve.
func (f *saveFake) Profile(context.Context) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.account, nil
}

// ReleaseSession marca que este catálogo dejó de guardar la sesión aprobada.
func (f *saveFake) ReleaseSession() { f.released = true }

func (f *saveFake) approval() (links []string, grants int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.links...), f.grants
}

// saveTrackState is a paused session whose Soloist item reported a track URI.
func saveTrackState() playerState {
	return playerState{
		Connected: true, SessionID: 5, Status: "paused", Volume: 50,
		Track: playerTrack{Title: "Song", Artist: "Artist", DurationMS: 200000, URI: "spotify:track:one"},
	}
}

// savePlayer builds the workspace over a catalog and replaces the browser hook,
// so no test opens a real browser.
func savePlayer(t *testing.T, p preferences, state playerState, source librarySource) (tea.Model, chan playerCommand, *[]string) {
	t.Helper()
	m, out, _ := libraryNewPlayer(t, 120, 40, p, state, source, nil, "")
	opened := &[]string{}
	m.(*playerModel).openBrowser = func(link string) error {
		*opened = append(*opened, link)
		return nil
	}
	return m, out, opened
}

// TestSaveActionAvailability proves the action exists only when it can work: a
// catalog that cannot write, a disconnected session or an item that is not a
// track never open the chooser nor advertise the key.
func TestSaveActionAvailability(t *testing.T) {
	t.Run("catálogo de solo lectura", func(t *testing.T) {
		readOnly := &libraryFake{playlists: libraryPlaylistsFixture()}
		m, out, _ := savePlayer(t, preferences{}, saveTrackState(), readOnly)
		if view := ansi.Strip(m.View()); strings.Contains(view, "a save") {
			t.Fatalf("a read-only catalog advertised saving:\n%s", view)
		}
		m, cmd := m.Update(playerViewKey("a"))
		if cmd != nil {
			t.Fatal("a read-only catalog scheduled save work")
		}
		if view := ansi.Strip(m.View()); strings.Contains(view, "Save to playlist") {
			t.Fatalf("a read-only catalog opened the chooser:\n%s", view)
		}
		if len(out) != 0 {
			t.Fatal("the save key sent a playback command")
		}
	})

	t.Run("pista sin uri", func(t *testing.T) {
		api := newSaveFake(libraryPlaylistsFixture())
		state := saveTrackState()
		state.Track.URI = ""
		m, out, _ := savePlayer(t, preferences{}, state, api)
		m, cmd := m.Update(playerViewKey("a"))
		if cmd != nil {
			t.Fatal("a track without a URI scheduled save work")
		}
		if view := ansi.Strip(m.View()); strings.Contains(view, "Save to playlist") {
			t.Fatalf("a track without a URI opened the chooser:\n%s", view)
		}
		if len(out) != 0 {
			t.Fatal("the save key sent a playback command")
		}
	})

	t.Run("elemento que no es pista", func(t *testing.T) {
		api := newSaveFake(libraryPlaylistsFixture())
		state := saveTrackState()
		state.Track.URI = "spotify:episode:one"
		m, _, _ := savePlayer(t, preferences{}, state, api)
		m, _ = m.Update(playerViewKey("a"))
		if view := ansi.Strip(m.View()); strings.Contains(view, "Save to playlist") {
			t.Fatalf("an episode opened the chooser:\n%s", view)
		}
	})

	t.Run("sin conexión", func(t *testing.T) {
		api := newSaveFake(libraryPlaylistsFixture())
		state := saveTrackState()
		state.Connected = false
		m, _, _ := savePlayer(t, preferences{}, state, api)
		m, _ = m.Update(playerViewKey("a"))
		if view := ansi.Strip(m.View()); strings.Contains(view, "Save to playlist") {
			t.Fatalf("a disconnected session opened the chooser:\n%s", view)
		}
	})

	t.Run("catálogo nil con tipo", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		t.Cleanup(cancel)
		states := make(chan playerState, 1)
		states <- saveTrackState()
		var missing *spotify.Catalog
		model := NewPlayerModelWithCatalog(ctx, states, nil, 120, 40, preferences{}, nil, missing, nil, "")
		model = librarySettle(t, model, model.Init())
		model, cmd := model.Update(playerViewKey("a"))
		if cmd != nil {
			t.Fatal("a typed nil catalog scheduled save work")
		}
		if view := ansi.Strip(model.View()); strings.Contains(view, "Save to playlist") {
			t.Fatalf("a typed nil catalog opened the chooser:\n%s", view)
		}
	})

	t.Run("la pista deja de estar disponible", func(t *testing.T) {
		api := newSaveFake(libraryPlaylistsFixture())
		m, _, _ := savePlayer(t, preferences{}, saveTrackState(), api)
		m, _ = m.Update(playerViewKey("a"))
		player := m.(*playerModel)
		player.pending = true
		state := saveTrackState()
		state.Track.URI = ""
		m, _ = m.Update(playerViewState{state: state, ok: true})
		if view := ansi.Strip(m.View()); strings.Contains(view, "Save to playlist") {
			t.Fatalf("the chooser survived a track that cannot be saved:\n%s", view)
		}
	})

	t.Run("con escritura", func(t *testing.T) {
		api := newSaveFake(libraryPlaylistsFixture())
		m, out, _ := savePlayer(t, preferences{}, saveTrackState(), api)
		playerViewText(t, m, "a save")
		m, cmd := m.Update(playerViewKey("a"))
		if cmd != nil {
			t.Fatal("opening the chooser scheduled work")
		}
		view := playerViewText(t, m, "Save to playlist · Rock Chileno", "Rock Chileno", "Jazz")
		if strings.Contains(view, "Saved to") {
			t.Fatalf("the chooser claimed a result before saving:\n%s", view)
		}
		if len(out) != 0 {
			t.Fatal("opening the chooser sent a playback command")
		}
	})
}

// TestSaveChooserConfirmsOnlyAfterSpotifyAccepts follows the chooser: the cursor
// names the target, Enter sends exactly one request and the notice appears only
// when Spotify accepted it.
func TestSaveChooserConfirmsOnlyAfterSpotifyAccepts(t *testing.T) {
	api := newSaveFake(libraryPlaylistsFixture())
	m, out, _ := savePlayer(t, preferences{}, saveTrackState(), api)
	m, _ = m.Update(playerViewKey("a"))
	playerViewText(t, m, "Save to playlist · Rock Chileno")
	m, _ = m.Update(playerViewKey("j"))
	playerViewText(t, m, "Save to playlist · Jazz")
	// El cursor no pasa del último elemento.
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyDown})
	playerViewText(t, m, "Save to playlist · Jazz")
	m, _ = m.Update(playerViewKey("k"))
	playerViewText(t, m, "Save to playlist · Rock Chileno")
	// Las flechas y jk comparten los hábitos de la biblioteca.
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyDown})
	playerViewText(t, m, "Save to playlist · Jazz")

	m, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("Enter on the chooser started no save")
	}
	if view := ansi.Strip(m.View()); strings.Contains(view, "Saved to") {
		t.Fatalf("the workspace announced a save before Spotify accepted:\n%s", view)
	}
	m = librarySettle(t, m, cmd)
	if got := api.saved(); len(got) != 1 || got[0] != (saveCall{playlistURI: "spotify:playlist:jazz", trackURI: "spotify:track:one"}) {
		t.Fatalf("saved = %+v, want one request for Jazz", got)
	}
	view := playerViewText(t, m, "Saved to Jazz.")
	if strings.Contains(view, "Save to playlist") {
		t.Fatalf("the chooser stayed open after the save:\n%s", view)
	}
	playerViewText(t, m, "Song")
	if len(out) != 0 {
		t.Fatal("saving touched playback")
	}

	// La misma acción se lee en español.
	es := newSaveFake(libraryPlaylistsFixture())
	localized, _, _ := savePlayer(t, preferences{Language: "es"}, saveTrackState(), es)
	localized, _ = localized.Update(playerViewKey("a"))
	playerViewText(t, localized, "Guardar en lista · Rock Chileno", "Enter guardar")
	localized, cmd = localized.Update(tea.KeyMsg{Type: tea.KeyEnter})
	localized = librarySettle(t, localized, cmd)
	playerViewText(t, localized, "Guardada en Rock Chileno.")
}

// TestSaveChooserCancels keeps Esc as the way out and restores the key's usual
// meaning once the panel is closed.
func TestSaveChooserCancels(t *testing.T) {
	api := newSaveFake(libraryPlaylistsFixture())
	m, out, _ := savePlayer(t, preferences{}, saveTrackState(), api)
	m, _ = m.Update(playerViewKey("a"))
	m, _ = m.Update(playerViewKey("j"))
	before := ansi.Strip(m.View())
	m, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if cmd != nil {
		t.Fatal("Esc scheduled work")
	}
	if view := ansi.Strip(m.View()); strings.Contains(view, "Save to playlist") {
		t.Fatalf("Esc left the chooser open:\n%s", view)
	}
	if strings.Contains(ansi.Strip(m.View()), "Saved to") {
		t.Fatalf("Esc reported a save:\n%s", before)
	}
	if len(api.saved()) != 0 {
		t.Fatalf("Esc saved %+v", api.saved())
	}
	if len(out) != 0 {
		t.Fatal("cancelling sent a playback command")
	}
	m, quit := m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if _, ok := playerViewRun(t, quit).(tea.QuitMsg); !ok {
		t.Fatal("Esc no longer quit after the chooser closed")
	}
}

// TestSaveAsksForWritePermissionThenRetriesOnce reproduces an older read-only
// approval: the panel offers the link, the new catalog replaces the old one and
// the save is retried exactly once.
func TestSaveAsksForWritePermissionThenRetriesOnce(t *testing.T) {
	upgraded := newSaveFake(libraryPlaylistsFixture())
	api := newSaveFake(libraryPlaylistsFixture())
	api.add = func(int, string, string) (string, error) { return "", spotify.ErrSpotifyWriteScope }
	api.grant = func(showURL func(string)) (spotify.PlaylistWriter, error) {
		showURL(saveFixtureLink)
		return upgraded, nil
	}
	m, out, opened := savePlayer(t, preferences{}, saveTrackState(), api)
	m, _ = m.Update(playerViewKey("a"))
	m, _ = m.Update(playerViewKey("j"))
	m, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = librarySettle(t, m, cmd)
	playerViewText(t, m, "Spotify needs your approval to change playlists. Enter: authorize again.")
	if len(api.saved()) != 1 || len(upgraded.saved()) != 0 {
		t.Fatalf("attempts before approval: %+v / %+v", api.saved(), upgraded.saved())
	}

	m, cmd = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("Enter did not start the approval")
	}
	m, cmd = m.Update(playerViewRun(t, cmd))
	// El enlace se presenta como en el asistente: el navegador lo abre y la URL
	// queda a la vista mientras se espera.
	playerViewText(t, m, "Waiting for Spotify…", "accounts.spotify.com/authorize")
	if len(*opened) != 1 || !strings.HasPrefix((*opened)[0], saveFixtureLink) {
		t.Fatalf("browser hook = %v, want the approval link", *opened)
	}
	if links, grants := api.approval(); grants != 1 || len(links) != 1 {
		t.Fatalf("approvals = %v, granted %d", links, grants)
	}

	m = librarySettle(t, m, cmd)
	if got := upgraded.saved(); len(got) != 1 || got[0] != (saveCall{playlistURI: "spotify:playlist:jazz", trackURI: "spotify:track:one"}) {
		t.Fatalf("retried save = %+v, want one request for Jazz", got)
	}
	if len(api.saved()) != 1 {
		t.Fatalf("the read-only approval was reused: %+v", api.saved())
	}
	playerViewText(t, m, "Saved to Jazz.")
	if len(out) != 0 {
		t.Fatal("recovering the permission touched playback")
	}
}

// TestSaveApprovalKeepsTheTrackAndAdoptsTheCatalog proves the approval cycle
// fixes the confirmed track and leaves the whole workspace on the new catalog.
func TestSaveApprovalKeepsTheTrackAndAdoptsTheCatalog(t *testing.T) {
	upgraded := newSaveFake(libraryPlaylistsFixture())
	upgraded.account = "Approved account"
	api := newSaveFake(libraryPlaylistsFixture())
	api.add = func(int, string, string) (string, error) { return "", spotify.ErrSpotifyWriteScope }
	api.grant = func(showURL func(string)) (spotify.PlaylistWriter, error) {
		showURL(saveFixtureLink)
		return upgraded, nil
	}
	m, _, _ := savePlayer(t, preferences{}, saveTrackState(), api)
	m, _ = m.Update(playerViewKey("a"))
	m, _ = m.Update(playerViewKey("j"))
	m, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = librarySettle(t, m, cmd)
	m, cmd = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m, cmd = m.Update(playerViewRun(t, cmd))
	// La reproducción avanza mientras la persona aprueba en el navegador.
	m.(*playerModel).pending = true
	m, _ = m.Update(playerViewState{ok: true, state: playerState{Connected: true, SessionID: 5, Track: playerTrack{URI: "spotify:track:two"}}})
	m = librarySettle(t, m, cmd)
	if got := upgraded.saved(); len(got) != 1 || got[0] != (saveCall{playlistURI: "spotify:playlist:jazz", trackURI: "spotify:track:one"}) {
		t.Fatalf("retried save = %+v, want the confirmed track for Jazz", got)
	}
	// La lista y la cuenta ya leen con la sesión aprobada.
	playerViewText(t, m, "Saved to Jazz.", "@Approved account")
	if !api.released {
		t.Fatal("the replaced catalog kept saving the session")
	}
	m, cmd = libraryOpenFirst(t, m)
	m = librarySettle(t, m, cmd)
	if _, tracks, _ := upgraded.requests(); len(tracks) == 0 {
		t.Fatal("the library kept reading with the replaced catalog")
	}
}

// TestSaveNoticeGivesWayToLaterSoloistNotices proves a save result stays until
// the person reads it and never hides a later playback or connection notice.
func TestSaveNoticeGivesWayToLaterSoloistNotices(t *testing.T) {
	api := newSaveFake(libraryPlaylistsFixture())
	m, _, _ := savePlayer(t, preferences{}, saveTrackState(), api)
	m, _ = m.Update(playerViewKey("a"))
	m, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = librarySettle(t, m, cmd)
	playerViewText(t, m, "Saved to Rock Chileno.")
	player := m.(*playerModel)
	// Un estado sin aviso no borra el resultado antes de que se lea.
	player.pending = true
	m, _ = m.Update(playerViewState{state: saveTrackState(), ok: true})
	playerViewText(t, m, "Saved to Rock Chileno.")
	// Un aviso posterior de Soloist vuelve a mostrarse, aunque la conexión caiga.
	later := saveTrackState()
	later.Connected, later.Notice = false, "Soloist reported a playback error."
	player.pending = true
	m, _ = m.Update(playerViewState{state: later, ok: true})
	if view := playerViewText(t, m, "Soloist reported a playback error.", "Disconnected"); strings.Contains(view, "Saved to") {
		t.Fatalf("the save notice hid a later notice:\n%s", view)
	}
}

// TestSaveRecoveryDeclined keeps a declined approval as a quiet notice and never
// repeats the save.
func TestSaveRecoveryDeclined(t *testing.T) {
	api := newSaveFake(libraryPlaylistsFixture())
	api.add = func(int, string, string) (string, error) { return "", spotify.ErrSpotifyWriteScope }
	api.grant = func(showURL func(string)) (spotify.PlaylistWriter, error) {
		showURL(saveFixtureLink)
		return nil, spotify.ErrSpotifyApprovalDeclined
	}
	m, out, opened := savePlayer(t, preferences{}, saveTrackState(), api)
	m, _ = m.Update(playerViewKey("a"))
	m, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = librarySettle(t, m, cmd)
	m, cmd = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = librarySettle(t, m, cmd)
	playerViewText(t, m, "Spotify authorization did not complete; nothing was saved.")
	if len(api.saved()) != 1 {
		t.Fatalf("a declined approval repeated the save: %+v", api.saved())
	}
	if len(*opened) != 1 {
		t.Fatalf("browser hook = %v, want one approval link", *opened)
	}
	if view := ansi.Strip(m.View()); strings.Contains(view, "Save to playlist") {
		t.Fatalf("the chooser stayed open after a declined approval:\n%s", view)
	}
	playerViewText(t, m, "Song")
	if len(out) != 0 {
		t.Fatal("a declined approval touched playback")
	}

	t.Run("la persona rechaza la oferta", func(t *testing.T) {
		api := newSaveFake(libraryPlaylistsFixture())
		api.add = func(int, string, string) (string, error) { return "", spotify.ErrSpotifyWriteScope }
		m, _, opened := savePlayer(t, preferences{}, saveTrackState(), api)
		m, _ = m.Update(playerViewKey("a"))
		m, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
		m = librarySettle(t, m, cmd)
		playerViewText(t, m, "Enter: authorize again.")
		m, cmd = m.Update(tea.KeyMsg{Type: tea.KeyEsc})
		if cmd != nil {
			t.Fatal("Esc during the offer scheduled work")
		}
		if view := ansi.Strip(m.View()); strings.Contains(view, "Save to playlist") || strings.Contains(view, "authorize again") {
			t.Fatalf("Esc left the offer open:\n%s", view)
		}
		if len(api.saved()) != 1 || len(*opened) != 0 {
			t.Fatalf("declining the offer saved %+v and opened %v", api.saved(), *opened)
		}
		if _, grants := api.approval(); grants != 0 {
			t.Fatalf("declining the offer approved %d times", grants)
		}
	})
}

// TestSaveFailuresAreQuietAndLocalized covers the remaining rejections: the
// notice is localized, never carries the response body and never blocks.
func TestSaveFailuresAreQuietAndLocalized(t *testing.T) {
	const secret = "fixture secret body"
	for _, tt := range []struct {
		name, language string
		failure        error
		want, hint     string
	}{
		{"lista ajena", "en", spotify.ErrPlaylistNotEditable,
			"Could not save to Rock Chileno: this account cannot change that playlist.", "a save"},
		{"fallo sin clasificar", "en", fmt.Errorf("%s: %w", secret, spotify.ErrSpotifyCatalog),
			"Could not save to Rock Chileno.", "a save"},
		{"lista ajena en español", "es", spotify.ErrPlaylistNotEditable,
			"No se pudo guardar en Rock Chileno: esta cuenta no puede cambiar esa lista.", "a guardar"},
		{"fallo en español", "es", fmt.Errorf("%s: %w", secret, spotify.ErrSpotifyCatalog),
			"No se pudo guardar en Rock Chileno.", "a guardar"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			api := newSaveFake(libraryPlaylistsFixture())
			api.add = func(int, string, string) (string, error) { return "", tt.failure }
			m, out, _ := savePlayer(t, preferences{Language: tt.language}, saveTrackState(), api)
			m, _ = m.Update(playerViewKey("a"))
			m, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
			m = librarySettle(t, m, cmd)
			view := playerViewText(t, m, tt.want)
			if strings.Contains(view, secret) {
				t.Fatalf("the response body reached the view:\n%s", view)
			}
			if strings.Contains(view, "Save to playlist") {
				t.Fatalf("the chooser stayed open after a failure:\n%s", view)
			}
			// El aviso no bloquea: la pista y sus controles siguen ahí.
			playerViewText(t, m, "Song", tt.hint)
			if len(out) != 0 {
				t.Fatal("a failed save touched playback")
			}
		})
	}
	// Una lista ajena no ofrece aprobar otra vez.
	api := newSaveFake(libraryPlaylistsFixture())
	api.add = func(int, string, string) (string, error) { return "", spotify.ErrPlaylistNotEditable }
	m, _, _ := savePlayer(t, preferences{}, saveTrackState(), api)
	m, _ = m.Update(playerViewKey("a"))
	m, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = librarySettle(t, m, cmd)
	if _, next := m.Update(tea.KeyMsg{Type: tea.KeyEnter}); next != nil {
		t.Fatal("a playlist this account cannot change offered an approval")
	}
	if _, grants := api.approval(); grants != 0 {
		t.Fatalf("approvals = %d, want none", grants)
	}
}

// TestSaveChooserReusesPlaylistPaging proves the chooser reads the same playlist
// page the library loads, including its paging.
func TestSaveChooserReusesPlaylistPaging(t *testing.T) {
	page := func(_ context.Context, offset int) ([]libraryPlaylist, int, bool, error) {
		if offset != 0 {
			return []libraryPlaylist{{Name: "Cuecas", URI: "spotify:playlist:cuecas", Total: 8}}, 3, false, nil
		}
		return []libraryPlaylist{
			{Name: "Rock Chileno", URI: "spotify:playlist:rock", Total: 40},
			{Name: "Jazz", URI: "spotify:playlist:jazz", Total: 12},
		}, 3, true, nil
	}
	api := newSaveFake(page)
	m, _, _ := savePlayer(t, preferences{}, saveTrackState(), api)
	m, _ = m.Update(playerViewKey("a"))
	m, _ = m.Update(playerViewKey("j"))
	m, cmd := m.Update(playerViewKey("j"))
	if cmd == nil {
		t.Fatal("the chooser did not page in more playlists")
	}
	m = librarySettle(t, m, cmd)
	playerViewText(t, m, "Save to playlist · Cuecas", "Cuecas")
	playlistCalls, _, _ := api.requests()
	if len(playlistCalls) != 2 || playlistCalls[1] != spotifyCatalogPageSize {
		t.Fatalf("playlist pages = %v, want the second page", playlistCalls)
	}
	m, cmd = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = librarySettle(t, m, cmd)
	if got := api.saved(); len(got) != 1 || got[0].playlistURI != "spotify:playlist:cuecas" {
		t.Fatalf("saved = %+v, want the paged playlist", got)
	}
	playerViewText(t, m, "Saved to Cuecas.")
}

// TestSaveWritableCatalogKeepsExistingCommands proves the new capability changes
// no pre-existing binding: the same keys still send the same playback commands.
func TestSaveWritableCatalogKeepsExistingCommands(t *testing.T) {
	playing := saveTrackState()
	playing.Status = "playing"
	for _, tt := range []struct {
		name  string
		key   tea.KeyMsg
		state playerState
		want  []playerCommand
	}{
		{"reproducir", tea.KeyMsg{Type: tea.KeySpace}, saveTrackState(), []playerCommand{{Name: "play"}}},
		{"pausar", tea.KeyMsg{Type: tea.KeySpace}, playing, []playerCommand{{Name: "pause"}}},
		{"siguiente", playerViewKey("n"), saveTrackState(), []playerCommand{{Name: "skip_next"}}},
		{"anterior", playerViewKey("p"), saveTrackState(), []playerCommand{{Name: "skip_prev"}}},
		{"más volumen", playerViewKey("+"), saveTrackState(), []playerCommand{{Name: "set_volume", Volume: 55}}},
		{"menos volumen", playerViewKey("-"), saveTrackState(), []playerCommand{{Name: "set_volume", Volume: 45}}},
		{"aleatorio", playerViewKey("s"), saveTrackState(), []playerCommand{{Name: "set_shuffle", Shuffle: true}, {Name: "get_state"}}},
		{"repetición", playerViewKey("r"), saveTrackState(), []playerCommand{{Name: "set_repeat", Repeat: "context"}, {Name: "get_state"}}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			api := newSaveFake(libraryPlaylistsFixture())
			m, out, _ := savePlayer(t, preferences{}, tt.state, api)
			if _, cmd := m.Update(tt.key); cmd != nil {
				t.Fatal("the key scheduled work instead of a playback command")
			}
			for _, want := range tt.want {
				want.SessionID = tt.state.SessionID
				select {
				case got := <-out:
					if got != want {
						t.Fatalf("got %+v, want %+v", got, want)
					}
				default:
					t.Fatalf("missing command %+v", want)
				}
			}
			if len(out) != 0 {
				t.Fatalf("extra command %+v", <-out)
			}
		})
	}
}

// TestSaveWritableCatalogKeepsExistingRouting keeps focus, the text field and
// the quit keys exactly as they were.
func TestSaveWritableCatalogKeepsExistingRouting(t *testing.T) {
	api := newSaveFake(libraryPlaylistsFixture())
	m, out, _ := savePlayer(t, preferences{}, saveTrackState(), api)
	// "/" sigue enfocando la búsqueda y "a" es texto ahí.
	m, _ = m.Update(playerViewKey("/"))
	m, cmd := m.Update(playerViewKey("a"))
	if cmd != nil {
		t.Fatal("typing a scheduled work")
	}
	playerViewText(t, m, "Search: a_")
	if view := ansi.Strip(m.View()); strings.Contains(view, "Save to playlist") {
		t.Fatalf("typing a opened the chooser:\n%s", view)
	}
	// Tab sigue recorriendo los paneles.
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyTab})
	if got := m.(*playerModel).focus; got != playerFocusPlaying {
		t.Fatalf("Tab moved the focus to %v", got)
	}
	// "l" sigue enfocando la biblioteca.
	m, _ = m.Update(playerViewKey("l"))
	if got := m.(*playerModel).focus; got != playerFocusSidebar {
		t.Fatalf("l moved the focus to %v", got)
	}
	// "?" sigue abriendo la ayuda, y q sigue saliendo.
	m, _ = m.Update(playerViewKey("?"))
	playerViewText(t, m, "Navigation")
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	m, quit := m.Update(playerViewKey("q"))
	if _, ok := playerViewRun(t, quit).(tea.QuitMsg); !ok {
		t.Fatal("q no longer quit")
	}
	if len(out) != 0 {
		t.Fatal("the routing keys sent a playback command")
	}
}

// TestHelpListsSaveAction keeps the help screen honest about the new key.
func TestHelpListsSaveAction(t *testing.T) {
	m, _ := playerViewModel(t, 120, 40, saveTrackState())
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyF1})
	playerViewText(t, m, "save the playing track to a playlist")
	es := NewPlayerModelWithPreferences(context.Background(), nil, nil, 120, 40, preferences{Language: "es"}, nil, "", nil)
	es, _ = es.Update(tea.KeyMsg{Type: tea.KeyF1})
	playerViewText(t, es, "guardar la pista en una lista")
}
