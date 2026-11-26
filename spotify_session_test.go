package main

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func spotifySessionFixtureExpiry() time.Time {
	return time.Unix(1_700_000_000, 0).UTC()
}

func TestSpotifySessionRoundTrip(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "pulso", "spotify-session.json")
	if _, ok := loadSpotifySessionAt(path); ok {
		t.Fatal("a missing session file yielded a session")
	}
	want := spotifySession{access: "fixture-access", refresh: "fixture-refresh", expires: spotifySessionFixtureExpiry()}
	if err := saveSpotifySessionAt(path, want); err != nil {
		t.Fatal(err)
	}
	got, ok := loadSpotifySessionAt(path)
	if !ok || got.access != want.access || got.refresh != want.refresh || !got.expires.Equal(want.expires) {
		t.Fatalf("loaded session = %+v, %v", got, ok)
	}
	// Replacing the record keeps exactly one private file and no staging leftovers.
	if err := saveSpotifySessionAt(path, want); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(filepath.Dir(path))
	if err != nil || len(entries) != 1 || entries[0].Name() != "spotify-session.json" {
		t.Fatalf("session directory = %v, %v", entries, err)
	}
	if runtime.GOOS != "windows" {
		if info, err := os.Stat(filepath.Dir(path)); err != nil || info.Mode().Perm() != 0o700 {
			t.Fatalf("session directory mode = %v, %v", info, err)
		}
		if info, err := os.Stat(path); err != nil || info.Mode().Perm() != 0o600 {
			t.Fatalf("session file mode = %v, %v", info, err)
		}
	}
}

func TestSpotifySessionRejectsMalformedFiles(t *testing.T) {
	const valid = `{"access_token":"fixture-access","refresh_token":"fixture-refresh","expires_at":"2023-11-14T22:13:20Z"}`
	for _, tc := range []struct{ name, content string }{
		{name: "empty", content: ""},
		{name: "not json", content: "fixture-access fixture-refresh"},
		{name: "array", content: `["fixture-access","fixture-refresh"]`},
		{name: "unknown field", content: strings.Replace(valid, `"expires_at"`, `"access": "fixture-access", "expires_at"`, 1)},
		{name: "duplicate field", content: strings.Replace(valid, `"access_token"`, `"access_token":"other", "access_token"`, 1)},
		{name: "numeric expiry", content: `{"access_token":"fixture-access","refresh_token":"fixture-refresh","expires_at":1700000000}`},
		{name: "missing refresh token", content: `{"access_token":"fixture-access","expires_at":"2023-11-14T22:13:20Z"}`},
		{name: "missing access token", content: `{"refresh_token":"fixture-refresh","expires_at":"2023-11-14T22:13:20Z"}`},
		{name: "missing expiry", content: `{"access_token":"fixture-access","refresh_token":"fixture-refresh"}`},
		{name: "unparseable expiry", content: `{"access_token":"fixture-access","refresh_token":"fixture-refresh","expires_at":"tomorrow"}`},
		{name: "empty refresh token", content: `{"access_token":"fixture-access","refresh_token":"","expires_at":"2023-11-14T22:13:20Z"}`},
		{name: "unsafe access token", content: `{"access_token":"fixture access\n","refresh_token":"fixture-refresh","expires_at":"2023-11-14T22:13:20Z"}`},
		{name: "trailing data", content: valid + `{}`},
		{name: "oversized", content: valid + strings.Repeat(" ", spotifySessionReadLimit)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "spotify-session.json")
			if err := os.WriteFile(path, []byte(tc.content), 0o600); err != nil {
				t.Fatal(err)
			}
			if session, ok := loadSpotifySessionAt(path); ok {
				t.Fatalf("malformed session loaded as %+v", session)
			}
		})
	}
	// A directory in the file's place is never a session.
	path := filepath.Join(t.TempDir(), "spotify-session.json")
	if err := os.Mkdir(path, 0o700); err != nil {
		t.Fatal(err)
	}
	if _, ok := loadSpotifySessionAt(path); ok {
		t.Fatal("a directory was read as a session")
	}
}

func TestSpotifySessionRejectsInvalidRecords(t *testing.T) {
	path := filepath.Join(t.TempDir(), "spotify-session.json")
	for _, session := range []spotifySession{
		{access: "", refresh: "fixture-refresh", expires: spotifySessionFixtureExpiry()},
		{access: "fixture-access", refresh: "", expires: spotifySessionFixtureExpiry()},
		{access: "fixture-access", refresh: "fixture-refresh"},
		{access: "fixture access", refresh: "fixture-refresh", expires: spotifySessionFixtureExpiry()},
	} {
		if err := saveSpotifySessionAt(path, session); err == nil {
			t.Fatalf("saved an invalid session: %+v", session)
		}
	}
	if _, err := os.Lstat(path); !os.IsNotExist(err) {
		t.Fatalf("a rejected session left a file: %v", err)
	}
}

func TestSpotifySessionDelete(t *testing.T) {
	path := filepath.Join(t.TempDir(), "spotify-session.json")
	if err := deleteSpotifySessionAt(path); err != nil {
		t.Fatalf("deleting a missing session = %v", err)
	}
	if err := saveSpotifySessionAt(path, spotifySession{access: "fixture-access", refresh: "fixture-refresh", expires: spotifySessionFixtureExpiry()}); err != nil {
		t.Fatal(err)
	}
	if err := deleteSpotifySessionAt(path); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(path); !os.IsNotExist(err) {
		t.Fatalf("session file survived deletion: %v", err)
	}
	// Deleting never removes anything that is not the session file itself.
	dir := filepath.Join(t.TempDir(), "spotify-session.json")
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := deleteSpotifySessionAt(dir); err == nil {
		t.Fatal("deleted a directory in place of the session file")
	}
	if _, err := os.Stat(dir); err != nil {
		t.Fatalf("directory was removed: %v", err)
	}
}

func TestRotatedRefreshTokenIsPersisted(t *testing.T) {
	path := filepath.Join(t.TempDir(), "spotify-session.json")
	client := &http.Client{Transport: spotifyFixtureTransport(func(*http.Request) (*http.Response, error) {
		return spotifyFixtureResponse(200, `{"access_token":"fresh","refresh_token":"rotated","token_type":"Bearer","expires_in":3600}`), nil
	})}
	catalog := newSpotifyCatalog(client, "client123",
		spotifyToken{access: "old", refresh: "original", expires: time.Now().Add(-time.Minute)}, path)
	if _, err := catalog.accessToken(context.Background()); err != nil {
		t.Fatal(err)
	}
	session, ok := loadSpotifySessionAt(path)
	if !ok || session.refresh != "rotated" || session.access != "fresh" {
		t.Fatalf("stored session = %+v ok=%v, want the rotated refresh token", session, ok)
	}
}
