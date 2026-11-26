package main

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

func playerTestState(t *testing.T, states <-chan playerState, want func(playerState) bool) playerState {
	t.Helper()
	timer := time.NewTimer(5 * time.Second)
	defer timer.Stop()
	var last playerState
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

func playerTestCommand(t *testing.T, commands chan<- playerCommand, command playerCommand) {
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
		match func(playerState) bool
	}{
		{`{"type":"playback_state","item":` + playerTestItem + `,"status":"playing","position":{"position_ms":4200},"volume":55}`, func(s playerState) bool {
			return s.Connected && s.Device == "Living Room" && s.Status == "playing" && s.PositionMS == 4200 && s.Volume == 55 && s.Track == (playerTrack{Title: "Song", Artist: "First, Second", Album: "Album", DurationMS: 190000})
		}},
		{`{"type":"queue_changed","upcoming":[{"item":` + playerTestItem + `}]}`, func(s playerState) bool { return len(s.Queue) == 1 && s.Queue[0] == s.Track }},
		{`{"type":"track_changed","item":{"decorations":{"identity":{"name":"Next\u001b"}}}}`, func(s playerState) bool { return s.Track.Title == "Next" && s.PositionMS == 0 && s.Status == "playing" }},
		{`{"type":"playback_changed","status":"paused","position":{"position_ms":700}}`, func(s playerState) bool {
			return s.Status == "paused" && s.PositionMS == 700 && s.Track.Title == "Next"
		}},
		{`{"type":"position_sync","position":{"position_ms":1234}}`, func(s playerState) bool { return s.PositionMS == 1234 && s.Status == "paused" }},
		{`{"type":"volume_changed","volume":27}`, func(s playerState) bool { return s.Volume == 27 }},
		{`{"type":"device_changed","device_name":"Bedroom"}`, func(s playerState) bool { return s.Device == "Bedroom" }},
		{`{"type":"error","message":"arbitrary server error"}`, func(s playerState) bool { return s.Connected && s.Notice == "Soloist reported a playback error." }},
		{`{"type":"logout"}`, func(s playerState) bool {
			return !s.Connected && s.Notice != "" && s.Status == "" && s.Track == (playerTrack{}) && len(s.Queue) == 0 && s.PositionMS == 0
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
				for _, cmd := range []string{`{"type":"command","command":"play"}`, `{"type":"command","command":"pause"}`, `{"type":"command","command":"skip_next"}`, `{"type":"command","command":"skip_prev"}`, `{"type":"command","command":"set_volume","volume":100}`, `{"type":"command","command":"set_volume","volume":0}`} {
					if !playerTestRead(t, ctx, conn, cmd) {
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
	states, commands := newPlayerStream(ctx, func() (string, error) { return dir, nil })
	select {
	case <-upgraded:
	case <-time.After(5 * time.Second):
		t.Fatal("no connection to fixture")
	}
	playerTestCommand(t, commands, playerCommand{Name: "play"})
	close(auth)
	for i, step := range steps {
		state := playerTestState(t, states, step.match)
		select {
		case next <- struct{}{}:
		case <-time.After(5 * time.Second):
			t.Fatal("fixture did not advance")
		}
		if i == 0 {
			for _, cmd := range []playerCommand{{Name: "get_state"}, {Name: "play"}, {Name: "pause"}, {Name: "skip_next"}, {Name: "skip_prev"}, {Name: "set_volume", Volume: 110}, {Name: "set_volume", Volume: -1}} {
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
	states, commands := newPlayerStream(ctx, func() (string, error) {
		if current.Load() {
			return secondDir, nil
		}
		return firstDir, nil
	})
	oldView := playerTestState(t, states, func(s playerState) bool { return s.Connected && s.Device == "old" })
	model := newPlayerModel(ctx, states, commands, 80, 24)
	model.Init()
	model, _ = model.Update(playerViewState{state: oldView, ok: true})
	current.Store(true)
	close(fail)
	playerTestState(t, states, func(s playerState) bool { return !s.Connected && s.Device == "" && s.Notice != "" })
	playerTestCommand(t, commands, playerCommand{Name: "play"})
	close(nextAuth)
	newView := playerTestState(t, states, func(s playerState) bool { return s.Connected && s.Device == "new" })
	// The replacement is ready while the UI still holds the previous view.
	model, _ = model.Update(setupRunes("n"))
	model, _ = model.Update(playerViewState{state: newView, ok: true})
	model.Update(setupRunes("p"))
	playerTestState(t, states, func(s playerState) bool { return !s.Connected && s.Notice != "" })
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
	states, _ := newPlayerStream(ctx, func() (string, error) { return dir, nil })
	playerTestState(t, states, func(s playerState) bool { return !s.Connected && s.Notice != "" })
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
			states, _ := newPlayerStream(ctx, func() (string, error) { return dir, nil })
			playerTestState(t, states, func(s playerState) bool { return s.Connected })
			close(send)
			playerTestState(t, states, func(s playerState) bool {
				if s.Connected && s.Volume == 30 {
					t.Error("unsafe frame updated the volume")
				}
				return !s.Connected && s.Volume == 0 && s.Notice != ""
			})
		})
	}
}
