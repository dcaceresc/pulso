package ui

import (
	"context"
	"fmt"
	"reflect"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/dcaceresc/pulso/internal/spotify"
)

// removeWorkspace opens the first playlist over a writable fixture catalog and
// settles its first track page, so the table shows rows a removal can act on.
func removeWorkspace(t *testing.T, p preferences, state playerState, api *saveFake) (tea.Model, chan playerCommand, *[]string) {
	t.Helper()
	m, out, opened := savePlayer(t, p, state, api)
	m, cmd := libraryOpenFirst(t, m)
	if cmd == nil {
		t.Fatal("opening the playlist scheduled no page")
	}
	return librarySettle(t, m, cmd), out, opened
}

// removeRowsFixture serves one playlist listing from a mutable slice, so a test
// can mirror what Spotify would return after the removal.
func removeRowsFixture(rows *[]libraryTrack) func(context.Context, string, int) (spotify.PlaylistItemsPage, error) {
	return func(_ context.Context, uri string, _ int) (spotify.PlaylistItemsPage, error) {
		return spotify.PlaylistItemsPage{Tracks: *rows, Total: len(*rows), Snapshot: librarySnapshotFor(uri)}, nil
	}
}

// TestRemoveActionAvailability proves the action exists only where it can work:
// a read-only catalog, a row without a snapshot, a row that is not a track, and
// search results never open the prompt nor advertise the key.
func TestRemoveActionAvailability(t *testing.T) {
	t.Run("catálogo de solo lectura", func(t *testing.T) {
		readOnly := &libraryFake{playlists: libraryPlaylistsFixture(), items: libraryTracksFixture(3)}
		m, out, _ := savePlayer(t, preferences{}, saveTrackState(), readOnly)
		m, cmd := libraryOpenFirst(t, m)
		m = librarySettle(t, m, cmd)
		if m.(*playerModel).removeAvailable() {
			t.Fatal("a read-only catalog offered the removal action")
		}
		m, cmd = m.Update(playerViewKey("x"))
		if cmd != nil {
			t.Fatal("a read-only catalog scheduled removal work")
		}
		if view := ansi.Strip(m.View()); strings.Contains(view, "Remove Tren al Sur") {
			t.Fatalf("a read-only catalog asked to remove:\n%s", view)
		}
		if len(out) != 0 {
			t.Fatal("the remove key sent a playback command")
		}
	})

	t.Run("fila sin snapshot", func(t *testing.T) {
		api := newSaveFake(libraryPlaylistsFixture())
		api.items = func(_ context.Context, uri string, _ int) (spotify.PlaylistItemsPage, error) {
			// Una lectura sin snapshot no puede nombrar la versión de la lista.
			return spotify.PlaylistItemsPage{Tracks: libraryTracksFor(uri), Total: 3}, nil
		}
		m, out, _ := removeWorkspace(t, preferences{}, saveTrackState(), api)
		if m.(*playerModel).removeAvailable() {
			t.Fatal("a row without a snapshot offered the removal action")
		}
		m, cmd := m.Update(playerViewKey("x"))
		if cmd != nil {
			t.Fatal("a row without a snapshot scheduled removal work")
		}
		if view := ansi.Strip(m.View()); strings.Contains(view, "Remove Tren al Sur") {
			t.Fatalf("a row without a snapshot asked to remove:\n%s", view)
		}
		if len(out) != 0 {
			t.Fatal("the remove key sent a playback command")
		}
	})

	t.Run("fila sin uri de pista", func(t *testing.T) {
		api := newSaveFake(libraryPlaylistsFixture())
		api.items = func(_ context.Context, uri string, _ int) (spotify.PlaylistItemsPage, error) {
			return spotify.PlaylistItemsPage{
				Tracks:   []libraryTrack{{Name: "Un episodio", DurationMS: 1000, URI: "spotify:episode:one"}},
				Total:    1,
				Snapshot: librarySnapshotFor(uri),
			}, nil
		}
		m, _, _ := removeWorkspace(t, preferences{}, saveTrackState(), api)
		m, cmd := m.Update(playerViewKey("x"))
		if cmd != nil {
			t.Fatal("a row without a track URI scheduled removal work")
		}
		if view := ansi.Strip(m.View()); strings.Contains(view, "Remove Un episodio") {
			t.Fatalf("a row without a track URI asked to remove:\n%s", view)
		}
	})

	t.Run("resultados de búsqueda", func(t *testing.T) {
		api := newSaveFake(libraryPlaylistsFixture())
		api.search = searchTracksFixture(1)
		m, _, _ := removeWorkspace(t, preferences{}, saveTrackState(), api)
		m, _ = m.Update(playerViewKey("/"))
		m, _ = m.Update(playerViewKey("tren"))
		m, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
		m = librarySettle(t, m, cmd)
		if m.(*playerModel).removeAvailable() {
			t.Fatal("search results offered the removal action")
		}
		m, cmd = m.Update(playerViewKey("x"))
		if cmd != nil {
			t.Fatal("search results scheduled removal work")
		}
		if view := ansi.Strip(m.View()); strings.Contains(view, "Remove ") {
			t.Fatalf("search results asked to remove:\n%s", view)
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
		model, cmd := model.Update(playerViewKey("x"))
		if cmd != nil {
			t.Fatal("a typed nil catalog scheduled removal work")
		}
		if view := ansi.Strip(model.View()); strings.Contains(view, "Remove ") {
			t.Fatalf("a typed nil catalog asked to remove:\n%s", view)
		}
	})
}

// TestRemoveConfirmsWithTrackAndPlaylist follows the confirmation: the prompt
// names the track and the playlist, Enter confirms, Esc cancels and the rest of
// the workspace keeps working.
func TestRemoveExplainsWhyTheKeyCannotAct(t *testing.T) {
	for _, tc := range []struct {
		name  string
		want  string
		setup func(t *testing.T) tea.Model
	}{
		{
			name: "sin lista abierta",
			want: "Open one of your playlists with Enter to remove tracks.",
			setup: func(t *testing.T) tea.Model {
				api := newSaveFake(libraryPlaylistsFixture())
				m, _, _ := savePlayer(t, preferences{}, saveTrackState(), api)
				return m
			},
		},
		{
			name: "sin permiso de escritura",
			want: "Could not remove: no write permission.",
			setup: func(t *testing.T) tea.Model {
				readOnly := &libraryFake{playlists: libraryPlaylistsFixture(), items: libraryTracksFixture(3)}
				m, _, _ := savePlayer(t, preferences{}, saveTrackState(), readOnly)
				return m
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := tc.setup(t)
			model, cmd := m.Update(playerViewKey("x"))
			m = model
			if cmd != nil {
				t.Fatal("the remove key scheduled work without a removable row")
			}
			if view := ansi.Strip(m.View()); !strings.Contains(view, tc.want) {
				t.Fatalf("the key did not explain itself, want %q:\n%s", tc.want, view)
			}
		})
	}
}

func TestRemoveConfirmsWithTrackAndPlaylist(t *testing.T) {
	api := newSaveFake(libraryPlaylistsFixture())
	m, out, _ := removeWorkspace(t, preferences{}, saveTrackState(), api)
	m, _ = m.Update(playerViewKey("j"))
	playerViewText(t, m, ">2 Estrechez de Corazón")
	m, cmd := m.Update(playerViewKey("x"))
	if cmd != nil {
		t.Fatal("asking to remove scheduled work")
	}
	view := playerViewText(t, m, "Remove from playlist · Rock Chileno",
		"Remove Estrechez de Corazón from Rock Chileno?")
	if strings.Contains(view, "Removed") {
		t.Fatalf("the prompt claimed a result:\n%s", view)
	}
	if len(api.removed()) != 0 {
		t.Fatalf("asking to remove reached Spotify: %+v", api.removed())
	}

	// Esc cierra la confirmación sin tocar nada.
	m, cmd = m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if cmd != nil {
		t.Fatal("Esc during the confirmation scheduled work")
	}
	view = ansi.Strip(m.View())
	if strings.Contains(view, "from Rock Chileno?") {
		t.Fatalf("Esc left the confirmation open:\n%s", view)
	}
	if len(api.removed()) != 0 {
		t.Fatalf("Esc removed %+v", api.removed())
	}
	playerViewText(t, m, "Rock Chileno", "Estrechez de Corazón", ">2 Estrechez de Corazón")
	if len(out) != 0 {
		t.Fatal("cancelling the removal sent a playback command")
	}

	// La misma acción se lee en español.
	es := newSaveFake(libraryPlaylistsFixture())
	localized, _, _ := removeWorkspace(t, preferences{Language: "es"}, saveTrackState(), es)
	localized, _ = localized.Update(playerViewKey("j"))
	localized, _ = localized.Update(playerViewKey("x"))
	playerViewText(t, localized, "Quitar de la lista · Rock Chileno",
		"¿Quitar Estrechez de Corazón de Rock Chileno?")

	t.Run("la confirmación no depende de la reproducción", func(t *testing.T) {
		api := newSaveFake(libraryPlaylistsFixture())
		m, out, _ := removeWorkspace(t, preferences{}, saveTrackState(), api)
		m, _ = m.Update(playerViewKey("j"))
		m, _ = m.Update(playerViewKey("x"))
		// Una sesión que deja de reportar pista no cierra la confirmación: quitar
		// una fila de la lista no depende de lo que suena.
		player := m.(*playerModel)
		player.pending = true
		m, _ = m.Update(playerViewState{state: playerState{Connected: true, SessionID: 5}, ok: true})
		playerViewText(t, m, "Remove Estrechez de Corazón from Rock Chileno?")
		m, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
		m = librarySettle(t, m, cmd)
		if got := api.removed(); len(got) != 1 {
			t.Fatalf("removed = %+v, want one removal without a playing track", got)
		}
		playerViewText(t, m, "Removed Estrechez de Corazón from Rock Chileno.")
		if len(out) != 0 {
			t.Fatal("removing without a playing track sent a playback command")
		}
	})
}

// removeListingRows devuelve las filas de la lista abierta que dibuja la tabla:
// las que quedan entre su encabezado y la caja del reproductor.
func removeListingRows(t *testing.T, m tea.Model, width, height int) string {
	t.Helper()
	rows := playerViewRows(t, m, width, height)
	start := -1
	for i, row := range rows {
		plain := ansi.Strip(row)
		if start < 0 && strings.Contains(plain, "#") && strings.Contains(plain, "Title") {
			start = i
		}
		if strings.HasPrefix(plain, "├─") {
			if start < 0 {
				t.Fatalf("the player box came before the listing header:\n%s", strings.Join(rows, "\n"))
			}
			return strings.Join(rows[start:i], "\n")
		}
	}
	t.Fatalf("the view drew no player box:\n%s", strings.Join(rows, "\n"))
	return ""
}

// TestRemoveSuccessNotifiesAndReloads proves one accepted removal names the
// track and the playlist, reloads the browsed listing so the row disappears and
// leaves playback and the queue untouched.
func TestRemoveSuccessNotifiesAndReloads(t *testing.T) {
	rows := libraryTracksFor("spotify:playlist:rock")
	api := newSaveFake(libraryPlaylistsFixture())
	api.items = removeRowsFixture(&rows)
	api.remove = func(_ int, _, trackURI, _ string) error {
		// Spotify quita una sola aparición; la prueba la refleja en su página.
		for i, track := range rows {
			if track.URI == trackURI {
				rows = append(rows[:i:i], rows[i+1:]...)
				break
			}
		}
		return nil
	}
	state := saveTrackState()
	state.Queue = []playerTrack{{Title: "Queued Song", Artist: "Queued Artist", DurationMS: 100000}}
	m, out, _ := removeWorkspace(t, preferences{}, state, api)
	m, _ = m.Update(playerViewKey("j"))
	m, _ = m.Update(playerViewKey("x"))
	m, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("Enter on the confirmation started no removal")
	}
	if view := ansi.Strip(m.View()); strings.Contains(view, "Removed") {
		t.Fatalf("the workspace announced a removal before Spotify accepted:\n%s", view)
	}
	m = librarySettle(t, m, cmd)
	if got := api.removed(); len(got) != 1 ||
		got[0] != (removeCall{playlistURI: "spotify:playlist:rock", trackURI: "spotify:track:two", snapshot: "snapshot-rock"}) {
		t.Fatalf("removed = %+v, want one removal of the chosen occurrence", got)
	}
	view := playerViewText(t, m, "Removed Estrechez de Corazón from Rock Chileno.")
	// La fila quitada ya no está en la lista abierta: el nombre solo sobrevive en
	// el aviso. Una recarga perdida volvería a dibujar la fila y esta comprobación
	// falla, aunque el aviso siga nombrando la canción.
	if listing := removeListingRows(t, m, 120, 40); strings.Contains(listing, "Estrechez de Corazón") {
		t.Fatalf("the removed track is still a row of the open list:\n%s", listing)
	}
	if strings.Contains(view, "Estrechez de Corazón") && strings.Count(view, "Estrechez de Corazón") != 1 {
		// La fila se fue de la tabla; el título del panel ya no la nombra.
		t.Logf("view still names the track once:\n%s", view)
	}
	if strings.Contains(view, "Remove from playlist") {
		t.Fatalf("the confirmation stayed open after the removal:\n%s", view)
	}
	// La lectura de la lista volvió a pedir la primera página: la fila desaparece.
	_, tracks, uris := api.requests()
	if len(tracks) != 2 || tracks[1] != 0 || uris[1] != "spotify:playlist:rock" {
		t.Fatalf("listing reads = %v %v, want the first page again", tracks, uris)
	}
	tableRow := ""
	for _, line := range strings.Split(view, "\n") {
		if strings.Contains(line, "Tren al Sur") {
			tableRow = line
		}
	}
	if tableRow == "" {
		t.Fatalf("the untouched rows disappeared:\n%s", view)
	}
	if strings.Contains(view, "Llueve sobre la Ciudad") != true {
		t.Fatalf("the untouched rows disappeared:\n%s", view)
	}
	// La reproducción y la cola siguen igual.
	playerViewText(t, m, "Song")
	if queue := m.(*playerModel).state.Queue; !reflect.DeepEqual(queue, state.Queue) {
		t.Fatalf("the removal touched the queue: %+v", queue)
	}
	if len(out) != 0 {
		t.Fatal("removing a track touched playback")
	}

	// La misma acción se lee en español.
	es := newSaveFake(libraryPlaylistsFixture())
	localized, _, _ := removeWorkspace(t, preferences{Language: "es"}, saveTrackState(), es)
	localized, _ = localized.Update(playerViewKey("x"))
	localized, cmd = localized.Update(tea.KeyMsg{Type: tea.KeyEnter})
	localized = librarySettle(t, localized, cmd)
	playerViewText(t, localized, "Se quitó Tren al Sur de Rock Chileno.")
}

// TestRemoveDuplicatesRemoveOneOccurrence proves a duplicated track sends one
// removal that names the snapshot of the listing the person acted on.
func TestRemoveDuplicatesRemoveOneOccurrence(t *testing.T) {
	rows := []libraryTrack{
		{Name: "Tren al Sur", Artist: "Los Prisioneros", DurationMS: 336000, URI: "spotify:track:one"},
		{Name: "Estrechez de Corazón", Artist: "Los Prisioneros", DurationMS: 381000, URI: "spotify:track:two"},
		{Name: "Tren al Sur", Artist: "Los Prisioneros", DurationMS: 336000, URI: "spotify:track:one"},
	}
	api := newSaveFake(libraryPlaylistsFixture())
	api.items = removeRowsFixture(&rows)
	api.remove = func(_ int, _, trackURI, _ string) error {
		for i, track := range rows {
			if track.URI == trackURI {
				rows = append(rows[:i:i], rows[i+1:]...)
				break
			}
		}
		return nil
	}
	m, _, _ := removeWorkspace(t, preferences{}, saveTrackState(), api)
	m, _ = m.Update(playerViewKey("x"))
	m, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = librarySettle(t, m, cmd)
	if got := api.removed(); len(got) != 1 ||
		got[0] != (removeCall{playlistURI: "spotify:playlist:rock", trackURI: "spotify:track:one", snapshot: "snapshot-rock"}) {
		t.Fatalf("removed = %+v, want one occurrence named by the listing snapshot", got)
	}
	if len(rows) != 2 {
		t.Fatalf("listing kept %d rows, want the other occurrence untouched", len(rows))
	}
	playerViewText(t, m, "Removed Tren al Sur from Rock Chileno.")
}

// TestRemoveStaleSnapshotReadsAgainAndAsks proves a stale snapshot re-reads the
// playlist and asks again instead of guessing which occurrence to drop.
func TestRemoveStaleSnapshotReadsAgainAndAsks(t *testing.T) {
	rows := libraryTracksFor("spotify:playlist:rock")
	reads := 0
	api := newSaveFake(libraryPlaylistsFixture())
	api.items = func(_ context.Context, uri string, _ int) (spotify.PlaylistItemsPage, error) {
		// Cada lectura devuelve un snapshot nuevo: la lista cambió entre medio.
		reads++
		return spotify.PlaylistItemsPage{Tracks: rows, Total: len(rows), Snapshot: fmt.Sprintf("snapshot-%d", reads)}, nil
	}
	api.remove = func(_ int, _, _, snapshot string) error {
		if snapshot == "snapshot-1" {
			return spotify.ErrPlaylistSnapshotConflict
		}
		return nil
	}
	m, out, _ := removeWorkspace(t, preferences{}, saveTrackState(), api)
	m, _ = m.Update(playerViewKey("j"))
	m, _ = m.Update(playerViewKey("x"))
	m, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("Enter on the confirmation started no removal")
	}
	m = librarySettle(t, m, cmd)
	if got := api.removed(); len(got) != 1 || got[0].snapshot != "snapshot-1" {
		t.Fatalf("removed = %+v, want one attempt with the stale snapshot", got)
	}
	// Nada se informó como quitado y la lista se volvió a leer.
	if got := ansi.Strip(m.View()); strings.Contains(got, "Removed") {
		t.Fatalf("a stale snapshot claimed a removal:\n%s", got)
	}
	if _, tracks, _ := api.requests(); len(tracks) != 2 || tracks[1] != 0 {
		t.Fatalf("listing reads = %v, want the first page again", tracks)
	}
	// La pregunta sigue ahí y hay que confirmarla otra vez.
	playerViewText(t, m, "Remove Estrechez de Corazón from Rock Chileno?")

	m, cmd = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("Enter after the re-read did not ask Spotify again")
	}
	m = librarySettle(t, m, cmd)
	if got := api.removed(); len(got) != 2 || got[1] != (removeCall{
		playlistURI: "spotify:playlist:rock", trackURI: "spotify:track:two", snapshot: "snapshot-2"}) {
		t.Fatalf("removed = %+v, want the retry with the refreshed snapshot", got)
	}
	playerViewText(t, m, "Removed Estrechez de Corazón from Rock Chileno.")
	if len(out) != 0 {
		t.Fatal("recovering from a stale snapshot touched playback")
	}
}

// TestRemoveAfterAddUsesTheFreshlyReadSnapshot reproduce el defecto de R1: una
// escritura aceptada publica una versión nueva de la lista, así que las filas de
// la lista abierta conservan el snapshot de la lectura anterior. Si la
// aplicación no relee la lista tras guardar, la retirada siguiente viaja con un
// snapshot viejo y Spotify la rechaza; la persona tendría que salir de la lista
// y volver a entrar para poder quitar una pista.
func TestRemoveAfterAddUsesTheFreshlyReadSnapshot(t *testing.T) {
	// La lista ficticia se comporta como Spotify: cada lectura informa la versión
	// vigente de la lista y cada escritura aceptada publica una nueva.
	rows, version := libraryTracksFor("spotify:playlist:rock"), 0
	snapshot := func() string { return fmt.Sprintf("snapshot-%d", version) }
	added := libraryTrack{Name: "Nueva Canción", Artist: "Artista Nueva", DurationMS: 200000, URI: "spotify:track:new"}
	api := newSaveFake(libraryPlaylistsFixture())
	api.items = func(_ context.Context, _ string, _ int) (spotify.PlaylistItemsPage, error) {
		return spotify.PlaylistItemsPage{Tracks: rows, Total: len(rows), Snapshot: snapshot()}, nil
	}
	api.add = func(_ int, _, trackURI string) (string, error) {
		rows, version = append(rows, added), version+1
		return snapshot(), nil
	}
	api.remove = func(_ int, _, trackURI, sent string) error {
		// Spotify rechaza una retirada que nombra una versión que ya no es la actual.
		if sent != snapshot() {
			return spotify.ErrPlaylistSnapshotConflict
		}
		for i, track := range rows {
			if track.URI == trackURI {
				rows = append(rows[:i:i], rows[i+1:]...)
				break
			}
		}
		version++
		return nil
	}
	state := saveTrackState()
	state.Track = playerTrack{Title: added.Name, Artist: added.Artist, DurationMS: added.DurationMS, URI: added.URI}
	m, out, _ := removeWorkspace(t, preferences{}, state, api)
	playerViewText(t, m, "Tren al Sur", "Estrechez de Corazón")

	// Guardar la pista en la lista abierta publica la versión 1 de Spotify.
	m, _ = m.Update(playerViewKey("a"))
	m, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("Enter on the chooser started no save")
	}
	m = librarySettle(t, m, cmd)
	if got := api.saved(); len(got) != 1 ||
		got[0] != (saveCall{playlistURI: "spotify:playlist:rock", trackURI: added.URI}) {
		t.Fatalf("saved = %+v, want one add to the open playlist", got)
	}
	// El guardado aceptado relee la lista: la fila nueva solo existe en la página
	// que Spotify sirve después de la escritura.
	playerViewText(t, m, "Saved to Rock Chileno.", added.Name)

	// Sin cambiar de lista, la retirada siguiente usa la lectura nueva: ni un
	// rechazo ni una segunda pregunta.
	m, _ = m.Update(playerViewKey("j"))
	m, _ = m.Update(playerViewKey("x"))
	playerViewText(t, m, "Remove Estrechez de Corazón from Rock Chileno?")
	m, cmd = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("Enter on the confirmation started no removal")
	}
	m = librarySettle(t, m, cmd)
	if got := api.removed(); len(got) != 1 || got[0] != (removeCall{
		playlistURI: "spotify:playlist:rock", trackURI: "spotify:track:two", snapshot: "snapshot-1"}) {
		t.Fatalf("removed = %+v, want the removal to name the listing read after the save", got)
	}
	view := playerViewText(t, m, "Removed Estrechez de Corazón from Rock Chileno.", added.Name, "Tren al Sur")
	if strings.Contains(view, "from Rock Chileno?") {
		t.Fatalf("the removal was asked again instead of using the refreshed snapshot:\n%s", view)
	}
	if len(out) != 0 {
		t.Fatal("saving and removing in the same playlist touched playback")
	}
}

// TestRemoveAsksForWriteScopeThenRetriesOnce proves a missing write scope reuses
// the one explicit reauthorization and retries the removal exactly once.
func TestRemoveAsksForWriteScopeThenRetriesOnce(t *testing.T) {
	upgraded := newSaveFake(libraryPlaylistsFixture())
	api := newSaveFake(libraryPlaylistsFixture())
	api.remove = func(int, string, string, string) error { return spotify.ErrSpotifyWriteScope }
	api.grant = func(showURL func(string)) (spotify.PlaylistWriter, error) {
		showURL(saveFixtureLink)
		return upgraded, nil
	}
	m, out, opened := removeWorkspace(t, preferences{}, saveTrackState(), api)
	m, _ = m.Update(playerViewKey("j"))
	m, _ = m.Update(playerViewKey("x"))
	m, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = librarySettle(t, m, cmd)
	playerViewText(t, m, "Spotify needs your approval to change playlists. Enter: authorize again.")
	if len(api.removed()) != 1 || len(upgraded.removed()) != 0 {
		t.Fatalf("attempts before approval: %+v / %+v", api.removed(), upgraded.removed())
	}

	m, cmd = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("Enter did not start the approval")
	}
	m, cmd = m.Update(playerViewRun(t, cmd))
	playerViewText(t, m, "Waiting for Spotify…", "accounts.spotify.com/authorize")
	if links, grants := api.approval(); grants != 1 || len(links) != 1 {
		t.Fatalf("approvals = %v, granted %d", links, grants)
	}
	if len(*opened) != 1 || !strings.HasPrefix((*opened)[0], saveFixtureLink) {
		t.Fatalf("browser hook = %v, want the approval link", *opened)
	}
	m = librarySettle(t, m, cmd)
	if got := upgraded.removed(); len(got) != 1 ||
		got[0] != (removeCall{playlistURI: "spotify:playlist:rock", trackURI: "spotify:track:two", snapshot: "snapshot-rock"}) {
		t.Fatalf("retried removal = %+v, want the confirmed occurrence", got)
	}
	if len(api.removed()) != 1 {
		t.Fatalf("the read-only approval was reused: %+v", api.removed())
	}
	playerViewText(t, m, "Removed Estrechez de Corazón from Rock Chileno.")
	if len(out) != 0 {
		t.Fatal("recovering the permission touched playback")
	}

	t.Run("un segundo rechazo no se reintenta", func(t *testing.T) {
		upgraded := newSaveFake(libraryPlaylistsFixture())
		upgraded.remove = func(int, string, string, string) error { return spotify.ErrSpotifyWriteScope }
		api := newSaveFake(libraryPlaylistsFixture())
		api.remove = func(int, string, string, string) error { return spotify.ErrSpotifyWriteScope }
		api.grant = func(showURL func(string)) (spotify.PlaylistWriter, error) {
			showURL(saveFixtureLink)
			return upgraded, nil
		}
		m, out, opened := removeWorkspace(t, preferences{}, saveTrackState(), api)
		m, _ = m.Update(playerViewKey("j"))
		m, _ = m.Update(playerViewKey("x"))
		m, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
		m = librarySettle(t, m, cmd)
		m, cmd = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
		m, cmd = m.Update(playerViewRun(t, cmd))
		m = librarySettle(t, m, cmd)
		view := playerViewText(t, m, "Could not remove Estrechez de Corazón from Rock Chileno.")
		if strings.Contains(view, "authorize again") {
			t.Fatalf("a second scope rejection offered another approval:\n%s", view)
		}
		if len(upgraded.removed()) != 1 || len(api.removed()) != 1 {
			t.Fatalf("attempts = %+v / %+v, want exactly one retry", api.removed(), upgraded.removed())
		}
		if len(*opened) != 1 {
			t.Fatalf("browser hook = %v, want one approval link", *opened)
		}
		if len(out) != 0 {
			t.Fatal("a failed retry touched playback")
		}
	})
}

// TestRemoveDeclinedApproval keeps a declined approval as a quiet notice and
// never repeats the removal.
func TestRemoveDeclinedApproval(t *testing.T) {
	api := newSaveFake(libraryPlaylistsFixture())
	api.remove = func(int, string, string, string) error { return spotify.ErrSpotifyWriteScope }
	api.grant = func(showURL func(string)) (spotify.PlaylistWriter, error) {
		showURL(saveFixtureLink)
		return nil, spotify.ErrSpotifyApprovalDeclined
	}
	m, out, opened := removeWorkspace(t, preferences{}, saveTrackState(), api)
	m, _ = m.Update(playerViewKey("j"))
	m, _ = m.Update(playerViewKey("x"))
	m, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = librarySettle(t, m, cmd)
	m, cmd = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = librarySettle(t, m, cmd)
	view := playerViewText(t, m, "Spotify authorization did not complete; nothing was removed.")
	if len(api.removed()) != 1 {
		t.Fatalf("a declined approval repeated the removal: %+v", api.removed())
	}
	if strings.Contains(view, "Remove from playlist") {
		t.Fatalf("the confirmation stayed open after a declined approval:\n%s", view)
	}
	if len(*opened) != 1 {
		t.Fatalf("browser hook = %v, want one approval link", *opened)
	}
	playerViewText(t, m, "Estrechez de Corazón")
	if len(out) != 0 {
		t.Fatal("a declined approval touched playback")
	}
}

// TestRemoveFailuresAreQuietAndLocalized covers the remaining rejections: the
// notice is localized, never carries the response body and never blocks.
func TestRemoveFailuresAreQuietAndLocalized(t *testing.T) {
	const secret = "fixture secret body"
	for _, tt := range []struct {
		name, language string
		failure        error
		want           string
	}{
		{"lista ajena", "en", spotify.ErrPlaylistNotEditable,
			"Could not remove Estrechez de Corazón from Rock Chileno: this account cannot change that playlist."},
		{"fallo sin clasificar", "en", fmt.Errorf("%s: %w", secret, spotify.ErrSpotifyCatalog),
			"Could not remove Estrechez de Corazón from Rock Chileno."},
		{"lista ajena en español", "es", spotify.ErrPlaylistNotEditable,
			"No se pudo quitar Estrechez de Corazón de Rock Chileno: esta cuenta no puede cambiar esa lista."},
		{"fallo en español", "es", fmt.Errorf("%s: %w", secret, spotify.ErrSpotifyCatalog),
			"No se pudo quitar Estrechez de Corazón de Rock Chileno."},
	} {
		t.Run(tt.name, func(t *testing.T) {
			api := newSaveFake(libraryPlaylistsFixture())
			api.remove = func(int, string, string, string) error { return tt.failure }
			m, out, _ := removeWorkspace(t, preferences{Language: tt.language}, saveTrackState(), api)
			m, _ = m.Update(playerViewKey("j"))
			m, _ = m.Update(playerViewKey("x"))
			m, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
			m = librarySettle(t, m, cmd)
			view := playerViewText(t, m, tt.want)
			if strings.Contains(view, secret) {
				t.Fatalf("the response body reached the view:\n%s", view)
			}
			if strings.Contains(view, "Remove from playlist") {
				t.Fatalf("the confirmation stayed open after a failure:\n%s", view)
			}
			// El aviso no bloquea: la lista y sus filas siguen ahí.
			playerViewText(t, m, "Rock Chileno", "Estrechez de Corazón")
			if len(out) != 0 {
				t.Fatal("a failed removal touched playback")
			}
		})
	}

	// Una lista ajena no ofrece aprobar otra vez.
	api := newSaveFake(libraryPlaylistsFixture())
	api.remove = func(int, string, string, string) error { return spotify.ErrPlaylistNotEditable }
	m, _, _ := removeWorkspace(t, preferences{}, saveTrackState(), api)
	m, _ = m.Update(playerViewKey("j"))
	m, _ = m.Update(playerViewKey("x"))
	m, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = librarySettle(t, m, cmd)
	if _, next := m.Update(tea.KeyMsg{Type: tea.KeyEnter}); next != nil {
		t.Fatal("a playlist this account cannot change offered an approval")
	}
	if _, grants := api.approval(); grants != 0 {
		t.Fatalf("approvals = %d, want none", grants)
	}
}

// TestRemoveWriteRefusalOffersApprovalInsteadOfBlamingThePlaylist fija la
// misma separación en la retirada que en el guardado: un rechazo sin clasificar
// sobre una lista propia ofrece la única reaprobación y nunca afirma que la
// cuenta no puede cambiar la lista; solo un rechazo explícito conserva el aviso.
func TestRemoveWriteRefusalOffersApprovalInsteadOfBlamingThePlaylist(t *testing.T) {
	t.Run("rechazo sin clasificar en una lista propia", func(t *testing.T) {
		api := newSaveFake(libraryPlaylistsFixture())
		api.remove = func(int, string, string, string) error { return spotify.ErrSpotifyWriteScope }
		m, out, opened := removeWorkspace(t, preferences{}, saveTrackState(), api)
		m, _ = m.Update(playerViewKey("j"))
		m, _ = m.Update(playerViewKey("x"))
		m, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
		m = librarySettle(t, m, cmd)
		if view := ansi.Strip(m.View()); strings.Contains(view, "cannot change that playlist") {
			t.Fatalf("a missing write grant was reported as an unchangeable playlist:\n%s", view)
		}
		playerViewText(t, m, "Spotify needs your approval to change playlists. Enter: authorize again.")
		if len(api.removed()) != 1 || len(*opened) != 0 {
			t.Fatalf("the offer removed %+v and opened %v before approving", api.removed(), *opened)
		}
		if len(out) != 0 {
			t.Fatal("the offer touched playback")
		}
	})

	t.Run("rechazo explícito de permiso", func(t *testing.T) {
		api := newSaveFake(libraryPlaylistsFixture())
		api.remove = func(int, string, string, string) error { return spotify.ErrPlaylistNotEditable }
		m, out, opened := removeWorkspace(t, preferences{}, saveTrackState(), api)
		m, _ = m.Update(playerViewKey("j"))
		m, _ = m.Update(playerViewKey("x"))
		m, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
		m = librarySettle(t, m, cmd)
		view := playerViewText(t, m, "Could not remove Estrechez de Corazón from Rock Chileno: this account cannot change that playlist.")
		if strings.Contains(view, "authorize again") {
			t.Fatalf("a definite refusal offered an approval:\n%s", view)
		}
		if len(out) != 0 {
			t.Fatal("the definite refusal touched playback")
		}
		m, cmd = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
		if cmd != nil || len(*opened) != 0 {
			t.Fatalf("the definite refusal scheduled %v and opened %v", cmd, *opened)
		}
		if len(api.removed()) != 1 {
			t.Fatalf("the definite refusal removed %+v", api.removed())
		}
	})
}

// TestRemoveKeepsPreExistingKeys proves the removal key changes no binding: over
// a browsable playlist the same keys still send the same playback commands and
// the add flow still opens its chooser.
func TestRemoveKeepsPreExistingKeys(t *testing.T) {
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
		{"anterior", playerViewKey("b"), saveTrackState(), []playerCommand{{Name: "skip_prev"}}},
		{"más volumen", playerViewKey("+"), saveTrackState(), []playerCommand{{Name: "set_volume", Volume: 55}}},
		{"menos volumen", playerViewKey("-"), saveTrackState(), []playerCommand{{Name: "set_volume", Volume: 45}}},
		{"aleatorio", playerViewKey("s"), saveTrackState(), []playerCommand{{Name: "set_shuffle", Shuffle: true}, {Name: "get_state"}}},
		{"repetición", playerViewKey("r"), saveTrackState(), []playerCommand{{Name: "set_repeat", Repeat: "context"}, {Name: "get_state"}}},
		{"reproducir la fila", tea.KeyMsg{Type: tea.KeyEnter}, saveTrackState(), []playerCommand{{Name: "play", URI: "spotify:track:one"}}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			api := newSaveFake(libraryPlaylistsFixture())
			m, out, _ := removeWorkspace(t, preferences{}, tt.state, api)
			// La fila marcada puede quitarse: la tecla nueva no cambia ninguna otra.
			if !m.(*playerModel).removeAvailable() {
				t.Fatal("the highlighted row lost the removal action")
			}
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
			if len(api.removed()) != 0 {
				t.Fatalf("a pre-existing key removed %+v", api.removed())
			}
		})
	}

	t.Run("guardar sigue igual", func(t *testing.T) {
		api := newSaveFake(libraryPlaylistsFixture())
		m, out, _ := removeWorkspace(t, preferences{}, saveTrackState(), api)
		m, _ = m.Update(playerViewKey("a"))
		playerViewText(t, m, "Save to playlist · Rock Chileno")
		m, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
		m = librarySettle(t, m, cmd)
		if got := api.saved(); len(got) != 1 || got[0] != (saveCall{playlistURI: "spotify:playlist:rock", trackURI: "spotify:track:one"}) {
			t.Fatalf("saved = %+v, want the approved add flow", got)
		}
		playerViewText(t, m, "Saved to Rock Chileno.")
		if len(api.removed()) != 0 {
			t.Fatalf("saving removed %+v", api.removed())
		}
		if len(out) != 0 {
			t.Fatal("saving touched playback")
		}
	})
}

// TestHelpListsRemoveAction keeps the help screen honest about the new key.
func TestHelpListsRemoveAction(t *testing.T) {
	m, _ := playerViewModel(t, 120, 40, saveTrackState())
	m, _ = m.Update(playerViewKey("?"))
	playerViewText(t, m, "remove the highlighted track from the open playlist")
	es := NewPlayerModelWithPreferences(context.Background(), nil, nil, 120, 40, preferences{Language: "es"}, nil, "", nil)
	es, _ = es.Update(playerViewKey("?"))
	playerViewText(t, es, "quitar la pista marcada de la lista abierta")
}
