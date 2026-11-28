package ui

import (
	"bytes"
	"context"
	"errors"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"net/http"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/muesli/termenv"

	"github.com/dcaceresc/pulso/internal/spotify"
)

// Las URLs de las pruebas: la carátula de una fila cargada y las dos que
// publica la consulta de respaldo, para comprobar cuál se elige.
const (
	coverFixtureURL = "https://i.scdn.co/fixture-cover.png"
	coverFixtureURI = "spotify:track:two"
	coverSmallURL   = "https://i.scdn.co/64.png"
	coverLargeURL   = "https://i.scdn.co/640.png"
)

// coverWait acota lo que una prueba espera a un comando que no es su objeto:
// la lectura del estado siguiente y la línea de tiempo nunca terminan solas.
const coverWait = 100 * time.Millisecond

// coverImagePNG codifica un PNG de un solo color, así la prueba ejerce el
// decodificador real sin arrastrar un archivo binario.
func coverImagePNG(t *testing.T, width, height int, shade color.RGBA) []byte {
	t.Helper()
	var buffer bytes.Buffer
	if err := png.Encode(&buffer, coverSolidImage(width, height, shade)); err != nil {
		t.Fatalf("encode fixture PNG: %v", err)
	}
	return buffer.Bytes()
}

// coverImageJPEG hace lo mismo en JPEG: la segunda pareja del decodificador.
func coverImageJPEG(t *testing.T, width, height int, shade color.RGBA) []byte {
	t.Helper()
	var buffer bytes.Buffer
	if err := jpeg.Encode(&buffer, coverSolidImage(width, height, shade), &jpeg.Options{Quality: 90}); err != nil {
		t.Fatalf("encode fixture JPEG: %v", err)
	}
	return buffer.Bytes()
}

func coverSolidImage(width, height int, shade color.RGBA) image.Image {
	img := image.NewRGBA(image.Rect(0, 0, width, height))
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			img.Set(x, y, shade)
		}
	}
	return img
}

// coverFetchFake es la descarga inyectada: anota las URL que se le pidieron y
// nunca toca la red.
type coverFetchFake struct {
	mu    sync.Mutex
	urls  []string
	serve func(context.Context, string) ([]byte, error)
}

func (f *coverFetchFake) fetch(ctx context.Context, url string) ([]byte, error) {
	f.mu.Lock()
	f.urls = append(f.urls, url)
	f.mu.Unlock()
	if f.serve == nil {
		return nil, errors.New("cover fixture has no answer")
	}
	return f.serve(ctx, url)
}

func (f *coverFetchFake) requested() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.urls...)
}

// coverTrackState es el estado de una pista en reproducción, con la URI que
// decide qué carátula le toca.
func coverTrackState(uri string) playerState {
	return playerState{
		Connected: true, Status: "playing", Volume: 37, OptionsKnown: true,
		Track: playerTrack{
			Title: "Estrechez de Corazón", Artist: "Los Prisioneros",
			Album: "La Cultura de la Basura", DurationMS: 381000, URI: uri,
		},
	}
}

// coverPlayer construye el espacio de trabajo con la descarga inyectada: la
// ruta de la carátula es la de producción y la red no se toca nunca.
func coverPlayer(t *testing.T, width, height int, api CatalogAPI, fetch *coverFetchFake) *playerModel {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	model := NewPlayerModelWithCatalog(ctx, make(chan playerState, 4), nil, width, height, preferences{}, nil, api, nil, "")
	player, ok := model.(*playerModel)
	if !ok {
		t.Fatalf("unexpected model %T", model)
	}
	player.covers.fetch = fetch.fetch
	return player
}

// coverState entrega un estado como lo haría la lectura pendiente de Soloist.
func coverState(t *testing.T, m *playerModel, state playerState) *playerModel {
	t.Helper()
	m.pending = true
	model, cmd := m.Update(playerViewState{state: state, ok: true})
	player, ok := model.(*playerModel)
	if !ok {
		t.Fatalf("unexpected model %T", model)
	}
	return coverSettle(t, player, cmd)
}

// coverSettle ejecuta los comandos que el modelo programó y entrega sus
// respuestas, igual que Bubble Tea. Los comandos que esperan el estado
// siguiente o el reloj quedan fuera: una prueba no produce ninguno de los dos.
func coverSettle(t *testing.T, m *playerModel, cmd tea.Cmd) *playerModel {
	t.Helper()
	for _, msg := range coverMessages(t, cmd) {
		model, next := m.Update(msg)
		player, ok := model.(*playerModel)
		if !ok {
			t.Fatalf("unexpected model %T", model)
		}
		m = coverSettle(t, player, next)
	}
	return m
}

// coverMessages aplana un lote y devuelve las respuestas que llegan dentro del
// plazo corto de la prueba.
func coverMessages(t *testing.T, cmd tea.Cmd) []tea.Msg {
	t.Helper()
	if cmd == nil {
		return nil
	}
	done := make(chan tea.Msg, 1)
	go func() { done <- cmd() }()
	select {
	case msg := <-done:
		batch, isBatch := msg.(tea.BatchMsg)
		if !isBatch {
			if msg == nil {
				return nil
			}
			return []tea.Msg{msg}
		}
		messages := make([]tea.Msg, 0, len(batch))
		for _, inner := range batch {
			messages = append(messages, coverMessages(t, inner)...)
		}
		return messages
	case <-time.After(coverWait):
		return nil
	}
}

// coverInitialsTop es el borde superior del bloque de iniciales: la ranura que
// la caja dibuja mientras no hay imagen.
func coverInitialsTop(slot artSlot) string {
	return "┌" + strings.Repeat("─", slot.columns-2) + "┐"
}

// coverFixtureRoute sirve la primera página de listas y la de la lista abierta,
// con una sola fila que anuncia su carátula.
func coverFixtureRoute(uri string) *libraryFake {
	return &libraryFake{
		playlists: libraryPlaylistsFixture(),
		items: func(context.Context, string, int) (spotify.PlaylistItemsPage, error) {
			return spotify.PlaylistItemsPage{
				Tracks: []libraryTrack{{
					Name: "Estrechez de Corazón", Artist: "Los Prisioneros", DurationMS: 381000,
					URI: uri, Cover: coverFixtureURL,
				}},
				Total: 1, Snapshot: "snapshot-rock",
			}, nil
		},
	}
}

// TestCoverReplacesInitialsFromLoadedRow fija el camino completo: una fila que
// la lista ya cargó anuncia la URL de su carátula, sus bytes se decodifican y la
// imagen sustituye al bloque de iniciales sin cambiar la ranura.
func TestCoverReplacesInitialsFromLoadedRow(t *testing.T) {
	source := coverFixtureRoute(coverFixtureURI)
	fetch := &coverFetchFake{serve: func(context.Context, string) ([]byte, error) {
		return coverImagePNG(t, 20, 20, color.RGBA{R: 0xC0, A: 0xFF}), nil
	}}
	m := coverPlayer(t, 120, 40, source, fetch)
	m = coverSettle(t, m, m.Init())
	opened, cmd := libraryOpenFirst(t, m)
	m = coverSettle(t, opened.(*playerModel), cmd)
	m = coverState(t, m, coverTrackState(coverFixtureURI))

	view := strings.Join(playerViewRows(t, m, 120, 40), "\n")
	if got, want := strings.Count(view, coverUpperHalf), playerArtWide.columns*playerArtWide.rows; got != want {
		t.Fatalf("the cover draws %d half blocks, want %d:\n%s", got, want, view)
	}
	for _, gone := range []string{coverInitialsTop(playerArtWide), "EC"} {
		if strings.Contains(view, gone) {
			t.Fatalf("the initials block survived the cover (%q):\n%s", gone, view)
		}
	}
	if got := fetch.requested(); len(got) != 1 || got[0] != coverFixtureURL {
		t.Fatalf("downloads = %v, want one for %q", got, coverFixtureURL)
	}
}

// TestCoverFetchFailureKeepsInitials fija que una descarga fallida no deje
// rastro: el bloque de iniciales sigue, no hay fila de aviso y no se dibuja
// ningún medio bloque.
func TestCoverFetchFailureKeepsInitials(t *testing.T) {
	source := coverFixtureRoute(coverFixtureURI)
	fetch := &coverFetchFake{serve: func(context.Context, string) ([]byte, error) {
		return nil, errors.New("fixture download failed")
	}}
	m := coverPlayer(t, 120, 40, source, fetch)
	m = coverSettle(t, m, m.Init())
	opened, cmd := libraryOpenFirst(t, m)
	m = coverSettle(t, opened.(*playerModel), cmd)
	m = coverState(t, m, coverTrackState(coverFixtureURI))

	view := strings.Join(playerViewRows(t, m, 120, 40), "\n")
	if !strings.Contains(view, coverInitialsTop(playerArtWide)) || !strings.Contains(view, "EC") {
		t.Fatalf("the initials block is gone after a failed download:\n%s", view)
	}
	if strings.Contains(view, coverUpperHalf) {
		t.Fatalf("a failed download drew a picture:\n%s", view)
	}
	if notice := m.playerNoticeText(); notice != "" {
		t.Fatalf("a failed cover download raised the notice %q", notice)
	}
	if got := fetch.requested(); len(got) != 1 {
		t.Fatalf("downloads = %v, want the single attempt", got)
	}
}

// TestCoverSlowFetchKeepsFirstPaint fija que la caja no espere la imagen: la
// primera pintura muestra las iniciales y la carátula aparece al llegar los
// bytes.
func TestCoverSlowFetchKeepsFirstPaint(t *testing.T) {
	started, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	fetch := &coverFetchFake{serve: func(context.Context, string) ([]byte, error) {
		once.Do(func() { close(started) })
		<-release
		return coverImagePNG(t, 20, 20, color.RGBA{G: 0x80, A: 0xFF}), nil
	}}
	m := coverPlayer(t, 120, 40, nil, fetch)
	m = coverState(t, m, coverTrackState(coverFixtureURI))
	// Una fila ya cargada anunció la carátula de esta pista.
	m.covers.urls[coverFixtureURI] = coverFixtureURL
	cmd := m.coverRequest()
	if cmd == nil {
		t.Fatal("the workspace did not request the cover")
	}
	done := make(chan tea.Msg, 1)
	go func() { done <- cmd() }()
	<-started

	view := strings.Join(playerViewRows(t, m, 120, 40), "\n")
	if !strings.Contains(view, coverInitialsTop(playerArtWide)) || strings.Contains(view, coverUpperHalf) {
		t.Fatalf("the first paint waited for the picture:\n%s", view)
	}

	close(release)
	model, _ := m.Update(<-done)
	after, ok := model.(*playerModel)
	if !ok {
		t.Fatalf("unexpected model %T", model)
	}
	view = strings.Join(playerViewRows(t, after, 120, 40), "\n")
	if strings.Contains(view, coverInitialsTop(playerArtWide)) || !strings.Contains(view, coverUpperHalf) {
		t.Fatalf("the cover did not replace the initials once it arrived:\n%s", view)
	}
}

// TestCoverCacheAsksOncePerURL fija la caché: la misma URL no se descarga dos
// veces aunque el estado llegue de nuevo y la caja se repinte.
func TestCoverCacheAsksOncePerURL(t *testing.T) {
	fetch := &coverFetchFake{serve: func(context.Context, string) ([]byte, error) {
		return coverImageJPEG(t, 32, 32, color.RGBA{B: 0x90, A: 0xFF}), nil
	}}
	m := coverPlayer(t, 120, 40, nil, fetch)
	m = coverState(t, m, coverTrackState(coverFixtureURI))
	m.covers.urls[coverFixtureURI] = coverFixtureURL
	m = coverSettle(t, m, m.coverRequest())
	for i := 0; i < 3; i++ {
		m = coverState(t, m, coverTrackState(coverFixtureURI))
		playerViewRows(t, m, 120, 40)
	}
	if got := fetch.requested(); len(got) != 1 || got[0] != coverFixtureURL {
		t.Fatalf("downloads = %v, want the single one for %q", got, coverFixtureURL)
	}
	if view := strings.Join(playerViewRows(t, m, 120, 40), "\n"); !strings.Contains(view, coverUpperHalf) {
		t.Fatalf("the cached cover is not drawn any more:\n%s", view)
	}
}

// TestCoverFallbackReadsTheTrackOnce fija la consulta de respaldo: una sola
// petición estrecha a la pista, y de sus imágenes se dibuja la más pequeña que
// alcanza el ancho de la ranura.
func TestCoverFallbackReadsTheTrackOnce(t *testing.T) {
	var mu sync.Mutex
	var requests []string
	catalog := libraryFixtureCatalog(func(r *http.Request) (*http.Response, error) {
		mu.Lock()
		requests = append(requests, r.Method+" "+r.URL.Path+"?"+r.URL.RawQuery)
		mu.Unlock()
		if r.URL.Path != "/v1/tracks/remoto" {
			t.Fatalf("unexpected request %s", r.URL.Path)
		}
		return libraryFixtureResponse(200, `{"album":{"images":[`+
			`{"url":"`+coverLargeURL+`","width":640,"height":640},`+
			`{"url":"`+coverSmallURL+`","width":64,"height":64}]}}`), nil
	})
	fetch := &coverFetchFake{serve: func(context.Context, string) ([]byte, error) {
		return coverImagePNG(t, 64, 64, color.RGBA{R: 0x20, G: 0x60, B: 0xA0, A: 0xFF}), nil
	}}
	m := coverPlayer(t, 120, 40, catalog, fetch)
	m = coverState(t, m, coverTrackState("spotify:track:remoto"))

	mu.Lock()
	got := append([]string(nil), requests...)
	mu.Unlock()
	want := []string{"GET /v1/tracks/remoto?fields=album.images"}
	if len(got) != 1 || got[0] != want[0] {
		t.Fatalf("requests = %v, want %v", got, want)
	}
	if downloaded := fetch.requested(); len(downloaded) != 1 || downloaded[0] != coverSmallURL {
		t.Fatalf("downloads = %v, want the smallest image that covers the slot", downloaded)
	}
	if view := strings.Join(playerViewRows(t, m, 120, 40), "\n"); !strings.Contains(view, coverUpperHalf) {
		t.Fatalf("the fallback cover is not drawn:\n%s", view)
	}

	// Un estado nuevo no repite ni la consulta ni la descarga.
	m = coverState(t, m, coverTrackState("spotify:track:remoto"))
	m = coverState(t, m, coverTrackState("spotify:track:remoto"))
	mu.Lock()
	got = append([]string(nil), requests...)
	mu.Unlock()
	if len(got) != 1 {
		t.Fatalf("the fallback was asked %d times, want one: %v", len(got), got)
	}
	if downloaded := fetch.requested(); len(downloaded) != 1 {
		t.Fatalf("downloads = %v, want the single one", downloaded)
	}
}

// TestCoverFallbackRemembersItsFailure fija que una pista sin carátula no se
// vuelva a preguntar: el fallo se recuerda y la caja sigue con sus iniciales,
// sin aviso.
func TestCoverFallbackRemembersItsFailure(t *testing.T) {
	var mu sync.Mutex
	asked := 0
	catalog := libraryFixtureCatalog(func(r *http.Request) (*http.Response, error) {
		mu.Lock()
		asked++
		mu.Unlock()
		return libraryFixtureResponse(404, `{"error":{"message":"fixture body"}}`), nil
	})
	fetch := &coverFetchFake{}
	m := coverPlayer(t, 120, 40, catalog, fetch)
	for i := 0; i < 3; i++ {
		m = coverState(t, m, coverTrackState("spotify:track:falta"))
		playerViewRows(t, m, 120, 40)
	}
	mu.Lock()
	got := asked
	mu.Unlock()
	if got != 1 {
		t.Fatalf("the fallback was asked %d times, want one", got)
	}
	if downloaded := fetch.requested(); len(downloaded) != 0 {
		t.Fatalf("a track without cover downloaded %v", downloaded)
	}
	view := strings.Join(playerViewRows(t, m, 120, 40), "\n")
	if !strings.Contains(view, coverInitialsTop(playerArtWide)) || strings.Contains(view, coverUpperHalf) {
		t.Fatalf("the failed fallback did not keep the initials:\n%s", view)
	}
	if notice := m.playerNoticeText(); notice != "" {
		t.Fatalf("a failed fallback raised the notice %q", notice)
	}
}

// TestCoverKeepsInitialsWithoutImage fija el comportamiento anterior: una pista
// sin carátula anunciada sigue dibujando su bloque de iniciales, en la misma
// ranura de siempre y sin ningún medio bloque.
func TestCoverKeepsInitialsWithoutImage(t *testing.T) {
	source := &libraryFake{playlists: libraryPlaylistsFixture(), items: libraryTracksForProbe}
	fetch := &coverFetchFake{}
	m := coverPlayer(t, 120, 40, source, fetch)
	m = coverSettle(t, m, m.Init())
	opened, cmd := libraryOpenFirst(t, m)
	m = coverSettle(t, opened.(*playerModel), cmd)
	m = coverState(t, m, coverTrackState(coverFixtureURI))

	// La caja de 80 columnas dibuja la ranura; por debajo, ni ranura ni imagen.
	view := strings.Join(playerViewRows(t, m, 120, 40), "\n")
	if !strings.Contains(view, coverInitialsTop(playerArtWide)) || !strings.Contains(view, "EC") {
		t.Fatalf("the initials block is gone without an image:\n%s", view)
	}
	if strings.Contains(view, coverUpperHalf) {
		t.Fatalf("a track without cover drew a picture:\n%s", view)
	}
	narrow := strings.Join(playerViewRows(t, m, 79, 23), "\n")
	if strings.Contains(narrow, coverInitialsTop(playerArtWide)) || strings.Contains(narrow, coverUpperHalf) {
		t.Fatalf("the art slot survived below 80 columns:\n%s", narrow)
	}
	if got := fetch.requested(); len(got) != 0 {
		t.Fatalf("a track without cover downloaded %v", got)
	}
	if notice := m.playerNoticeText(); notice != "" {
		t.Fatalf("the box raised the notice %q", notice)
	}
}

// TestCoverURLComesFromAlreadyRequestedPages fija que la URL viaja en las dos
// respuestas que la biblioteca ya pide: la página de una lista y la búsqueda.
// Ninguna carátula necesita un endpoint nuevo.
func TestCoverURLComesFromAlreadyRequestedPages(t *testing.T) {
	catalog := libraryFixtureCatalog(func(r *http.Request) (*http.Response, error) {
		album := `"album":{"images":[` +
			`{"url":"` + coverLargeURL + `","width":640,"height":640},` +
			`{"url":"` + coverSmallURL + `","width":64,"height":64}]}`
		switch r.URL.Path {
		case "/v1/playlists/rock/items":
			return libraryFixtureResponse(200, `{"total":1,"items":[{"item":{"name":"Tren al Sur","artists":[{"name":"Los Prisioneros"}],"duration_ms":336000,"uri":"spotify:track:one",`+album+`}}],"next":null}`), nil
		case "/v1/playlists/rock":
			return libraryFixtureResponse(200, `{"snapshot_id":"snapshot-rock"}`), nil
		case "/v1/search":
			return libraryFixtureResponse(200, `{"tracks":{"total":1,"items":[{"name":"Tren al Sur","artists":[{"name":"Los Prisioneros"}],"duration_ms":336000,"uri":"spotify:track:one",`+album+`}],"next":null}}`), nil
		}
		t.Fatalf("unexpected request %s", r.URL.Path)
		return nil, nil
	})
	// La carátula se agrega sin cambiar el resto de la fila.
	page, err := catalog.PlaylistItems(context.Background(), "spotify:playlist:rock", 0)
	if err != nil || len(page.Tracks) != 1 || page.Tracks[0].Cover != coverSmallURL {
		t.Fatalf("playlist items cover = %#v, %v", page.Tracks, err)
	}
	// El snapshot de la lectura vive en la página, no en cada fila.
	if want := (spotify.Track{Name: "Tren al Sur", Artist: "Los Prisioneros", DurationMS: 336000,
		URI: "spotify:track:one", Cover: coverSmallURL}); page.Tracks[0] != want || page.Snapshot != "snapshot-rock" {
		t.Fatalf("playlist row = %#v, want %#v", page.Tracks[0], want)
	}
	tracks, _, _, err := catalog.SearchPage(context.Background(), "tren", 0)
	if err != nil || len(tracks) != 1 || tracks[0].Cover != coverSmallURL {
		t.Fatalf("search cover = %#v, %v", tracks, err)
	}
	if want := (spotify.Track{Name: "Tren al Sur", Artist: "Los Prisioneros", DurationMS: 336000,
		URI: "spotify:track:one", Cover: coverSmallURL}); tracks[0] != want {
		t.Fatalf("search row = %#v, want %#v", tracks[0], want)
	}
}

// TestCoverImageChoice fija la regla del tamaño: la imagen más pequeña que
// alcanza el ancho de la ranura y, si ninguna llega, la más grande.
func TestCoverImageChoice(t *testing.T) {
	image := func(url string, width int) spotify.Image { return spotify.Image{URL: url, Width: width} }
	for _, tt := range []struct {
		name   string
		images []spotify.Image
		want   string
	}{
		{"smallest that reaches the slot", []spotify.Image{image("a", 640), image("b", 300), image("c", 64)}, "c"},
		{"first of equal widths", []spotify.Image{image("a", 64), image("b", 64)}, "a"},
		{"largest when none reaches the slot", []spotify.Image{image("a", 4), image("b", 8), image("c", 2)}, "b"},
		// La ranura de veinte columnas ya no la cubre la imagen de la ranura
		// anterior: la regla pide al menos el ancho nuevo.
		{"the ten-pixel image no longer covers the slot", []spotify.Image{image("a", 10), image("b", 20)}, "b"},
		{"empty URL is not an image", []spotify.Image{image("", 64), image("b", 300)}, "b"},
		{"no images", nil, ""},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if got := spotify.CoverImage(tt.images, spotify.CoverWidth); got != tt.want {
				t.Fatalf("CoverImage(%v) = %q, want %q", tt.images, got, tt.want)
			}
		})
	}
}

// TestCoverSlotIsTwentyByTen garantiza la geometría de R2: la ranura mide veinte
// celdas por diez filas, dibuja doscientos medios bloques —dos píxeles por celda,
// 400 píxeles de imagen en total— y el catálogo pide la imagen con ese ancho. La
// ranura pequeña de la banda compacta se queda en las diez celdas por cinco filas
// de 10×10 píxeles, y las dos salen de un solo decodificado de la misma imagen.
func TestCoverSlotSizes(t *testing.T) {
	if spotify.CoverWidth != 20 {
		t.Fatalf("spotify.CoverWidth = %d, want the twenty cells of the wide slot", spotify.CoverWidth)
	}
	if playerArtWide != (artSlot{columns: 20, rows: 10}) {
		t.Fatalf("the wide slot is %+v, want 20x10 cells", playerArtWide)
	}
	if playerArtCompact != (artSlot{columns: 10, rows: 5}) {
		t.Fatalf("the compact slot is %+v, want 10x5 cells", playerArtCompact)
	}
	picture, err := coverPictureFrom(coverImagePNG(t, 40, 40, color.RGBA{R: 0x30, G: 0x60, B: 0x90, A: 0xFF}))
	if err != nil {
		t.Fatalf("decode fixture: %v", err)
	}
	for _, slot := range []artSlot{playerArtWide, playerArtCompact} {
		rows, ok := picture.rows[slot]
		if !ok || len(rows) != slot.rows {
			t.Fatalf("the %+v slot draws %d rows, want %d", slot, len(rows), slot.rows)
		}
		blocks := 0
		for _, row := range rows {
			blocks += strings.Count(row, coverUpperHalf)
			if lipgloss.Width(row) != slot.columns {
				t.Fatalf("the %+v slot row measures %d cells, want %d", slot, lipgloss.Width(row), slot.columns)
			}
		}
		if want := slot.columns * slot.rows; blocks != want {
			t.Fatalf("the %+v slot draws %d half blocks, want %d", slot, blocks, want)
		}
		if pixels := slot.pixelWidth() * slot.pixelHeight(); pixels != slot.columns*slot.rows*coverPixelRows {
			t.Fatalf("the %+v slot holds %d pixels", slot, pixels)
		}
	}
	if pixels := playerArtWide.pixelWidth() * playerArtWide.pixelHeight(); pixels != 400 {
		t.Fatalf("the wide slot holds %d pixels, want the 400 of a 20x20 reduction", pixels)
	}
	if pixels := playerArtCompact.pixelWidth() * playerArtCompact.pixelHeight(); pixels != 100 {
		t.Fatalf("the compact slot holds %d pixels, want the 100 of a 10x10 reduction", pixels)
	}
}

// TestCoverDecodesPNGAndJPEG fija el decodificador: los dos formatos de la
// biblioteca estándar producen las filas de la ranura en color de 24 bits.
func TestCoverDecodesPNGAndJPEG(t *testing.T) {
	profile := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.TrueColor)
	t.Cleanup(func() { lipgloss.SetColorProfile(profile) })
	red := color.RGBA{R: 0xFF, A: 0xFF}
	for _, tt := range []struct {
		name string
		data []byte
	}{
		{"png", coverImagePNG(t, 24, 24, red)},
		{"jpeg", coverImageJPEG(t, 24, 24, red)},
	} {
		t.Run(tt.name, func(t *testing.T) {
			picture, err := coverPictureFrom(tt.data)
			if err != nil {
				t.Fatalf("decode %s: %v", tt.name, err)
			}
			rows := picture.rows[playerArtWide]
			if len(rows) != playerArtWide.rows {
				t.Fatalf("%s cover draws %d rows, want %d", tt.name, len(rows), playerArtWide.rows)
			}
			for _, row := range rows {
				if lipgloss.Width(row) != playerArtWide.columns {
					t.Fatalf("%s cover row measures %d cells, want %d: %q", tt.name, lipgloss.Width(row), playerArtWide.columns, row)
				}
				if !strings.Contains(row, "▀") {
					t.Fatalf("%s cover row is not drawn with half blocks: %q", tt.name, row)
				}
			}
			if tt.name == "png" && !strings.Contains(rows[0], "38;2;255;0;0") {
				t.Fatalf("the PNG cover lost its 24-bit colour: %q", rows[0])
			}
		})
	}
	for _, data := range [][]byte{nil, []byte("not an image"), []byte("\x89PNG\r\n\x1a\ntruncated")} {
		if picture, err := coverPictureFrom(data); err == nil {
			t.Fatalf("coverPictureFrom(%q) = %v, %v; want a format error", data, picture, err)
		}
	}
}

// TestCoverDownloaderRejectsUnsafeURLs fija las dos condiciones que la descarga
// por omisión exige antes de tocar la red: https y un host real.
func TestCoverDownloaderRejectsUnsafeURLs(t *testing.T) {
	for _, raw := range []string{"", "http://i.scdn.co/x.png", "ftp://i.scdn.co/x.png",
		"https:///sin-host.png", "https://ana@i.scdn.co/x.png"} {
		if data, err := coverDownload(context.Background(), raw); !errors.Is(err, errCoverFetch) || data != nil {
			t.Fatalf("coverDownload(%q) = %v, %v; want a fetch error", raw, data, err)
		}
	}
}

// TestCoverFetcherIsInjected fija que las pruebas nunca usan la descarga real:
// el modelo guarda la función y la prueba la reemplaza antes de pedir nada.
func TestCoverFetcherIsInjected(t *testing.T) {
	m := coverPlayer(t, 120, 40, nil, &coverFetchFake{})
	if m.covers.fetch == nil {
		t.Fatal("the model has no cover download")
	}
	if reflect.ValueOf(m.covers.fetch).Pointer() == reflect.ValueOf(coverDownload).Pointer() {
		t.Fatal("the model kept the real download; a test would reach the network")
	}
}

// TestCoverAnsiRowsAreStripped garante que la ranura nunca desborda: las filas
// de la carátula miden lo mismo que el bloque de iniciales, con y sin color.
func TestCoverAnsiRowsAreStripped(t *testing.T) {
	picture, err := coverPictureFrom(coverImagePNG(t, 16, 16, color.RGBA{G: 0x40, A: 0xFF}))
	if err != nil {
		t.Fatalf("decode fixture: %v", err)
	}
	rows := picture.rows[playerArtWide]
	for _, row := range rows {
		if stripped := ansi.Strip(row); stripped != strings.Repeat(coverUpperHalf, playerArtWide.columns) {
			t.Fatalf("cover row = %q, want %d half blocks", stripped, playerArtWide.columns)
		}
	}
}
