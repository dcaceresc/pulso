package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestLoginWithoutClientID(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := runLogin("", &stdout, &stderr); code != 1 || stdout.Len() != 0 {
		t.Fatalf("exit=%d stdout=%q", code, &stdout)
	}
	if !strings.Contains(stderr.String(), "developer.spotify.com/dashboard") {
		t.Fatalf("missing guidance: %q", stderr.String())
	}
}
