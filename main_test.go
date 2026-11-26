package main

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"testing"
)

func TestCommandHelp(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := run([]string{"--help"}, &stdout, &stderr, doctorDeps{})
	if code != 0 || stdout.String() != usage || stderr.Len() != 0 {
		t.Fatalf("exit=%d stdout=%q stderr=%q", code, &stdout, &stderr)
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
			deps := doctorDeps{
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
