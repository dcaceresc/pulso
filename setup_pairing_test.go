package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/coder/websocket"
)

const setupTestLoggedOut = `{"type":"auth_state","logged_in":false}`
const setupTestLoggedIn = `{"type":"auth_state","logged_in":true}`

func setupPairFixture(t *testing.T, ctx context.Context, handle func(context.Context, *websocket.Conn, int32)) (string, *atomic.Int32) {
	t.Helper()
	var count atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, nil)
		if err == nil {
			defer conn.CloseNow()
			handle(ctx, conn, count.Add(1))
		}
	}))
	t.Cleanup(server.Close)
	return server.URL, &count
}

func setupPairWrite(ctx context.Context, conn *websocket.Conn, event string) {
	_ = conn.Write(ctx, websocket.MessageText, []byte(event))
}

func setupPairModel(t *testing.T, ctx context.Context, local *localSetup) (tea.Model, *int) {
	t.Helper()
	var players int
	return newSetupModel(ctx, setupService{
		inspect: local.inspect,
		watch:   local.watchPairing,
		start:   func(context.Context, string) error { t.Error("pairing must not start Soloist"); return nil },
		player:  func(int, int) tea.Model { players++; return setupTestPlayer{} },
	}), &players
}

func setupPairMessage(t *testing.T, ctx context.Context, cmd tea.Cmd) tea.Msg {
	t.Helper()
	if cmd == nil {
		t.Fatal("expected a pairing command")
	}
	messages := make(chan tea.Msg, 1)
	go func() { messages <- cmd() }()
	select {
	case msg := <-messages:
		return msg
	case <-ctx.Done():
		t.Fatal("timed out waiting for pairing command")
		return nil
	}
}

func TestLocalSetupPairingAuthHandoff(t *testing.T) {
	for _, scenario := range []string{"already paired", "watch sees initial true", "watch sees false then true"} {
		t.Run(scenario, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			showRenamed, pair := make(chan struct{}), make(chan struct{})
			url, count := setupPairFixture(t, ctx, func(ctx context.Context, conn *websocket.Conn, attempt int32) {
				switch {
				case scenario == "already paired" || scenario == "watch sees initial true" && attempt == 2:
					setupPairWrite(ctx, conn, setupTestLoggedIn)
				case attempt == 1:
					setupPairWrite(ctx, conn, setupTestLoggedOut)
				default:
					setupPairWrite(ctx, conn, `{"type":"auth_state","logged_in":false,"device_name":"\u001b[31mSala\u001b[0m\n音\u202e"}`)
					select {
					case <-showRenamed:
						setupPairWrite(ctx, conn, `{"type":"logged_out"}`)
						setupPairWrite(ctx, conn, `{"type":"other","logged_in":true}`)
						setupPairWrite(ctx, conn, `{"type":"auth_state","logged_in":false,"device_name":"Kitchen"}`)
					case <-ctx.Done():
						return
					}
					select {
					case <-pair:
						setupPairWrite(ctx, conn, setupTestLoggedIn)
					case <-ctx.Done():
					}
				}
			})
			dir := t.TempDir()
			setupFixtureEndpoint(t, dir, url)
			model, players := setupPairModel(t, ctx, setupFixtureLocal(dir, "soloist"))
			model, cmd := runSetupCmd(t, model, model.Init())
			wantConnections := int32(1)
			if scenario != "already paired" {
				wantConnections++
				expectSetupText(t, model.View(), "Waiting for pairing", "Pulso or your existing Soloist device name")
				if *players != 0 {
					t.Fatal("probe opened the player before pairing")
				}
				model, cmd = model.Update(setupPairMessage(t, ctx, cmd))
				if scenario == "watch sees false then true" {
					view := model.View()
					expectSetupText(t, view, "Waiting for pairing", "Device: Sala 音", "Enter: recheck | Esc: quit")
					if strings.ContainsAny(view, "\x1b\u202e") || *players != 0 {
						t.Fatal("unsafe advertised name or premature player handoff")
					}
					close(showRenamed)
					model, cmd = model.Update(setupPairMessage(t, ctx, cmd))
					expectSetupText(t, model.View(), "Waiting for pairing", "Device: Kitchen")
					if *players != 0 {
						t.Fatal("an unrelated event must not confirm pairing")
					}
					close(pair)
					model, cmd = model.Update(setupPairMessage(t, ctx, cmd))
				}
			}
			if model.View() != "Now Playing" || *players != 1 || cmd == nil || cmd() != "player initialized" || count.Load() != wantConnections {
				t.Fatalf("handoff: view=%q players=%d connections=%d", model.View(), *players, count.Load())
			}
		})
	}
}

func TestLocalSetupPairingReinspectsAfterDisconnect(t *testing.T) {
	for _, stopped := range []bool{false, true} {
		name := "new endpoint"
		if stopped {
			name = "stopped with stale markers"
		}
		t.Run(name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 6*time.Second)
			defer cancel()
			dir := t.TempDir()
			drop := make(chan struct{})
			pair := make(chan struct{})
			oldURL, oldCount := setupPairFixture(t, ctx, func(ctx context.Context, conn *websocket.Conn, attempt int32) {
				setupPairWrite(ctx, conn, setupTestLoggedOut)
				if attempt == 2 {
					select {
					case <-drop:
					case <-ctx.Done():
					}
				}
			})
			setupFixtureEndpoint(t, dir, oldURL)
			model, players := setupPairModel(t, ctx, setupFixtureLocal(dir, "soloist"))
			model, cmd := runSetupCmd(t, model, model.Init())
			model, cmd = model.Update(setupPairMessage(t, ctx, cmd))
			var targetCount *atomic.Int32
			if stopped {
				// Leave markers pointing at a closed listener when the session drops.
				unavailable := httptest.NewServer(http.NotFoundHandler())
				setupFixtureEndpoint(t, dir, unavailable.URL)
				unavailable.Close()
			} else {
				url, count := setupPairFixture(t, ctx, func(ctx context.Context, conn *websocket.Conn, attempt int32) {
					setupPairWrite(ctx, conn, setupTestLoggedOut)
					if attempt == 2 {
						select {
						case <-pair:
							setupPairWrite(ctx, conn, setupTestLoggedIn)
						case <-ctx.Done():
						}
					}
				})
				targetCount = count
				setupFixtureEndpoint(t, dir, url)
			}
			started := time.Now()
			close(drop)
			model, cmd = model.Update(setupPairMessage(t, ctx, cmd))
			expectSetupText(t, model.View(), "Reconnecting to Soloist", "Enter: recheck | Esc: quit")
			model, cmd = model.Update(setupPairMessage(t, ctx, cmd))
			if elapsed := time.Since(started); elapsed < 250*time.Millisecond || oldCount.Load() != 2 || *players != 0 {
				t.Fatalf("retry: elapsed=%v old connections=%d player calls=%d", elapsed, oldCount.Load(), *players)
			}
			if stopped {
				expectSetupView(t, model, "Soloist stopped. Start it again, then press Enter to recheck.", "Enter: recheck | Esc: quit")
				if cmd != nil {
					t.Fatal("stopped daemon kept retrying")
				}
				for _, name := range []string{"ws.addr", "ws.port"} {
					if _, err := os.Stat(filepath.Join(dir, name)); err != nil {
						t.Fatalf("runtime marker changed: %v", err)
					}
				}
				return
			}
			expectSetupText(t, model.View(), "Waiting for pairing")
			close(pair)
			model, _ = model.Update(setupPairMessage(t, ctx, cmd))
			if model.View() != "Now Playing" || *players != 1 || targetCount.Load() != 2 {
				t.Fatalf("recovery: view=%q players=%d new connections=%d", model.View(), *players, targetCount.Load())
			}
		})
	}
}

func TestLocalSetupPairingEscapeClosesSocket(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	closed := make(chan struct{})
	url, count := setupPairFixture(t, ctx, func(ctx context.Context, conn *websocket.Conn, attempt int32) {
		setupPairWrite(ctx, conn, setupTestLoggedOut)
		if attempt == 2 {
			defer close(closed)
			_, _, _ = conn.Read(ctx)
		}
	})
	dir := t.TempDir()
	setupFixtureEndpoint(t, dir, url)
	model, players := setupPairModel(t, ctx, setupFixtureLocal(dir, "soloist"))
	model, cmd := runSetupCmd(t, model, model.Init())
	model, _ = model.Update(setupPairMessage(t, ctx, cmd))
	model, cmd = model.Update(tea.KeyMsg{Type: tea.KeyEsc})
	select {
	case <-closed:
	case <-ctx.Done():
		t.Fatal("Esc did not close the pairing socket before quitting")
	}
	if _, ok := cmd().(tea.QuitMsg); !ok || model.View() != "" || *players != 0 || count.Load() != 2 {
		t.Fatal("Esc should quit without reconnecting or opening the player")
	}
}

func TestSetupManualPairingCheckCancelsBeforeReplacingWatcher(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	firstStarted, firstCanceled, firstFinished := make(chan struct{}), make(chan struct{}), make(chan struct{})
	releaseFirst, secondStarted := make(chan struct{}), make(chan struct{})
	var watches atomic.Int32
	checks := 0
	model := newSetupModel(ctx, setupService{
		inspect: func(context.Context) (setupState, error) {
			checks++
			return setupAwaitPairing, nil
		},
		watch: func(watchCtx context.Context, _ func(string) bool) error {
			if watches.Add(1) == 1 {
				defer close(firstFinished)
				close(firstStarted)
				<-watchCtx.Done()
				close(firstCanceled)
				select {
				case <-releaseFirst:
				case <-ctx.Done():
				}
			} else {
				select {
				case <-firstFinished:
				default:
					t.Error("replacement watcher overlapped its predecessor")
				}
				close(secondStarted)
				<-watchCtx.Done()
			}
			return watchCtx.Err()
		},
	})
	await := func(ch <-chan struct{}, what string) {
		t.Helper()
		select {
		case <-ch:
		case <-ctx.Done():
			t.Fatalf("timed out waiting for %s", what)
		}
	}
	model, _ = runSetupCmd(t, model, model.Init())
	await(firstStarted, "first watcher")
	model, cmd := model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	model, cmd = runSetupCmd(t, model, cmd)
	if checks != 2 || cmd == nil {
		t.Fatal("Enter should check immediately and resume waiting if still unpaired")
	}
	await(firstCanceled, "first watcher cancellation")
	if watches.Load() != 1 {
		t.Fatal("replacement watcher started before the canceled watcher finished")
	}
	close(releaseFirst)
	await(secondStarted, "replacement watcher")
}

func TestSetupManualPairingCheckFencesOldWatcher(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	inspections, players := 0, 0
	model := newSetupModel(ctx, setupService{
		inspect: func(context.Context) (setupState, error) {
			inspections++
			if inspections < 3 {
				return setupAwaitPairing, nil
			}
			return setupReady, nil
		},
		watch: func(context.Context, func(string) bool) error { return nil },
		player: func(int, int) tea.Model {
			players++
			return setupTestPlayer{}
		},
	})
	model, cmd := runSetupCmd(t, model, model.Init())
	stale := setupPairMessage(t, ctx, cmd)
	model, cmd = model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	model, _ = model.Update(stale)
	if players != 0 || inspections != 1 {
		t.Fatal("stale watcher won the race against Enter")
	}
	model, cmd = runSetupCmd(t, model, cmd)
	model, _ = model.Update(stale)
	if players != 0 || inspections != 2 || cmd == nil {
		t.Fatal("stale watcher was applied to a new pairing session")
	}
	model, cmd = model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	oldModel := model
	model, cmd = runSetupCmd(t, model, cmd)
	oldModel.Update(stale)
	if players != 1 || inspections != 3 || model.View() != "Now Playing" || cmd == nil || cmd() != "player initialized" {
		t.Fatal("manual recheck did not open the player exactly once")
	}
}
