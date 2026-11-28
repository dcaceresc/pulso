package ui

import (
	"bytes"
	"context"
	"errors"
	"fmt"
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
	if downloaded := fetch.requested(); len(downloaded) != 1 || downloaded[0] != coverLargeURL {
		t.Fatalf("downloads = %v, want the image the new rule chooses, which is the largest published", downloaded)
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
	if err != nil || len(page.Tracks) != 1 || page.Tracks[0].Cover != coverLargeURL {
		t.Fatalf("playlist items cover = %#v, %v", page.Tracks, err)
	}
	// El snapshot de la lectura vive en la página, no en cada fila.
	if want := (spotify.Track{Name: "Tren al Sur", Artist: "Los Prisioneros", DurationMS: 336000,
		URI: "spotify:track:one", Cover: coverLargeURL}); page.Tracks[0] != want || page.Snapshot != "snapshot-rock" {
		t.Fatalf("playlist row = %#v, want %#v", page.Tracks[0], want)
	}
	tracks, _, _, err := catalog.SearchPage(context.Background(), "tren", 0)
	if err != nil || len(tracks) != 1 || tracks[0].Cover != coverLargeURL {
		t.Fatalf("search cover = %#v, %v", tracks, err)
	}
	if want := (spotify.Track{Name: "Tren al Sur", Artist: "Los Prisioneros", DurationMS: 336000,
		URI: "spotify:track:one", Cover: coverLargeURL}); tracks[0] != want {
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
		{"the source closest above the preferred width", []spotify.Image{image("a", 640), image("b", 300), image("c", 64)}, "a"},
		{"first of equal widths", []spotify.Image{image("a", 64), image("b", 64)}, "a"},
		{"largest when none reaches the slot", []spotify.Image{image("a", 4), image("b", 8), image("c", 2)}, "b"},
		// La ranura de veinte columnas ya no la cubre la imagen de la ranura
		// anterior: la regla pide al menos el ancho nuevo.
		{"the ten-pixel image no longer covers the slot", []spotify.Image{image("a", 10), image("b", 20)}, "b"},
		{"empty URL is not an image", []spotify.Image{image("", 64), image("b", 300)}, "b"},
		{"no images", nil, ""},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if got := spotify.CoverImage(tt.images); got != tt.want {
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

// coverSixelPlayer construye el espacio de trabajo con la carátula ya
// decodificada y el modo gráfico inyectado: el terminal es un búfer y la celda
// la decide la prueba, así que nada toca un terminal real.
func coverSixelPlayer(t *testing.T, width, height int) *playerModel {
	t.Helper()
	fetch := &coverFetchFake{serve: func(context.Context, string) ([]byte, error) {
		return coverImagePNG(t, 40, 40, color.RGBA{R: 0x10, G: 0x80, B: 0x30, A: 0xFF}), nil
	}}
	m := coverPlayer(t, width, height, nil, fetch)
	m.sixel = coverGraphics{renderer: coverSixel, cell: coverCell{width: 10, height: 20}}
	m.covers.urls[coverFixtureURI] = coverFixtureURL
	m = coverState(t, m, coverTrackState(coverFixtureURI))
	return coverSettle(t, m, m.coverRequest())
}

// coverSixelImages cuenta las imágenes que viajan en el cuadro.
func coverSixelImages(view string) int {
	return strings.Count(view, "\x1bP0;0;8q")
}

// coverSixelLastLine es la última línea del cuadro: la única que puede llevar la imagen.
func coverSixelLastLine(view string) string {
	return view[strings.LastIndex(view, "\n")+1:]
}

// coverPlacement es la fila y la columna donde la vista deja la ranura: el borde
// superior de la caja y, dos filas más abajo, la primera fila de la imagen.
func coverPlacement(t *testing.T, rows []string) (int, int) {
	t.Helper()
	for index, row := range rows {
		if strings.HasPrefix(row, "├") {
			return index + coverSixelRowOffset, coverSixelColumn
		}
	}
	t.Fatalf("the view has no player box:\n%s", strings.Join(rows, "\n"))
	return 0, 0
}

// TestCoverRendererChoosesOnceWithOverride fija la regla de detección y la
// salida de emergencia: PULSO_COVER impone el modo sin preguntar nada, la
// detección exige un terminal y, si el terminal no declara Sixel, la carátula se
// queda en el medio bloque.
func TestCoverRendererChoosesOnceWithOverride(t *testing.T) {
	for _, tt := range []struct {
		name      string
		override  string
		terminal  bool
		supported bool
		want      coverRenderer
	}{
		{"the environment forces sixel over a terminal that denies it", "sixel", false, false, coverSixel},
		{"the environment forces blocks over a terminal that declares it", "blocks", true, true, coverBlocks},
		{"the override ignores case and surrounding spaces", " SIXEL ", false, false, coverSixel},
		{"detection picks sixel when the terminal declares it", "", true, true, coverSixel},
		{"detection keeps blocks when the terminal denies it", "", true, false, coverBlocks},
		{"an output that is not a terminal never hears about sixel", "", false, true, coverBlocks},
		{"an unknown value leaves detection in charge", "kitty", true, true, coverSixel},
	} {
		t.Run(tt.name, func(t *testing.T) {
			terminal := func() bool { return tt.terminal }
			supported := func() bool { return tt.supported }
			if got := coverRendererFor(tt.override, terminal, supported); got != tt.want {
				t.Fatalf("coverRendererFor(%q, terminal=%v, sixel=%v) = %v, want %v",
					tt.override, tt.terminal, tt.supported, got, tt.want)
			}
		})
	}

	// Un modo impuesto no pregunta nada al terminal.
	for _, override := range []string{"sixel", "blocks"} {
		asked := false
		coverRendererFor(override, func() bool { asked = true; return true }, func() bool { asked = true; return true })
		if asked {
			t.Fatalf("the override %q still queried the terminal", override)
		}
	}
}

// TestCoverCellSizeUsesTheMeasurementOrTheFallback fija el tamaño de celda: la
// medida del terminal cuando llega y, cuando no contesta o contesta algo que no
// sirve, la celda de respaldo documentada.
func TestCoverCellSizeUsesTheMeasurementOrTheFallback(t *testing.T) {
	if want := (coverCell{width: 10, height: 20}); coverCellFallback != want {
		t.Fatalf("the documented fallback cell is %+v, want %+v", coverCellFallback, want)
	}
	for _, tt := range []struct {
		name    string
		measure func() (int, int, error)
		want    coverCell
	}{
		{"the terminal reports its cell", func() (int, int, error) { return 12, 24, nil }, coverCell{width: 12, height: 24}},
		{"no answer falls back to the documented cell", func() (int, int, error) { return 0, 0, errors.New("no terminal") }, coverCellFallback},
		{"a report without pixels falls back", func() (int, int, error) { return 0, 20, nil }, coverCellFallback},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if got := coverCellSize(tt.measure); got != tt.want {
				t.Fatalf("coverCellSize = %+v, want %+v", got, tt.want)
			}
		})
	}
}

// TestCoverSixelScalesToTheSlotInPixels fija la medida de la imagen: la ranura
// en píxeles, columnas por ancho de celda y filas por alto de celda. Con una
// celda de diez por veinte píxeles, la ranura de 20×10 celdas de 120×34 pide
// 200×200 píxeles y la de 10×5 de 80×24 pide 100×100, y lo codificado declara
// ese tamaño y no el número de celdas.
func TestCoverSixelScalesToTheSlotInPixels(t *testing.T) {
	for _, tt := range []struct {
		width, height int
		pixels        int
	}{
		{120, 34, 200},
		{80, 24, 100},
	} {
		m := coverSixelPlayer(t, tt.width, tt.height)
		art, ok := m.coverSixelArt()
		if !ok {
			t.Fatalf("%dx%d has no graphic cover", tt.width, tt.height)
		}
		if bounds := art.image.Bounds(); bounds.Dx() != tt.pixels || bounds.Dy() != tt.pixels {
			t.Fatalf("%dx%d scales the cover to %dx%d pixels, want %dx%d",
				tt.width, tt.height, bounds.Dx(), bounds.Dy(), tt.pixels, tt.pixels)
		}
		data, err := coverSixelEncode(art.image)
		if err != nil {
			t.Fatalf("%dx%d encode: %v", tt.width, tt.height, err)
		}
		if !bytes.HasPrefix(data, []byte("\x1bP")) || !bytes.HasSuffix(data, []byte("\x1b\\")) {
			t.Fatalf("%dx%d does not emit a sixel block", tt.width, tt.height)
		}
		if raster := fmt.Sprintf(`"1;1;%d;%d`, tt.pixels, tt.pixels); !bytes.Contains(data, []byte(raster)) {
			t.Fatalf("%dx%d does not declare the raster %q", tt.width, tt.height, raster)
		}
	}
}

// TestCoverSixelLeavesTheSlotBlank fija la vista del modo gráfico: la ranura se
// queda en blanco —ni medios bloques ni iniciales— y todo lo demás es la misma
// caja, con cada fila del ancho exacto de la ventana.
// TestCoverSixelRidesTheLastLine fija la colocación: la imagen viaja una sola vez,
// en la última línea del cuadro, y el cuadro conserva su alto y el ancho de cada
// fila; con la misma clave, el cuadro siguiente no la repite.
func TestCoverSixelRidesTheLastLine(t *testing.T) {
	for _, size := range [][2]int{{120, 40}, {120, 34}, {100, 30}, {80, 24}} {
		m := coverSixelPlayer(t, size[0], size[1])
		view := m.View()
		if got := coverSixelImages(view); got != 1 {
			t.Fatalf("%dx%d carries %d images, want one: %q", size[0], size[1], got, view)
		}
		if last := coverSixelLastLine(view); !strings.HasPrefix(last, "\x1b[") {
			t.Fatalf("%dx%d does not carry the image in its last line: %q", size[0], size[1], last)
		} else if lipgloss.Width(last) != size[0] {
			t.Fatalf("%dx%d the escape widened its line to %d cells", size[0], size[1], lipgloss.Width(last))
		}
		rows := playerViewRows(t, m, size[0], size[1])
		if len(rows) != size[1] {
			t.Fatalf("%dx%d fills %d rows, want %d", size[0], size[1], len(rows), size[1])
		}
		for index, row := range rows {
			if lipgloss.Width(row) != size[0] {
				t.Fatalf("%dx%d row %d measures %d cells, want %d: %q", size[0], size[1], index, lipgloss.Width(row), size[0], row)
			}
		}
		// El cuadro siguiente tiene la misma clave: no repite la imagen.
		if again := m.View(); coverSixelImages(again) != 0 {
			t.Fatalf("%dx%d repeated the image without a change", size[0], size[1])
		}
	}
}

// TestCoverSixelKeepsTheSlotContent fija que la ranura sigue llevando su medio
// bloque como contenido inmediato: la imagen se dibuja encima, en la última línea.
func TestCoverSixelKeepsTheSlotContent(t *testing.T) {
	m := coverSixelPlayer(t, 120, 40)
	view := m.View()
	if got, want := strings.Count(view, coverUpperHalf), playerArtWide.columns*playerArtWide.rows; got != want {
		t.Fatalf("the slot draws %d half blocks, want %d", got, want)
	}
	if strings.Contains(view, coverInitialsTop(playerArtWide)) {
		t.Fatal("the initials block survived the cover")
	}
}

// TestCoverSixelReplacesTheImageOnAChange fija que un cambio de estado reemplaza la
// imagen: cada cuadro nuevo lleva la secuencia de la pista y del tamaño nuevos.
func TestCoverSixelReplacesTheImageOnAChange(t *testing.T) {
	m := coverSixelPlayer(t, 120, 40)
	view := m.View()
	first := coverSixelImages(view)
	m.covers.urls["spotify:track:otra"] = coverFixtureURL
	m = coverState(t, m, coverTrackState("spotify:track:otra"))
	view = m.View()
	if first != 1 || coverSixelImages(view) != 1 {
		t.Fatalf("the new track carries %d images", coverSixelImages(view))
	}
	if !strings.Contains(view, "spotify") && !strings.Contains(coverSixelLastLine(view), "\x1b[") {
		t.Fatalf("the new image does not ride the last line: %q", view)
	}
	// Otro tamaño lleva la imagen del tamaño nuevo.
	m = coverState(t, m, coverTrackState(coverFixtureURI))
	model, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m = model.(*playerModel)
	if last := coverSixelLastLine(m.View()); !strings.Contains(last, `"1;1;100;100`) {
		t.Fatalf("the compact image is not 100x100 pixels: %q", last)
	}
}

// TestCoverSixelForgetsAHiddenScreen fija que una pantalla que tapa la caja olvida
// la imagen: al volver, el cuadro la escribe otra vez.
func TestCoverSixelForgetsAHiddenScreen(t *testing.T) {
	m := coverSixelPlayer(t, 120, 40)
	if coverSixelImages(m.View()) != 1 {
		t.Fatal("the first frame carries no image")
	}
	m.help = true
	if view := m.View(); coverSixelImages(view) != 0 {
		t.Fatalf("the help carries an image: %q", view)
	}
	m.help = false
	if coverSixelImages(m.View()) != 1 {
		t.Fatal("the cover was not written again after the help")
	}
	// La banda sin caja también la olvida y la recupera al volver.
	playerViewRows(t, m, 30, 10)
	model, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	if coverSixelImages(model.(*playerModel).View()) != 1 {
		t.Fatal("the cover was not written again after the minimum size")
	}
}

// TestCoverSixelWritesAgainAfterTheSlotReturns fija que una ranura que dejó de
// dibujarse olvida la imagen: la misma pista que vuelve la escribe otra vez.
func TestCoverSixelWritesAgainAfterTheSlotReturns(t *testing.T) {
	m := coverSixelPlayer(t, 120, 40)
	if coverSixelImages(m.View()) != 1 {
		t.Fatal("the first frame carries no image")
	}
	// La caja deja de afirmar la sesión: la ranura pierde la carátula.
	m = coverState(t, m, playerState{})
	if view := m.View(); coverSixelImages(view) != 0 {
		t.Fatalf("an unavailable slot carries an image: %q", view)
	}
	// La misma pista y el mismo tamaño vuelven: la imagen se escribe otra vez.
	m = coverState(t, m, coverTrackState(coverFixtureURI))
	if coverSixelImages(m.View()) != 1 {
		t.Fatal("the returning slot was never written again")
	}
}

func TestCoverBlocksViewIsTheFallback(t *testing.T) {
	t.Setenv(coverOverrideEnv, "blocks")
	blocks := coverSixelPlayer(t, 120, 40)
	blocks.sixel.renderer = coverBlocks

	detected := coverSixelPlayer(t, 120, 40)
	// La detección real, con el modo impuesto por el entorno, no mide la celda ni
	// pregunta nada al terminal.
	detected.sixel = newCoverSixel()
	if detected.sixel.renderer != coverBlocks {
		t.Fatalf("detection chose %v with PULSO_COVER=blocks, want blocks", detected.sixel.renderer)
	}
	for _, size := range [][2]int{{120, 40}, {80, 24}} {
		want := strings.Join(playerViewRows(t, blocks, size[0], size[1]), "\n")
		got := strings.Join(playerViewRows(t, detected, size[0], size[1]), "\n")
		if !strings.Contains(want, coverUpperHalf) {
			t.Fatalf("%dx%d does not draw the half blocks:\n%s", size[0], size[1], want)
		}
		if got != want {
			t.Fatalf("%dx%d the forced blocks view differs from the detected one", size[0], size[1])
		}
	}
}
