package app

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
	view := playerViewText(t, m, "Navigation", "Playback", "Library", "Search", "Appearance", "Exit")
	for _, gone := range []string{"LIBRARY:", "Search: ", "Volume "} {
		if strings.Contains(view, gone) {
			t.Fatalf("help left the workspace %q visible:\n%s", gone, view)
		}
	}
	return view
}

func TestHelpOpensFromEveryPanel(t *testing.T) {
	for panel := 0; panel < int(playerFocusCount); panel++ {
		t.Run(fmt.Sprint(panel), func(t *testing.T) {
			m, _ := playerViewModel(t, 120, 40, playerState{Connected: true, SessionID: 4})
			for range panel {
				m, _ = m.Update(tea.KeyMsg{Type: tea.KeyTab})
			}
			focus := m.(*playerModel).focus
			m, cmd := m.Update(tea.KeyMsg{Type: tea.KeyF1})
			if cmd != nil {
				t.Fatal("F1 scheduled work")
			}
			helpOpened(t, m)
			if got := m.(*playerModel).focus; got != focus {
				t.Fatalf("F1 moved the focus from %v to %v", focus, got)
			}
		})
	}
}

func TestHelpQuestionMarkOutsideTextInput(t *testing.T) {
	m, out := playerViewModel(t, 120, 40, playerState{Connected: true})
	m, _ = m.Update(playerViewKey("?"))
	helpOpened(t, m)
	m, _ = m.Update(playerViewKey("?"))
	playerViewText(t, m, "Tab focus")

	// In the focused text field ? is text, and F1 still opens help there.
	m, _ = m.Update(playerViewKey("/"))
	m, _ = m.Update(playerViewKey("?"))
	playerViewText(t, m, "Search: ?_")
	if strings.Contains(ansi.Strip(m.View()), "Navigation") {
		t.Fatal("? inside the search field opened the help view")
	}
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyF1})
	helpOpened(t, m)
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	playerViewText(t, m, "Search: ?_")
	if got := m.(*playerModel).focus; got != playerFocusSearch {
		t.Fatalf("closing help left the focus on %v", got)
	}
	if len(out) != 0 {
		t.Fatal("the help keys sent a playback command")
	}
}

func TestHelpClosesToPreviousPanelAndCursor(t *testing.T) {
	for _, tt := range []struct {
		name string
		key  tea.KeyMsg
	}{
		{"F1", tea.KeyMsg{Type: tea.KeyF1}},
		{"question mark", playerViewKey("?")},
		{"escape", tea.KeyMsg{Type: tea.KeyEsc}},
		{"q", playerViewKey("q")},
	} {
		t.Run(tt.name, func(t *testing.T) {
			m, out := playerViewModel(t, 120, 40, playerState{
				Connected: true,
				Queue:     []playerTrack{{Title: "A"}, {Title: "B"}, {Title: "C"}},
			})
			m, _ = m.Update(playerViewKey("j"))
			playerViewText(t, m, ">2 B")
			before := ansi.Strip(m.View())
			m, _ = m.Update(tea.KeyMsg{Type: tea.KeyF1})
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
			playerViewText(t, m, ">2 B", "Tab focus")
			if len(out) != 0 {
				t.Fatal("closing help sent a command")
			}
			// q closed help instead of quitting.
			if tt.name == "q" {
				_, quit := m.Update(playerViewKey("q"))
				if _, ok := playerViewRun(t, quit).(tea.QuitMsg); !ok {
					t.Fatal("q closed help but the next q did not quit")
				}
			}
		})
	}
	t.Run("sidebar", func(t *testing.T) {
		m, _ := playerViewModel(t, 120, 40, playerState{Connected: true})
		for range 3 {
			m, _ = m.Update(tea.KeyMsg{Type: tea.KeyTab})
		}
		before := ansi.Strip(m.View())
		m, _ = m.Update(tea.KeyMsg{Type: tea.KeyF1})
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

func TestHelpIgnoresOtherKeys(t *testing.T) {
	m, out := playerViewModel(t, 120, 40, playerState{Connected: true, SessionID: 3, Volume: 40})
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyF1})
	before := ansi.Strip(m.View())
	focus := m.(*playerModel).focus
	for _, key := range []tea.KeyMsg{
		{Type: tea.KeyTab}, {Type: tea.KeyShiftTab}, playerViewKey("j"), playerViewKey("k"),
		{Type: tea.KeyDown}, {Type: tea.KeyUp}, {Type: tea.KeySpace}, {Type: tea.KeyEnter},
		{Type: tea.KeyLeft}, {Type: tea.KeyRight}, {Type: tea.KeyF2}, {Type: tea.KeyF3},
		{Type: tea.KeyCtrlC},
		playerViewKey("s"), playerViewKey("r"), playerViewKey("+"), playerViewKey("-"),
		playerViewKey("n"), playerViewKey("p"), playerViewKey("/"), playerViewKey("l"),
	} {
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

// TestHelpListsEveryKey proves the help screen documents every key the footer
// can advertise, plus the exit aliases.
func TestHelpListsEveryKey(t *testing.T) {
	m, _ := playerViewModel(t, 120, 40, playerState{Connected: true})
	player := m.(*playerModel)
	keys := map[string]bool{}
	for panel := 0; panel < int(playerFocusCount); panel++ {
		player.focus = playerPanel(panel)
		for _, line := range player.hintRows(120) {
			for _, item := range strings.Split(strings.TrimSpace(line), "  ") {
				key, _, ok := strings.Cut(item, " ")
				if !ok {
					t.Fatalf("footer pair %q is not `Key label`", item)
				}
				keys[key] = true
			}
		}
	}
	player.focus = playerFocusTable
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyF1})
	view := helpOpened(t, m)
	for key := range keys {
		if !strings.Contains(view, " "+key+" ") {
			t.Fatalf("the help screen does not list the advertised key %q:\n%s", key, view)
		}
	}
	for _, want := range []string{
		"next panel", "move the cursor", "open or play", "play or pause", "seek 10 s",
		"shuffle", "repeat", "volume", "focus the library", "open the playlist",
		"search", "delete", "language", "theme", "quit", "Ctrl+C",
	} {
		if !strings.Contains(view, want) {
			t.Fatalf("the help screen misses %q:\n%s", want, view)
		}
	}
}

func TestHelpLocalized(t *testing.T) {
	m := newPlayerModelWithPreferences(context.Background(), nil, nil, 120, 40, preferences{Language: "es", Theme: "standard"}, nil, "", nil)
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyF1})
	view := playerViewText(t, m, "Pulso · Ayuda", "Navegación", "Reproducción", "Biblioteca", "Búsqueda", "Apariencia", "Salir", "ayuda", "cerrar")
	for _, gone := range []string{"Navigation", "Playback", "Appearance", "Exit"} {
		if strings.Contains(view, gone) {
			t.Fatalf("Spanish help kept %q:\n%s", gone, view)
		}
	}
}

func TestHelpBounds(t *testing.T) {
	m, _ := playerViewModel(t, 120, 40, playerState{Connected: true, Status: "playing"})
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyF1})
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
