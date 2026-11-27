package soloist

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/websocket"
)

const playerTestItem = `{"decorations":{"identity":{"name":"Song"},"parent":{"entity":{"name":"Album"}},"creators":[{"entity":{"name":"First"}},{"entity":{"name":"Second"}}],"playback":{"duration_ms":190000}}}`

func playerTestSend(t *testing.T, ctx context.Context, conn *websocket.Conn, msg string) bool {
	t.Helper()
	if err := conn.Write(ctx, websocket.MessageText, []byte(msg)); err != nil {
		t.Errorf("write fixture event: %v", err)
		return false
	}
	return true
}

func playerTestRead(t *testing.T, ctx context.Context, conn *websocket.Conn, want string) bool {
	t.Helper()
	kind, data, err := conn.Read(ctx)
	var got, expected map[string]any
	if err != nil || kind != websocket.MessageText || json.Unmarshal(data, &got) != nil || json.Unmarshal([]byte(want), &expected) != nil || !reflect.DeepEqual(got, expected) {
		t.Errorf("fixture received %q (%v), want %s", data, err, want)
		return false
	}
	return true
}

func playerTestServer(t *testing.T, handle func(context.Context, *websocket.Conn)) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			t.Errorf("upgrade fixture: %v", err)
			return
		}
		defer conn.CloseNow()
		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()
		handle(ctx, conn)
	}))
	t.Cleanup(server.Close)
	return server
}

func playerTestMarkers(t *testing.T, dir, endpoint string) {
	t.Helper()
	u, err := url.Parse(endpoint)
	if err != nil {
		t.Fatal(err)
	}
	host, port, err := net.SplitHostPort(u.Host)
	if err != nil {
		t.Fatal(err)
	}
	for name, value := range map[string]string{"ws.addr": host, "ws.port": port} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(value+"\n"), 0600); err != nil {
			t.Fatal(err)
		}
	}
}

func playerTestState(t *testing.T, states <-chan State, want func(State) bool) State {
	t.Helper()
	timer := time.NewTimer(5 * time.Second)
	defer timer.Stop()
	var last State
	for {
		select {
		case s, ok := <-states:
			if !ok {
				t.Fatal("states closed early")
			}
			last = s
			if want(s) {
				return s
			}
		case <-timer.C:
			t.Fatalf("no matching state; last: %+v", last)
		}
	}
}

func playerTestCommand(t *testing.T, commands chan<- Command, command Command) {
	t.Helper()
	select {
	case commands <- command:
	case <-time.After(5 * time.Second):
		t.Fatalf("command not consumed: %+v", command)
	}
}

func TestPlayerTransportEventsAndCommands(t *testing.T) {
	dir := t.TempDir()
	upgraded, auth, next, complete := make(chan struct{}), make(chan struct{}), make(chan struct{}), make(chan struct{})
	steps := []struct {
		event string
		match func(State) bool
	}{
		{`{"type":"playback_state","item":` + playerTestItem + `,"status":"playing","position":{"position_ms":4200},"volume":55}`, func(s State) bool {
			return s.Connected && s.Device == "Living Room" && s.Status == "playing" && s.PositionMS == 4200 && s.Volume == 55 && s.Track == (Track{Title: "Song", Artist: "First, Second", Album: "Album", DurationMS: 190000})
		}},
		{`{"type":"queue_changed","upcoming":[{"item":` + playerTestItem + `}]}`, func(s State) bool { return len(s.Queue) == 1 && s.Queue[0] == s.Track }},
		{`{"type":"track_changed","item":{"decorations":{"identity":{"name":"Next\u001b"}}}}`, func(s State) bool { return s.Track.Title == "Next" && s.PositionMS == 0 && s.Status == "playing" }},
		{`{"type":"playback_changed","status":"paused","position":{"position_ms":700}}`, func(s State) bool {
			return s.Status == "paused" && s.PositionMS == 700 && s.Track.Title == "Next"
		}},
		{`{"type":"playback_state","item":` + playerTestItem + `,"status":"paused","context":{"uri":"spotify:playlist:First","entity_type":"playlist","decorations":{"identity":{"name":"First list"}}}}`, func(s State) bool {
			return s.PlaylistContext == "First list"
		}},
		{`{"type":"context_changed","context":{"uri":"spotify:playlist:Second","entity_type":"playlist"}}`, func(s State) bool {
			return s.PlaylistContext == "spotify:playlist:Second" && s.Track.Title == "Song"
		}},
		{`{"type":"playback_state","is_active":false,"status":"paused","volume":30,"item":` + playerTestItem + `,"context":{"uri":"spotify:playlist:Second","entity_type":"playlist"}}`, func(s State) bool {
			return s.Volume == 30 && s.PlaylistContext == "" && s.Track.Title == "Song"
		}},
		{`{"type":"playback_state","is_active":true,"status":"paused","volume":31,"item":` + playerTestItem + `,"context":{"uri":"spotify:playlist:Second","entity_type":"playlist"}}`, func(s State) bool {
			return s.Volume == 31 && s.PlaylistContext == "spotify:playlist:Second"
		}},
		{`{"type":"device_changed","is_active":false,"device_name":"Away"}`, func(s State) bool {
			return s.Device == "Away" && s.PlaylistContext == ""
		}},
		{`{"type":"device_changed","is_active":true,"device_name":"Living Room"}`, func(s State) bool {
			return s.Device == "Living Room" && s.PlaylistContext == ""
		}},
		{`{"type":"context_changed","context":{"uri":"spotify:playlist:Bad?","entity_type":"playlist"}}`, func(s State) bool {
			return s.PlaylistContext == "" && s.Track.Title == "Song"
		}},
		{`{"type":"position_sync","position":{"position_ms":1234}}`, func(s State) bool { return s.PositionMS == 1234 && s.Status == "paused" }},
		{`{"type":"volume_changed","volume":27}`, func(s State) bool { return s.Volume == 27 }},
		{`{"type":"device_changed","device_name":"Bedroom"}`, func(s State) bool { return s.Device == "Bedroom" }},
		{`{"type":"error","message":"arbitrary server error"}`, func(s State) bool { return s.Connected && s.Notice == "Soloist reported a playback error." }},
		{`{"type":"logout"}`, func(s State) bool {
			return !s.Connected && s.Notice != "" && s.Status == "" && s.Track == (Track{}) && len(s.Queue) == 0 && s.PositionMS == 0
		}},
	}
	server := playerTestServer(t, func(ctx context.Context, conn *websocket.Conn) {
		close(upgraded)
		select {
		case <-auth:
		case <-ctx.Done():
			return
		}
		if !playerTestSend(t, ctx, conn, `{"type":"auth_state","logged_in":true,"device_name":"Living Room"}`) ||
			!playerTestRead(t, ctx, conn, `{"type":"command","command":"get_state"}`) || !playerTestRead(t, ctx, conn, `{"type":"command","command":"get_queue","limit":10}`) {
			return
		}
		for i, step := range steps {
			if !playerTestSend(t, ctx, conn, step.event) {
				return
			}
			select {
			case <-next:
			case <-ctx.Done():
				return
			}
			if i == 0 {
				for _, cmd := range []string{`{"type":"command","command":"play"}`, `{"type":"command","command":"pause"}`, `{"type":"command","command":"skip_next"}`, `{"type":"command","command":"skip_prev"}`, `{"type":"command","command":"set_volume","volume":100}`, `{"type":"command","command":"set_volume","volume":0}`, `{"type":"command","command":"play","uri":"spotify:track:AbC123"}`, `{"type":"command","command":"play","uri":"spotify:album:AbC123"}`, `{"type":"command","command":"play","uri":"spotify:playlist:AbC123"}`, `{"type":"command","command":"play","uri":"spotify:episode:AbC123"}`} {
					if !playerTestRead(t, ctx, conn, cmd) {
						return
					}
					if strings.Contains(cmd, `"command":"play"`) && !playerTestSend(t, ctx, conn, `{"type":"command_result","command":"play"}`) {
						return
					}
				}
			}
		}
		close(complete)
		_, _, _ = conn.Read(ctx)
	})
	playerTestMarkers(t, dir, server.URL)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	states, commands := NewStream(ctx, func() (string, error) { return dir, nil })
	select {
	case <-upgraded:
	case <-time.After(5 * time.Second):
		t.Fatal("no connection to fixture")
	}
	playerTestCommand(t, commands, Command{Name: "play"})
	close(auth)
	for i, step := range steps {
		state := playerTestState(t, states, step.match)
		select {
		case next <- struct{}{}:
		case <-time.After(5 * time.Second):
			t.Fatal("fixture did not advance")
		}
		if i == 0 {
			for _, cmd := range []Command{{Name: "play"}, {Name: "pause"}, {Name: "skip_next"}, {Name: "skip_prev"}, {Name: "set_volume", Volume: 110}, {Name: "set_volume", Volume: -1},
				{Name: "play", URI: "https://open.spotify.com/track/AbC123"},
				{Name: "play", URI: "spotify:artist:AbC123"},
				{Name: "play", URI: "spotify:track:"},
				{Name: "play", URI: "spotify:track:AbC123\n"},
				{Name: "play", URI: "spotify:track:AbC123?x=1"},
				{Name: "pause", URI: "spotify:track:AbC123"},
				{Name: "play", URI: "spotify:track:AbC123"},
				{Name: "play", URI: "spotify:album:AbC123"},
				{Name: "play", URI: "spotify:playlist:AbC123"},
				{Name: "play", URI: "spotify:episode:AbC123"},
			} {
				cmd.SessionID = state.SessionID
				playerTestCommand(t, commands, cmd)
			}
		}
	}
	select {
	case <-complete:
	case <-time.After(5 * time.Second):
		t.Fatal("fixture not complete")
	}
	cancel()
	stop := time.After(time.Second)
	for {
		select {
		case _, ok := <-states:
			if !ok {
				return
			}
		case <-stop:
			t.Fatal("states did not close after cancellation")
		}
	}
}

func TestPlaylistShuffleSwitchover(t *testing.T) {
	const (
		playlist = "spotify:playlist:Target"
		play = `{"type":"command","command":"play","uri":"spotify:playlist:Target"}`
		match = `{"type":"context_changed","context":{"uri":"spotify:playlist:Target","entity_type":"playlist"}}`
		other = `{"type":"context_changed","context":{"uri":"spotify:playlist:Other","entity_type":"playlist"}}`
		ack = `{"type":"command_result","command":"play"}`
		shuffle = `{"type":"command","command":"set_shuffle","enabled":true}`
		probe = `{"type":"command","command":"set_volume","volume":12}`
		stateRequest = `{"type":"command","command":"get_state"}`
	)
	for _, tc := range []struct {
		name     string
		exchange []string
		barrier  PlaylistShuffleStatus
		command  Command
	}{
		{name: "initial context then target", exchange: []string{other, match, ack, ">" + shuffle, ">" + stateRequest}},
		{name: "ack before context", exchange: []string{ack, ">" + stateRequest, other, match, ">" + shuffle, ">" + stateRequest}},
		{name: "observed switchover away from target", exchange: []string{match, other, ack, match}},
		{name: "device loss cancels before a matching context", exchange: []string{
			`{"type":"device_changed","is_active":false,"device_name":"Away"}`,
			`{"type":"device_changed","is_active":true,"device_name":"Back"}`, match, ack,
		}},
		{
			name:     "replacement fences old confirmation and waits for both play results",
			exchange: []string{match, "!", `>{"type":"command","command":"play","uri":"spotify:playlist:Other"}`, match, ack, other, match, ack},
			barrier:  PlaylistShuffleWaiting,
			command:  Command{Name: "play_playlist_shuffle", URI: "spotify:playlist:Other"},
		},
		{
			name:     "standalone play cancels pending playlist",
			exchange: []string{match, "!", `>{"type":"command","command":"play","uri":"spotify:track:Single"}`, ack, match},
			barrier:  PlaylistShuffleWaiting,
			command:  Command{Name: "play", URI: "spotify:track:Single"},
		},

	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			done := make(chan bool, 1)
			ready := make(chan struct{})
			server := playerTestServer(t, func(ctx context.Context, conn *websocket.Conn) {
				if !playerTestSend(t, ctx, conn, `{"type":"auth_state","logged_in":true}`) ||
					!playerTestRead(t, ctx, conn, stateRequest) ||
					!playerTestRead(t, ctx, conn, `{"type":"command","command":"get_queue","limit":10}`) ||
					!playerTestRead(t, ctx, conn, play) {
					done <- false
					return
				}
				for _, step := range tc.exchange {
					if step == "!" {
						if !playerTestSend(t, ctx, conn, `{"type":"volume_changed","volume":40}`) {
							return
						}
						select {
						case <-ready:
						case <-ctx.Done():
							return
						}
						continue
					}
					var ok bool
					if want, read := strings.CutPrefix(step, ">"); read {
						ok = playerTestRead(t, ctx, conn, want)
					} else {
						ok = playerTestSend(t, ctx, conn, step)
					}
					if !ok {
						done <- false
						return
					}
				}
				done <- playerTestSend(t, ctx, conn, `{"type":"volume_changed","volume":41}`) &&
					playerTestRead(t, ctx, conn, probe)
			})
			playerTestMarkers(t, dir, server.URL)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			states, commands := NewStream(ctx, func() (string, error) { return dir, nil })
			state := playerTestState(t, states, func(s State) bool { return s.Connected })
			playerTestCommand(t, commands, Command{Name: "play_playlist_shuffle", URI: playlist, SessionID: state.SessionID})
			if tc.barrier != PlaylistShuffleIdle {
				playerTestState(t, states, func(s State) bool { return s.Volume == 40 && s.PlaylistShuffle == tc.barrier })
				if cmd := tc.command; cmd.Name != "" {
					cmd.SessionID = state.SessionID
					playerTestCommand(t, commands, cmd)
				}
				close(ready)
			}
			state = playerTestState(t, states, func(s State) bool { return s.Volume == 41 })
			playerTestCommand(t, commands, Command{Name: "set_volume", Volume: 12, SessionID: state.SessionID})
			select {
			case ok := <-done:
				if !ok {
					t.Fatal("playlist exchange failed")
				}
			case <-time.After(5 * time.Second):
				t.Fatal("playlist exchange did not finish")
			}
		})
	}
}

func TestPlaylistShuffleReconnectsAfterUnansweredPlay(t *testing.T) {
	const (
		stateRequest = `{"type":"command","command":"get_state"}`
		queueRequest = `{"type":"command","command":"get_queue","limit":10}`
		oldPlay      = `{"type":"command","command":"play","uri":"spotify:playlist:Old"}`
		legacyPlay   = `{"type":"command","command":"play","uri":"spotify:track:Legacy"}`
		newPlay      = `{"type":"command","command":"play","uri":"spotify:playlist:Fresh"}`
		playlist     = `{"uri":"spotify:playlist:Fresh","entity_type":"playlist"}`
	)
	for _, tc := range []struct {
		name, command, event, notice string
		unconfirmed                  bool
	}{
		{name: "legacy error", command: "play", event: `{"type":"error","message":"rejected"}`, notice: playerErrorNotice},
		{name: "legacy logout", command: "play", event: `{"type":"logout"}`, notice: playerPairNotice},
		{name: "legacy authentication lost", command: "play", event: `{"type":"auth_state","logged_in":false}`, notice: playerPairNotice},
		{name: "targeted error", command: PlayPlaylistShuffle, event: `{"type":"error","message":"rejected"}`, notice: playerErrorNotice},
		{name: "targeted timeout", command: PlayPlaylistShuffle, notice: playerRetryNotice, unconfirmed: true},
		{name: "targeted logout", command: PlayPlaylistShuffle, event: `{"type":"logout"}`, notice: playerPairNotice},
		{name: "targeted authentication lost", command: PlayPlaylistShuffle, event: `{"type":"auth_state","logged_in":false}`, notice: playerPairNotice},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			var accepted atomic.Int32
			freshAuth := make(chan struct{})
			legacyReauth := make(chan struct{})
			legacy := tc.command == "play"
			server := playerTestServer(t, func(ctx context.Context, conn *websocket.Conn) {
				socket := accepted.Add(1)
				if socket == 2 {
					select {
					case <-freshAuth:
					case <-ctx.Done():
						return
					}
				}
				if socket > 2 || !playerTestSend(t, ctx, conn, `{"type":"auth_state","logged_in":true}`) ||
					!playerTestRead(t, ctx, conn, stateRequest) || !playerTestRead(t, ctx, conn, queueRequest) {
					return
				}
				if socket == 1 {
					wantPlay := oldPlay
					if legacy {
						wantPlay = legacyPlay
					}
					if !playerTestRead(t, ctx, conn, wantPlay) || tc.event != "" && !playerTestSend(t, ctx, conn, tc.event) {
						return
					}
					if legacy {
						if tc.notice == playerPairNotice {
							select {
							case <-legacyReauth:
							case <-ctx.Done():
								return
							}
							if !playerTestSend(t, ctx, conn, `{"type":"auth_state","logged_in":true}`) ||
								!playerTestRead(t, ctx, conn, stateRequest) || !playerTestRead(t, ctx, conn, queueRequest) {
								return
							}
						}
						if !playerTestRead(t, ctx, conn, `{"type":"command","command":"set_volume","volume":21}`) ||
							!playerTestSend(t, ctx, conn, `{"type":"command_result","command":"play"}`) ||
							!playerTestSend(t, ctx, conn, `{"type":"volume_changed","volume":21}`) {
							return
						}
					}
					if _, data, err := conn.Read(ctx); err == nil || ctx.Err() != nil {
						t.Errorf("ambiguous socket was not retired; frame %q, error %v", data, err)
					}
					return
				}
				if !playerTestRead(t, ctx, conn, newPlay) ||
					!playerTestSend(t, ctx, conn, `{"type":"context_changed","context":`+playlist+`}`) ||
					!playerTestSend(t, ctx, conn, `{"type":"command_result","command":"play"}`) ||
					!playerTestRead(t, ctx, conn, `{"type":"command","command":"set_shuffle","enabled":true}`) ||
					!playerTestRead(t, ctx, conn, stateRequest) ||
					!playerTestSend(t, ctx, conn, `{"type":"playback_state","context":`+playlist+`,"is_active":true,"options":{"shuffle":true}}`) {
					return
				}
				_, _, _ = conn.Read(ctx)
			})
			playerTestMarkers(t, dir, server.URL)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			states, commands := NewStream(ctx, func() (string, error) { return dir, nil })
			old := playerTestState(t, states, func(s State) bool { return s.Connected })
			uri := "spotify:playlist:Old"
			if legacy {
				uri = "spotify:track:Legacy"
			}
			playerTestCommand(t, commands, Command{Name: tc.command, URI: uri, SessionID: old.SessionID})
			if legacy {
				if tc.notice == playerErrorNotice {
					playerTestState(t, states, func(s State) bool {
						return s.Connected && s.SessionID == old.SessionID && s.Notice == playerErrorNotice
					})
				} else {
					playerTestState(t, states, func(s State) bool { return !s.Connected && s.Notice == playerPairNotice })
					close(legacyReauth)
					old = playerTestState(t, states, func(s State) bool { return s.Connected && s.SessionID != old.SessionID })
				}
				playerTestCommand(t, commands, Command{Name: "set_volume", Volume: 21, SessionID: old.SessionID})
				playerTestState(t, states, func(s State) bool { return s.Connected && s.SessionID == old.SessionID && s.Volume == 21 })
				playerTestCommand(t, commands, Command{Name: PlayPlaylistShuffle, URI: "spotify:playlist:Old", SessionID: old.SessionID})
				playerTestState(t, states, func(s State) bool {
					return !s.Connected && s.Notice == playerRetryNotice && s.PlaylistShuffle == PlaylistShuffleUnconfirmed
				})
			} else {
				playerTestState(t, states, func(s State) bool {
					return !s.Connected && s.Notice == tc.notice && (s.PlaylistShuffle == PlaylistShuffleUnconfirmed) == tc.unconfirmed
				})
			}
			close(freshAuth)
			fresh := playerTestState(t, states, func(s State) bool { return s.Connected && s.SessionID != old.SessionID })
			playerTestCommand(t, commands, Command{Name: PlayPlaylistShuffle, URI: "spotify:playlist:Old", SessionID: old.SessionID})
			playerTestCommand(t, commands, Command{Name: PlayPlaylistShuffle, URI: "spotify:playlist:Fresh", SessionID: fresh.SessionID})
			playerTestState(t, states, func(s State) bool {
				return s.SessionID == fresh.SessionID && s.PlaylistContext == "spotify:playlist:Fresh" && s.OptionsKnown && s.Shuffle
			})
		})
	}
}

func TestPlayerTransportReconnect(t *testing.T) {
	firstDir, secondDir := t.TempDir(), t.TempDir()
	var current atomic.Bool
	fail, nextAuth := make(chan struct{}), make(chan struct{})
	baseHandler := func(device string, auth, gate <-chan struct{}) func(context.Context, *websocket.Conn) {
		return func(ctx context.Context, conn *websocket.Conn) {
			if auth != nil {
				select {
				case <-auth:
				case <-ctx.Done():
					return
				}
			}
			if !playerTestSend(t, ctx, conn, `{"type":"auth_state","logged_in":true,"device_name":"`+device+`"}`) ||
				!playerTestRead(t, ctx, conn, `{"type":"command","command":"get_state"}`) || !playerTestRead(t, ctx, conn, `{"type":"command","command":"get_queue","limit":10}`) {
				return
			}
			if gate == nil {
				playerTestRead(t, ctx, conn, `{"type":"command","command":"skip_prev"}`)
				return
			}
			select {
			case <-gate:
			case <-ctx.Done():
			}
		}
	}
	first := playerTestServer(t, baseHandler("old", nil, fail))
	second := playerTestServer(t, baseHandler("new", nextAuth, nil))
	playerTestMarkers(t, firstDir, first.URL)
	playerTestMarkers(t, secondDir, second.URL)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	states, commands := NewStream(ctx, func() (string, error) {
		if current.Load() {
			return secondDir, nil
		}
		return firstDir, nil
	})
	oldView := playerTestState(t, states, func(s State) bool { return s.Connected && s.Device == "old" })
	current.Store(true)
	close(fail)
	playerTestState(t, states, func(s State) bool { return !s.Connected && s.Device == "" && s.Notice != "" })
	// The command channel still holds no usable session, so it is discarded.
	playerTestCommand(t, commands, Command{Name: "play"})
	close(nextAuth)
	newView := playerTestState(t, states, func(s State) bool { return s.Connected && s.Device == "new" })
	// A command bound to the previous session must not reach the replacement.
	playerTestCommand(t, commands, Command{Name: "play", URI: "spotify:track:AbC123", SessionID: oldView.SessionID})
	playerTestCommand(t, commands, Command{Name: "skip_prev", SessionID: newView.SessionID})
	playerTestState(t, states, func(s State) bool { return !s.Connected && s.Notice != "" })
}

func TestPlayerTransportRejectsRedirect(t *testing.T) {
	var redirects atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { redirects.Add(1) }))
	t.Cleanup(target.Close)
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL, http.StatusFound)
	}))
	t.Cleanup(origin.Close)
	dir := t.TempDir()
	playerTestMarkers(t, dir, origin.URL)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	states, _ := NewStream(ctx, func() (string, error) { return dir, nil })
	playerTestState(t, states, func(s State) bool { return !s.Connected && s.Notice != "" })
	if got := redirects.Load(); got != 0 {
		t.Fatalf("followed %d redirects", got)
	}
}

func TestPlayerTransportRejectsUnsafeFrames(t *testing.T) {
	for _, tc := range []struct {
		name string
		kind websocket.MessageType
		body string
	}{
		{"binary", websocket.MessageBinary, `{"type":"volume_changed","volume":30}`},
		{"oversized text", websocket.MessageText, `{"type":"volume_changed","volume":30,"ignored":"` + strings.Repeat("x", 1<<20) + `"}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			send := make(chan struct{})
			server := playerTestServer(t, func(ctx context.Context, conn *websocket.Conn) {
				if !playerTestSend(t, ctx, conn, `{"type":"auth_state","logged_in":true,"device_name":"Desk"}`) ||
					!playerTestRead(t, ctx, conn, `{"type":"command","command":"get_state"}`) || !playerTestRead(t, ctx, conn, `{"type":"command","command":"get_queue","limit":10}`) {
					return
				}
				select {
				case <-send:
				case <-ctx.Done():
					return
				}
				_ = conn.Write(ctx, tc.kind, []byte(tc.body))
				_, _, _ = conn.Read(ctx)
			})
			playerTestMarkers(t, dir, server.URL)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			states, _ := NewStream(ctx, func() (string, error) { return dir, nil })
			playerTestState(t, states, func(s State) bool { return s.Connected })
			close(send)
			playerTestState(t, states, func(s State) bool {
				if s.Connected && s.Volume == 30 {
					t.Error("unsafe frame updated the volume")
				}
				return !s.Connected && s.Volume == 0 && s.Notice != ""
			})
		})
	}
}

func TestPauseSoloistOnExit(t *testing.T) {
	dir := t.TempDir()
	got := make(chan bool, 1)
	server := playerTestServer(t, func(ctx context.Context, conn *websocket.Conn) {
		if !playerTestSend(t, ctx, conn, `{"type":"auth_state","logged_in":true,"device_name":"Desk"}`) {
			return
		}
		got <- playerTestRead(t, ctx, conn, `{"type":"command","command":"pause"}`)
	})
	playerTestMarkers(t, dir, server.URL)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	Pause(ctx, func() (string, error) { return dir, nil })
	select {
	case ok := <-got:
		if !ok {
			t.Fatal("Soloist did not receive pause on exit")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Pause did not send a command")
	}
}

// TestPlayerTransportOptionsSeekAndModes drives Soloist's transport options,
// anchored positions and the seek/shuffle/repeat command frames.
func TestPlayerTransportOptionsSeekAndModes(t *testing.T) {
	dir := t.TempDir()
	next, complete := make(chan struct{}), make(chan struct{})
	var anchored time.Time
	steps := []struct {
		event string
		match func(State) bool
		send  []Command
		want  []string
		// delay keeps consecutive anchors distinguishable on coarse clocks.
		delay time.Duration
	}{
		{
			// A full snapshot carries the position, its speed and the modes.
			event: `{"type":"playback_state","item":` + playerTestItem + `,"status":"playing","position":{"position_ms":1000,"speed":1.5},"options":{"shuffle":true,"repeat":"context"},"volume":40}`,
			match: func(s State) bool {
				ok := s.Connected && s.Shuffle && s.Repeat == "context" && s.Speed == 1.5 && s.PositionMS == 1000 && !s.AnchorAt.IsZero() && s.Volume == 40
				anchored = s.AnchorAt
				return ok
			},
			send: []Command{
				{Name: "seek", PositionMS: 12345},
				{Name: "seek", PositionMS: -5000},
				{Name: "set_shuffle", Shuffle: true},
				{Name: "set_shuffle", Shuffle: false},
			},
			want: []string{
				`{"type":"command","command":"seek","position_ms":12345}`,
				`{"type":"command","command":"seek","position_ms":0}`,
				`{"type":"command","command":"set_shuffle","enabled":true}`,
				`{"type":"command","command":"set_shuffle","enabled":false}`,
			},
		},
		{
			// Options travel on their own event; each repeat mode is two frames.
			event: `{"type":"options_changed","options":{"shuffle":false,"repeat":"track"}}`,
			match: func(s State) bool { return !s.Shuffle && s.Repeat == "track" },
			send: []Command{
				{Name: "set_repeat", Repeat: "off"},
				{Name: "set_repeat", Repeat: "context"},
				{Name: "set_repeat", Repeat: "track"},
				{Name: "set_repeat", Repeat: "bogus"},
				{Name: "set_repeat", Repeat: ""},
				{Name: "set_shuffle", Shuffle: true},
			},
			want: []string{
				`{"type":"command","command":"set_repeat_track","enabled":false}`,
				`{"type":"command","command":"set_repeat_context","enabled":false}`,
				`{"type":"command","command":"set_repeat_track","enabled":false}`,
				`{"type":"command","command":"set_repeat_context","enabled":true}`,
				`{"type":"command","command":"set_repeat_context","enabled":false}`,
				`{"type":"command","command":"set_repeat_track","enabled":true}`,
				`{"type":"command","command":"set_shuffle","enabled":true}`,
			},
		},
		{
			// position_sync is the periodic correction; it reports 0 while paused.
			event: `{"type":"position_sync","position":{"position_ms":5000,"speed":0}}`,
			match: func(s State) bool {
				if s.PositionMS != 5000 || s.Speed != 0 {
					return false
				}
				ok := s.AnchorAt.After(anchored)
				anchored = s.AnchorAt
				return ok
			},
			send:  []Command{{Name: "seek", PositionMS: 5000}},
			want:  []string{`{"type":"command","command":"seek","position_ms":5000}`},
			delay: 5 * time.Millisecond,
		},
		{
			// A new track resets the position and the anchor, and a mode value
			// outside the three is ignored instead of guessed.
			event: `{"type":"track_changed","item":{"decorations":{"identity":{"name":"Next"}}},"options":{"repeat":"bogus"}}`,
			match: func(s State) bool {
				if s.Track.Title != "Next" || s.PositionMS != 0 || s.Repeat != "track" {
					return false
				}
				ok := s.AnchorAt.After(anchored)
				anchored = s.AnchorAt
				return ok
			},
			// A keypress bound to an older session must not reach this socket.
			send: []Command{
				{Name: "seek", PositionMS: 9999, SessionID: 1 << 40},
				{Name: "set_shuffle", Shuffle: true},
			},
			want:  []string{`{"type":"command","command":"set_shuffle","enabled":true}`},
			delay: 5 * time.Millisecond,
		},
	}
	server := playerTestServer(t, func(ctx context.Context, conn *websocket.Conn) {
		if !playerTestSend(t, ctx, conn, `{"type":"auth_state","logged_in":true,"device_name":"Desk"}`) ||
			!playerTestRead(t, ctx, conn, `{"type":"command","command":"get_state"}`) ||
			!playerTestRead(t, ctx, conn, `{"type":"command","command":"get_queue","limit":10}`) {
			return
		}
		for _, step := range steps {
			if step.delay > 0 {
				time.Sleep(step.delay)
			}
			if !playerTestSend(t, ctx, conn, step.event) {
				return
			}
			select {
			case <-next:
			case <-ctx.Done():
				return
			}
			for _, want := range step.want {
				if !playerTestRead(t, ctx, conn, want) {
					return
				}
			}
		}
		close(complete)
		_, _, _ = conn.Read(ctx)
	})
	playerTestMarkers(t, dir, server.URL)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	states, commands := NewStream(ctx, func() (string, error) { return dir, nil })
	for _, step := range steps {
		state := playerTestState(t, states, step.match)
		for _, command := range step.send {
			if command.SessionID == 0 {
				command.SessionID = state.SessionID
			}
			playerTestCommand(t, commands, command)
		}
		select {
		case next <- struct{}{}:
		case <-time.After(5 * time.Second):
			t.Fatal("fixture did not advance")
		}
	}
	select {
	case <-complete:
	case <-time.After(5 * time.Second):
		t.Fatal("fixture not complete")
	}
}
