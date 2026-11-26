package app

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestClientIDRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "pulso", "client-id")
	if got := loadClientIDAt(path); got != "" {
		t.Fatalf("missing file returned %q", got)
	}
	const want = "0123456789ABCDEF0123456789abcdef"
	if err := saveClientIDAt(path, "  "+want+"  "); err != nil {
		t.Fatal(err)
	}
	if got, lowered := loadClientIDAt(path), strings.ToLower(want); got != lowered {
		t.Fatalf("loaded %q, want %q", got, lowered)
	}
	if runtime.GOOS != "windows" {
		info, err := os.Stat(path)
		if err != nil || info.Mode().Perm()&0o077 != 0 {
			t.Fatalf("client id is not private: %v %v", info, err)
		}
	}
	if err := saveClientIDAt(path, strings.Repeat("a", 65)); err == nil {
		t.Fatal("saved an oversized client id")
	}
	if err := saveClientIDAt(path, strings.Repeat("a", 31)); err == nil {
		t.Fatal("saved an undersized client id")
	}
	if err := saveClientIDAt(path, "5o1eist-key-0123456789abcdef012345"); err == nil {
		t.Fatal("saved a value that is not a Spotify client id")
	}
	if err := saveClientIDAt(path, "bad id\n"); err == nil {
		t.Fatal("saved a client id with unsafe characters")
	}
}
