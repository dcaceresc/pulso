package app

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"testing"
)

func TestLocalSetupCommand(t *testing.T) {
	for _, tc := range []struct {
		name, stdout, stderr string
		err                  error
		code                 int
	}{
		{name: "opens wizard", stdout: "Welcome to Pulso\n"},
		{name: "terminal failure", err: errors.New("internal terminal details"), code: 1, stderr: "Could not open the Pulso setup wizard. Run pulso doctor.\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			calls := 0
			code := run(nil, &stdout, &stderr, commandDeps{setup: func(out io.Writer) error {
				calls++
				io.WriteString(out, tc.stdout)
				return tc.err
			}})
			if code != tc.code || stdout.String() != tc.stdout || stderr.String() != tc.stderr || calls != 1 {
				t.Fatalf("exit=%d stdout=%q stderr=%q launches=%d", code, &stdout, &stderr, calls)
			}
		})
	}
}

func TestCommandHelp(t *testing.T) {
	const want = `Pulso — Spotify in the terminal

Usage:
  pulso                          Open the Soloist setup wizard
  pulso doctor [--data-dir PATH]  Report local Soloist status
  pulso uninstall                Delete Pulso and Soloist files (asks first)
  pulso --help                   Show this help
`
	for _, flag := range []string{"--help", "-h"} {
		var stdout, stderr bytes.Buffer
		code := run([]string{flag}, &stdout, &stderr, commandDeps{})
		if code != 0 || stdout.String() != want || stderr.Len() != 0 {
			t.Fatalf("%s: exit=%d stdout=%q stderr=%q", flag, code, &stdout, &stderr)
		}
	}
	for _, line := range []string{
		"  pulso                          Open the Soloist setup wizard",
		"  pulso doctor [--data-dir PATH]  Report local Soloist status",
		"  pulso uninstall                Delete Pulso and Soloist files (asks first)",
		"  pulso --help                   Show this help",
	} {
		if !strings.Contains(want, line) {
			t.Fatalf("help text lost %q", line)
		}
	}
}

func TestUninstallCommandDispatch(t *testing.T) {
	var stdout, stderr bytes.Buffer
	calls := 0
	code := run([]string{"uninstall"}, &stdout, &stderr, commandDeps{
		uninstall: func(stdin io.Reader, output, diagnostic io.Writer) int {
			calls++
			if stdin == nil {
				t.Fatal("uninstall received no input stream")
			}
			io.WriteString(output, "plan\n")
			return 7
		},
	})
	if code != 7 || stdout.String() != "plan\n" || stderr.Len() != 0 || calls != 1 {
		t.Fatalf("exit=%d stdout=%q stderr=%q calls=%d", code, &stdout, &stderr, calls)
	}
}

func TestDoctor(t *testing.T) {
	tests := []struct {
		name, path, report string
		probeErr           error
		args               []string
		code               int
		stdout, stderr     string
	}{
		{name: "missing", code: 1, stderr: "Soloist not found. Install it from https://developer.spotify.com/documentation/soloist/reference/downloads-and-updates\n"},
		{name: "status reported", path: "soloist", report: "Daemon: running\n", stdout: "Soloist: soloist\nDaemon: running\n"},
		{name: "status failed", path: "soloist", report: "Could not connect\n", probeErr: errors.New("exit status 2"), code: 1, stdout: "Soloist: soloist\n", stderr: "Could not connect\nSoloist status check failed: exit status 2\n"},
		{name: "custom data directory", path: "soloist", args: []string{"--data-dir", "my spotify data"}, report: "Daemon: stopped\n", stdout: "Soloist: soloist\nDaemon: stopped\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			calls := 0
			deps := commandDeps{
				find: func() (string, error) {
					if tt.path == "" {
						return "", nil
					}
					return tt.path, nil
				},
				probe: func(ctx context.Context, path string, args []string, output, diagnostic io.Writer) error {
					calls++
					if _, ok := ctx.Deadline(); !ok || path != tt.path || strings.Join(args, "|") != strings.Join(append([]string{"ctl", "status"}, tt.args...), "|") {
						t.Fatalf("unexpected status invocation: %s %q", path, args)
					}
					if tt.probeErr != nil {
						io.WriteString(diagnostic, tt.report)
					} else {
						io.WriteString(output, tt.report)
					}
					return tt.probeErr
				},
			}
			code := run(append([]string{"doctor"}, tt.args...), &stdout, &stderr, deps)
			wantCalls := 0
			if tt.path != "" {
				wantCalls = 1
			}
			if code != tt.code || stdout.String() != tt.stdout || stderr.String() != tt.stderr || calls != wantCalls {
				t.Fatalf("exit=%d stdout=%q stderr=%q probes=%d", code, &stdout, &stderr, calls)
			}
		})
	}
}
