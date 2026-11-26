package main

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/websocket"
)

func TestLocalSetupDataDir(t *testing.T) {
	root := t.TempDir()
	state, data, home, windows := filepath.Join(root, "state"), filepath.Join(root, "data"), filepath.Join(root, "home"), filepath.Join(root, "local")
	env := map[string]string{"STATE_DIRECTORY": state, "XDG_DATA_HOME": data, "HOME": home, "LOCALAPPDATA": windows}
	getenv := func(name string) string { return env[name] }
	for _, tc := range []struct{ platform, unset, want string }{
		{"windows", "", filepath.Join(windows, "soloist", "data")},
		{"linux", "", state},
		{"linux", "STATE_DIRECTORY", filepath.Join(data, "soloist")},
		{"linux", "XDG_DATA_HOME", filepath.Join(home, ".local", "share", "soloist")},
	} {
		delete(env, tc.unset)
		if got, err := soloistDataDir(tc.platform, getenv); err != nil || got != tc.want {
			t.Fatalf("%s directory = %q, %v; want %q", tc.platform, got, err, tc.want)
		}
	}
	delete(env, "HOME")
	if got, err := soloistDataDir("linux", getenv); got != "" || err == nil {
		t.Fatalf("missing default directory = %q, %v", got, err)
	}
}

func setupFixtureFile(t *testing.T, dir, name, value string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(value), 0600); err != nil {
		t.Fatal(err)
	}
}

func setupFixtureEndpoint(t *testing.T, dir, address string) {
	t.Helper()
	host, port, err := net.SplitHostPort(strings.TrimPrefix(address, "http://"))
	if err != nil {
		t.Fatal(err)
	}
	setupFixtureFile(t, dir, "ws.addr", host+"\n")
	setupFixtureFile(t, dir, "ws.port", port+"\n")
}

func setupFixtureWS(t *testing.T, event string) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer conn.CloseNow()
		if event == "" {
			// Never send the first event; wait for the client to time out.
			_, _, _ = conn.Read(r.Context())
			return
		}
		_ = conn.Write(r.Context(), websocket.MessageText, []byte(event))
	}))
	t.Cleanup(server.Close)
	return server
}

func setupFixtureLocal(dir, path string) *localSetup {
	return &localSetup{
		find:           func() (string, error) { return path, nil },
		dataDir:        func() (string, error) { return dir, nil },
		startupTimeout: 5 * time.Second,
		command:        exec.Command,
	}
}

func TestLocalSetupInspect(t *testing.T) {
	for _, tc := range []struct {
		name, binary, event, addr, port string
		marker, server, broken          bool
		want                            setupState
	}{
		{name: "missing", want: setupMissing},
		{name: "stopped", binary: "soloist", want: setupNeedsKey},
		{name: "paired existing", server: true, marker: true, event: `{"type":"auth_state","logged_in":true}`, want: setupReady},
		{name: "mobile pairing", server: true, binary: "soloist", event: `{"type":"auth_state","logged_in":false}`, want: setupAwaitPairing},
		{name: "stale pid", marker: true, broken: true},
		{name: "incomplete address", addr: "127.0.0.1", broken: true},
		{name: "reject hostname", addr: "localhost", port: "1234", broken: true},
		{name: "reject non-loopback", addr: "192.0.2.1", port: "1234", broken: true},
		{name: "reject zero port", addr: "127.0.0.1", port: "0", broken: true},
		{name: "reject overlarge port", addr: "127.0.0.1", port: "65536", broken: true},
		{name: "reject large address marker", addr: strings.Repeat(" ", 1024) + "127.0.0.1", port: "1234", broken: true},
		{name: "wrong first event", server: true, event: `{"type":"other","logged_in":true}`, broken: true},
		{name: "missing login", server: true, event: `{"type":"auth_state"}`, broken: true},
		{name: "non-boolean login", server: true, event: `{"type":"auth_state","logged_in":"true"}`, broken: true},
		{name: "large event", server: true, event: strings.Repeat(" ", 8192) + `{"type":"auth_state","logged_in":true}`, broken: true},
		{name: "silent daemon", server: true, broken: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			if tc.marker {
				setupFixtureFile(t, dir, "pid", "101\n")
			}
			if tc.server {
				setupFixtureEndpoint(t, dir, setupFixtureWS(t, tc.event).URL)
			}
			if tc.addr != "" {
				setupFixtureFile(t, dir, "ws.addr", tc.addr)
			}
			if tc.port != "" {
				setupFixtureFile(t, dir, "ws.port", tc.port)
			}
			local := setupFixtureLocal(dir, tc.binary)
			got, err := local.inspect(context.Background())
			if tc.broken {
				if err == nil || err.Error() != "Soloist runtime is unreachable or invalid. Run pulso doctor." {
					t.Fatalf("broken runtime: state=%v err=%v", got, err)
				}
			} else if err != nil || got != tc.want {
				t.Fatalf("inspect = %v, %v; want %v", got, err, tc.want)
			}
		})
	}
}

func TestLocalSetupHandshakeDoesNotRedirect(t *testing.T) {
	var initial, redirected atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/redirected" {
			redirected.Add(1)
			return
		}
		initial.Add(1)
		http.Redirect(w, r, "/redirected", http.StatusFound)
	}))
	t.Cleanup(server.Close)
	dir := t.TempDir()
	setupFixtureEndpoint(t, dir, server.URL)
	local := setupFixtureLocal(dir, "soloist")
	if _, err := local.inspect(context.Background()); err == nil || initial.Load() != 1 || redirected.Load() != 0 {
		t.Fatalf("inspect err=%v initial=%d redirected=%d", err, initial.Load(), redirected.Load())
	}
}

func TestLocalSetupStart(t *testing.T) {
	for _, tc := range []struct {
		name, mode, marker, wantError string
		calls                         int
	}{
		{name: "reuses reachable session", mode: "reuse"},
		{name: "pid appears after initial inspection", marker: "pid", wantError: "Soloist runtime is unreachable or invalid. Run pulso doctor."},
		{name: "stale alternate pid marker", marker: "soloist.pid", wantError: "Soloist runtime is unreachable or invalid. Run pulso doctor."},
		{name: "starts and waits for auth event", mode: "ready", calls: 1},
		{name: "early exit reaped", mode: "exit", wantError: "Soloist exited before it was ready. Run pulso doctor.", calls: 1},
		{name: "timeout kills and reaps owned child", mode: "wait", wantError: "Soloist did not become ready. Run pulso doctor.", calls: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			binary := filepath.Join(dir, "resolved executable")
			local := setupFixtureLocal(dir, binary)
			if tc.mode == "wait" {
				local.startupTimeout = 200 * time.Millisecond
			}
			if got, err := local.inspect(context.Background()); got != setupNeedsKey || err != nil {
				t.Fatalf("initial inspection = %v, %v", got, err)
			}
			if tc.marker != "" {
				setupFixtureFile(t, dir, tc.marker, "101\n")
			}
			endpoint := ""
			if tc.mode == "ready" || tc.mode == "reuse" {
				server := setupFixtureWS(t, `{"type":"auth_state","logged_in":false}`)
				endpoint = server.Listener.Addr().String()
				if tc.mode == "reuse" {
					setupFixtureEndpoint(t, dir, endpoint)
				}
			}
			self, err := os.Executable()
			if err != nil {
				t.Fatal(err)
			}
			const fakeKey = "fixture-only&not-a-key"
			calls := 0
			var child *exec.Cmd
			local.command = func(path string, args ...string) *exec.Cmd {
				calls++
				if want := []string{"--device-name", "Pulso", "--api-key", fakeKey, "--ws", "127.0.0.1:0", "--data-dir", dir}; path != binary || !reflect.DeepEqual(args, want) {
					t.Fatalf("unexpected launch: %q %q, want %q %q", path, args, binary, want)
				}
				child = exec.Command(self, "-test.run=^TestLocalSetupChild$", "--", tc.mode, dir, endpoint)
				t.Cleanup(func() {
					if child.Process != nil {
						_ = child.Process.Kill()
					}
				})
				return child
			}
			err = local.start(context.Background(), fakeKey)
			if (err == nil) != (tc.wantError == "") || err != nil && err.Error() != tc.wantError || calls != tc.calls {
				t.Fatalf("start err=%v, launches=%d; want %q, %d", err, calls, tc.wantError, tc.calls)
			}
			if child != nil && tc.wantError != "" && (child.ProcessState == nil || child.ProcessState.Success()) {
				t.Fatal("failed child was not reaped before start returned")
			}
			if tc.marker != "" {
				if data, err := os.ReadFile(filepath.Join(dir, tc.marker)); err != nil || string(data) != "101\n" {
					t.Fatalf("existing marker was changed: %q, %v", data, err)
				}
			}
			if tc.mode == "ready" || tc.mode == "reuse" {
				if got, err := local.inspect(context.Background()); got != setupAwaitPairing || err != nil {
					t.Fatalf("after start: %v, %v", got, err)
				}
			}
		})
	}
}

func TestLocalSetupChild(t *testing.T) {
	if len(os.Args) != 6 || os.Args[2] != "--" {
		return
	}
	switch os.Args[3] {
	case "exit":
		os.Stdout.WriteString("fake private daemon stdout")
		os.Stderr.WriteString("fake private daemon stderr")
		os.Exit(9)
	case "ready":
		setupFixtureEndpoint(t, os.Args[4], os.Args[5])
	}
	time.Sleep(time.Minute)
}
