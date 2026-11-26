package main

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
)

// runTickCmds executes commands like the event loop and collects their messages.
func runTickCmds(cmds ...tea.Cmd) []tea.Msg {
	var (
		mu  sync.Mutex
		out []tea.Msg
		wg  sync.WaitGroup
	)
	var run func(tea.Cmd)
	run = func(cmd tea.Cmd) {
		if cmd == nil {
			return
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			msg := cmd()
			batch, ok := msg.(tea.BatchMsg)
			if !ok {
				if msg != nil {
					mu.Lock()
					out = append(out, msg)
					mu.Unlock()
				}
				return
			}
			for _, sub := range batch {
				run(sub)
			}
		}()
	}
	for _, cmd := range cmds {
		run(cmd)
	}
	wg.Wait()
	return out
}

func countTicks(msgs []tea.Msg) int {
	n := 0
	for _, msg := range msgs {
		if _, ok := msg.(playerTickMsg); ok {
			n++
		}
	}
	return n
}

func playingState() playerState {
	return playerState{Connected: true, Status: "playing", SessionID: 5, PositionMS: 1000, Speed: 1,
		AnchorAt: time.Now(), Track: playerTrack{Title: "Song", DurationMS: 200000}}
}

func TestPlayerOneTickChainPerSession(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	states := make(chan playerState, 8)
	m := newPlayerModel(ctx, states, nil, 120, 40).(*playerModel)

	var cmds []tea.Cmd
	states <- playingState()
	_, cmd := m.Update(playerViewRun(t, m.Init()))
	cmds = append(cmds, cmd)
	for range 4 {
		m.pending = true
		_, cmd = m.Update(playerViewState{state: playingState(), ok: true})
		cmds = append(cmds, cmd)
		// Feed the companion state read so its command returns instead of blocking.
		states <- playingState()
	}
	states <- playingState()
	if got := countTicks(runTickCmds(cmds...)); got != 1 {
		t.Fatalf("five playing states produced %d tick chains, want 1", got)
	}

	m.pending = true
	_, paused := m.Update(playerViewState{state: playerState{Connected: true, Status: "paused"}, ok: true})
	states <- playingState()
	if got := countTicks(runTickCmds(paused)); got != 0 {
		t.Fatalf("a paused state armed %d tick chains", got)
	}
	if next := m.tick(); next != nil {
		t.Fatal("a paused session still schedules ticks")
	}
}

func TestPlayerPlayheadHoldsWhenPaused(t *testing.T) {
	anchor := time.Now().Add(-10 * time.Second)
	m := &playerModel{clock: func() time.Time { return time.Now() }}
	m.state = playerState{Status: "paused", PositionMS: 30000, Speed: 1, AnchorAt: anchor,
		Track: playerTrack{DurationMS: 200000}}
	if got := m.playhead(); got != 30000 {
		t.Fatalf("paused playhead = %d, want the reported 30000", got)
	}
	m.state.Status = "playing"
	if got := m.playhead(); got <= 30000 {
		t.Fatalf("playing playhead = %d, want it to advance", got)
	}
}

func TestPlayerUnknownModesRenderNeutral(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	states := make(chan playerState, 2)
	states <- playerState{Connected: true, SessionID: 1, Track: playerTrack{Title: "Song", DurationMS: 1000}}
	m := newPlayerModel(ctx, states, nil, 120, 40)
	m, _ = m.Update(playerViewRun(t, m.Init()))
	view := ansi.Strip(m.View())
	if strings.Contains(view, "⇄ off") || strings.Contains(view, "↻ off") {
		t.Fatalf("unknown modes were rendered as off:\n%s", view)
	}
	m.(*playerModel).pending = true
	m, _ = m.Update(playerViewState{state: playerState{Connected: true, SessionID: 1, OptionsKnown: true,
		Track: playerTrack{Title: "Song", DurationMS: 1000}}, ok: true})
	view = ansi.Strip(m.View())
	if !strings.Contains(view, "⇄ off") || !strings.Contains(view, "↻ off") {
		t.Fatalf("confirmed off modes were not rendered:\n%s", view)
	}
}
