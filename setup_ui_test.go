package main

import (
	"context"
	"errors"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

const setupKeyGuide = "Soloist is installed. To create your Soloist key:\n" +
	"1. Open https://developer.spotify.com/dashboard/soloist\n" +
	"2. Sign in with your Spotify Premium account.\n" +
	"3. Create a Soloist key and paste it below.\n\n"

func expectSetupView(t *testing.T, model tea.Model, body, hints string) {
	t.Helper()
	want := "Welcome to Pulso\n\n" + body + "\n\n" + hints
	got := model.View()
	if got != want {
		t.Fatalf("View() = %q\nwant     %q", got, want)
	}
	lines := strings.Split(got, "\n")
	if len(lines) > 24 {
		t.Fatalf("view has %d lines, want at most 24", len(lines))
	}
	for _, line := range lines {
		if len(line) > 80 {
			t.Fatalf("line exceeds 80 columns: %q", line)
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
	expectSetupView(t, model, "Pair Soloist in the Spotify app using the same account.\nIn the device picker, choose Pulso or your existing Soloist device name.\nThen recheck here.", "Enter: recheck | Esc: quit")
	model, cmd = model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	model, cmd = runSetupCmd(t, model, cmd)
	if cmd != nil || checks != 3 || starts != 2 {
		t.Fatal("pairing recheck should not restart Soloist")
	}
	expectSetupView(t, model, "Soloist is ready.", "Esc: quit")
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
