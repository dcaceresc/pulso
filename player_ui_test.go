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
	for _, size := range []struct{ width, height int }{{100, 20}, {70, 16}, {30, 12}, {12, 5}, {1, 1}, {0, 10}, {10, 0}} {
		t.Run(fmt.Sprintf("%dx%d", size.width, size.height), func(t *testing.T) {
			m, _ = m.Update(tea.WindowSizeMsg{Width: size.width, Height: size.height})
			view := ansi.Strip(m.View())
			if size.width == 0 || size.height == 0 {
				if view != "" {
					t.Fatalf("expected an empty view, got %q", view)
				}
				return
			}
			rows := strings.Split(view, "\n")
			if len(rows) > size.height {
				t.Fatalf("rendered %d rows", len(rows))
			}
			for _, row := range rows {
				if lipgloss.Width(row) > size.width {
					t.Fatalf("row overflows: %q", row)
				}
			}
			if strings.ContainsAny(view, "\r\t\x00\x07\x1b") || strings.Contains(m.View(), "UNTRUSTED") || strings.Contains(m.View(), "\x1b[31m") {
				t.Fatalf("metadata injected controls: %q", m.View())
			}
			if size.width >= 30 {
				playerViewText(t, m, "NOW PLAYING", "彩虹 Title", "Artist", "Album", "1:02 / 3:07", "Volume 37%", "QUEUE", "Next", "STATUS", "Desk Device", "Good news")
				var playerRow, queueRow int
				for i, row := range rows {
					if strings.Contains(row, "NOW PLAYING") {
						playerRow = i
					}
					if strings.Contains(row, "QUEUE") {
						queueRow = i
					}
				}
				if (playerRow == queueRow) != (size.width == 100) {
					t.Fatalf("unexpected panel layout:\n%s", view)
				}
			}
		})
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
	playerViewText(t, m, "Pulso / Playback", "F2: language", "F3: theme")
	if len(saved) != 0 {
		t.Fatalf("startup unexpectedly saved: %v", saved)
	}
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyF2})
	playerViewText(t, m, "Pulso / Reproducción", "Cargando", "F2: idioma", "F3: tema")
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

func TestPlayerViewPreferenceAliases(t *testing.T) {
	var saved []preferences
	m := newPlayerModelWithPreferences(context.Background(), nil, nil, 100, 24, preferences{Language: "en", Theme: "standard"}, func(p preferences) error {
		saved = append(saved, p)
		return nil
	}, "", nil)
	m.Init()
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyCtrlL})
	playerViewText(t, m, "Pulso / Reproducción")
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyCtrlT})
	if len(saved) != 2 || saved[0].Language != "es" || saved[1].Theme != "dark" {
		t.Fatalf("alias keys did not persist: %v", saved)
	}
}
