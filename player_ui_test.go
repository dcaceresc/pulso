package main

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
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
	m := newPlayerModel(ctx, states, out, width, height)
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
	m := newPlayerModel(ctx, states, out, 80, 24)
	playerViewText(t, m, "Pulso", "Loading", "Esc/q: quit")
	next := m.Init()
	if m.Init() != nil {
		t.Fatal("scheduled a second pending read")
	}
	for _, state := range []struct {
		value playerState
		want  []string
	}{
		{playerState{Connected: true}, []string{"Connected", "Nothing playing", "Queue is empty"}},
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
	m := newPlayerModel(ctx, states, nil, 100, 20)
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
			if size.width >= 30 {
				playerViewText(t, m, "Pulso", "playing", "Good news", "Title", "Volume 37%")
			}
			if size.width >= 40 {
				playerViewText(t, m, "Artist")
			}
			if size.width >= 60 {
				playerViewText(t, m, "彩虹 Title", "1:02", "3:07", "Desk Device", "Duration")
			}
		})
	}
}

func TestPlayerViewWorkspaceBounds(t *testing.T) {
	state := playerState{
		Connected: true, Status: "playing", Device: "Desk", Volume: 37, PositionMS: 62000,
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
			"Pulso", "Search:", "LIBRARY", "No playlists yet", "#", "Title", "Artist", "Duration",
			"Estrechez de Corazón", "Tren al Sur", "1:02", "6:21", "Volume 37%", "Device Desk",
			"⇄ off", "↻ off", "Tab: panel", "Esc/q: quit", "s: search", "l: playlists", "F2: language",
		}, nil},
		{100, 30, []string{
			"Pulso", "Search:", "LIBRARY", "No playlists yet", "#", "Title", "Artist", "Duration",
			"Estrechez de Corazón", "1:02", "6:21", "Volume 37%", "Device Desk", "Esc/q: quit",
		}, nil},
		{80, 24, []string{
			"Pulso", "LIBRARY:", "No playlists yet", "#", "Title", "Artist", "Duration",
			"Estrechez de Corazón", "Volume 37%", "Esc/q: quit",
		}, []string{"│"}},
		{40, 12, []string{"Pulso", "▶", "Volume 37%"}, nil},
		{20, 8, []string{"Pulso", "Volume 37%"}, nil},
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
				sidebar := false
				for _, row := range rows {
					if strings.Contains(row, "LIBRARY") && strings.Contains(row, "│") {
						sidebar = true
					}
				}
				if !sidebar {
					t.Fatalf("wide layout lost the sidebar column:\n%s", view)
				}
			}
		})
	}
}

func TestPlayerViewWorkspaceLocalized(t *testing.T) {
	m := newPlayerModelWithPreferences(context.Background(), nil, nil, 120, 40, preferences{Language: "es", Theme: "standard"}, nil, "", nil)
	playerViewText(t, m, "Pulso", "Buscar:", "BIBLIOTECA", "Aún no hay listas", "Reproducción", "Título", "Artista", "Duración", "Esc/q: salir", "F2: idioma")
}

func TestPlayerViewFocusCycleAndHints(t *testing.T) {
	m, out := playerViewModel(t, 120, 40, playerState{Connected: true})
	// The table owns focus first; every panel advertises itself in the hints.
	for _, step := range []struct {
		key  tea.KeyMsg
		want []string
		drop []string
	}{
		{tea.KeyMsg{Type: tea.KeyTab}, []string{"Search: _", "Enter: keep", "Backspace: delete"}, []string{"↑↓/jk: move"}},
		{tea.KeyMsg{Type: tea.KeyTab}, []string{"Space: pause", "Esc/q: quit"}, []string{"Enter: keep"}},
		{tea.KeyMsg{Type: tea.KeyTab}, []string{"↑↓/jk: library", "Esc/q: quit"}, []string{"Space: pause"}},
		{tea.KeyMsg{Type: tea.KeyTab}, []string{"↑↓/jk: move", "Esc/q: quit"}, []string{"↑↓/jk: library"}},
		{tea.KeyMsg{Type: tea.KeyShiftTab}, []string{"↑↓/jk: library"}, []string{"↑↓/jk: move"}},
		{tea.KeyMsg{Type: tea.KeyShiftTab}, []string{"Space: pause"}, []string{"↑↓/jk: library"}},
		{tea.KeyMsg{Type: tea.KeyShiftTab}, []string{"Enter: keep"}, []string{"Space: pause"}},
		{tea.KeyMsg{Type: tea.KeyShiftTab}, []string{"↑↓/jk: move"}, []string{"Enter: keep"}},
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
		{playerState{Connected: true}, "Queue is empty"},
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
			m := newPlayerModel(ctx, states, out, 80, 24)
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
			m := newPlayerModel(ctx, states, out, 80, 24)
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

// Drain the catalog's async OAuth/data results through the parent model.
func playerViewCatalogDrain(t *testing.T, m tea.Model, cmd tea.Cmd) {
	t.Helper()
	for steps := 0; cmd != nil; steps++ {
		if steps == 5 {
			t.Fatal("catalog commands did not settle")
		}
		m, cmd = m.Update(playerViewRun(t, cmd))
	}
}

func TestPlayerViewCatalogOverlay(t *testing.T) {
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
	m := newPlayerModelWithPreferences(ctx, states, out, 100, 24, preferences{}, nil, "fixture-id", catalogViewReply(api))
	playerViewText(t, m, "s: search", "l: playlists")
	states <- playerState{Connected: true, SessionID: 42}
	m, next := m.Update(playerViewRun(t, m.Init()))
	m, cmd := m.Update(playerViewKey("s"))
	playerViewCatalogDrain(t, m, cmd)
	m, _ = m.Update(playerViewKey("jazz"))
	m, cmd = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	playerViewCatalogDrain(t, m, cmd)
	playerViewText(t, m, "Fixture result")
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	select {
	case got := <-out:
		if want := (playerCommand{Name: "play", URI: "spotify:track:AbC123", SessionID: 42}); got != want {
			t.Fatalf("selected command %+v, want %+v", got, want)
		}
	default:
		t.Fatal("expected selection to send a command to the displayed session")
	}
	m, _ = m.Update(tea.WindowSizeMsg{Width: 14, Height: 5})
	rows := strings.Split(ansi.Strip(m.View()), "\n")
	if len(rows) > 5 {
		t.Fatalf("overlay resized to too many rows: %v", rows)
	}
	for _, row := range rows {
		if lipgloss.Width(row) > 14 {
			t.Fatalf("overlay row was not resized: %q", row)
		}
	}
	states <- playerState{Connected: true, SessionID: 43, Track: playerTrack{Title: "Fresh playback"}}
	m, next = m.Update(playerViewRun(t, next))
	if next == nil {
		t.Fatal("overlay stopped player reception")
	}
	m, cmd = m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if cmd != nil {
		t.Fatal("Esc in catalog should not quit or reschedule player reception")
	}
	m, _ = m.Update(tea.WindowSizeMsg{Width: 100, Height: 24})
	playerViewText(t, m, "Fresh playback")
	m, cmd = m.Update(playerViewKey("l"))
	playerViewCatalogDrain(t, m, cmd)
	playerViewText(t, m, "Fixture playlist")
	states <- playerState{Notice: playerPairNotice}
	m, _ = m.Update(playerViewRun(t, next))
	m.Update(playerViewKey("p"))
	if len(out) != 0 {
		t.Fatal("a catalog selection reached an unauthenticated Soloist session")
	}
}

func TestPlayerViewPreferenceShortcuts(t *testing.T) {
	var saved []preferences
	m := newPlayerModelWithPreferences(context.Background(), nil, nil, 100, 24, preferences{Language: "en", Theme: "standard"}, func(p preferences) error {
		saved = append(saved, p)
		return nil
	}, "", nil)
	m.Init()
	playerViewText(t, m, "Pulso", "Playback", "F2: language", "F3: theme")
	if len(saved) != 0 {
		t.Fatalf("startup unexpectedly saved: %v", saved)
	}
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyF2})
	playerViewText(t, m, "Pulso", "Reproducción", "Cargando", "F2: idioma", "F3: tema")
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
	m := newPlayerModel(ctx, states, out, 80, 24)
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

func TestPlayerViewOverlayPreferencesAndClose(t *testing.T) {
	states, out := make(chan playerState, 1), make(chan playerCommand, 1)
	api := catalogViewFake{search: func(context.Context, string, int) ([]catalogItem, bool, error) {
		return []catalogItem{{Name: "Track", URI: "spotify:track:1"}}, false, nil
	}}
	var saved []preferences
	m := newPlayerModelWithPreferences(context.Background(), states, out, 100, 24, preferences{Language: "en", Theme: "standard"}, func(p preferences) error {
		saved = append(saved, p)
		return nil
	}, "fixture-id", catalogViewReply(api))
	m.Init()
	m, cmd := m.Update(playerViewKey("s"))
	playerViewCatalogDrain(t, m, cmd)
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyF2})
	playerViewText(t, m, "Buscar canciones")
	if len(saved) != 1 || saved[0].Language != "es" {
		t.Fatalf("overlay shortcut did not persist: %v", saved)
	}
	m, cmd = m.Update(tea.KeyMsg{Type: tea.KeyCtrlC})
	if cmd != nil {
		t.Fatal("Ctrl+C in the overlay should close it without quitting")
	}
	m, _ = m.Update(tea.WindowSizeMsg{Width: 100, Height: 24})
	playerViewText(t, m, "Pulso", "Reproducción")
}

func TestPlayerViewPlayingBarKeepsAlbum(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	states := make(chan playerState, 1)
	m := newPlayerModel(ctx, states, nil, 100, 30)
	cmd := m.Init()
	states <- playerState{Connected: true, Track: playerTrack{Title: "Song", Artist: "Artist", Album: "Record", DurationMS: 200000}}
	m, _ = m.Update(playerViewRun(t, cmd))
	playerViewText(t, m, "Record")
}
