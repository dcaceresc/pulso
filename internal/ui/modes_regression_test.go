package ui

import (
	"context"
	"testing"
)

// modeModel builds a connected player with frozen Soloist options so the tests
// reproduce a session whose mode events never arrive.
func modeModel(t *testing.T, state playerState) (*playerModel, chan playerCommand) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	states := make(chan playerState, 2)
	out := make(chan playerCommand, 8)
	m := NewPlayerModel(ctx, states, out, 120, 40)
	states <- state
	model, _ := m.Update(playerViewRun(t, m.Init()))
	return model.(*playerModel), out
}

func TestModesToggleWithoutConfirmingEvents(t *testing.T) {
	state := playerState{Connected: true, SessionID: 7, OptionsKnown: true, Status: "playing",
		Track: playerTrack{Title: "Song", DurationMS: 200000}}
	m, out := modeModel(t, state)

	updated, _ := m.Update(playerViewKey("s"))
	m = updated.(*playerModel)
	for _, want := range []string{"set_shuffle", "get_state"} {
		if got := (<-out).Name; got != want {
			t.Fatalf("shuffle press sent %q, want %q", got, want)
		}
	}
	if m.shuffleMode() != "on" {
		t.Fatalf("shuffle did not show on after one press: %q", m.shuffleMode())
	}
	// A second press must ask to turn it off even though no event confirmed the first.
	if cmd := m.handlePlayingKey(playerViewKey("s")); !cmd {
		t.Fatal("shuffle key was not handled")
	}
	if got := <-out; got.Name != "set_shuffle" || got.Shuffle {
		t.Fatalf("second press sent %+v, want set_shuffle false", got)
	}
	<-out // get_state
	if m.shuffleMode() != "off" {
		t.Fatalf("shuffle stuck on: %q", m.shuffleMode())
	}

	for _, want := range []string{"context", "track", "off"} {
		m.handlePlayingKey(playerViewKey("r"))
		if got := <-out; got.Name != "set_repeat" || got.Repeat != want {
			t.Fatalf("repeat press sent %+v, want %q", got, want)
		}
		<-out // get_state
	}
	if m.repeatMode() != "off" {
		t.Fatalf("repeat did not cycle back to off: %q", m.repeatMode())
	}
}

func TestModesReconcileWithReportedState(t *testing.T) {
	state := playerState{Connected: true, SessionID: 7, OptionsKnown: true,
		Track: playerTrack{Title: "Song", DurationMS: 200000}}
	m, out := modeModel(t, state)
	m.Update(playerViewKey("s"))
	<-out
	<-out

	m.pending = true
	updated, _ := m.Update(playerViewState{state: playerState{Connected: true, SessionID: 7, OptionsKnown: true,
		Shuffle: true, Track: playerTrack{Title: "Song", DurationMS: 200000}}, ok: true})
	m = updated.(*playerModel)
	if m.wantShuffle != nil {
		t.Fatal("a matching report left the shuffle intent pending")
	}
	m.Update(playerViewKey("s"))
	if got := <-out; got.Name != "set_shuffle" || got.Shuffle {
		t.Fatalf("confirmed state did not flip the next press: %+v", got)
	}
	<-out

	// A new session drops intents that belonged to the old one.
	m.Update(playerViewKey("s"))
	<-out
	<-out
	m.pending = true
	updated, _ = m.Update(playerViewState{state: playerState{Connected: true, SessionID: 8, OptionsKnown: true,
		Track: playerTrack{Title: "Song", DurationMS: 200000}}, ok: true})
	m = updated.(*playerModel)
	if m.wantShuffle != nil || m.wantRepeat != "" {
		t.Fatalf("session change kept stale intents: %v %q", m.wantShuffle, m.wantRepeat)
	}
}
