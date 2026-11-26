package main

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

const setupKeyGuide = "Soloist is installed. To create your Soloist key:\n" +
	"1. Open https://developer.spotify.com/dashboard/soloist\n" +
	"2. Sign in with your Spotify Premium account.\n" +
	"3. Create a Soloist key and paste it below.\n\n"

func expectSetupView(t *testing.T, model tea.Model, body, hints string) {
	t.Helper()
	got := model.View()
	expectSetupBounds(t, got, 80, 24)
	expectSetupText(t, got, "Welcome to Pulso", hints)
	for _, line := range strings.Split(body, "\n") {
		if strings.HasPrefix(line, "Key: ") {
			found := false
			for _, rendered := range strings.Split(got, "\n") {
				if strings.TrimSpace(rendered) == strings.TrimSpace(line) {
					found = true
				}
			}
			if !found {
				t.Fatalf("missing exact masked input %q in view:\n%s", line, got)
			}
		} else if line != "" {
			expectSetupText(t, got, line)
		}
	}
}

func expectSetupBounds(t *testing.T, view string, width, height int) {
	t.Helper()
	lines := strings.Split(view, "\n")
	if len(lines) > height {
		t.Fatalf("view has %d lines, want at most %d", len(lines), height)
	}
	for _, line := range lines {
		if lipgloss.Width(line) > width {
			t.Fatalf("line exceeds %d columns: %q", width, line)
		}
	}
}

func expectSetupText(t *testing.T, view string, parts ...string) {
	t.Helper()
	for _, part := range parts {
		if !strings.Contains(view, part) {
			t.Errorf("view missing %q:\n%s", part, view)
		}
	}
}

func runSetupCmd(t *testing.T, model tea.Model, cmd tea.Cmd) (tea.Model, tea.Cmd) {
	t.Helper()
	if cmd == nil {
		t.Fatal("expected a command")
	}
	return model.Update(cmd())
}

func setupRunes(s string) tea.KeyMsg {
	return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)}
}

type setupTestPlayer struct{}

func (setupTestPlayer) Init() tea.Cmd                         { return func() tea.Msg { return "player initialized" } }
func (p setupTestPlayer) Update(tea.Msg) (tea.Model, tea.Cmd) { return p, nil }
func (setupTestPlayer) View() string                          { return "Now Playing" }

func TestSetupOpensPlayerWhenReady(t *testing.T) {
	for _, paired := range []bool{true, false} {
		t.Run(fmt.Sprint("already paired=", paired), func(t *testing.T) {
			calls := 0
			setup := newSetupModel(context.Background(), setupService{
				inspect: func(context.Context) (setupState, error) {
					if paired {
						return setupReady, nil
					}
					return setupAwaitPairing, nil
				},
				player: func(width, height int) tea.Model {
					calls++
					if width != 111 || height != 32 {
						t.Fatalf("player viewport = %dx%d", width, height)
					}
					return setupTestPlayer{}
				},
			})
			setup, _ = setup.Update(tea.WindowSizeMsg{Width: 111, Height: 32})
			model, cmd := runSetupCmd(t, setup, setup.Init())
			if !paired {
				if calls != 0 {
					t.Fatal("player opened before pairing")
				}
				paired = true
				model, cmd = model.Update(tea.KeyMsg{Type: tea.KeyEnter})
				model, cmd = runSetupCmd(t, model, cmd)
			}
			if calls != 1 || model.View() != "Now Playing" || cmd == nil || cmd() != "player initialized" {
				t.Fatalf("player handoff failed: calls=%d view=%q", calls, model.View())
			}
		})
	}
}

func TestSetupWelcomeAndConfirmation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var checks, installs int
	model := newSetupModel(ctx, setupService{
		inspect: func(got context.Context) (setupState, error) {
			if got != ctx {
				t.Fatal("inspect did not receive the setup context")
			}
			checks++
			if installs == 0 {
				return setupMissing, nil
			}
			return setupNeedsKey, nil
		},
		install: func(got context.Context) error {
			if got != ctx {
				t.Fatal("install did not receive the setup context")
			}
			installs++
			return nil
		},
	})
	expectSetupView(t, model, "Checking Soloist...", "Esc: quit")
	model, cmd := runSetupCmd(t, model, model.Init())
	if cmd != nil || checks != 1 || installs != 0 {
		t.Fatal("checking Soloist should not install it")
	}
	expectSetupView(t, model, "Soloist is not installed. Install it now?", "Y: install | Esc: quit")
	for _, key := range []tea.KeyMsg{{Type: tea.KeyEnter}, setupRunes("yes"), {Type: tea.KeyRunes, Runes: []rune{'y'}, Paste: true}} {
		model, cmd = model.Update(key)
		if cmd != nil || checks != 1 || installs != 0 {
			t.Fatal("input other than the Y key should not install Soloist")
		}
		expectSetupView(t, model, "Soloist is not installed. Install it now?", "Y: install | Esc: quit")
	}
	model, cmd = model.Update(setupRunes("y"))
	expectSetupView(t, model, "Installing Soloist...", "Esc: quit")
	if installs != 0 {
		t.Fatal("install should run via the returned command")
	}
	model, cmd = runSetupCmd(t, model, cmd)
	if checks != 1 || installs != 1 {
		t.Fatal("confirmation should install once before the next check")
	}
	expectSetupView(t, model, "Checking Soloist...", "Esc: quit")
	model, cmd = runSetupCmd(t, model, cmd)
	if cmd != nil || checks != 2 || installs != 1 {
		t.Fatal("installation success should recheck once")
	}
	expectSetupView(t, model, setupKeyGuide+"Key: ", "Enter: start | Esc: quit")
}

func TestSetupExistingBinary(t *testing.T) {
	for _, tc := range []struct {
		name  string
		state setupState
		body  string
		hints string
	}{
		{"needs key", setupNeedsKey, setupKeyGuide + "Key: ", "Enter: start | Esc: quit"},
		{"ready", setupReady, "Soloist is ready.", "Esc: quit"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var checks int
			model := newSetupModel(context.Background(), setupService{
				inspect: func(context.Context) (setupState, error) {
					checks++
					return tc.state, nil
				},
			})
			model, cmd := runSetupCmd(t, model, model.Init())
			if cmd != nil || checks != 1 {
				t.Fatal("an existing binary should be reused, not installed or started")
			}
			expectSetupView(t, model, tc.body, tc.hints)
		})
	}
}

func TestSetupMaskingRetryAndPairing(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	const first, second = "fake-key", "another-key"
	var checks, starts int
	model := newSetupModel(ctx, setupService{
		inspect: func(context.Context) (setupState, error) {
			checks++
			switch checks {
			case 1:
				return setupNeedsKey, nil
			case 2:
				return setupAwaitPairing, nil
			default:
				return setupReady, nil
			}
		},
		start: func(got context.Context, key string) error {
			starts++
			if got != ctx {
				t.Fatal("start did not receive the setup context")
			}
			if starts == 1 && key == first {
				return errors.New("raw start error with " + key)
			}
			if starts != 2 || key != second {
				t.Fatal("start received an unexpected key or call count")
			}
			return nil
		},
	})
	model, _ = runSetupCmd(t, model, model.Init())
	model, cmd := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(first + "x"), Paste: true})
	if cmd != nil {
		t.Fatal("pasting a key should not start Soloist")
	}
	expectSetupView(t, model, setupKeyGuide+"Key: "+strings.Repeat("*", len(first)+1), "Enter: start | Esc: quit")
	model, cmd = model.Update(tea.KeyMsg{Type: tea.KeyBackspace})
	if cmd != nil {
		t.Fatal("editing a key should not start Soloist")
	}
	expectSetupView(t, model, setupKeyGuide+"Key: "+strings.Repeat("*", len(first)), "Enter: start | Esc: quit")
	model, cmd = model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	expectSetupView(t, model, "Starting Soloist...", "Esc: quit")
	if starts != 0 {
		t.Fatal("start should run via the returned command")
	}
	model, cmd = runSetupCmd(t, model, cmd)
	if cmd != nil || checks != 1 || starts != 1 {
		t.Fatal("failed start should require re-entry before retry")
	}
	retryView := setupKeyGuide + "Could not start Soloist. Enter your key to retry.\nKey: "
	expectSetupView(t, model, retryView, "Enter: start | Esc: quit")
	if strings.Contains(model.View(), first) || strings.Contains(model.View(), "raw start error") {
		t.Fatal("failed start leaked the submitted key or raw error")
	}
	model, cmd = model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd != nil || starts != 1 {
		t.Fatal("an empty retry should not submit the previous key")
	}
	expectSetupView(t, model, retryView, "Enter: start | Esc: quit")

	model, _ = model.Update(setupRunes(second))
	model, cmd = model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	model, cmd = runSetupCmd(t, model, cmd)
	if checks != 1 || starts != 2 {
		t.Fatal("start retry should run once before rechecking")
	}
	expectSetupView(t, model, "Checking Soloist...", "Esc: quit")
	model, cmd = runSetupCmd(t, model, cmd)
	if cmd != nil || checks != 2 {
		t.Fatal("successful start should recheck once, without polling")
	}
	expectSetupView(t, model, "1. Leave Pulso open. On your phone, open Spotify with the same account.\n2. Connect both devices to the same local network.\n3. Play a song and tap Connect to a device.\n4. Select Pulso or your existing Soloist device name.\n5. When audio plays here, return to Pulso and press Enter.", "Enter: recheck | Esc: quit")
	model, cmd = model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	model, cmd = runSetupCmd(t, model, cmd)
	if cmd != nil || checks != 3 || starts != 2 {
		t.Fatal("pairing recheck should not restart Soloist")
	}
	expectSetupView(t, model, "Soloist is ready.", "Esc: quit")
}

func TestSetupResponsiveWorkspace(t *testing.T) {
	model := newSetupModel(context.Background(), setupService{
		inspect: func(context.Context) (setupState, error) { return setupNeedsKey, nil },
	})
	model, _ = runSetupCmd(t, model, model.Init())
	model, _ = model.Update(setupRunes(strings.Repeat("q", 90)))
	for _, tc := range []struct {
		width, height int
		sidebar       bool
	}{
		{110, 30, true}, {95, 27, true}, {94, 27, false}, {95, 26, false}, {40, 16, false}, {25, 8, false},
	} {
		t.Run(fmt.Sprintf("%dx%d", tc.width, tc.height), func(t *testing.T) {
			model, cmd := model.Update(tea.WindowSizeMsg{Width: tc.width, Height: tc.height})
			if cmd != nil {
				t.Fatal("resizing should not perform setup work")
			}
			view := model.View()
			expectSetupBounds(t, view, tc.width, tc.height)
			expectSetupText(t, view, "Welcome to Pulso", "Key:", "****", "Enter: start", "Esc: quit")
			if tc.width >= 40 {
				expectSetupText(t, view, "[x] Soloist", "[>] Key", "[ ] Pairing", "ACTION", "STATUS", "below.")
			} else {
				expectSetupText(t, view, "Resize for details.")
			}
			if got := strings.Contains(view, "│"); got != tc.sidebar {
				t.Errorf("sidebar visible = %v, want %v:\n%s", got, tc.sidebar, view)
			}
			if strings.Contains(view, "qq") {
				t.Fatal("resizing exposed the key")
			}
			t.Logf("\n%s", view)
		})
	}
}

func TestSetupInstallFailureActions(t *testing.T) {
	for _, tc := range []struct {
		name, reason string
		err          error
	}{
		{"generic", "Automatic install failed.", errors.New("raw install details")},
		{"permission", "Cannot write to the install location.", fmt.Errorf("raw install details: %w", fs.ErrPermission)},
		{"timeout", "Soloist download timed out.", fmt.Errorf("raw install details: %w", context.DeadlineExceeded)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var checks, installs int
			model := newSetupModel(context.Background(), setupService{
				inspect: func(context.Context) (setupState, error) {
					checks++
					if checks > 1 {
						return setupNeedsKey, nil
					}
					return setupMissing, nil
				},
				install: func(context.Context) error {
					installs++
					return tc.err
				},
			})
			model, _ = model.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
			model, _ = runSetupCmd(t, model, model.Init())
			model, cmd := model.Update(setupRunes("Y"))
			expectSetupText(t, model.View(), "Working", "Installing Soloist...")
			if strings.Contains(model.View(), "retry install") || installs != 0 {
				t.Fatal("installing should not show retry or run synchronously")
			}
			model, cmd = runSetupCmd(t, model, cmd)
			if cmd != nil || checks != 1 || installs != 1 {
				t.Fatal("failed installation should wait for a choice without retrying")
			}
			for _, width := range []int{100, 40} {
				model, _ = model.Update(tea.WindowSizeMsg{Width: width, Height: 30})
				view := model.View()
				expectSetupBounds(t, view, width, 30)
				expectSetupText(t, view, "Retry needed", tc.reason, "Install Soloist manually", "on your PATH", "Y: retry install", "Enter: recheck", "Esc: quit")
				if strings.Contains(view, "raw install details") || strings.Contains(view, "Working") {
					t.Fatal("failure view exposed a raw error or still showed working")
				}
				// Long links may be wrapped, but must retain the full address.
				if !strings.Contains(strings.Join(strings.Fields(view), ""), downloads) {
					t.Errorf("view missing official downloads link: %s", view)
				}
				t.Logf("\n%s", view)
			}
			model, cmd = model.Update(setupRunes("Y"))
			expectSetupText(t, model.View(), "Working", "Installing Soloist...")
			model, _ = runSetupCmd(t, model, cmd)
			model, cmd = model.Update(tea.KeyMsg{Type: tea.KeyEnter})
			if checks != 1 || installs != 2 {
				t.Fatal("Enter should schedule a check, not another install")
			}
			model, cmd = runSetupCmd(t, model, cmd)
			if cmd != nil || checks != 2 || installs != 2 {
				t.Fatal("manual install recheck should inspect exactly once")
			}
			expectSetupText(t, model.View(), "Key:", "Enter: start")
		})
	}
}

func TestSetupQuitWithUnsubmittedKey(t *testing.T) {
	for _, key := range []tea.KeyType{tea.KeyEsc, tea.KeyCtrlC} {
		t.Run(key.String(), func(t *testing.T) {
			model := newSetupModel(context.Background(), setupService{
				inspect: func(context.Context) (setupState, error) { return setupNeedsKey, nil },
			})
			model, _ = runSetupCmd(t, model, model.Init())
			model, _ = model.Update(setupRunes("fake-key"))
			model, cmd := model.Update(tea.KeyMsg{Type: key})
			if cmd == nil {
				t.Fatal("expected a quit command")
			}
			if _, ok := cmd().(tea.QuitMsg); !ok || model.View() != "" {
				t.Fatal("quit should leave no key prompt and perform no start")
			}
		})
	}
}
