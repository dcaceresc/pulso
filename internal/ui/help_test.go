package ui

import (
	"context"
	"fmt"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

// helpOpened asserts the full-screen help view replaced the workspace.
func helpOpened(t *testing.T, m tea.Model) string {
	t.Helper()
	view := playerViewText(t, m, "Global", "Zones", "Library", "Table", "Search")
	for _, gone := range []string{"LIBRARY:", "Search: ", "Volume "} {
		if strings.Contains(view, gone) {
			t.Fatalf("help left the workspace %q visible:\n%s", gone, view)
		}
	}
	return view
}

// helpZones son las zonas alcanzables, con la tecla que lleva a cada una.
func helpZones() []struct{ name, zone string } {
	return []struct{ name, zone string }{
		{"table", ""},
		{"queue", "c"},
		{"sidebar", "l"},
	}
}

func TestHelpOpensFromEveryZone(t *testing.T) {
	for _, tt := range helpZones() {
		t.Run(tt.name, func(t *testing.T) {
			m, _ := playerViewModel(t, 120, 40, playerState{Connected: true, SessionID: 4})
			if tt.zone != "" {
				m, _ = m.Update(playerViewKey(tt.zone))
			}
			focus := m.(*playerModel).focus
			m, cmd := m.Update(playerViewKey("?"))
			if cmd != nil {
				t.Fatal("? scheduled work")
			}
			helpOpened(t, m)
			if got := m.(*playerModel).focus; got != focus {
				t.Fatalf("? moved the focus from %v to %v", focus, got)
			}
		})
	}
}

// TestHelpIsTheOnlyKeyReference prueba que `?` es la única ayuda: `F1` ya no
// abre nada y dentro del campo de texto `?` es texto.
func TestHelpIsTheOnlyKeyReference(t *testing.T) {
	m, _ := playerViewModel(t, 120, 40, playerState{Connected: true})
	m, cmd := m.Update(tea.KeyMsg{Type: tea.KeyF1})
	if cmd != nil {
		t.Fatal("F1 scheduled work")
	}
	if view := ansi.Strip(m.View()); strings.Contains(view, "Pulso · Help") {
		t.Fatalf("F1 opened the help view:\n%s", view)
	}
	m, _ = m.Update(playerViewKey("/"))
	m, _ = m.Update(playerViewKey("?"))
	playerViewText(t, m, "Search: ?_")
	if strings.Contains(ansi.Strip(m.View()), "Pulso · Help") {
		t.Fatal("? inside the search field opened the help view")
	}
}

func TestHelpClosesToPreviousZoneAndCursor(t *testing.T) {
	for _, tt := range []struct {
		name string
		key  tea.KeyMsg
	}{
		{"question mark", playerViewKey("?")},
		{"escape", tea.KeyMsg{Type: tea.KeyEsc}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			m, out := playerViewModel(t, 120, 40, playerState{
				Connected: true,
				Queue:     []playerTrack{{Title: "A"}, {Title: "B"}, {Title: "C"}},
			})
			m, _ = m.Update(playerViewKey("j"))
			playerViewText(t, m, ">2 B")
			before := ansi.Strip(m.View())
			m, _ = m.Update(playerViewKey("?"))
			helpOpened(t, m)
			m, cmd := m.Update(tt.key)
			if cmd != nil {
				t.Fatalf("%q while help is open scheduled work", tt.key.String())
			}
			if after := ansi.Strip(m.View()); after != before {
				t.Fatalf("closing help with %q changed the workspace:\n%s", tt.name, after)
			}
			if got := m.(*playerModel).focus; got != playerFocusTable {
				t.Fatalf("closing help left the focus on %v", got)
			}
			playerViewText(t, m, ">2 B")
			playerViewNoFooter(t, m)
			if len(out) != 0 {
				t.Fatal("closing help sent a command")
			}
		})
	}
	t.Run("sidebar", func(t *testing.T) {
		m, _ := playerViewModel(t, 120, 40, playerState{Connected: true})
		m, _ = m.Update(playerViewKey("l"))
		before := ansi.Strip(m.View())
		m, _ = m.Update(playerViewKey("?"))
		helpOpened(t, m)
		m, _ = m.Update(tea.KeyMsg{Type: tea.KeyEsc})
		if after := ansi.Strip(m.View()); after != before {
			t.Fatalf("closing help changed the sidebar workspace:\n%s", after)
		}
		if got := m.(*playerModel).focus; got != playerFocusSidebar {
			t.Fatalf("closing help left the focus on %v", got)
		}
	})
}

// TestHelpQuitsOnExitKeys prueba que las teclas globales de salida siguen
// valiendo sobre la ayuda: `q` y `Ctrl+C` cierran la aplicación.
func TestHelpQuitsOnExitKeys(t *testing.T) {
	for _, tt := range []struct {
		name string
		key  tea.KeyMsg
	}{
		{"q", playerViewKey("q")},
		{"ctrl+c", tea.KeyMsg{Type: tea.KeyCtrlC}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			m, out := playerViewModel(t, 120, 40, playerState{Connected: true, SessionID: 3, Volume: 40})
			m, _ = m.Update(playerViewKey("?"))
			helpOpened(t, m)
			m, quit := m.Update(tt.key)
			if _, ok := playerViewRun(t, quit).(tea.QuitMsg); !ok || m.View() != "" {
				t.Fatalf("%q did not quit from the help view", tt.key.String())
			}
			if len(out) != 0 {
				t.Fatal("quitting from help sent a playback command")
			}
		})
	}
}

func TestHelpIgnoresOtherKeys(t *testing.T) {
	m, out := playerViewModel(t, 120, 40, playerState{Connected: true, SessionID: 3, Volume: 40})
	m, _ = m.Update(playerViewKey("?"))
	before := ansi.Strip(m.View())
	focus := m.(*playerModel).focus
	keys := []tea.KeyMsg{
		{Type: tea.KeyTab}, {Type: tea.KeyShiftTab}, playerViewKey("j"), playerViewKey("k"),
		{Type: tea.KeyDown}, {Type: tea.KeyUp}, {Type: tea.KeySpace}, {Type: tea.KeyEnter},
		{Type: tea.KeyLeft}, {Type: tea.KeyRight}, {Type: tea.KeyF1}, {Type: tea.KeyF2}, {Type: tea.KeyF3},
		playerViewKey("s"), playerViewKey("r"), playerViewKey("+"), playerViewKey("-"),
		playerViewKey("n"), playerViewKey("b"), playerViewKey("/"), playerViewKey("l"),
		playerViewKey("c"), playerViewKey("a"), playerViewKey("x"), playerViewKey("d"),
	}
	for _, key := range keys {
		var cmd tea.Cmd
		m, cmd = m.Update(key)
		if cmd != nil {
			t.Fatalf("%q while help is open scheduled work", key.String())
		}
	}
	if got := m.(*playerModel).focus; got != focus {
		t.Fatalf("a key moved the focus to %v behind help", got)
	}
	if after := ansi.Strip(m.View()); after != before {
		t.Fatalf("a key changed the help view:\n%s", after)
	}
	if len(out) != 0 {
		t.Fatal("help forwarded a playback command")
	}
	if got := m.(*playerModel).preferences.Language; got != "en" {
		t.Fatalf("F2 changed the language while help is open: %q", got)
	}
}

// TestHelpListsEveryBinding fija que la ayuda, ahora la única referencia de
// teclas, nombre cada enlace del modelo y ninguna tecla retirada.
func TestHelpListsEveryBinding(t *testing.T) {
	m, _ := playerViewModel(t, 120, 40, playerState{Connected: true})
	m, _ = m.Update(playerViewKey("?"))
	view := helpOpened(t, m)
	for _, want := range []string{
		"q / Ctrl+C", "Space", "←→", "n / b", "+/-", "s", "r", "d", "F2 / F3", "?", "Esc",
		"l", "/", "c", "↑↓/jk", "Enter", "a", "x", "Backspace",
		"quit", "play or pause", "seek 10 s", "next or previous track", "volume",
		"shuffle playback", "repeat off, all, one", "discover songs similar to the playing track", "language or theme", "help",
		"back to the table or cancel", "library", "search: q s r + - are text", "queue",
		"move the cursor", "open the playlist or play it", "shuffle the selected playlist",
		"play the row", "save the playing track to a playlist",
		"remove the highlighted track from the open playlist", "search", "delete", "leave the field",
	} {
		if !strings.Contains(view, want) {
			t.Fatalf("the help screen misses %q:\n%s", want, view)
		}
	}
	for _, gone := range []string{"Tab focus", "Tab:", "Shift+Tab", "F1", "next panel", "previous panel"} {
		if strings.Contains(view, gone) {
			t.Fatalf("the help screen still lists the retired %q:\n%s", gone, view)
		}
	}
}

func TestHelpLocalized(t *testing.T) {
	m := NewPlayerModelWithPreferences(context.Background(), nil, nil, 120, 40, preferences{Language: "es", Theme: "standard"}, nil, "", nil)
	m, _ = m.Update(playerViewKey("?"))
	view := playerViewText(t, m, "Pulso · Ayuda", "Global", "Zonas", "Biblioteca", "Tabla", "Búsqueda",
		"salir", "reproducir o pausar", "volumen", "ayuda", "cerrar", "cola", "mover el cursor",
		"reproducir la fila", "guardar la pista en una lista", "quitar la pista marcada de la lista abierta")
	for _, gone := range []string{"Zones", "Library", "Table", "Search", "play or pause", "next or previous track"} {
		if strings.Contains(view, gone) {
			t.Fatalf("Spanish help kept %q:\n%s", gone, view)
		}
	}
}

func TestHelpBounds(t *testing.T) {
	m, _ := playerViewModel(t, 120, 40, playerState{Connected: true, Status: "playing"})
	m, _ = m.Update(playerViewKey("?"))
	for _, size := range []struct{ width, height int }{
		{120, 40}, {100, 30}, {80, 24}, {40, 12}, {20, 8}, {1, 1}, {0, 10}, {10, 0},
	} {
		t.Run(fmt.Sprintf("%dx%d", size.width, size.height), func(t *testing.T) {
			rows := playerViewRows(t, m, size.width, size.height)
			if size.width < 1 || size.height < 1 {
				return
			}
			view := strings.Join(rows, "\n")
			for _, r := range view {
				if r == '\n' {
					continue
				}
				if r >= 0x1F000 {
					t.Fatalf("emoji %q in the help screen at %dx%d", r, size.width, size.height)
				}
				if lipgloss.Width(string(r)) != 1 {
					t.Fatalf("wide glyph %q in the help screen at %dx%d", r, size.width, size.height)
				}
			}
		})
	}
}
