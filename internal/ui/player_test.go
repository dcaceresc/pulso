package ui

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/dcaceresc/pulso/internal/spotify"
)

func playerViewKey(s string) tea.KeyMsg { return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)} }

func playerViewRun(t *testing.T, cmd tea.Cmd) tea.Msg {
	t.Helper()
	if cmd == nil {
		t.Fatal("expected a Bubble Tea command")
	}
	done := make(chan tea.Msg, 1)
	go func() { done <- cmd() }()
	select {
	case msg := <-done:
		return msg
	case <-time.After(2 * time.Second):
		t.Fatal("Bubble Tea command did not return")
		return nil
	}
}

func playerViewText(t *testing.T, m tea.Model, parts ...string) string {
	t.Helper()
	view := ansi.Strip(m.View())
	for _, part := range parts {
		if !strings.Contains(view, part) {
			t.Errorf("missing %q in view:\n%s", part, view)
		}
	}
	return view
}

// playerViewModel drives a fresh model to its first rendered playback state.
func playerViewModel(t *testing.T, width, height int, state playerState) (tea.Model, chan playerCommand) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	states := make(chan playerState, 1)
	out := make(chan playerCommand, 1)
	states <- state
	m := NewPlayerModel(ctx, states, out, width, height)
	m, _ = m.Update(playerViewRun(t, m.Init()))
	return m, out
}

func playerViewRows(t *testing.T, m tea.Model, width, height int) []string {
	t.Helper()
	m, _ = m.Update(tea.WindowSizeMsg{Width: width, Height: height})
	view := ansi.Strip(m.View())
	if width < 1 || height < 1 {
		if view != "" {
			t.Fatalf("expected an empty view, got %q", view)
		}
		return nil
	}
	rows := strings.Split(view, "\n")
	if len(rows) > height {
		t.Fatalf("%dx%d rendered %d rows:\n%s", width, height, len(rows), view)
	}
	for _, row := range rows {
		if lipgloss.Width(row) > width {
			t.Fatalf("%dx%d row overflows (%d cells): %q", width, height, lipgloss.Width(row), row)
		}
	}
	return rows
}

func TestPlayerViewLifecycle(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	states := make(chan playerState, 1)
	out := make(chan playerCommand, 1)
	m := NewPlayerModel(ctx, states, out, 80, 24)
	playerViewText(t, m, "Pulso", "Loading", "Esc back/quit", "F1 help")
	next := m.Init()
	if m.Init() != nil {
		t.Fatal("scheduled a second pending read")
	}
	for _, state := range []struct {
		value playerState
		want  []string
	}{
		{playerState{Connected: true}, []string{"Connected", "Nothing playing", "Nothing here yet"}},
		{playerState{Notice: "Retrying Soloist"}, []string{"Disconnected", "Retrying Soloist"}},
		{playerState{Connected: true, Track: playerTrack{Title: "Returned"}}, []string{"Connected", "Returned"}},
	} {
		// Window messages and ignored keys must not create additional readers.
		for _, msg := range []tea.Msg{tea.WindowSizeMsg{Width: 80, Height: 24}, playerViewKey("x")} {
			var extra tea.Cmd
			m, extra = m.Update(msg)
			if extra != nil {
				t.Fatal("unexpected work while awaiting state")
			}
		}
		states <- state.value
		m, next = m.Update(playerViewRun(t, next))
		playerViewText(t, m, state.want...)
	}
	close(states)
	m, next = m.Update(playerViewRun(t, next))
	if next != nil {
		t.Fatal("closed state channel scheduled another read")
	}
	view := playerViewText(t, m, "Disconnected")
	if strings.Contains(view, "Returned") {
		t.Fatal("closed state stream still displays stale playback")
	}
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeySpace})
	if cmd != nil || len(out) != 0 {
		t.Fatal("closed state stream still accepts playback commands")
	}
}

func TestPlayerViewResponsiveAndSafeMetadata(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	states := make(chan playerState, 1)
	states <- playerState{
		Connected: true, Status: "playing", Device: "Desk\tDevice", Notice: "Good\r\nnews",
		PositionMS: 62000, Volume: 37,
		Track: playerTrack{Title: "\x1b[31m彩虹\x1b[0m\nTitle\x00", Artist: "Artist", Album: "Album", DurationMS: 187000},
		Queue: []playerTrack{{Title: "\x1b]0;UNTRUSTED\x07Next", Artist: "Up next"}, {Title: strings.Repeat("界", 70)}},
	}
	m := NewPlayerModel(ctx, states, nil, 100, 20)
	m, _ = m.Update(playerViewRun(t, m.Init()))
	for _, size := range []struct{ width, height int }{
		{120, 40}, {100, 30}, {100, 20}, {80, 24}, {70, 16}, {40, 12}, {30, 12}, {20, 8}, {12, 5}, {1, 1}, {0, 10}, {10, 0},
	} {
		t.Run(fmt.Sprintf("%dx%d", size.width, size.height), func(t *testing.T) {
			rows := playerViewRows(t, m, size.width, size.height)
			if size.width < 1 || size.height < 1 {
				return
			}
			view := strings.Join(rows, "\n")
			if strings.ContainsAny(view, "\r\t\x00\x07\x1b") || strings.Contains(m.View(), "UNTRUSTED") || strings.Contains(m.View(), "\x1b[31m") {
				t.Fatalf("metadata injected controls: %q", m.View())
			}
			band := playerBand(size.width, size.height)
			if band == playerBandInsufficient {
				// La banda insuficiente solo nombra el tamaño: ningún dato de la
				// sesión se dibuja.
				for _, gone := range []string{"Pulso", "playing", "Good news", "Title", "Volume"} {
					if strings.Contains(view, gone) {
						t.Fatalf("the size message kept %q at %dx%d:\n%s", gone, size.width, size.height, view)
					}
				}
				return
			}
			playerViewText(t, m, "Pulso", "playing", "Good news", "Title", "Volume 37%")
			if size.width >= 60 {
				playerViewText(t, m, "Artist", "彩虹 Title", "1:02", "3:07", "Desk Device")
			}
			// El encabezado de la tabla solo existe en las bandas que dibujan la
			// tabla: la mínima se queda con el contexto y el reproductor.
			if playerViewBandHasTable(band) {
				playerViewText(t, m, "Duration")
			}
		})
	}
}

func TestPlayerViewWorkspaceBounds(t *testing.T) {
	state := playerState{
		Connected: true, Status: "playing", Device: "Desk", Volume: 37, PositionMS: 62000, OptionsKnown: true,
		Track: playerTrack{Title: "Estrechez de Corazón", Artist: "Los Prisioneros", DurationMS: 381000},
		Queue: []playerTrack{
			{Title: "Tren al Sur", Artist: "Los Prisioneros", DurationMS: 336000},
			{Title: "Llueve sobre la Ciudad", Artist: "Los Bunkers", DurationMS: 236000},
		},
	}
	m, _ := playerViewModel(t, 120, 40, state)
	for _, tt := range []struct {
		width, height int
		want          []string
		absent        []string
	}{
		{120, 40, []string{
			"Pulso", "Search:", "LIBRARY", "Nothing here yet", "#", "Title", "Artist", "Duration",
			"Estrechez de Corazón", "Tren al Sur", "1:02", "6:21", "Volume 37%", "Device Desk",
			"⇄ off", "↻ off", "Tab focus", "Esc back/quit", "l library", "F2 language", "F1 help",
		}, nil},
		{100, 30, []string{
			"Pulso", "Search:", "LIBRARY", "Nothing here yet", "#", "Title", "Artist", "Duration",
			"Estrechez de Corazón", "1:02", "6:21", "Volume 37%", "Device Desk", "Esc back/quit",
		}, []string{"UP NEXT", "CONTEXT"}},
		{80, 24, []string{
			"Pulso", "LIBRARY:", "Nothing here yet", "#", "Title", "Artist", "Duration",
			"Estrechez de Corazón", "Volume 37%", "Esc back/quit", "├─", "└",
		}, nil},
		{60, 20, []string{
			"Pulso", "LIBRARY:", "Nothing here yet", "#", "Title", "Artist", "Duration",
			"Estrechez de Corazón", "Volume 37%",
		}, nil},
		{40, 12, []string{"Pulso", "▶", "Volume 37%", "Estrechez de Corazón", "├─", "└"}, nil},
		{20, 8, []string{"Minimum 40×12"}, []string{"Pulso", "├─"}},
		{1, 1, nil, nil},
	} {
		t.Run(fmt.Sprintf("%dx%d", tt.width, tt.height), func(t *testing.T) {
			rows := playerViewRows(t, m, tt.width, tt.height)
			if tt.width == 1 && tt.height == 1 {
				if len(rows) != 1 || lipgloss.Width(rows[0]) != 1 {
					t.Fatalf("expected a single one-cell row, got %q", rows)
				}
				return
			}
			view := strings.Join(rows, "\n")
			for _, want := range tt.want {
				if !strings.Contains(view, want) {
					t.Errorf("missing %q in %dx%d view:\n%s", want, tt.width, tt.height, view)
				}
			}
			for _, absent := range tt.absent {
				if strings.Contains(view, absent) {
					t.Errorf("unexpected %q in %dx%d view:\n%s", absent, tt.width, tt.height, view)
				}
			}
			if tt.width >= 100 && tt.height >= 30 {
				// La biblioteca es una caja: su título vive en el borde superior.
				library := false
				for _, row := range rows {
					if strings.Contains(row, "LIBRARY") && strings.Contains(row, "┌") {
						library = true
					}
				}
				if !library {
					t.Fatalf("wide layout lost the library box:\n%s", view)
				}
			}
		})
	}
}

func TestPlayerViewWorkspaceLocalized(t *testing.T) {
	m := NewPlayerModelWithPreferences(context.Background(), nil, nil, 120, 40, preferences{Language: "es", Theme: "standard"}, nil, "", nil)
	playerViewText(t, m, "Pulso", "Buscar:", "BIBLIOTECA", "Aún no hay nada", "Reproducción", "Título", "Artista", "Duración", "Esc volver/salir", "F2 idioma")
}

func TestPlayerViewFocusCycleAndHints(t *testing.T) {
	m, out := playerViewModel(t, 120, 40, playerState{Connected: true})
	// The table owns focus first; every panel advertises itself in the hints.
	for _, step := range []struct {
		key  tea.KeyMsg
		want []string
		drop []string
	}{
		{tea.KeyMsg{Type: tea.KeyTab}, []string{"Search: _", "Enter search", "Backspace delete"}, []string{"↑↓/jk", "Space play/pause"}},
		{tea.KeyMsg{Type: tea.KeyTab}, []string{"Space play/pause", "←→ seek"}, []string{"Enter search", "↑↓/jk"}},
		{tea.KeyMsg{Type: tea.KeyTab}, []string{"↑↓/jk library", "Enter open"}, []string{"←→ seek", "Enter search"}},
		{tea.KeyMsg{Type: tea.KeyTab}, []string{"↑↓/jk move", "Enter play"}, []string{"Enter open"}},
		{tea.KeyMsg{Type: tea.KeyShiftTab}, []string{"↑↓/jk library", "Enter open"}, []string{"Enter play"}},
		{tea.KeyMsg{Type: tea.KeyShiftTab}, []string{"Space play/pause", "←→ seek"}, []string{"↑↓/jk library"}},
		{tea.KeyMsg{Type: tea.KeyShiftTab}, []string{"Enter search"}, []string{"Space play/pause"}},
		{tea.KeyMsg{Type: tea.KeyShiftTab}, []string{"↑↓/jk move"}, []string{"Enter search"}},
	} {
		var cmd tea.Cmd
		m, cmd = m.Update(step.key)
		if cmd != nil {
			t.Fatal("focus movement scheduled work")
		}
		view := playerViewText(t, m, step.want...)
		for _, drop := range step.drop {
			if strings.Contains(view, drop) {
				t.Fatalf("stale hint %q after focus change:\n%s", drop, view)
			}
		}
		if len(out) != 0 {
			t.Fatal("focus movement sent a playback command")
		}
	}
}

func TestPlayerViewSearchInputSwallowsCommands(t *testing.T) {
	m, out := playerViewModel(t, 100, 30, playerState{Connected: true, Status: "paused", Volume: 50})
	m, _ = m.Update(playerViewKey("/"))
	playerViewText(t, m, "Search: _")
	for _, key := range []string{"q", "s", "r", "+", "-"} {
		var cmd tea.Cmd
		m, cmd = m.Update(playerViewKey(key))
		if cmd != nil {
			t.Fatalf("%q scheduled work while typing", key)
		}
		if len(out) != 0 {
			t.Fatalf("%q sent a playback command while typing", key)
		}
	}
	view := playerViewText(t, m, "Search: qsr+-_")
	if strings.Contains(view, "Search tracks") {
		t.Fatalf("typing opened the catalog overlay:\n%s", view)
	}
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyBackspace})
	playerViewText(t, m, "Search: qsr+_")
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeySpace})
	playerViewText(t, m, "Search: qsr+ _")
	if len(out) != 0 {
		t.Fatal("typing space toggled playback")
	}
	if m.View() == "" {
		t.Fatal("typing quit the workspace")
	}
}

func TestPlayerViewSearchBlur(t *testing.T) {
	for _, tt := range []struct {
		name string
		key  tea.KeyMsg
	}{
		{"enter", tea.KeyMsg{Type: tea.KeyEnter}},
		{"escape", tea.KeyMsg{Type: tea.KeyEsc}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			m, out := playerViewModel(t, 100, 30, playerState{Connected: true})
			m, _ = m.Update(playerViewKey("/"))
			m, _ = m.Update(playerViewKey("zzr"))
			m, cmd := m.Update(tt.key)
			if cmd != nil {
				t.Fatal("blurring the search input quit the workspace")
			}
			if len(out) != 0 {
				t.Fatal("blurring the search input sent a command")
			}
			// The buffer survives and the cursor leaves with the focus.
			view := playerViewText(t, m, "Search: zzr")
			if strings.Contains(view, "Search: zzr_") {
				t.Fatalf("blurred input kept its cursor:\n%s", view)
			}
			if m.View() == "" {
				t.Fatal("blurring quit the workspace")
			}
			// Focus is back on a panel where q quits.
			_, quit := m.Update(playerViewKey("q"))
			if _, ok := playerViewRun(t, quit).(tea.QuitMsg); !ok {
				t.Fatal("q did not quit after the search input blurred")
			}
		})
	}
}

func TestPlayerViewQuitOutsideSearch(t *testing.T) {
	for _, tt := range []struct {
		name string
		tabs int
	}{
		{"table", 0},
		{"playing bar", 2},
		{"sidebar", 3},
	} {
		t.Run(tt.name, func(t *testing.T) {
			for _, key := range []tea.KeyMsg{playerViewKey("q"), {Type: tea.KeyEsc}} {
				m, out := playerViewModel(t, 100, 30, playerState{Connected: true, Volume: 50})
				for range tt.tabs {
					m, _ = m.Update(tea.KeyMsg{Type: tea.KeyTab})
				}
				m, quit := m.Update(key)
				if _, ok := playerViewRun(t, quit).(tea.QuitMsg); !ok || m.View() != "" {
					t.Fatalf("%q did not quit from the %s", key.String(), tt.name)
				}
				if len(out) != 0 {
					t.Fatal("quitting sent a playback command")
				}
			}
		})
	}
}

func TestPlayerViewSelectionMoves(t *testing.T) {
	queue := []playerTrack{{Title: "A"}, {Title: "B"}, {Title: "C"}}
	m, out := playerViewModel(t, 100, 30, playerState{Connected: true, Queue: queue})
	playerViewText(t, m, ">1 A")
	for _, tt := range []struct {
		key  tea.KeyMsg
		want string
	}{
		{playerViewKey("j"), ">2 B"},
		{playerViewKey("k"), ">1 A"},
		{tea.KeyMsg{Type: tea.KeyDown}, ">2 B"},
		{tea.KeyMsg{Type: tea.KeyDown}, ">3 C"},
		{tea.KeyMsg{Type: tea.KeyDown}, ">3 C"},
		{tea.KeyMsg{Type: tea.KeyUp}, ">2 B"},
	} {
		var cmd tea.Cmd
		m, cmd = m.Update(tt.key)
		if cmd != nil {
			t.Fatal("selection movement scheduled work")
		}
		if len(out) != 0 {
			t.Fatal("selection movement sent a playback command")
		}
		playerViewText(t, m, tt.want)
	}
	// Empty lists keep index 0.
	for _, tt := range []struct {
		state playerState
		want  string
	}{
		{playerState{Connected: true}, "Nothing here yet"},
		{playerState{}, "Queue unavailable"},
	} {
		empty, _ := playerViewModel(t, 100, 30, tt.state)
		for _, key := range []tea.KeyMsg{playerViewKey("j"), playerViewKey("k"), {Type: tea.KeyDown}, {Type: tea.KeyUp}} {
			empty, _ = empty.Update(key)
		}
		playerViewText(t, empty, tt.want)
	}
}

func TestPlayerViewPlaybackKeys(t *testing.T) {
	for _, tt := range []struct {
		name   string
		key    tea.KeyMsg
		status string
		volume int
		want   playerCommand
	}{
		{"pause", tea.KeyMsg{Type: tea.KeySpace}, "playing", 50, playerCommand{Name: "pause"}},
		{"resume", tea.KeyMsg{Type: tea.KeySpace}, "paused", 50, playerCommand{Name: "play"}},
		{"next", playerViewKey("n"), "playing", 50, playerCommand{Name: "skip_next"}},
		{"previous", playerViewKey("p"), "playing", 50, playerCommand{Name: "skip_prev"}},
		{"louder", playerViewKey("+"), "playing", 50, playerCommand{Name: "set_volume", Volume: 55}},
		{"quieter", playerViewKey("-"), "playing", 50, playerCommand{Name: "set_volume", Volume: 45}},
		{"volume ceiling", playerViewKey("+"), "playing", 98, playerCommand{Name: "set_volume", Volume: 100}},
		{"volume floor", playerViewKey("-"), "playing", 2, playerCommand{Name: "set_volume", Volume: 0}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			states := make(chan playerState, 1)
			out := make(chan playerCommand, 1)
			states <- playerState{Connected: true, Status: tt.status, Volume: tt.volume}
			m := NewPlayerModel(ctx, states, out, 80, 24)
			m, _ = m.Update(playerViewRun(t, m.Init()))
			_, cmd := m.Update(tt.key)
			if cmd != nil {
				playerViewRun(t, cmd)
			}
			select {
			case got := <-out:
				if got != tt.want || len(out) != 0 {
					t.Fatalf("got %+v, want exactly one %+v", got, tt.want)
				}
			default:
				t.Fatalf("missing command %+v", tt.want)
			}
		})
	}
}

func TestPlayerViewNonblockingAndQuit(t *testing.T) {
	for _, tt := range []struct {
		name string
		quit tea.KeyMsg
	}{
		{"escape", tea.KeyMsg{Type: tea.KeyEsc}},
		{"q", playerViewKey("q")},
	} {
		t.Run(tt.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			states := make(chan playerState, 1)
			out := make(chan playerCommand, 1)
			states <- playerState{}
			m := NewPlayerModel(ctx, states, out, 80, 24)
			m, next := m.Update(playerViewRun(t, m.Init()))
			if _, cmd := m.Update(playerViewKey("n")); cmd != nil || len(out) != 0 {
				t.Fatal("disconnected playback accepted a command")
			}
			states <- playerState{Connected: true, Volume: 50}
			m, next = m.Update(playerViewRun(t, next))
			out <- playerCommand{Name: "already enqueued"}
			m = playerViewRun(t, func() tea.Msg {
				updated, cmd := m.Update(playerViewKey("+"))
				if cmd != nil {
					cmd()
				}
				return updated
			}).(tea.Model)
			playerViewText(t, m, "Volume 50%", "Controls busy; try again.")
			if got := <-out; got.Name != "already enqueued" || len(out) != 0 {
				t.Fatal("full command channel was changed")
			}
			m, quit := m.Update(tt.quit)
			if _, ok := playerViewRun(t, quit).(tea.QuitMsg); !ok || m.View() != "" {
				t.Fatal("quit did not end the view")
			}
			if _, again := m.Update(playerViewRun(t, next)); again != nil {
				t.Fatal("quit should cancel the pending read without restarting it")
			}
		})
	}
}

// TestPlayerViewCatalogKeysNoLongerOpenAnOverlay keeps the old catalog test's
// intent: an authorized catalog never renders a modal any more, `l` moves the
// focus to the sidebar, and the player keeps receiving Soloist state.
func TestPlayerViewCatalogKeysNoLongerOpenAnOverlay(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	states, out := make(chan playerState, 1), make(chan playerCommand, 1)
	api := catalogViewFake{
		search: func(context.Context, string, int) ([]catalogItem, bool, error) {
			return []catalogItem{{Name: "Fixture result", URI: "spotify:track:AbC123"}}, false, nil
		},
		lists: func(context.Context, int) ([]catalogItem, bool, error) {
			return []catalogItem{{Name: "Fixture playlist", URI: "spotify:playlist:AbC123"}}, false, nil
		},
	}
	m := NewPlayerModelWithPreferences(ctx, states, out, 100, 24, preferences{}, nil, "fixture-id", catalogViewReply(api))
	states <- playerState{Connected: true, SessionID: 42}
	m, next := m.Update(playerViewRun(t, m.Init()))
	if next == nil {
		t.Fatal("the workspace stopped receiving playback state")
	}
	before := ansi.Strip(m.View())
	if strings.Contains(before, "Search tracks") || strings.Contains(before, "Fixture result") {
		t.Fatalf("a catalog overlay is still rendered:\n%s", before)
	}

	// s reaches Soloist from the table without reopening an overlay and
	// without guessing the new state before the event.
	m, cmd := m.Update(playerViewKey("s"))
	if cmd != nil {
		t.Fatal("s scheduled work outside the playing bar")
	}
	if after := ansi.Strip(m.View()); after == before {
		t.Fatalf("s did not reflect the requested shuffle:\n%s", after)
	}
	select {
	case got := <-out:
		if got != (playerCommand{Name: "set_shuffle", Shuffle: true, SessionID: 42}) {
			t.Fatalf("s sent %+v, want set_shuffle", got)
		}
	default:
		t.Fatal("s sent no shuffle command from the table")
	}
	// The snapshot refresh may be dropped when the bounded channel is full.
	select {
	case got := <-out:
		if got.Name != "get_state" {
			t.Fatalf("follow-up = %+v, want get_state", got)
		}
	default:
	}

	// l focuses the sidebar instead of opening the playlists overlay.
	m, _ = m.Update(playerViewKey("l"))
	playerViewText(t, m, "↑↓/jk library")
	if view := ansi.Strip(m.View()); strings.Contains(view, "Fixture playlist") {
		t.Fatalf("l opened the playlist overlay:\n%s", view)
	}

	// The playing bar still follows the displayed session after those keys.
	states <- playerState{Connected: true, SessionID: 43, Track: playerTrack{Title: "Fresh playback"}}
	m, next = m.Update(playerViewRun(t, next))
	if next == nil {
		t.Fatal("focus changes stopped player reception")
	}
	playerViewText(t, m, "Fresh playback")
	if len(out) != 0 {
		t.Fatal("catalog keys sent a playback command")
	}
}

func TestPlayerViewPreferenceShortcuts(t *testing.T) {
	var saved []preferences
	// La banda amplia nombra la tabla, así que “Playback” sigue siendo legible.
	m := NewPlayerModelWithPreferences(context.Background(), nil, nil, 120, 40, preferences{Language: "en", Theme: "standard"}, func(p preferences) error {
		saved = append(saved, p)
		return nil
	}, "", nil)
	m.Init()
	playerViewText(t, m, "Pulso", "Playback", "F2 language", "F3 theme")
	if len(saved) != 0 {
		t.Fatalf("startup unexpectedly saved: %v", saved)
	}
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyF2})
	playerViewText(t, m, "Pulso", "Reproducción", "Cargando", "F2 idioma", "F3 tema")
	for range 3 {
		m, _ = m.Update(tea.KeyMsg{Type: tea.KeyF3})
	}
	want := []preferences{{Language: "es", Theme: "standard"}, {Language: "es", Theme: "dark"}, {Language: "es", Theme: "light"}, {Language: "es", Theme: "standard"}}
	if fmt.Sprint(saved) != fmt.Sprint(want) {
		t.Fatalf("saved %v, want %v", saved, want)
	}
}

func TestPlayerViewContextCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	states := make(chan playerState, 1)
	out := make(chan playerCommand, 1)
	states <- playerState{Connected: true}
	m := NewPlayerModel(ctx, states, out, 80, 24)
	m, next := m.Update(playerViewRun(t, m.Init()))
	cancel()
	if _, cmd := m.Update(playerViewKey("n")); cmd != nil || len(out) != 0 {
		t.Fatal("canceled context accepted a command")
	}
	m, next = m.Update(playerViewRun(t, next))
	if next != nil {
		t.Fatal("canceled context scheduled another read")
	}
	playerViewText(t, m, "Disconnected")
}

// TestPlayerViewSearchFieldPreferences keeps the old overlay test's intent:
// F2/F3 work while the text input owns the keyboard and persist, and Ctrl+C
// still quits instead of being swallowed.
func TestPlayerViewSearchFieldPreferences(t *testing.T) {
	states, out := make(chan playerState, 1), make(chan playerCommand, 1)
	var saved []preferences
	m := NewPlayerModelWithPreferences(context.Background(), states, out, 100, 24, preferences{Language: "en", Theme: "standard"}, func(p preferences) error {
		saved = append(saved, p)
		return nil
	}, "fixture-id", nil)
	m.Init()
	m, _ = m.Update(playerViewKey("/"))
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyF2})
	playerViewText(t, m, "Buscar:", "Backspace borrar")
	if len(saved) != 1 || saved[0].Language != "es" {
		t.Fatalf("a shortcut inside the search field did not persist: %v", saved)
	}
	if len(out) != 0 {
		t.Fatal("a shortcut inside the search field sent a command")
	}
	m, cmd := m.Update(tea.KeyMsg{Type: tea.KeyCtrlC})
	if cmd == nil {
		t.Fatal("Ctrl+C in the search field did not quit")
	}
	if _, ok := playerViewRun(t, cmd).(tea.QuitMsg); !ok {
		t.Fatal("Ctrl+C in the search field did not quit")
	}
}

func TestPlayerViewPlayingBarKeepsAlbum(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	states := make(chan playerState, 1)
	m := NewPlayerModel(ctx, states, nil, 100, 30)
	cmd := m.Init()
	states <- playerState{Connected: true, Track: playerTrack{Title: "Song", Artist: "Artist", Album: "Record", DurationMS: 200000}}
	m, _ = m.Update(playerViewRun(t, cmd))
	playerViewText(t, m, "Record")
}

// playerViewPrefsModel applies one snapshot to a 120x40 workspace.
func playerViewPrefsModel(t *testing.T, p preferences, state playerState) tea.Model {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	states := make(chan playerState, 1)
	states <- state
	m := NewPlayerModelWithPreferences(ctx, states, nil, 120, 40, p, nil, "", nil)
	m, _ = m.Update(playerViewRun(t, m.Init()))
	return m
}

// playerViewPlayingFocus is the same workspace with the playing bar focused.
func playerViewPlayingFocus(t *testing.T, p preferences, state playerState) (tea.Model, chan playerCommand) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	states, out := make(chan playerState, 1), make(chan playerCommand, 4)
	states <- state
	m := NewPlayerModelWithPreferences(ctx, states, out, 120, 40, p, nil, "", nil)
	m, _ = m.Update(playerViewRun(t, m.Init()))
	for range 2 {
		m, _ = m.Update(tea.KeyMsg{Type: tea.KeyTab})
	}
	return m, out
}

// playerViewClockModel applies one snapshot with an injected clock.
func playerViewClockModel(t *testing.T, clock func() time.Time, state playerState) *playerModel {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	states := make(chan playerState, 1)
	states <- state
	model := NewPlayerModel(ctx, states, nil, 100, 30)
	model.(*playerModel).clock = clock
	model, _ = model.Update(playerViewRun(t, model.Init()))
	return model.(*playerModel)
}

func TestPlayerViewPlayingBarSeekKeys(t *testing.T) {
	for _, tt := range []struct {
		name     string
		key      tea.KeyMsg
		position int64
		duration int64
		want     int64
	}{
		{"forward", tea.KeyMsg{Type: tea.KeyRight}, 60000, 200000, 70000},
		{"back", tea.KeyMsg{Type: tea.KeyLeft}, 60000, 200000, 50000},
		{"forward stops at the duration", tea.KeyMsg{Type: tea.KeyRight}, 195000, 200000, 200000},
		{"back stops at the start", tea.KeyMsg{Type: tea.KeyLeft}, 3000, 200000, 0},
		{"unknown duration keeps only the floor", tea.KeyMsg{Type: tea.KeyLeft}, 3000, 0, 0},
	} {
		t.Run(tt.name, func(t *testing.T) {
			m, out := playerViewPlayingFocus(t, preferences{}, playerState{
				Connected: true, SessionID: 4, Status: "paused", PositionMS: tt.position,
				Track: playerTrack{Title: "Song", DurationMS: tt.duration},
			})
			if _, cmd := m.Update(tt.key); cmd != nil {
				t.Fatal("seeking scheduled work")
			}
			select {
			case got := <-out:
				want := playerCommand{Name: "seek", PositionMS: tt.want, SessionID: 4}
				if got != want {
					t.Fatalf("got %+v, want %+v", got, want)
				}
			default:
				t.Fatal("the playing bar did not seek")
			}
			// The bar moves at once and Soloist confirms with position_sync.
			playerViewText(t, m, playerElapsed(tt.want))
		})
	}
	t.Run("disconnected", func(t *testing.T) {
		m, out := playerViewPlayingFocus(t, preferences{}, playerState{PositionMS: 60000, Track: playerTrack{DurationMS: 200000}})
		for _, key := range []tea.KeyMsg{{Type: tea.KeyLeft}, {Type: tea.KeyRight}} {
			if _, cmd := m.Update(key); cmd != nil {
				t.Fatal("a disconnected session scheduled work")
			}
		}
		if len(out) != 0 {
			t.Fatal("a disconnected session accepted a seek")
		}
		playerViewText(t, m, "1:00")
	})
	t.Run("seeks from the interpolated playhead", func(t *testing.T) {
		anchor := time.Unix(1700000000, 0)
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		states, out := make(chan playerState, 1), make(chan playerCommand, 1)
		states <- playerState{
			Connected: true, SessionID: 7, Status: "playing", PositionMS: 60000, Speed: 1, AnchorAt: anchor,
			Track: playerTrack{Title: "Song", DurationMS: 200000},
		}
		m := NewPlayerModelWithPreferences(ctx, states, out, 120, 40, preferences{}, nil, "", nil)
		m.(*playerModel).clock = func() time.Time { return anchor.Add(5 * time.Second) }
		m, _ = m.Update(playerViewRun(t, m.Init()))
		for range 2 {
			m, _ = m.Update(tea.KeyMsg{Type: tea.KeyTab})
		}
		if _, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRight}); cmd != nil {
			t.Fatal("seeking scheduled work")
		}
		select {
		case got := <-out:
			if got != (playerCommand{Name: "seek", PositionMS: 75000, SessionID: 7}) {
				t.Fatalf("the seek ignored the five elapsed seconds: %+v", got)
			}
		default:
			t.Fatal("the playing bar did not seek")
		}
		playerViewText(t, m, "1:15")
	})
}

func TestPlayerViewPlayingBarCyclesModes(t *testing.T) {
	for _, tt := range []struct {
		name    string
		key     string
		shuffle bool
		repeat  string
		want    playerCommand
	}{
		{"shuffle on", "s", false, "", playerCommand{Name: "set_shuffle", Shuffle: true}},
		{"shuffle off", "s", true, "", playerCommand{Name: "set_shuffle", Shuffle: false}},
		{"repeat off to context", "r", false, "", playerCommand{Name: "set_repeat", Repeat: "context"}},
		{"repeat context to track", "r", false, "context", playerCommand{Name: "set_repeat", Repeat: "track"}},
		{"repeat track to off", "r", false, "track", playerCommand{Name: "set_repeat", Repeat: "off"}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			m, out := playerViewPlayingFocus(t, preferences{}, playerState{
				Connected: true, SessionID: 9, Shuffle: tt.shuffle, Repeat: tt.repeat,
			})
			before := ansi.Strip(m.View())
			if _, cmd := m.Update(playerViewKey(tt.key)); cmd != nil {
				t.Fatal("a mode key scheduled work")
			}
			want := tt.want
			want.SessionID = 9
			select {
			case got := <-out:
				if got != want {
					t.Fatalf("got %+v, want %+v", got, want)
				}
			default:
				t.Fatalf("%q sent no command", tt.key)
			}
			// A fresh snapshot request follows, so a lost event cannot stick.
			select {
			case refresh := <-out:
				if refresh.Name != "get_state" {
					t.Fatalf("follow-up = %+v, want get_state", refresh)
				}
			default:
			}
			// The requested mode shows until Soloist reports its own value.
			if after := ansi.Strip(m.View()); after == before {
				t.Fatalf("the requested mode was not reflected:\n%s", after)
			}
		})
	}
}

// TestPlayerViewSeekKeysArePlayingBarOnly keeps the arrows with the playing
// bar: shuffle and repeat moved to every panel, seeking did not.
func TestPlayerViewSeekKeysArePlayingBarOnly(t *testing.T) {
	for _, tt := range []struct {
		name string
		tabs int
	}{
		{"table", 0},
		{"search", 1},
		{"sidebar", 3},
	} {
		t.Run(tt.name, func(t *testing.T) {
			m, out := playerViewModel(t, 120, 40, playerState{
				Connected: true, SessionID: 3, Status: "playing", PositionMS: 60000,
				Track: playerTrack{Title: "Song", DurationMS: 200000},
			})
			for range tt.tabs {
				m, _ = m.Update(tea.KeyMsg{Type: tea.KeyTab})
			}
			for _, key := range []tea.KeyMsg{{Type: tea.KeyLeft}, {Type: tea.KeyRight}} {
				if _, cmd := m.Update(key); cmd != nil {
					t.Fatalf("%q scheduled work in the %s panel", key.String(), tt.name)
				}
			}
			if len(out) != 0 {
				t.Fatalf("the %s panel sent a seek", tt.name)
			}
		})
	}
}

// TestPlayerViewShuffleAndRepeatWorkOutsideTheBar is the S17 discovery fix: s
// and r reach Soloist from the table and the library, and only the focused
// search field keeps them as text.
func TestPlayerViewShuffleAndRepeatWorkOutsideTheBar(t *testing.T) {
	for _, tt := range []struct {
		name string
		tabs int
	}{
		{"table", 0},
		{"playing bar", 2},
		{"sidebar", 3},
	} {
		t.Run(tt.name, func(t *testing.T) {
			for _, tc := range []struct {
				key  string
				want playerCommand
			}{
				{"s", playerCommand{Name: "set_shuffle", Shuffle: true}},
				{"r", playerCommand{Name: "set_repeat", Repeat: "context"}},
			} {
				m, out := playerViewModel(t, 120, 40, playerState{Connected: true, SessionID: 5})
				for range tt.tabs {
					m, _ = m.Update(tea.KeyMsg{Type: tea.KeyTab})
				}
				before := ansi.Strip(m.View())
				if _, cmd := m.Update(playerViewKey(tc.key)); cmd != nil {
					t.Fatal("a mode key scheduled work")
				}
				want := tc.want
				want.SessionID = 5
				select {
				case got := <-out:
					if got != want {
						t.Fatalf("got %+v, want %+v", got, want)
					}
				default:
					t.Fatalf("%q sent no command from the %s", tc.key, tt.name)
				}
				select {
				case refresh := <-out:
					if refresh.Name != "get_state" {
						t.Fatalf("%q follow-up = %+v, want get_state", tc.key, refresh)
					}
				default:
				}
				// The requested mode shows until Soloist reports its own value.
				if after := ansi.Strip(m.View()); after == before {
					t.Fatalf("%q was not reflected in the workspace:\n%s", tc.key, after)
				}
			}
		})
	}
	t.Run("search field keeps letters", func(t *testing.T) {
		m, out := playerViewModel(t, 120, 40, playerState{Connected: true, SessionID: 5})
		m, _ = m.Update(playerViewKey("/"))
		for _, key := range []string{"s", "r"} {
			if _, cmd := m.Update(playerViewKey(key)); cmd != nil {
				t.Fatalf("%q scheduled work while typing", key)
			}
		}
		playerViewText(t, m, "Search: sr_")
		if len(out) != 0 {
			t.Fatal("typing sent a playback command")
		}
	})
}

func TestPlayerViewPlayingBarModes(t *testing.T) {
	for _, tt := range []struct {
		language string
		shuffle  bool
		repeat   string
		want     []string
	}{
		{"en", false, "off", []string{"⇄ off", "↻ off"}},
		{"en", true, "context", []string{"⇄ on", "↻ all"}},
		{"en", false, "track", []string{"⇄ off", "↻ one"}},
		{"es", false, "off", []string{"⇄ apagado", "↻ apagado"}},
		{"es", true, "context", []string{"⇄ activado", "↻ todo"}},
		{"es", false, "track", []string{"⇄ apagado", "↻ una"}},
	} {
		t.Run(tt.language+"-"+tt.repeat, func(t *testing.T) {
			m := playerViewPrefsModel(t, preferences{Language: tt.language}, playerState{
				Connected: true, OptionsKnown: true, Shuffle: tt.shuffle, Repeat: tt.repeat, Volume: 40,
				Track: playerTrack{Title: "Song", DurationMS: 200000},
			})
			playerViewText(t, m, append(tt.want, "Song")...)
		})
	}
}

func TestPlayerViewPlayingBarHints(t *testing.T) {
	m, _ := playerViewPlayingFocus(t, preferences{}, playerState{Connected: true})
	playerViewText(t, m, "Space play/pause", "←→ seek", "s shuffle", "r repeat")
	// The table keeps its own hints and never advertises the bar-only arrows.
	table, _ := playerViewModel(t, 120, 40, playerState{Connected: true})
	view := playerViewText(t, table, "l library", "/ search", "s shuffle")
	if strings.Contains(view, "←→ seek") || strings.Contains(view, "s: search") {
		t.Fatalf("the table advertised the wrong hints:\n%s", view)
	}
}

func TestPlayerViewPlayingBarInterpolation(t *testing.T) {
	anchor := time.Unix(1700000000, 0)
	now := anchor
	clock := func() time.Time { return now }
	state := playerState{
		Connected: true, Status: "playing", PositionMS: 30000, Speed: 1, AnchorAt: anchor,
		Track: playerTrack{Title: "Song", DurationMS: 200000},
	}
	m := playerViewClockModel(t, clock, state)
	if got := m.playhead(); got != 30000 {
		t.Fatalf("playhead %d, want the reported 30000", got)
	}
	playerViewText(t, m, "0:30")
	now = anchor.Add(5 * time.Second)
	if got := m.playhead(); got != 35000 {
		t.Fatalf("playhead %d, want 35000 after five seconds", got)
	}
	playerViewText(t, m, "0:35")
	// The playhead never passes the duration.
	now = anchor.Add(time.Hour)
	if got := m.playhead(); got != 200000 {
		t.Fatalf("playhead %d, want the 200000 duration", got)
	}
	playerViewText(t, m, "3:20")
	// Speed scales the elapsed time.
	now = anchor.Add(5 * time.Second)
	fast := state
	fast.Speed = 2
	if got := playerViewClockModel(t, clock, fast).playhead(); got != 40000 {
		t.Fatalf("playhead %d at double speed, want 40000", got)
	}
	// A paused session reports speed 0 and holds still.
	paused := state
	paused.Status, paused.Speed = "paused", 0
	if got := playerViewClockModel(t, clock, paused).playhead(); got != 30000 {
		t.Fatalf("playhead %d while paused, want 30000", got)
	}
	// Without an anchor the reported position is final.
	unanchored := state
	unanchored.AnchorAt = time.Time{}
	if got := playerViewClockModel(t, clock, unanchored).playhead(); got != 30000 {
		t.Fatalf("playhead %d without an anchor, want 30000", got)
	}
}

func TestPlayerViewPlayingBarTicks(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	states := make(chan playerState, 1)
	states <- playerState{Connected: true, Status: "playing", PositionMS: 1000, Speed: 1, AnchorAt: time.Now(), Track: playerTrack{Title: "Song", DurationMS: 200000}}
	m := NewPlayerModel(ctx, states, nil, 100, 30)
	m, cmd := m.Update(playerViewRun(t, m.Init()))
	msg := playerViewRun(t, cmd)
	batch, ok := msg.(tea.BatchMsg)
	if !ok || len(batch) != 2 {
		t.Fatalf("a playing state scheduled %T with %d commands, want the state read and one tick", msg, len(batch))
	}
	// The playhead tick is the second command of the pair.
	if _, ok := playerViewRun(t, batch[1]).(playerTickMsg); !ok {
		t.Fatal("the scheduled command was not a playhead tick")
	}
	m, again := m.Update(playerTickMsg{})
	if again == nil {
		t.Fatal("the playhead tick did not re-arm while playing")
	}
	// Paused and disconnected sessions stop scheduling it.
	for _, tt := range []struct {
		name      string
		status    string
		connected bool
	}{
		{"paused", "paused", true},
		{"disconnected", "playing", false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			inner, innerCancel := context.WithCancel(context.Background())
			defer innerCancel()
			innerStates := make(chan playerState, 1)
			innerStates <- playerState{Connected: tt.connected, Status: tt.status, AnchorAt: time.Now()}
			model := NewPlayerModel(inner, innerStates, nil, 100, 30)
			model, _ = model.Update(playerViewRun(t, model.Init()))
			if _, next := model.Update(playerTickMsg{}); next != nil {
				t.Fatal("the playhead tick kept scheduling")
			}
		})
	}
	// Quitting and a closed stream stop it too.
	m, quit := m.Update(playerViewKey("q"))
	if _, ok := playerViewRun(t, quit).(tea.QuitMsg); !ok {
		t.Fatal("q did not quit")
	}
	if _, next := m.Update(playerTickMsg{}); next != nil {
		t.Fatal("the playhead tick survived the quit")
	}
	closedCtx, closedCancel := context.WithCancel(context.Background())
	defer closedCancel()
	closed := make(chan playerState, 1)
	closed <- playerState{Connected: true, Status: "playing", AnchorAt: time.Now()}
	closedModel := NewPlayerModel(closedCtx, closed, nil, 100, 30)
	closedModel, closedCmd := closedModel.Update(playerViewRun(t, closedModel.Init()))
	closedBatch, ok := playerViewRun(t, closedCmd).(tea.BatchMsg)
	if !ok || len(closedBatch) != 2 {
		t.Fatalf("a playing state scheduled %d commands", len(closedBatch))
	}
	close(closed)
	closedModel, _ = closedModel.Update(playerViewRun(t, closedBatch[0]))
	if _, next := closedModel.Update(playerTickMsg{}); next != nil {
		t.Fatal("the playhead tick survived a closed stream")
	}
}

// playerViewSidebarState is the last sidebar row, which is where every library
// state lands so the panel is never blank.
func playerViewSidebarState(t *testing.T, m tea.Model) string {
	t.Helper()
	player := m.(*playerModel)
	rows := player.sidebarRows(player.styles(), 30)
	if len(rows) < 2 {
		t.Fatalf("the sidebar rendered no state row: %+v", rows)
	}
	return rows[len(rows)-1].text
}

// playerViewTableState is the first body row when the table is in a state.
func playerViewTableState(t *testing.T, m tea.Model) string {
	t.Helper()
	player := m.(*playerModel)
	rows := player.tableRows(40, player.styles(), 4)
	if len(rows) == 0 {
		t.Fatal("the table rendered no state row")
	}
	return rows[0].text
}

func playerViewSearchState(t *testing.T, m tea.Model) string {
	t.Helper()
	player := m.(*playerModel)
	rows := player.searchTableRows(40, player.styles(), 4)
	if len(rows) == 0 {
		t.Fatal("the search table rendered no state row")
	}
	return rows[0].text
}

// TestPlayerViewPanelStateRows proves each panel renders one localized row per
// concept and that a concept reads the same in every panel.
func TestPlayerViewPanelStateRows(t *testing.T) {
	for _, v := range []struct {
		language                              string
		loading, empty, failure, unauthorized string
		restricted                            string
	}{
		{"en",
			"… Loading",
			"○ Nothing here yet",
			"! Could not load Spotify data. Enter: retry.",
			"● No Spotify. Enter: connect",
			"! Playlist contents unavailable. p: play the playlist."},
		{"es",
			"… Cargando",
			"○ Aún no hay nada",
			"! No se pudieron cargar los datos de Spotify. Enter: reintentar.",
			"● Sin Spotify. Enter: conectar",
			"! Contenido de la lista no disponible. p: reproducir la lista."},
	} {
		t.Run(v.language, func(t *testing.T) {
			p := preferences{Language: v.language}
			// One shared vocabulary: the rows are the same code the panels use.
			vocabulary := NewPlayerModelWithPreferences(context.Background(), nil, nil, 120, 40, p, nil, "", nil).(*playerModel)
			for name, got := range map[string]string{
				"loading":      vocabulary.markLoading(),
				"empty":        vocabulary.markEmpty(),
				"failure":      vocabulary.markError(),
				"unauthorized": vocabulary.markUnauthorized(),
				"restricted":   vocabulary.markRestricted(),
			} {
				want := map[string]string{"loading": v.loading, "empty": v.empty, "failure": v.failure,
					"unauthorized": v.unauthorized, "restricted": v.restricted}[name]
				if got != want {
					t.Fatalf("%s vocabulary = %q, want %q", name, got, want)
				}
			}

			t.Run("loading", func(t *testing.T) {
				m, _, _ := libraryNewPlayerPending(t, 120, 40, p, playerState{Connected: true},
					&libraryFake{playlists: libraryPlaylistsFixture()}, nil, "")
				if got := playerViewSidebarState(t, m); got != v.loading {
					t.Fatalf("sidebar loading row = %q", got)
				}
				if got := playerViewTableState(t, m); got != v.loading {
					t.Fatalf("table loading row = %q", got)
				}
			})

			t.Run("empty", func(t *testing.T) {
				emptyLibrary := &libraryFake{playlists: func(context.Context, int) ([]libraryPlaylist, int, bool, error) {
					return nil, 0, false, nil
				}}
				m, _, _ := libraryNewPlayer(t, 120, 40, p, playerState{Connected: true}, emptyLibrary, nil, "")
				if got := playerViewSidebarState(t, m); got != v.empty {
					t.Fatalf("sidebar empty row = %q", got)
				}
				queue := playerViewPrefsModel(t, p, playerState{Connected: true})
				if got := playerViewTableState(t, queue); got != v.empty {
					t.Fatalf("queue empty row = %q", got)
				}
				source := &libraryFake{playlists: libraryPlaylistsFixture(), search: func(context.Context, string, int) ([]libraryTrack, int, bool, error) {
					return nil, 0, false, nil
				}}
				searched, _ := searchWorkspace(t, p, playerState{Connected: true}, source)
				searched = searchSubmit(t, searched, "tren")
				if got := playerViewSearchState(t, searched); got != v.empty {
					t.Fatalf("search empty row = %q", got)
				}
			})

			t.Run("failure", func(t *testing.T) {
				failing := func(context.Context, string, int) ([]libraryTrack, int, bool, error) {
					return nil, 0, false, errSpotifyCatalog
				}
				failingItems := func(context.Context, string, int) (spotify.PlaylistItemsPage, error) {
					return spotify.PlaylistItemsPage{}, errSpotifyCatalog
				}
				m, _, _ := libraryNewPlayer(t, 120, 40, p, playerState{Connected: true}, &libraryFake{
					playlists: func(context.Context, int) ([]libraryPlaylist, int, bool, error) {
						return nil, 0, false, errSpotifyCatalog
					},
				}, nil, "")
				if got := playerViewSidebarState(t, m); got != v.failure {
					t.Fatalf("sidebar failure row = %q", got)
				}
				tracks, _, _ := libraryNewPlayer(t, 120, 40, p, playerState{Connected: true}, &libraryFake{
					playlists: libraryPlaylistsFixture(), items: failingItems,
				}, nil, "")
				tracks, cmd := libraryOpenFirst(t, tracks)
				tracks = librarySettle(t, tracks, cmd)
				if got := playerViewTableState(t, tracks); got != v.failure {
					t.Fatalf("tracks failure row = %q", got)
				}
				source := &libraryFake{playlists: libraryPlaylistsFixture(), search: failing}
				searched, _ := searchWorkspace(t, p, playerState{Connected: true}, source)
				searched = searchSubmit(t, searched, "tren")
				if got := playerViewSearchState(t, searched); got != v.failure {
					t.Fatalf("search failure row = %q", got)
				}
			})

			t.Run("unauthorized", func(t *testing.T) {
				authorize := func(context.Context, string, func(string)) (CatalogAPI, error) {
					return nil, errors.New("offline fixture")
				}
				m, _, _ := libraryNewPlayer(t, 120, 40, p, playerState{Connected: true}, nil, authorize, "fixture-client")
				if got := playerViewSidebarState(t, m); got != v.unauthorized {
					t.Fatalf("sidebar unauthorized row = %q", got)
				}
				m, _ = m.Update(playerViewKey("/"))
				m, _ = m.Update(playerViewKey("tren"))
				m, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
				if got := playerViewSearchState(t, m); got != v.unauthorized {
					t.Fatalf("search unauthorized row = %q", got)
				}
			})

			t.Run("restricted", func(t *testing.T) {
				source := &libraryFake{playlists: libraryPlaylistsFixture(), items: func(context.Context, string, int) (spotify.PlaylistItemsPage, error) {
					return spotify.PlaylistItemsPage{}, errPlaylistUnavailable
				}}
				m, _, _ := libraryNewPlayer(t, 120, 40, p, playerState{Connected: true}, source, nil, "")
				m, cmd := libraryOpenFirst(t, m)
				m = librarySettle(t, m, cmd)
				if got := playerViewTableState(t, m); got != v.restricted {
					t.Fatalf("restricted row = %q", got)
				}
			})
		})
	}
}

// TestPlayerViewPolishBounds renders the whole workspace at every target size
// with a loaded library, and checks single-width glyphs and no emoji.
func TestPlayerViewPolishBounds(t *testing.T) {
	source := &libraryFake{playlists: libraryPlaylistsFixture(), items: libraryTracksFixture(42)}
	m, _, _ := libraryNewPlayer(t, 120, 40, preferences{}, playerState{
		Connected: true, Status: "playing", Volume: 37, PositionMS: 62000, OptionsKnown: true, Shuffle: true, Repeat: "context",
		Track: playerTrack{Title: "Estrechez de Corazón", Artist: "Los Prisioneros", DurationMS: 381000},
	}, source, nil, "")
	m, cmd := libraryOpenFirst(t, m)
	m = librarySettle(t, m, cmd)
	for _, size := range []struct{ width, height int }{
		{120, 40}, {100, 30}, {80, 24}, {60, 20}, {40, 12}, {20, 8}, {1, 1},
	} {
		t.Run(fmt.Sprintf("%dx%d", size.width, size.height), func(t *testing.T) {
			rows := playerViewRows(t, m, size.width, size.height)
			view := strings.Join(rows, "\n")
			if playerBand(size.width, size.height) != playerBandInsufficient && !strings.Contains(view, "Pulso") {
				t.Fatalf("missing the frame at %dx%d:\n%s", size.width, size.height, view)
			}
			for _, r := range view {
				if r == '\n' {
					continue
				}
				if r >= 0x1F000 {
					t.Fatalf("emoji %q in the chrome at %dx%d", r, size.width, size.height)
				}
				if lipgloss.Width(string(r)) != 1 {
					t.Fatalf("wide glyph %q in the chrome at %dx%d", r, size.width, size.height)
				}
			}
		})
	}
}

// TestPlayerViewNarrowColumns keeps the column rules: the narrowest band that
// still holds a table drops the artist and the long duration label, and 40x12
// has no rows for a header at all.
func TestPlayerViewNarrowColumns(t *testing.T) {
	m, _ := playerViewModel(t, 120, 40, playerState{
		Connected: true, Status: "playing", Volume: 42, PositionMS: 1000,
		Track: playerTrack{Title: "Song", Artist: "Artist", DurationMS: 200000},
		Queue: []playerTrack{{Title: "Queued", Artist: "Queued Artist", DurationMS: 100000}},
	})
	// A 40x12 la banda mínima no tiene filas para el encabezado: el pie es de
	// la caja del reproductor.
	box := strings.Join(playerViewRows(t, m, 40, 12), "\n")
	for _, want := range []string{"Song", "Volume 42%", "├─", "│", "└"} {
		if !strings.Contains(box, want) {
			t.Fatalf("missing %q at 40x12:\n%s", want, box)
		}
	}
	narrow := strings.Join(playerViewRows(t, m, 50, 24), "\n")
	for _, want := range []string{"#", "Title", "Dur.", "Queued"} {
		if !strings.Contains(narrow, want) {
			t.Fatalf("missing %q at 50x24:\n%s", want, narrow)
		}
	}
	// La caja escribe el artista dentro de su texto: la regla de columnas se
	// comprueba en la cabecera de la tabla estrecha.
	header := ""
	for _, row := range strings.Split(narrow, "\n") {
		if strings.Contains(row, "#") && strings.Contains(row, "Title") {
			header = row
			break
		}
	}
	if header == "" {
		t.Fatalf("the narrow table lost its header:\n%s", narrow)
	}
	for _, gone := range []string{"Artist", "Duration"} {
		if strings.Contains(header, gone) {
			t.Fatalf("narrow layout kept %q in the table header:\n%s", gone, narrow)
		}
	}
	// La regla de columnas sigue viva donde se calcula, por si otra banda la usa.
	player := m.(*playerModel)
	short := player.tableColumns(50)
	if len(short) != 3 || short[2].label != "Dur." {
		t.Fatalf("the 50-column table renders %+v, want three columns ending in %q", short, "Dur.")
	}
	// Desde 60 columnas caben las cuatro columnas.
	wide := strings.Join(playerViewRows(t, m, 60, 20), "\n")
	for _, want := range []string{"#", "Title", "Artist", "Duration"} {
		if !strings.Contains(wide, want) {
			t.Fatalf("missing %q at 60x20:\n%s", want, wide)
		}
	}
}

// TestPlayerViewLibrarySwitcher checks the stacked sidebar: one row that names
// the current playlist and how to change it.
func TestPlayerViewLibrarySwitcher(t *testing.T) {
	source := &libraryFake{playlists: libraryPlaylistsFixture()}
	m, _, _ := libraryNewPlayer(t, 120, 40, preferences{}, playerState{Connected: true}, source, nil, "")
	// A 20x8 no queda fila de contenido: manda la caja del reproductor.
	for _, tt := range []struct {
		width, height int
		want          []string
	}{
		{80, 24, []string{"LIBRARY: Rock Chileno", "Tab: library"}},
		{60, 20, []string{"LIBRARY: Rock Chileno", "Tab: library"}},
		{40, 12, []string{"LIBRARY: Rock Chileno", "Tab"}},
		{20, 8, []string{"Minimum 40×12"}},
	} {
		view := strings.Join(playerViewRows(t, m, tt.width, tt.height), "\n")
		for _, want := range tt.want {
			if !strings.Contains(view, want) {
				t.Fatalf("missing %q in the switcher at %dx%d:\n%s", want, tt.width, tt.height, view)
			}
		}
		// La caja dibuja sus propios bordes: el separador prohibido es el de
		// columnas, que quedaría fuera de ella.
		for _, row := range strings.Split(view, "\n") {
			if strings.HasPrefix(row, "├") {
				break
			}
			if strings.Contains(row, "│") {
				t.Fatalf("the stacked layout kept the column rule:\n%s", view)
			}
		}
	}
	// Changing the selection renames the switcher.
	m = libraryFocusSidebar(t, m)
	m, _ = m.Update(playerViewKey("j"))
	view := strings.Join(playerViewRows(t, m, 80, 24), "\n")
	for _, want := range []string{"Tab: library", "LIBRARY: Jazz"} {
		if !strings.Contains(view, want) {
			t.Fatalf("missing %q after moving the selection:\n%s", want, view)
		}
	}
	// The switcher is localized too.
	es, _, _ := libraryNewPlayer(t, 120, 40, preferences{Language: "es"}, playerState{Connected: true}, source, nil, "")
	esView := strings.Join(playerViewRows(t, es, 80, 24), "\n")
	for _, want := range []string{"BIBLIOTECA: Rock Chileno", "Tab: biblioteca"} {
		if !strings.Contains(esView, want) {
			t.Fatalf("missing %q in the Spanish switcher:\n%s", want, esView)
		}
	}
}

// TestPlayerViewHintsFollowFocus keeps the hints honest: they change with the
// focused panel and never advertise a key that does nothing there.
func TestPlayerViewHintsFollowFocus(t *testing.T) {
	for _, size := range []struct{ width, height int }{{120, 40}, {70, 20}, {40, 12}} {
		t.Run(fmt.Sprintf("%dx%d", size.width, size.height), func(t *testing.T) {
			m, _ := playerViewModel(t, size.width, size.height, playerState{
				Connected: true, Status: "paused", Track: playerTrack{Title: "Song"},
			})
			hints := func() string {
				player := m.(*playerModel)
				return strings.Join(player.hintRows(player.width), "\n")
			}
			table := hints()
			for _, want := range []string{"↑↓/jk move", "/ search", "Enter play", "Space play/pause", "s shuffle", "r repeat", "l library"} {
				if !strings.Contains(table, want) {
					t.Fatalf("table hints miss %q:\n%s", want, table)
				}
			}
			for _, gone := range []string{"←→ seek", "Enter open", "n/p"} {
				if strings.Contains(table, gone) {
					t.Fatalf("table hints advertise %q:\n%s", gone, table)
				}
			}
			m, _ = m.Update(tea.KeyMsg{Type: tea.KeyTab})
			search := hints()
			for _, want := range []string{"Enter search", "Esc leave", "Backspace delete", "F1 help"} {
				if !strings.Contains(search, want) {
					t.Fatalf("search hints miss %q:\n%s", want, search)
				}
			}
			for _, gone := range []string{"q quit", "↑↓", "Space", "s shuffle"} {
				if strings.Contains(search, gone) {
					t.Fatalf("search hints advertise %q:\n%s", gone, search)
				}
			}
			m, _ = m.Update(tea.KeyMsg{Type: tea.KeyTab})
			playing := hints()
			for _, want := range []string{"Space play/pause", "←→ seek", "n/p track", "s shuffle", "r repeat", "+/- volume", "l library"} {
				if !strings.Contains(playing, want) {
					t.Fatalf("playing-bar hints miss %q:\n%s", want, playing)
				}
			}
			for _, gone := range []string{"↑↓/jk", "Enter", "Backspace"} {
				if strings.Contains(playing, gone) {
					t.Fatalf("playing-bar hints advertise %q:\n%s", gone, playing)
				}
			}
			m, _ = m.Update(tea.KeyMsg{Type: tea.KeyTab})
			sidebar := hints()
			for _, want := range []string{"↑↓/jk library", "/ search", "Enter open", "s shuffle"} {
				if !strings.Contains(sidebar, want) {
					t.Fatalf("sidebar hints miss %q:\n%s", want, sidebar)
				}
			}
			for _, gone := range []string{"s: shuffle", "Enter: play", "←→", "l library"} {
				if strings.Contains(sidebar, gone) {
					t.Fatalf("sidebar hints advertise %q:\n%s", gone, sidebar)
				}
			}
		})
	}
}

// TestPlayerViewSearchReachableAtEverySize keeps `/` working from every panel
// and proves the field is rendered wherever a second row fits.
func TestPlayerViewSearchReachableAtEverySize(t *testing.T) {
	for _, size := range []struct{ width, height int }{
		{120, 40}, {100, 30}, {80, 24}, {60, 20}, {40, 12}, {20, 8}, {1, 1},
	} {
		t.Run(fmt.Sprintf("%dx%d", size.width, size.height), func(t *testing.T) {
			m, _ := playerViewModel(t, size.width, size.height, playerState{Connected: true})
			for panel := 0; panel < int(playerFocusCount); panel++ {
				for range panel {
					m, _ = m.Update(tea.KeyMsg{Type: tea.KeyTab})
				}
				m, cmd := m.Update(playerViewKey("/"))
				if cmd != nil {
					t.Fatalf("/ from panel %d scheduled work", panel)
				}
				if got := m.(*playerModel).focus; got != playerFocusSearch {
					t.Fatalf("/ from panel %d focused %v", panel, got)
				}
				playerViewRows(t, m, size.width, size.height)
				if size.height >= 2 && playerBand(size.width, size.height) != playerBandInsufficient {
					playerViewText(t, m, "Search:")
				}
				// Esc blurs the empty field, so the next panel starts from the table.
				m, _ = m.Update(tea.KeyMsg{Type: tea.KeyEsc})
			}
		})
	}
}

// TestPlayerViewAccentContrast keeps the accent surface readable: it uses the
// accent text colour, and muted text never lands on it.
func TestPlayerViewAccentContrast(t *testing.T) {
	for _, theme := range []string{"standard", "dark", "light"} {
		t.Run(theme, func(t *testing.T) {
			p := preferences{Language: "en", Theme: theme}
			model := NewPlayerModelWithPreferences(context.Background(), nil, nil, 120, 40, p, nil, "", nil)
			styles := model.(*playerModel).styles()
			palette := p.Palette()
			if styles.banner.GetForeground() != palette.AccentText || styles.banner.GetBackground() != palette.Accent {
				t.Fatalf("the banner does not pair accent text with the accent surface: %+v", styles.banner)
			}
			if styles.banner.GetFaint() {
				t.Fatal("the banner is dim on the accent surface")
			}
			for name, style := range map[string]lipgloss.Style{
				"muted": styles.muted, "label": styles.label, "accent": styles.accent, "failure": styles.failure,
			} {
				if _, plain := style.GetBackground().(lipgloss.NoColor); !plain {
					t.Fatalf("the %s style paints a background: %+v", name, style)
				}
			}
		})
	}
}

// TestPlayerViewUnauthorizedSearchEnterStartsAuth keeps the advertised key
// honest: Enter on the unauthorized row starts the same flow as the sidebar.
func TestPlayerViewUnauthorizedSearchEnterStartsAuth(t *testing.T) {
	requested := make(chan string, 1)
	authorize := func(_ context.Context, clientID string, _ func(string)) (CatalogAPI, error) {
		requested <- clientID
		return nil, errors.New("offline fixture")
	}
	m, _, _ := libraryNewPlayer(t, 120, 40, preferences{}, playerState{Connected: true}, nil, authorize, "fixture-client")
	m, _ = m.Update(playerViewKey("/"))
	m, _ = m.Update(playerViewKey("tren"))
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if got := m.(*playerModel).focus; got != playerFocusTable {
		t.Fatalf("submitting a search focused %v", got)
	}
	m, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("Enter on the unauthorized row did not start authorization")
	}
	msg := playerViewRun(t, cmd)
	select {
	case got := <-requested:
		if got != "fixture-client" {
			t.Fatalf("authorized with %q", got)
		}
	default:
		t.Fatal("the authorize seam was never called")
	}
	if m, _ = m.Update(msg); m.View() == "" {
		t.Fatal("a failed authorization blanked the workspace")
	}
}

// playerViewFooterPairs parses the footer back into its advertised bindings.
// Every row must use sqlterm's flat shape: one leading and one trailing space,
// `Key label` pairs, and two spaces between pairs.
func playerViewFooterPairs(t *testing.T, m *playerModel, width int) map[string]string {
	t.Helper()
	pairs := map[string]string{}
	for _, line := range m.hintRows(width) {
		if !strings.HasPrefix(line, " ") || !strings.HasSuffix(line, " ") ||
			strings.HasPrefix(line, "  ") || strings.HasSuffix(line, "  ") {
			t.Fatalf("footer row %q does not use one leading and one trailing space", line)
		}
		if strings.Contains(line, " | ") || strings.Contains(line, "   ") {
			t.Fatalf("footer row %q does not use two spaces between pairs", line)
		}
		for _, item := range strings.Split(strings.TrimSpace(line), "  ") {
			key, label, ok := strings.Cut(item, " ")
			if !ok || key == "" || label == "" || strings.HasPrefix(label, " ") {
				t.Fatalf("footer pair %q is not `Key label`", item)
			}
			pairs[key] = label
		}
	}
	return pairs
}

// TestPlayerViewFooterPairFormat adopts sqlterm's footer: one flat row of
// `Key label` pairs built from the focused panel's own keys, wrapping to more
// rows of the same shape instead of falling back to short labels.
func TestPlayerViewFooterPairFormat(t *testing.T) {
	for _, size := range []struct {
		width, height int
		footer        string
	}{{120, 40, "full"}, {100, 30, "full"}, {80, 24, "full"}, {40, 12, "absent"}} {
		t.Run(fmt.Sprintf("%dx%d", size.width, size.height), func(t *testing.T) {
			m, _ := playerViewModel(t, size.width, size.height, playerState{Connected: true, Status: "paused"})
			player := m.(*playerModel)
			if len(player.hintRows(size.width)) == 0 {
				t.Fatal("the workspace rendered no footer")
			}

			table := playerViewFooterPairs(t, player, size.width)
			for _, key := range []string{"Tab", "↑↓/jk", "Enter", "Space", "s", "r", "+/-", "/", "l", "F1", "F2", "F3", "q", "Esc"} {
				if _, ok := table[key]; !ok {
					t.Fatalf("the table footer does not advertise %q: %v", key, table)
				}
			}
			for _, gone := range []string{"←→", "n/p"} {
				if _, ok := table[gone]; ok {
					t.Fatalf("the table footer advertised the bar-only %q", gone)
				}
			}
			if table["Tab"] != "focus" || table["F1"] != "help" || table["Esc"] != "back/quit" {
				t.Fatalf("unexpected locale or labels: %v", table)
			}
			if size.width < playerNarrowWidth && len(player.hintRows(size.width)) < 2 {
				t.Fatalf("the %d-column footer did not wrap", size.width)
			}

			m, _ = m.Update(tea.KeyMsg{Type: tea.KeyTab})
			search := playerViewFooterPairs(t, m.(*playerModel), size.width)
			for _, key := range []string{"Enter", "Esc", "Backspace", "F1"} {
				if _, ok := search[key]; !ok {
					t.Fatalf("the search footer does not advertise %q: %v", key, search)
				}
			}
			for _, gone := range []string{"Space", "q", "↑↓/jk", "s"} {
				if _, ok := search[gone]; ok {
					t.Fatalf("the search footer advertised %q", gone)
				}
			}

			m, _ = m.Update(tea.KeyMsg{Type: tea.KeyTab})
			playing := playerViewFooterPairs(t, m.(*playerModel), size.width)
			for _, key := range []string{"Space", "←→", "n/p", "s", "r", "+/-", "/", "l"} {
				if _, ok := playing[key]; !ok {
					t.Fatalf("the playing-bar footer does not advertise %q: %v", key, playing)
				}
			}
			if _, ok := playing["Enter"]; ok {
				t.Fatal("the playing bar advertised Enter")
			}

			m, _ = m.Update(tea.KeyMsg{Type: tea.KeyTab})
			sidebar := playerViewFooterPairs(t, m.(*playerModel), size.width)
			for _, key := range []string{"↑↓/jk", "Enter", "Space", "s", "r"} {
				if _, ok := sidebar[key]; !ok {
					t.Fatalf("the sidebar footer does not advertise %q: %v", key, sidebar)
				}
			}
			for _, gone := range []string{"←→", "l"} {
				if _, ok := sidebar[gone]; ok {
					t.Fatalf("the sidebar footer advertised %q", gone)
				}
			}

			// El pie cede sus filas al contenido: a 40x12 no queda ninguna fila de
			// pie y la caja conserva sus dos bordes.
			view := strings.Join(playerViewRows(t, m, size.width, size.height), "\n")
			if size.footer == "absent" {
				for _, want := range []string{"├─"} {
					if !strings.Contains(view, want) {
						t.Fatalf("missing %q in the %dx%d frame:\n%s", want, size.width, size.height, view)
					}
				}
				bottom := "└" + strings.Repeat("─", size.width-2) + "┘"
				if !strings.Contains(view, bottom) {
					t.Fatalf("the %dx%d box lost its bottom border:\n%s", size.width, size.height, view)
				}
				for _, gone := range []string{"Tab focus", "F1 help", "q quit"} {
					if strings.Contains(view, gone) {
						t.Fatalf("the %dx%d frame kept the footer pair %q:\n%s", size.width, size.height, gone, view)
					}
				}
				return
			}
			for _, want := range []string{"Tab focus", "F1 help", "q quit"} {
				if !strings.Contains(view, want) {
					t.Fatalf("missing %q in the %dx%d frame:\n%s", want, size.width, size.height, view)
				}
			}
		})
	}
}

// TestPlayerViewFooterFitsEverySize renders the workspace with every panel
// focused at each supported size; the shared rows helper fails on overflow.
func TestPlayerViewFooterFitsEverySize(t *testing.T) {
	for _, size := range []struct{ width, height int }{
		{120, 40}, {100, 30}, {80, 24}, {60, 20}, {40, 12}, {20, 8}, {12, 5}, {1, 1},
	} {
		t.Run(fmt.Sprintf("%dx%d", size.width, size.height), func(t *testing.T) {
			m, _ := playerViewModel(t, size.width, size.height, playerState{
				Connected: true, Status: "playing", Volume: 37, PositionMS: 62000,
				Track: playerTrack{Title: "Song", Artist: "Artist", DurationMS: 200000},
			})
			for panel := 0; panel < int(playerFocusCount); panel++ {
				playerViewRows(t, m, size.width, size.height)
				m, _ = m.Update(tea.KeyMsg{Type: tea.KeyTab})
			}
		})
	}
}

// playerProfileFake sirve el nombre de la cuenta y cuenta sus peticiones, para
// que una prueba pueda demostrar que la barra superior pregunta una sola vez.
type playerProfileFake struct {
	mu    sync.Mutex
	calls int
	name  string
	err   error
}

func (f *playerProfileFake) Profile(context.Context) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	return f.name, f.err
}

func (f *playerProfileFake) requests() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}

func (f *playerProfileFake) Search(context.Context, string, int) ([]catalogItem, bool, error) {
	return nil, false, nil
}

func (f *playerProfileFake) Playlists(context.Context, int) ([]catalogItem, bool, error) {
	return nil, false, nil
}

func (f *playerProfileFake) PlaylistTracks(context.Context, string, int) ([]catalogItem, bool, error) {
	return nil, false, nil
}

// playerProfilePlayer builds the workspace with the catalog that answers the
// account name, leaving the startup requests unrun.
func playerProfilePlayer(t *testing.T, api CatalogAPI, authorize func(context.Context, string, func(string)) (CatalogAPI, error), state playerState) (tea.Model, chan playerState) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	states := make(chan playerState, 4)
	states <- state
	return NewPlayerModelWithCatalog(ctx, states, nil, 120, 40, preferences{}, nil, api, authorize, "fixture-client"), states
}

func TestPlayerViewProfileName(t *testing.T) {
	api := &playerProfileFake{name: "  Ana\tPérez  "}
	m, _ := playerProfilePlayer(t, api, nil, playerState{Connected: true, SessionID: 3})
	// El marcador sigue visible hasta que llega la respuesta.
	playerViewText(t, m, "Search:", "@user")
	m = librarySettle(t, m, m.Init())
	view := playerViewText(t, m, "@Ana Pérez")
	if strings.Contains(view, "@user") {
		t.Fatalf("the placeholder survived the account name:\n%s", view)
	}
	if got := api.requests(); got != 1 {
		t.Fatalf("profile requests = %d, want one", got)
	}
}

func TestPlayerViewProfileEmptyName(t *testing.T) {
	api := &playerProfileFake{}
	m, _ := playerProfilePlayer(t, api, nil, playerState{Connected: true})
	m = librarySettle(t, m, m.Init())
	// Una cuenta sin nombre publicado no deja una etiqueta vacía ni el marcador.
	view := playerViewText(t, m, "Search:")
	if strings.Contains(view, "@") {
		t.Fatalf("an empty account name left a label:\n%s", view)
	}
	if got := api.requests(); got != 1 {
		t.Fatalf("profile requests = %d, want one", got)
	}
}

func TestPlayerViewProfileTruncatedToBar(t *testing.T) {
	long := strings.Repeat("界", 80)
	api := &playerProfileFake{name: long}
	m, _ := playerProfilePlayer(t, api, nil, playerState{Connected: true})
	m = librarySettle(t, m, m.Init())
	for _, size := range []struct{ width, height int }{
		{120, 40}, {100, 30}, {60, 20}, {40, 12}, {20, 8}, {1, 1},
	} {
		t.Run(fmt.Sprintf("%dx%d", size.width, size.height), func(t *testing.T) {
			// playerViewRows falla si una fila desborda el ancho.
			view := strings.Join(playerViewRows(t, m, size.width, size.height), "\n")
			if strings.Contains(view, long) {
				t.Fatalf("the whole name fit a %d-column bar", size.width)
			}
			if size.width >= 40 && !strings.Contains(view, "@界界") {
				t.Fatalf("the truncated name is missing:\n%s", view)
			}
		})
	}
}

func TestPlayerViewProfileFailureKeepsPlaceholder(t *testing.T) {
	api := &playerProfileFake{err: errors.New("fixture profile failure")}
	m, _ := playerProfilePlayer(t, api, nil, playerState{Connected: true, Volume: 40})
	m = librarySettle(t, m, m.Init())
	view := playerViewText(t, m, "@user")
	if strings.Contains(view, "fixture profile failure") {
		t.Fatalf("raw failure text reached the view:\n%s", view)
	}
	// La barra sigue respondiendo: ninguna tecla repite la petición.
	for _, key := range []tea.KeyMsg{playerViewKey("j"), playerViewKey("k"), {Type: tea.KeyTab}, playerViewKey("/"), playerViewKey("a")} {
		var cmd tea.Cmd
		m, cmd = m.Update(key)
		if cmd != nil {
			t.Fatalf("%q scheduled work after a failed profile read", key.String())
		}
	}
	// Un evento de sesión tampoco la repite: solo una sesión nueva puede.
	// Un modelo ya asentado tiene armada la siguiente lectura de estado, así que
	// el evento entra directamente.
	m, cmd := m.Update(playerViewState{state: playerState{Connected: true, SessionID: 2, Volume: 40}, ok: true})
	if cmd == nil {
		t.Fatal("the session event did not schedule the next state read")
	}
	if api.requests() != 1 {
		t.Fatalf("a session event repeated the profile read: %d requests", api.requests())
	}
	playerViewText(t, m, "@user", "Volume 40%")
}

func TestPlayerViewProfileFetchesOncePerSession(t *testing.T) {
	api := &playerProfileFake{name: "ana"}
	m, _ := playerProfilePlayer(t, api, nil, playerState{Connected: true, SessionID: 1})
	m = librarySettle(t, m, m.Init())
	if got := api.requests(); got != 1 {
		t.Fatalf("profile requests = %d, want one", got)
	}
	m, _ = m.Update(playerViewState{state: playerState{Connected: true, SessionID: 2}, ok: true})
	m, _ = m.Update(playerViewKey("j"))
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyTab})
	if got := api.requests(); got != 1 {
		t.Fatalf("profile requests = %d after events and keys, want one", got)
	}
	playerViewText(t, m, "@ana")
}

func TestPlayerViewProfileAfterAuthorization(t *testing.T) {
	const url = "https://accounts.spotify.com/authorize?client_id=fixture-client&response_type=code"
	api := &playerProfileFake{name: "beto"}
	authorize := func(_ context.Context, _ string, showURL func(string)) (CatalogAPI, error) {
		showURL(url)
		return api, nil
	}
	m, _ := playerProfilePlayer(t, nil, authorize, playerState{Connected: true, SessionID: 2})
	m = librarySettle(t, m, m.Init())
	// Sin catálogo autorizado no hay petición de perfil.
	if got := api.requests(); got != 0 {
		t.Fatalf("profile requests = %d without a catalog, want none", got)
	}
	playerViewText(t, m, "@user")
	// La autorización instala el catálogo y pide el nombre una sola vez.
	m = libraryFocusSidebar(t, m)
	m, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = librarySettle(t, m, cmd)
	playerViewText(t, m, "@beto")
	if got := api.requests(); got != 1 {
		t.Fatalf("profile requests = %d after authorization, want one", got)
	}
}

func TestPlayerViewDeviceSegment(t *testing.T) {
	base := playerState{
		Connected: true, Status: "playing", Volume: 37, PositionMS: 62000,
		Track: playerTrack{Title: "Song", Album: "Album", DurationMS: 200000},
	}
	for _, tt := range []struct {
		name   string
		device string
		shown  bool
	}{
		{"local device", "Pulso", false},
		{"local device case", "pULSO", false},
		{"local device padded", "\tPulso  ", false},
		{"another device", "Desk", true},
		{"no device", "", false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			state := base
			state.Device = tt.device
			m, _ := playerViewModel(t, 120, 40, state)
			view := strings.Join(playerViewRows(t, m, 120, 40), "\n")
			if strings.Contains(view, "Device ") != tt.shown {
				t.Fatalf("device segment shown=%v in view:\n%s", !tt.shown, view)
			}
			// El volumen se conserva y el álbum recibe el espacio liberado.
			playerViewText(t, m, "Volume 37%", "Album")
		})
	}
}

func TestPlayerViewDeviceLocalized(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	states := make(chan playerState, 1)
	states <- playerState{Connected: true, Status: "paused", Volume: 37, Device: "Desk"}
	m := NewPlayerModelWithPreferences(ctx, states, nil, 120, 40, preferences{Language: "es"}, nil, "", nil)
	m, _ = m.Update(playerViewRun(t, m.Init()))
	playerViewText(t, m, "Dispositivo Desk", "Volumen 37%")
}

// TestPlayerBoxMarksDisconnectedOverStaleTrack fija que la caja no muestre la
// pista guardada cuando la sesión se desconecta: el estado manda sobre el
// título viejo, y con una pista obsoleta tampoco se pinta el bloque de iniciales.
func TestPlayerBoxMarksDisconnectedOverStaleTrack(t *testing.T) {
	m, _ := playerViewModel(t, 120, 40, playerState{
		Connected: false,
		Track: playerTrack{
			Title: "Estrechez de Corazón", Artist: "Los Prisioneros",
			Album: "La Cultura de la Basura", DurationMS: 381000,
		},
	})
	view := strings.Join(playerViewRows(t, m, 120, 40), "\n")
	for _, gone := range []string{"Estrechez de Corazón", "Los Prisioneros", "La Cultura de la Basura"} {
		if strings.Contains(view, gone) {
			t.Fatalf("disconnected box kept the stale track %q:\n%s", gone, view)
		}
	}
	playerViewText(t, m, "Disconnected")
}

// TestPlayerBoxFitsShortWideViewport fija que una ventana ancha y baja conserve
// la caja completa: la altura interior cede antes de que el borde inferior se
// pierda.
func TestPlayerBoxFitsShortWideViewport(t *testing.T) {
	m, _ := playerViewModel(t, 120, 40, playerState{
		Connected: true, Status: "playing", Volume: 85, PositionMS: 62000,
		Track: playerTrack{Title: "Estrechez de Corazón", Artist: "Los Prisioneros", DurationMS: 381000},
	})
	// La matriz reserva los altos menores que 12 para el mensaje de tamaño, así
	// que la caja se comprueba en el alto mínimo que aún la dibuja.
	for _, size := range []struct{ width, height int }{{120, 14}, {120, 12}, {60, 12}} {
		rows := playerViewRows(t, m, size.width, size.height)
		view := strings.Join(rows, "\n")
		for _, want := range []string{"├─", "Estrechez de Corazón"} {
			if !strings.Contains(view, want) {
				t.Fatalf("missing %q at %dx%d:\n%s", want, size.width, size.height, view)
			}
		}
		// El bloque de iniciales también dibuja un `└`: el borde que importa es
		// el de la caja completa.
		bottom := "└" + strings.Repeat("─", size.width-2) + "┘"
		if !strings.Contains(view, bottom) {
			t.Fatalf("the %dx%d box lost its bottom border:\n%s", size.width, size.height, view)
		}
	}
}

// playerZoneModel builds the workspace with the first playlist open: that is
// the state that names the table box after a playlist and leaves the queue with
// its own entries.
func playerZoneModel(t *testing.T, language string) tea.Model {
	t.Helper()
	source := &libraryFake{playlists: libraryPlaylistsFixture(), items: libraryTracksFixture(42)}
	m, _, _ := libraryNewPlayer(t, 120, 40, preferences{Language: language}, playerState{
		Connected: true, Status: "playing", Volume: 37, OptionsKnown: true,
		Shuffle: true, Repeat: "context", PlaylistContext: "Rock Chileno",
		Track: playerTrack{Title: "Estrechez de Corazón", Artist: "Los Prisioneros", DurationMS: 381000},
		Queue: []playerTrack{{Title: "Huelga de Dolores", Artist: "Los Miserables", DurationMS: 258000}},
	}, source, nil, "")
	m, cmd := libraryOpenFirst(t, m)
	return librarySettle(t, m, cmd)
}

// playerViewZoneCells cuts one zone out of a rendered row, so a rule about a
// zone cannot be satisfied by a neighbour's text. The view is single-width, so
// one rune is one cell.
func playerViewZoneCells(row string, start, width int) string {
	cells := []rune(row)
	if start < 0 || width <= 0 || start+width > len(cells) {
		return ""
	}
	return string(cells[start : start+width])
}

// playerViewBandHasTable names the bands whose composition still draws the
// table: the three above the minimum one.
func playerViewBandHasTable(band int) bool {
	return band == playerBandWide || band == playerBandMedium || band == playerBandCompact
}

// TestPlayerViewZoneBands fixes the size matrix: the boxes each band draws, the
// table header it hides and the exact width every row measures.
func TestPlayerViewZoneBands(t *testing.T) {
	m := playerZoneModel(t, "en")
	for _, band := range []struct {
		name          string
		width, height int
		want, absent  []string
	}{
		{"wide 140x40", 140, 40,
			[]string{"LIBRARY", "Rock Chileno · 42 tracks", "UP NEXT", "CONTEXT", "Huelga de Dolores", "Los Miserables"},
			nil},
		{"wide 120x34", 120, 34,
			[]string{"LIBRARY", "UP NEXT", "CONTEXT", "#", "Title"},
			nil},
		{"medium 100x30", 100, 30,
			[]string{"LIBRARY", "Rock Chileno · 42 tracks", "#", "Title", "Tren al Sur"},
			[]string{"UP NEXT", "CONTEXT"}},
		{"compact 80x24", 80, 24,
			[]string{"LIBRARY:", "#", "Title", "Tren al Sur"},
			[]string{"UP NEXT", "CONTEXT"}},
		{"minimum 79x23", 79, 23,
			[]string{"LIBRARY:", "Rock Chileno", "#", "Title", "Tren al Sur"},
			[]string{"UP NEXT", "CONTEXT"}},
		{"minimum 40x12", 40, 12,
			[]string{"Pulso", "LIBRARY:", "├─"},
			[]string{"#", "Title", "UP NEXT"}},
		{"insufficient 39x11", 39, 11,
			[]string{"40×12", "39×11"},
			[]string{"Pulso", "LIBRARY", "┌", "│", "└"}},
	} {
		t.Run(band.name, func(t *testing.T) {
			rows := playerViewRows(t, m, band.width, band.height)
			view := strings.Join(rows, "\n")
			for _, want := range band.want {
				if !strings.Contains(view, want) {
					t.Errorf("missing %q in %dx%d:\n%s", want, band.width, band.height, view)
				}
			}
			for _, gone := range band.absent {
				if strings.Contains(view, gone) {
					t.Errorf("unexpected %q in %dx%d:\n%s", gone, band.width, band.height, view)
				}
			}
			for i, row := range rows {
				if got := lipgloss.Width(row); got != band.width {
					t.Fatalf("row %d of %dx%d measures %d cells: %q", i, band.width, band.height, got, row)
				}
			}
		})
	}
}

// TestPlayerViewZoneBandsLocalized repeats the zone titles in Spanish: every
// visible string travels through m.text.
func TestPlayerViewZoneBandsLocalized(t *testing.T) {
	m := playerZoneModel(t, "es")
	wide := strings.Join(playerViewRows(t, m, 120, 34), "\n")
	for _, want := range []string{"BIBLIOTECA", "A CONTINUACIÓN", "CONTEXTO", "Rock Chileno", "Título"} {
		if !strings.Contains(wide, want) {
			t.Fatalf("missing %q in the Spanish wide band:\n%s", want, wide)
		}
	}
	minimum := strings.Join(playerViewRows(t, m, 79, 23), "\n")
	for _, want := range []string{"BIBLIOTECA: Rock Chileno", "Tab: biblioteca"} {
		if !strings.Contains(minimum, want) {
			t.Fatalf("missing %q in the Spanish minimum band:\n%s", want, minimum)
		}
	}
	insufficient := strings.Join(playerViewRows(t, m, 39, 11), "\n")
	for _, want := range []string{"Mínimo 40×12", "ahora 39×11"} {
		if !strings.Contains(insufficient, want) {
			t.Fatalf("missing %q in the Spanish size message:\n%s", want, insufficient)
		}
	}
}

// TestPlayerViewQueueKeepsArtistBelowTitle holds the queue box shape: each
// entry writes its title and, on the next row, its artist.
func TestPlayerViewQueueKeepsArtistBelowTitle(t *testing.T) {
	m := playerZoneModel(t, "en")
	const width, height, queueWidth = 120, 40, 30
	rows := playerViewRows(t, m, width, height)
	start := width - queueWidth
	found := false
	for i := 0; i+1 < len(rows); i++ {
		cells := playerViewZoneCells(rows[i], start, queueWidth)
		if !strings.Contains(cells, "Huelga de Dolores") {
			continue
		}
		found = true
		if strings.Contains(cells, "Los Miserables") {
			t.Fatalf("the queue wrote the artist beside the title:\n%s", strings.Join(rows, "\n"))
		}
		if next := playerViewZoneCells(rows[i+1], start, queueWidth); !strings.Contains(next, "Los Miserables") {
			t.Fatalf("the queue did not write the artist below the title:\n%s", strings.Join(rows, "\n"))
		}
	}
	if !found {
		t.Fatalf("the queue box did not list the playback queue:\n%s", strings.Join(rows, "\n"))
	}
}

// TestPlayerViewZoneFocusAccentsItsBorder keeps the accent honest: the zone
// that holds the focus draws an accented border and every other zone stays
// muted. Los colores se comprueban en el estilo, porque la prueba los dibuja sin
// terminal.
func TestPlayerViewZoneFocusAccentsItsBorder(t *testing.T) {
	opened := playerZoneModel(t, "en").(*playerModel)
	opened.focus = playerFocusTable
	styles := opened.styles()
	if got := opened.zoneEdge(playerFocusTable, styles); got.GetForeground() != styles.accent.GetForeground() || !got.GetBold() {
		t.Fatalf("the focused table box is not accented: %+v", got)
	}
	if got := opened.zoneEdge(playerFocusSidebar, styles); got.GetForeground() != styles.muted.GetForeground() {
		t.Fatalf("the unfocused library box is accented: %+v", got)
	}
	// Con una lista abierta la cola no es el contenido de la tabla: va apagada.
	if got := opened.queueEdge(styles); got.GetForeground() != styles.muted.GetForeground() {
		t.Fatalf("the queue zone is accented while the table shows a playlist: %+v", got)
	}
	// La biblioteca lleva el acento cuando el foco la alcanza.
	opened.focus = playerFocusSidebar
	if got := opened.zoneEdge(playerFocusSidebar, styles); got.GetForeground() != styles.accent.GetForeground() {
		t.Fatalf("the focused library box is not accented: %+v", got)
	}
	// Sin lista ni búsqueda, la tabla muestra la cola y su zona tiene el foco.
	queue, _ := playerViewModel(t, 120, 34, playerState{Connected: true})
	if got := queue.(*playerModel).queueEdge(styles); got.GetForeground() != styles.accent.GetForeground() {
		t.Fatalf("the queue zone is not accented while the table shows the queue: %+v", got)
	}
}

// TestPlayerViewZonesShareOneHeight proves the boxed bands draw three boxes of
// one height and leave the player box its own rows.
func TestPlayerViewZonesShareOneHeight(t *testing.T) {
	m := playerZoneModel(t, "en")
	for _, size := range []struct{ width, height int }{{140, 40}, {120, 34}} {
		rows := playerViewRows(t, m, size.width, size.height)
		tops, bottoms := -1, -1
		for i, row := range rows {
			switch {
			case strings.Count(row, "┌─ ") == 3:
				if tops >= 0 {
					t.Fatalf("%dx%d drew two rows of zone tops", size.width, size.height)
				}
				tops = i
			case strings.Count(row, "└") >= 3:
				if bottoms >= 0 {
					t.Fatalf("%dx%d drew two rows of zone bottoms", size.width, size.height)
				}
				bottoms = i
			}
		}
		if tops < 0 || bottoms <= tops {
			t.Fatalf("%dx%d has no three-zone body:\n%s", size.width, size.height, strings.Join(rows, "\n"))
		}
		if bottoms-tops+1 < 5 {
			t.Fatalf("%dx%d drew %d zone rows, want at least five", size.width, size.height, bottoms-tops+1)
		}
		bottom := "└" + strings.Repeat("─", size.width-2) + "┘"
		if !strings.Contains(strings.Join(rows, "\n"), bottom) {
			t.Fatalf("%dx%d lost the player box bottom border", size.width, size.height)
		}
	}
}

// TestPlayerViewMediumLibraryFitsNames keeps Rule 1: the medium library box is
// 24 columns, so a playlist name with its cursor still fits its interior.
func TestPlayerViewMediumLibraryFitsNames(t *testing.T) {
	source := &libraryFake{playlists: func(context.Context, int) ([]libraryPlaylist, int, bool, error) {
		return []libraryPlaylist{{Name: "Rock Chileno Clasico", Owner: "owner", URI: "spotify:playlist:rock", Total: 1}}, 1, false, nil
	}}
	m, _, _ := libraryNewPlayer(t, 100, 30, preferences{}, playerState{Connected: true}, source, nil, "")
	rows := playerViewRows(t, m, 100, 30)
	view := strings.Join(rows, "\n")
	if !strings.Contains(view, "Rock Chileno Clasico") {
		t.Fatalf("the medium library box cut the playlist name:\n%s", view)
	}
	top := ""
	for _, row := range rows {
		if strings.Contains(row, "LIBRARY") && strings.Contains(row, "┌") {
			top = row
			break
		}
	}
	if top == "" {
		t.Fatalf("the medium band has no library box:\n%s", view)
	}
	cells := []rune(top)
	if len(cells) < playerLibraryMid || string(cells[playerLibraryMid-1]) != "┐" {
		t.Fatalf("the medium library box is not %d columns wide: %q", playerLibraryMid, top)
	}
	// La tabla se queda el resto: 100 - 24 - 1 columnas.
	_, table, _ := playerZoneWidths(100, false)
	if table != 100-playerLibraryMid-playerZoneGap {
		t.Fatalf("medium table width = %d, want %d", table, 100-playerLibraryMid-playerZoneGap)
	}
}

// playerViewPromotedRow locates the only row that carries a promoted sentence
// and checks it measures the window width.
func playerViewPromotedRow(t *testing.T, rows []string, width int, sentence string) int {
	t.Helper()
	at := -1
	for i, row := range rows {
		if !strings.Contains(row, sentence) {
			continue
		}
		if at >= 0 {
			t.Fatalf("the sentence appears twice:\n%s", strings.Join(rows, "\n"))
		}
		if got := lipgloss.Width(row); got != width {
			t.Fatalf("the promoted row measures %d cells, want %d: %q", got, width, row)
		}
		at = i
	}
	if at < 0 {
		t.Fatalf("missing the promoted sentence %q:\n%s", sentence, strings.Join(rows, "\n"))
	}
	return at
}

// playerViewBoxMark is the only content of the library box while its sentence
// is promoted: the short state marker.
func playerViewBoxMark(t *testing.T, rows []string, mark string) {
	t.Helper()
	for _, row := range rows {
		cells := []rune(row)
		if len(cells) < 3 || cells[0] != '│' {
			continue
		}
		end := -1
		for i := 1; i < len(cells); i++ {
			if cells[i] == '│' {
				end = i
				break
			}
		}
		if end > 0 && strings.TrimSpace(string(cells[1:end])) == mark {
			return
		}
	}
	t.Fatalf("the library box lost its %q marker:\n%s", mark, strings.Join(rows, "\n"))
}

// TestPlayerViewPromotesOverflowSentence keeps Rule 2: a session sentence that
// does not fit its zone is drawn once, full width, directly above the player
// box, and the zone keeps only its short marker.
func TestPlayerViewPromotesOverflowSentence(t *testing.T) {
	authorize := func(context.Context, string, func(string)) (CatalogAPI, error) {
		return nil, errors.New("offline fixture")
	}
	unauthorized, _, _ := libraryNewPlayer(t, 120, 40, preferences{}, playerState{Connected: true}, nil, authorize, "fixture-client")
	failing := &libraryFake{playlists: func(context.Context, int) ([]libraryPlaylist, int, bool, error) {
		return nil, 0, false, errSpotifyCatalog
	}}
	failed, _, _ := libraryNewPlayer(t, 120, 40, preferences{}, playerState{Connected: true}, failing, nil, "")
	for _, tt := range []struct {
		name     string
		model    tea.Model
		sentence string
		mark     string
	}{
		{"unauthorized", unauthorized, "● No Spotify. Enter: connect", playerMarkNotice},
		{"failure", failed, "! Could not load Spotify data. Enter: retry.", playerMarkFailure},
	} {
		for _, size := range []struct{ width, height int }{{120, 40}, {100, 30}} {
			t.Run(fmt.Sprintf("%s %dx%d", tt.name, size.width, size.height), func(t *testing.T) {
				rows := playerViewRows(t, tt.model, size.width, size.height)
				at := playerViewPromotedRow(t, rows, size.width, tt.sentence)
				box := -1
				for i, row := range rows {
					if strings.HasPrefix(row, "├─") {
						box = i
						break
					}
				}
				if box < 0 || at != box-1 {
					t.Fatalf("the sentence is not directly above the player box:\n%s", strings.Join(rows, "\n"))
				}
				playerViewBoxMark(t, rows, tt.mark)
			})
		}
	}
	// En la banda compacta no hay caja de biblioteca: la fila del selector ya
	// ocupa todo el ancho y no hay nada que promover.
	compact := strings.Join(playerViewRows(t, failed, 80, 24), "\n")
	if !strings.Contains(compact, "LIBRARY: ! Could not load Spotify data. Enter: retry.") {
		t.Fatalf("the compact selector lost the sentence:\n%s", compact)
	}
}

// TestPlayerViewPromotesSavePrompt keeps Rule 2 for the save panel: its
// approval prompt is session state and leaves the table box whole.
func TestPlayerViewPromotesSavePrompt(t *testing.T) {
	const sentence = "Spotify needs your approval to change playlists. Enter: authorize again."
	model := playerZoneModel(t, "en").(*playerModel)
	model.saveTo.phase = saveScopeOffered
	rows := playerViewRows(t, model, 120, 40)
	at := playerViewPromotedRow(t, rows, 120, sentence)
	box := -1
	for i, row := range rows {
		if strings.HasPrefix(row, "├─") {
			box = i
			break
		}
	}
	if box < 0 || at != box-1 {
		t.Fatalf("the prompt is not directly above the player box:\n%s", strings.Join(rows, "\n"))
	}
	if !strings.Contains(strings.Join(rows, "\n"), "┌─ Save to playlist ─") {
		t.Fatalf("the save panel lost its box title:\n%s", strings.Join(rows, "\n"))
	}
	// La caja de la tabla conserva la marca corta y no repite la frase.
	if !playerViewTableMark(rows, playerMarkFailure) {
		t.Fatalf("the table box lost its %q marker:\n%s", playerMarkFailure, strings.Join(rows, "\n"))
	}
}

// playerViewTableMark reports whether the table box, which starts after the
// library box and its gap, holds only the short state marker in a row.
func playerViewTableMark(rows []string, mark string) bool {
	start := playerLibraryZone + playerZoneGap
	for _, row := range rows {
		cells := []rune(row)
		if len(cells) <= start+2 || cells[start] != '│' {
			continue
		}
		end := -1
		for i := start + 1; i < len(cells); i++ {
			if cells[i] == '│' {
				end = i
				break
			}
		}
		if end > start && strings.TrimSpace(string(cells[start+1:end])) == mark {
			return true
		}
	}
	return false
}

// TestPlayerViewBandBoundaries keeps the ladder monotone: a height that misses
// the threshold of its width lands on the next richest shape, never on the
// poorest, and the table is drawn whenever the resolved shape has one.
func TestPlayerViewBandBoundaries(t *testing.T) {
	m := playerZoneModel(t, "en")
	for _, tt := range []struct {
		width, height int
		band          int
		shape         string
		table         bool
	}{
		{120, 29, playerBandMedium, "medium", true},
		{100, 25, playerBandCompact, "compact", true},
		{80, 19, playerBandMinimum, "minimum", true},
		{119, 40, playerBandMedium, "medium", true},
		{99, 40, playerBandCompact, "compact", true},
		{79, 24, playerBandMinimum, "minimum", true},
	} {
		t.Run(fmt.Sprintf("%dx%d %s", tt.width, tt.height, tt.shape), func(t *testing.T) {
			if got := playerBand(tt.width, tt.height); got != tt.band {
				t.Fatalf("band at %dx%d = %d, want %d", tt.width, tt.height, got, tt.band)
			}
			rows := playerViewRows(t, m, tt.width, tt.height)
			view := strings.Join(rows, "\n")
			for i, row := range rows {
				if got := lipgloss.Width(row); got != tt.width {
					t.Fatalf("row %d of %dx%d measures %d cells: %q", i, tt.width, tt.height, got, row)
				}
			}
			tops := 0
			for _, row := range rows {
				if n := strings.Count(row, "┌─ "); n > tops {
					tops = n
				}
			}
			switch tt.shape {
			case "medium":
				if tops != 2 {
					t.Fatalf("the medium shape drew %d zone titles, want two:\n%s", tops, view)
				}
				if strings.Contains(view, "UP NEXT") {
					t.Fatalf("the medium shape kept the queue column:\n%s", view)
				}
			default:
				if tops != 0 {
					t.Fatalf("the %s shape drew %d zone titles, want none:\n%s", tt.shape, tops, view)
				}
				if !strings.Contains(view, "LIBRARY:") {
					t.Fatalf("the %s shape lost the library selector:\n%s", tt.shape, view)
				}
			}
			header := false
			for _, row := range rows {
				if strings.Contains(row, "#") && strings.Contains(row, "Title") {
					header = true
				}
			}
			if header != tt.table {
				t.Fatalf("the %s shape table header = %v, want %v:\n%s", tt.shape, header, tt.table, view)
			}
		})
	}
}

// TestPlayerViewBandMatrix pins the resolved band, the exact row width and the
// table header at every size of the approved matrix.
func TestPlayerViewBandMatrix(t *testing.T) {
	m := playerZoneModel(t, "en")
	for _, tt := range []struct {
		width, height int
		band          int
		table         bool
	}{
		{140, 40, playerBandWide, true},
		{120, 34, playerBandWide, true},
		{120, 29, playerBandMedium, true},
		{100, 30, playerBandMedium, true},
		{100, 25, playerBandCompact, true},
		{80, 24, playerBandCompact, true},
		{80, 19, playerBandMinimum, true},
		{79, 24, playerBandMinimum, true},
		{40, 12, playerBandMinimum, false},
		{39, 11, playerBandInsufficient, false},
	} {
		t.Run(fmt.Sprintf("%dx%d", tt.width, tt.height), func(t *testing.T) {
			if got := playerBand(tt.width, tt.height); got != tt.band {
				t.Fatalf("band at %dx%d = %d, want %d", tt.width, tt.height, got, tt.band)
			}
			rows := playerViewRows(t, m, tt.width, tt.height)
			view := strings.Join(rows, "\n")
			for i, row := range rows {
				if got := lipgloss.Width(row); got != tt.width {
					t.Fatalf("row %d measures %d cells, want %d: %q", i, got, tt.width, row)
				}
			}
			header := false
			for _, row := range rows {
				if strings.Contains(row, "#") && strings.Contains(row, "Title") {
					header = true
				}
			}
			if header != tt.table {
				t.Fatalf("table header = %v at %dx%d, want %v:\n%s", header, tt.width, tt.height, tt.table, view)
			}
		})
	}
}

// TestPlayerViewEdgeSizes walks the sizes around and below the matrix: none may
// panic, none may overflow, and every window with room renders rows.
func TestPlayerViewEdgeSizes(t *testing.T) {
	m := playerZoneModel(t, "en")
	for _, size := range []struct{ width, height int }{
		{1, 1}, {0, 0}, {0, 10}, {10, 0}, {1, 12}, {2, 2}, {2, 40}, {3, 3},
		{40, 1}, {39, 12}, {40, 11}, {41, 12}, {120, 1},
	} {
		t.Run(fmt.Sprintf("%dx%d", size.width, size.height), func(t *testing.T) {
			// playerViewRows falla si una fila desborda o si sobran filas.
			rows := playerViewRows(t, m, size.width, size.height)
			if size.width < 1 || size.height < 1 {
				if len(rows) != 0 {
					t.Fatalf("an empty window rendered %d rows", len(rows))
				}
				return
			}
			if len(rows) == 0 {
				t.Fatal("a window with room rendered no rows")
			}
			for i, row := range rows {
				if got := lipgloss.Width(row); got != size.width {
					t.Fatalf("row %d measures %d cells, want %d: %q", i, got, size.width, row)
				}
			}
		})
	}
}

// TestPlayerViewVolumeLabelFitsTheBox pins the shortest state line the player
// box can draw: at 40 and 41 columns the volume keeps its label and its
// percentage, because the percentage-only form was unreachable and is gone.
func TestPlayerViewVolumeLabelFitsTheBox(t *testing.T) {
	for _, size := range []struct{ width, height int }{{40, 12}, {41, 12}} {
		t.Run(fmt.Sprintf("%dx%d", size.width, size.height), func(t *testing.T) {
			m, _ := playerViewModel(t, size.width, size.height, playerState{
				Connected: true, Status: "playing", Volume: 37, OptionsKnown: true,
			})
			view := playerViewText(t, m, "Volume 37%")
			if strings.Contains(view, "▸ 37%") && !strings.Contains(view, "Volume 37%") {
				t.Fatalf("the state line dropped the volume label:\n%s", view)
			}
			if !strings.Contains(ansi.Strip(m.View()), "Volume 37%") {
				t.Fatalf("the state line lost the volume at %d columns:\n%s", size.width, m.View())
			}
		})
	}
}

// TestPlayerViewFooterPresence pins where the footer still has rows: every band
// down to the minimum one keeps them, and the sizes without room keep none.
func TestPlayerViewFooterPresence(t *testing.T) {
	m := playerZoneModel(t, "en")
	for _, tt := range []struct {
		width, height int
		hints         bool
	}{
		{140, 40, true}, {120, 34, true}, {100, 30, true}, {80, 24, true}, {79, 23, true},
		{40, 12, false}, {39, 11, false},
	} {
		t.Run(fmt.Sprintf("%dx%d", tt.width, tt.height), func(t *testing.T) {
			view := strings.Join(playerViewRows(t, m, tt.width, tt.height), "\n")
			for _, want := range []string{"Tab focus", "q quit"} {
				if got := strings.Contains(view, want); got != tt.hints {
					t.Fatalf("%q present = %v at %dx%d, want %v:\n%s", want, got, tt.width, tt.height, tt.hints, view)
				}
			}
		})
	}
}

// TestPlayerViewRemoveConfirmationKeepsItsText pins the promotion defect: the
// confirmation is session state, so a prompt that does not fit the table box is
// drawn whole on the full-width row and the box keeps no cut copy.
func TestPlayerViewRemoveConfirmationKeepsItsText(t *testing.T) {
	const track = "Bohemian Rhapsody (Remastered 2011)"
	const playlist = "Clásicos del Rock Chileno & Latinoamericano"
	for _, size := range []struct{ width, height int }{{140, 40}, {120, 34}} {
		t.Run(fmt.Sprintf("%dx%d", size.width, size.height), func(t *testing.T) {
			model := playerZoneModel(t, "en").(*playerModel)
			model.saveTo.phase = saveConfirming
			model.saveTo.removeTrackName = track
			model.saveTo.target = libraryPlaylist{Name: playlist, URI: "spotify:playlist:clasicos"}
			prompt := model.removePrompt()
			library, table, _ := playerZoneWidths(size.width, playerBand(size.width, size.height) == playerBandWide)
			if lipgloss.Width(prompt) <= table-2 {
				t.Fatalf("the prompt (%d cells) fits the table box (%d cells): the test proves nothing",
					lipgloss.Width(prompt), table-2)
			}
			rows := playerViewRows(t, model, size.width, size.height)
			view := strings.Join(rows, "\n")
			at := playerViewPromotedRow(t, rows, size.width, prompt)
			box := -1
			for i, row := range rows {
				if strings.HasPrefix(row, "├─") {
					box = i
					break
				}
			}
			if box < 0 || at != box-1 {
				t.Fatalf("the confirmation is not directly above the player box:\n%s", view)
			}
			// Dentro de las zonas no queda una copia recortada del aviso.
			top, bottom := -1, -1
			for i, row := range rows {
				if top < 0 && strings.Contains(row, "┌─ LIBRARY") {
					top = i
				}
				if top >= 0 && i > top && strings.HasPrefix(row, "└") {
					bottom = i
					break
				}
			}
			if top < 0 || bottom < 0 {
				t.Fatalf("the zones are missing at %dx%d:\n%s", size.width, size.height, view)
			}
			for _, row := range rows[top : bottom+1] {
				if strings.Contains(row, "Bohemian") {
					t.Fatalf("the table box kept a cut copy of the confirmation:\n%s", view)
				}
			}
			_ = library
		})
	}
}
