package ui

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"hash/crc32"
	"image/color"
	"net/http"
	"strings"
	"sync"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/muesli/termenv"

	"github.com/dcaceresc/pulso/internal/spotify"
)

// coverSlotRoute sirve la primera página de listas y la de la lista abierta con
// una sola fila: la carátula que anuncia es la que decida la prueba, vacía para
// la pista sin imagen.
func coverSlotRoute(cover string) *libraryFake {
	return &libraryFake{
		playlists: libraryPlaylistsFixture(),
		items: func(context.Context, string, int) (spotify.PlaylistItemsPage, error) {
			return spotify.PlaylistItemsPage{
				Tracks: []libraryTrack{{
					Name: "Estrechez de Corazón", Artist: "Los Prisioneros", DurationMS: 381000,
					URI: coverFixtureURI, Cover: cover,
				}},
				Total: 1, Snapshot: "snapshot-rock",
			}, nil
		},
	}
}

// coverSlotPlayer abre la lista con la fila que anuncia —o no— su carátula y
// entrega el estado de esa pista sonando.
func coverSlotPlayer(t *testing.T, cover string, fetch *coverFetchFake) *playerModel {
	t.Helper()
	m := coverPlayer(t, 120, 40, coverSlotRoute(cover), fetch)
	m = coverSettle(t, m, m.Init())
	opened, cmd := libraryOpenFirst(t, m)
	m = coverSettle(t, opened.(*playerModel), cmd)
	return coverState(t, m, coverTrackState(coverFixtureURI))
}

// coverSlotSolid es la descarga que responde una imagen de un color con tres
// canales distintos, suficiente para ver el color de 24 bits en la ranura.
func coverSlotSolid(t *testing.T) *coverFetchFake {
	t.Helper()
	return &coverFetchFake{serve: func(context.Context, string) ([]byte, error) {
		return coverImagePNG(t, 40, 40, color.RGBA{R: 0x10, G: 0x80, B: 0x30, A: 0xFF}), nil
	}}
}

// coverSlotCase es un tamaño de la matriz con la ranura que su banda dibuja.
type coverSlotCase struct {
	width, height int
	slot          artSlot
}

// TestCoverSlotMetrics fija la ranura de cada banda y que un solo dibujo sirva
// para las dos: diez filas de veinte medios bloques en las bandas amplia y media
// y cinco filas de diez en la compacta, con color de 24 bits, nada dibujado
// fuera de la ranura, cada fila del ancho exacto de la ventana y una única
// descarga para todas las vistas.
func TestCoverSlotMetrics(t *testing.T) {
	profile := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.TrueColor)
	t.Cleanup(func() { lipgloss.SetColorProfile(profile) })

	fetch := coverSlotSolid(t)
	m := coverSlotPlayer(t, coverFixtureURL, fetch)
	for _, tt := range []coverSlotCase{
		{120, 40, playerArtWide},
		{120, 34, playerArtWide},
		{100, 30, playerArtWide},
		{80, 24, playerArtCompact},
	} {
		rows := playerViewRows(t, m, tt.width, tt.height)
		slot := make([]int, 0, tt.slot.rows)
		for index, row := range rows {
			if width := lipgloss.Width(row); width != tt.width {
				t.Fatalf("%dx%d row %d measures %d cells, want %d: %q", tt.width, tt.height, index, width, tt.width, row)
			}
			if blocks := strings.Count(row, coverUpperHalf); blocks > 0 {
				slot = append(slot, index)
				if blocks != tt.slot.columns {
					t.Fatalf("%dx%d row %d carries %d half blocks, want %d: %q", tt.width, tt.height, index, blocks, tt.slot.columns, row)
				}
				// Los medios bloques de la ranura van juntos y son lo único de la
				// fila: nada de la carátula se sale de ella.
				if want := "  " + strings.Repeat(coverUpperHalf, tt.slot.columns) + "  "; !strings.Contains(row, want) {
					t.Fatalf("%dx%d row %d does not keep the slot together: %q", tt.width, tt.height, index, row)
				}
			}
		}
		if len(slot) != tt.slot.rows {
			t.Fatalf("%dx%d draws the cover in %v, want %d consecutive rows", tt.width, tt.height, slot, tt.slot.rows)
		}
		for row := slot[0]; row < slot[0]+tt.slot.rows; row++ {
			if !containsInt(slot, row) {
				t.Fatalf("%dx%d leaves a gap in the slot from row %d: %v", tt.width, tt.height, slot[0], slot)
			}
		}
		raw := m.View()
		for _, colour := range []string{"38;2;", "48;2;"} {
			if !strings.Contains(raw, colour) {
				t.Fatalf("%dx%d lost the 24-bit colour %q", tt.width, tt.height, colour)
			}
		}
	}
	// Cambiar de banda elige la otra ranura con la misma descarga y el mismo
	// decodificado: ni una petición más, ni una entrada más en la caché.
	if got := len(m.covers.images); got != 1 {
		t.Fatalf("the cache holds %d pictures, want the single download", got)
	}
	if got := fetch.requested(); len(got) != 1 || got[0] != coverFixtureURL {
		t.Fatalf("downloads across the bands = %v, want one for %q", got, coverFixtureURL)
	}
}

func containsInt(values []int, value int) bool {
	for _, candidate := range values {
		if candidate == value {
			return true
		}
	}
	return false
}

// TestCoverFailedDownloadMatchesNoCoverView fija que una descarga fallida no
// deje ningún rastro: la vista es idéntica a la de una pista sin carátula y no
// hay aviso.
func TestCoverFailedDownloadMatchesNoCoverView(t *testing.T) {
	failing := coverSlotPlayer(t, coverFixtureURL, &coverFetchFake{serve: func(context.Context, string) ([]byte, error) {
		return nil, errors.New("fixture download failed")
	}})
	plain := coverSlotPlayer(t, "", &coverFetchFake{})
	if got, want := ansi.Strip(failing.View()), ansi.Strip(plain.View()); got != want {
		t.Fatalf("a failed download changed the view:\n%s\n--- sin carátula ---\n%s", got, want)
	}
	if notice := failing.playerNoticeText(); notice != "" {
		t.Fatalf("a failed download raised the notice %q", notice)
	}
}

// TestCoverSlotThreshold fija qué ranura da cada banda: la amplia y la media la
// grande, la compacta la pequeña, y la mínima ninguna por muchas filas que su
// caja recortada conserve. Una ventana baja nunca hereda la carátula de la banda
// a la que no pertenece.
func TestCoverSlotThreshold(t *testing.T) {
	m := coverSlotPlayer(t, coverFixtureURL, coverSlotSolid(t))
	for _, tt := range []struct {
		width, height int
		slot          artSlot
	}{
		{120, 40, playerArtWide}, {120, 34, playerArtWide}, {100, 30, playerArtWide},
		{80, 24, playerArtCompact}, {100, 25, playerArtCompact},
		{120, 17, artSlot{}}, {120, 16, artSlot{}}, {40, 12, artSlot{}}, {79, 40, artSlot{}},
	} {
		t.Run(fmt.Sprintf("%dx%d", tt.width, tt.height), func(t *testing.T) {
			rows := playerViewRows(t, m, tt.width, tt.height)
			view := strings.Join(rows, "\n")
			if got := playerArtSlot(tt.width, tt.height); got != tt.slot {
				t.Fatalf("playerArtSlot(%d, %d) = %+v, want %+v", tt.width, tt.height, got, tt.slot)
			}
			art := coverSlotRows(rows, coverUpperHalf)
			switch {
			case !tt.slot.ok() && len(art) != 0:
				t.Fatalf("%dx%d draws %d cover rows without a slot:\n%s", tt.width, tt.height, len(art), view)
			case !tt.slot.ok() && strings.Contains(view, "┌"+strings.Repeat("─", playerArtCompact.columns-2)+"┐"):
				t.Fatalf("%dx%d draws the initials block without a slot:\n%s", tt.width, tt.height, view)
			case tt.slot.ok() && len(art) != tt.slot.rows:
				t.Fatalf("%dx%d draws %d cover rows, want the %d of its slot:\n%s", tt.width, tt.height, len(art), tt.slot.rows, view)
			}
		})
	}
}

// coverSlotRows cuenta las filas de una vista que dibujan la carátula con el
// glifo dado, que es como se ve la ranura sin mirar el color.
func coverSlotRows(rows []string, glyph string) []int {
	drawn := make([]int, 0, len(rows))
	for i, row := range rows {
		if strings.Contains(row, glyph) {
			drawn = append(drawn, i)
		}
	}
	return drawn
}

// TestCoverRequestFollowsTheBand fija la corrección del refutador: la ranura la
// decide la banda resuelta y no las filas que una ventana baja deja a la caja. A
// 120×17 y a 120×16 —banda mínima— la petición no se programa y no se descarga
// nada; en las bandas con ranura se pide una sola imagen y se dibuja el tamaño de
// esa banda.
func TestCoverRequestFollowsTheBand(t *testing.T) {
	for _, tt := range []struct {
		width, height int
		slot          artSlot
	}{
		{120, 17, artSlot{}},
		{120, 16, artSlot{}},
		{40, 12, artSlot{}},
		{79, 40, artSlot{}},
		{80, 24, playerArtCompact},
		{100, 25, playerArtCompact},
		{120, 34, playerArtWide},
		{100, 30, playerArtWide},
	} {
		t.Run(fmt.Sprintf("%dx%d", tt.width, tt.height), func(t *testing.T) {
			fetch := coverSlotSolid(t)
			m := coverPlayer(t, tt.width, tt.height, nil, fetch)
			m.covers.urls[coverFixtureURI] = coverFixtureURL
			m = coverState(t, m, coverTrackState(coverFixtureURI))
			if !tt.slot.ok() {
				if cmd := m.coverRequest(); cmd != nil {
					t.Fatalf("%dx%d scheduled a download without a slot", tt.width, tt.height)
				}
				if got := fetch.requested(); len(got) != 0 {
					t.Fatalf("%dx%d downloaded %v without a slot", tt.width, tt.height, got)
				}
				if art := coverSlotRows(playerViewRows(t, m, tt.width, tt.height), coverUpperHalf); len(art) != 0 {
					t.Fatalf("%dx%d drew the cover without a slot", tt.width, tt.height)
				}
				return
			}
			if got := fetch.requested(); len(got) != 1 || got[0] != coverFixtureURL {
				t.Fatalf("%dx%d downloads = %v, want one for %q", tt.width, tt.height, got, coverFixtureURL)
			}
			rows := playerViewRows(t, m, tt.width, tt.height)
			art := coverSlotRows(rows, coverUpperHalf)
			if len(art) != tt.slot.rows {
				t.Fatalf("%dx%d draws %d cover rows, want the %d of its slot:\n%s", tt.width, tt.height, len(art), tt.slot.rows, strings.Join(rows, "\n"))
			}
			for _, row := range art {
				if blocks := strings.Count(rows[row], coverUpperHalf); blocks != tt.slot.columns {
					t.Fatalf("%dx%d row %d carries %d half blocks, want %d", tt.width, tt.height, row, blocks, tt.slot.columns)
				}
			}
		})
	}
}

// TestCoverInFlightDownloadIsNotRepeated fija que una imagen en camino no se
// vuelva a pedir: ni un repintado ni un estado nuevo arrancan otra descarga.
func TestCoverInFlightDownloadIsNotRepeated(t *testing.T) {
	started, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	fetch := &coverFetchFake{serve: func(context.Context, string) ([]byte, error) {
		once.Do(func() { close(started) })
		<-release
		return coverImagePNG(t, 20, 20, color.RGBA{G: 0x60, A: 0xFF}), nil
	}}
	m := coverPlayer(t, 120, 40, nil, fetch)
	m = coverState(t, m, coverTrackState(coverFixtureURI))
	m.covers.urls[coverFixtureURI] = coverFixtureURL
	cmd := m.coverRequest()
	if cmd == nil {
		t.Fatal("the workspace did not request the cover")
	}
	done := make(chan tea.Msg, 1)
	go func() { done <- cmd() }()
	<-started

	if again := m.coverRequest(); again != nil {
		t.Fatal("a second download was scheduled while the first was in flight")
	}
	if got := fetch.requested(); len(got) != 1 {
		t.Fatalf("downloads while in flight = %v, want the single one", got)
	}

	close(release)
	model, _ := m.Update(<-done)
	landed, ok := model.(*playerModel)
	if !ok {
		t.Fatalf("unexpected model %T", model)
	}
	if view := strings.Join(playerViewRows(t, landed, 120, 40), "\n"); !strings.Contains(view, coverUpperHalf) {
		t.Fatalf("the picture did not land after the wait:\n%s", view)
	}
	if got := fetch.requested(); len(got) != 1 {
		t.Fatalf("downloads after landing = %v, want the single one", got)
	}
}

// TestCoverNarrowWindowSkipsTheRequest fija que una ventana sin ranura no
// descarga ni pregunta nada, y que al crecer pide todo una sola vez.
func TestCoverNarrowWindowSkipsTheRequest(t *testing.T) {
	var mu sync.Mutex
	lookups := 0
	catalog := libraryFixtureCatalog(func(r *http.Request) (*http.Response, error) {
		mu.Lock()
		lookups++
		mu.Unlock()
		return libraryFixtureResponse(200, `{"album":{"images":[`+
			`{"url":"`+coverSmallURL+`","width":64,"height":64}]}}`), nil
	})
	fetch := coverSlotSolid(t)
	m := coverPlayer(t, 40, 12, catalog, fetch)
	m = coverState(t, m, coverTrackState("spotify:track:remoto"))

	mu.Lock()
	asked := lookups
	mu.Unlock()
	if asked != 0 {
		t.Fatalf("a 40x12 window asked for the cover %d times, want none", asked)
	}
	if got := fetch.requested(); len(got) != 0 {
		t.Fatalf("a 40x12 window downloaded %v", got)
	}
	view := strings.Join(playerViewRows(t, m, 40, 12), "\n")
	if strings.Contains(view, coverUpperHalf) || strings.Contains(view, coverInitialsTop(playerArtWide)) {
		t.Fatalf("a 40x12 window drew art:\n%s", view)
	}

	// La ventana crece hasta la ranura: el tamaño programa lo que faltaba.
	model, cmd := m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	grown, ok := model.(*playerModel)
	if !ok {
		t.Fatalf("unexpected model %T", model)
	}
	grown = coverSettle(t, grown, cmd)
	mu.Lock()
	asked = lookups
	mu.Unlock()
	if asked != 1 {
		t.Fatalf("the grown window asked for the cover %d times, want one", asked)
	}
	if got := fetch.requested(); len(got) != 1 || got[0] != coverSmallURL {
		t.Fatalf("downloads after growing = %v, want one for %q", got, coverSmallURL)
	}
	if view := strings.Join(playerViewRows(t, grown, 120, 40), "\n"); !strings.Contains(view, coverUpperHalf) {
		t.Fatalf("the grown window does not draw the cover:\n%s", view)
	}

	// Otro tamaño con la imagen ya decodificada no repite nada.
	model, cmd = grown.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	settled, ok := model.(*playerModel)
	if !ok {
		t.Fatalf("unexpected model %T", model)
	}
	settled = coverSettle(t, settled, cmd)
	mu.Lock()
	asked = lookups
	mu.Unlock()
	if asked != 1 || len(fetch.requested()) != 1 {
		t.Fatalf("a second resize asked %d times and downloaded %v, want one each", asked, fetch.requested())
	}
}

// TestCoverRejectsDeclaredPixelsBeyondTheCap fija que la cabecera acota la
// memoria: una imagen que declara más píxeles de los que la ranura necesita se
// rechaza antes de decodificarse, con el mismo silencio que un fallo.
func TestCoverRejectsDeclaredPixelsBeyondTheCap(t *testing.T) {
	if _, err := coverPictureFrom(coverBigPNG(t, 640, 640)); err != nil {
		t.Fatalf("a 640x640 cover, the largest Spotify publishes, was rejected: %v", err)
	}
	// La misma imagen con la cabecera reescrita: unos cientos de bytes que
	// anuncian cuatro millones de píxeles.
	oversized := coverBigPNG(t, 20, 20)
	binary.BigEndian.PutUint32(oversized[16:20], 2000)
	binary.BigEndian.PutUint32(oversized[20:24], 2000)
	binary.BigEndian.PutUint32(oversized[29:33], crc32.ChecksumIEEE(oversized[12:29]))
	if picture, err := coverPictureFrom(oversized); !errors.Is(err, errCoverTooLarge) {
		t.Fatalf("coverPictureFrom(2000x2000 header) = %v, %v; want the pixel cap", picture, err)
	}
}

// coverBigPNG codifica un PNG del tamaño pedido sin materializarlo en el árbol
// de pruebas: se genera en memoria con la biblioteca estándar.
func coverBigPNG(t *testing.T, width, height int) []byte {
	t.Helper()
	return coverImagePNG(t, width, height, color.RGBA{B: 0xC0, A: 0xFF})
}
