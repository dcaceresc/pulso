package ui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// e6Captures lee los bloques cercados de `## Capturas de E6` en su orden: antes
// 120, antes 80, después 120, después 80 y la de carátula.
func e6Captures(t *testing.T) [][]string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "..", "odd", "tasks", "tui-reformulation.md"))
	if err != nil {
		t.Fatalf("no se pudo leer el registro de E6: %v", err)
	}
	lines := strings.Split(string(data), "\n")
	var blocks [][]string
	for i := 0; i < len(lines); i++ {
		if !strings.HasPrefix(lines[i], "## Capturas de E6") {
			continue
		}
		for i++; i < len(lines) && !strings.HasPrefix(lines[i], "## "); i++ {
			if strings.TrimSpace(lines[i]) != "```" {
				continue
			}
			var block []string
			for i++; i < len(lines) && strings.TrimSpace(lines[i]) != "```"; i++ {
				block = append(block, lines[i])
			}
			blocks = append(blocks, block)
		}
		break
	}
	if len(blocks) != 5 {
		t.Fatalf("el registro de E6 guarda %d capturas; se esperaban cinco", len(blocks))
	}
	return blocks
}

// e6DeclaredWorkspace construye el espacio entregado con el estado que el
// registro declara: conectado, en reproducción, aleatorio activado, repetición
// de lista y volumen 85.
func e6DeclaredWorkspace(t *testing.T) tea.Model {
	t.Helper()
	source := &libraryFake{playlists: libraryPlaylistsFixture(), items: libraryTracksFixture(42)}
	m, _, _ := libraryNewPlayer(t, 120, 40, preferences{Language: "es"}, playerState{
		Connected: true, Status: "playing", Volume: 85, PositionMS: 0, OptionsKnown: true,
		Shuffle: true, Repeat: "context", PlaylistContext: "Rock Chileno",
		Track: playerTrack{Title: "Estrechez de Corazón", Artist: "Los Prisioneros", Album: "La Cultura de la Basura", DurationMS: 381000},
		Queue: []playerTrack{{Title: "Huelga de Dolores", Artist: "Los Miserables", DurationMS: 258000}},
	}, source, nil, "")
	next, cmd := libraryOpenFirst(t, m)
	return librarySettle(t, next, cmd)
}

// TestE6CapturesMatchTheRecord fija la verificación visual: cada captura mide el
// ancho de su banda y las tres del estado entregado son idénticas a lo que la
// vista dibuja con el estado que el registro declara.
func TestE6CapturesMatchTheRecord(t *testing.T) {
	blocks := e6Captures(t)
	for i, block := range blocks {
		width := 120
		if i == 1 || i == 3 {
			width = 80
		}
		for row, line := range block {
			if got := lipgloss.Width(line); got != width {
				t.Errorf("captura %d fila %d mide %d celdas, se esperaban %d: %q", i, row, got, width, line)
			}
		}
	}
	if len(blocks[0]) != 34 || len(blocks[2]) != 34 || len(blocks[3]) != 24 || len(blocks[4]) != 34 {
		t.Fatalf("alturas = %d, %d, %d, %d; se esperaban 34, 34, 24 y 34", len(blocks[0]), len(blocks[2]), len(blocks[3]), len(blocks[4]))
	}

	m := e6DeclaredWorkspace(t)
	for i, size := range []struct{ width, height int }{{120, 34}, {80, 24}} {
		got := strings.Join(playerViewRows(t, m, size.width, size.height), "\n")
		want := strings.Join(blocks[2+i], "\n")
		if got != want {
			t.Errorf("la captura %dx%d no coincide con el estado declarado:\n--- registro\n%s\n--- vista\n%s", size.width, size.height, want, got)
		}
	}
}
