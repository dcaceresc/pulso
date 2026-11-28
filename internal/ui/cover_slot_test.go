package ui

import (
	"context"
	"encoding/binary"
	"errors"
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

// TestCoverSlotMetrics fija la ranura: cinco filas de diez medios bloques, con
// color de 24 bits, nada dibujado fuera de ella y cada fila del ancho exacto de
// la ventana.
func TestCoverSlotMetrics(t *testing.T) {
	profile := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.TrueColor)
	t.Cleanup(func() { lipgloss.SetColorProfile(profile) })

	m := coverSlotPlayer(t, coverFixtureURL, coverSlotSolid(t))
	for _, size := range []struct{ width, height int }{{120, 40}, {120, 34}} {
		rows := playerViewRows(t, m, size.width, size.height)
		slot := make([]int, 0, playerArtRows)
		for index, row := range rows {
			if width := lipgloss.Width(row); width != size.width {
				t.Fatalf("%dx%d row %d measures %d cells, want %d: %q", size.width, size.height, index, width, size.width, row)
			}
			if blocks := strings.Count(row, coverUpperHalf); blocks > 0 {
				slot = append(slot, index)
				if blocks != playerArtWidth {
					t.Fatalf("%dx%d row %d carries %d half blocks, want %d: %q", size.width, size.height, index, blocks, playerArtWidth, row)
				}
				// Los diez medios bloques van juntos y son lo único de la fila:
				// nada de la carátula se sale de la ranura.
				if want := "  " + strings.Repeat(coverUpperHalf, playerArtWidth) + "  "; !strings.Contains(row, want) {
					t.Fatalf("%dx%d row %d does not keep the slot together: %q", size.width, size.height, index, row)
				}
			}
		}
		if len(slot) != playerArtRows {
			t.Fatalf("%dx%d draws the cover in %v, want %d consecutive rows", size.width, size.height, slot, playerArtRows)
		}
		for row := slot[0]; row < slot[0]+playerArtRows; row++ {
			if !containsInt(slot, row) {
				t.Fatalf("%dx%d leaves a gap in the slot from row %d: %v", size.width, size.height, slot[0], slot)
			}
		}
		raw := m.View()
		for _, colour := range []string{"38;2;", "48;2;"} {
			if !strings.Contains(raw, colour) {
				t.Fatalf("%dx%d lost the 24-bit colour %q", size.width, size.height, colour)
			}
		}
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

// TestCoverSlotThreshold fija cuándo existe la ranura: con las filas de la
// carátula disponibles y al menos 80 columnas.
func TestCoverSlotThreshold(t *testing.T) {
	m := coverSlotPlayer(t, coverFixtureURL, coverSlotSolid(t))
	for _, tt := range []struct {
		width, height int
		slot          bool
	}{
		{120, 18, true}, {80, 24, true},
		{120, 17, false}, {80, 16, false}, {79, 40, false},
	} {
		view := strings.Join(playerViewRows(t, m, tt.width, tt.height), "\n")
		switch drawn := strings.Contains(view, coverUpperHalf); {
		case drawn != tt.slot:
			t.Fatalf("%dx%d draws the cover = %v, want %v:\n%s", tt.width, tt.height, drawn, tt.slot, view)
		case !tt.slot && strings.Contains(view, coverInitialsTop()):
			t.Fatalf("%dx%d draws the initials block without a slot:\n%s", tt.width, tt.height, view)
		}
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
	if strings.Contains(view, coverUpperHalf) || strings.Contains(view, coverInitialsTop()) {
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
	if _, err := coverRows(coverBigPNG(t, 640, 640)); err != nil {
		t.Fatalf("a 640x640 cover, the largest Spotify publishes, was rejected: %v", err)
	}
	// La misma imagen con la cabecera reescrita: unos cientos de bytes que
	// anuncian cuatro millones de píxeles.
	oversized := coverBigPNG(t, 20, 20)
	binary.BigEndian.PutUint32(oversized[16:20], 2000)
	binary.BigEndian.PutUint32(oversized[20:24], 2000)
	binary.BigEndian.PutUint32(oversized[29:33], crc32.ChecksumIEEE(oversized[12:29]))
	if rows, err := coverRows(oversized); !errors.Is(err, errCoverTooLarge) || rows != nil {
		t.Fatalf("coverRows(2000x2000 header) = %v, %v; want the pixel cap", rows, err)
	}
}

// coverBigPNG codifica un PNG del tamaño pedido sin materializarlo en el árbol
// de pruebas: se genera en memoria con la biblioteca estándar.
func coverBigPNG(t *testing.T, width, height int) []byte {
	t.Helper()
	return coverImagePNG(t, width, height, color.RGBA{B: 0xC0, A: 0xFF})
}
