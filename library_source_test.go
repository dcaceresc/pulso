package main

import (
	"context"
	"errors"
	"net/http"
	"reflect"
	"strings"
	"testing"
)

func TestLibrarySourcePlaylistPage(t *testing.T) {
	calls := 0
	c := spotifyFixtureCatalog(func(r *http.Request) (*http.Response, error) {
		calls++
		if r.Method != http.MethodGet || r.URL.Host != "api.spotify.com" || r.URL.Path != "/v1/me/playlists" {
			t.Fatalf("unexpected request %s", r.URL.String())
		}
		if r.URL.Query().Get("limit") != "10" || r.URL.Query().Get("offset") != "0" {
			t.Fatalf("unexpected pagination %s", r.URL.RawQuery)
		}
		return spotifyFixtureResponse(200, `{"total":3,"items":[null,`+
			`{"name":"Mine","owner":{"display_name":"ana"},"uri":"spotify:playlist:mine","tracks":{"total":42}},`+
			`{"name":"Suspect","uri":"spotify:playlist:../items"}],"next":"https://invalid.example/never"}`), nil
	})
	playlists, total, more, err := c.PlaylistPage(context.Background(), 0)
	want := []libraryPlaylist{{Name: "Mine", Owner: "ana", URI: "spotify:playlist:mine", Total: 42}}
	if err != nil || !more || total != 3 || !reflect.DeepEqual(playlists, want) || calls != 1 {
		t.Fatalf("playlist page = %#v, %d, %v, %v; calls=%d", playlists, total, more, err, calls)
	}
}

func TestLibrarySourceTrackPage(t *testing.T) {
	c := spotifyFixtureCatalog(func(r *http.Request) (*http.Response, error) {
		if r.Method != http.MethodGet || r.URL.Path != "/v1/playlists/mine/items" {
			t.Fatalf("unexpected request %s", r.URL.String())
		}
		if r.URL.Query().Get("limit") != "10" || r.URL.Query().Get("offset") != "10" {
			t.Fatalf("unexpected pagination %s", r.URL.RawQuery)
		}
		return spotifyFixtureResponse(200, `{"total":42,"items":[null,{"item":null},`+
			`{"item":{"name":"Tren al Sur","artists":[{"name":"Los Prisioneros"},{"name":""}],"duration_ms":336000,"uri":"spotify:track:one"}},`+
			`{"item":{"name":"Blocked","uri":"spotify:track:two","is_playable":false}},`+
			`{"item":{"name":"No URI","duration_ms":1000}}],"next":null}`), nil
	})
	tracks, total, more, err := c.TrackPage(context.Background(), "spotify:playlist:mine", 10)
	want := []libraryTrack{{Name: "Tren al Sur", Artist: "Los Prisioneros", DurationMS: 336000, URI: "spotify:track:one"}}
	if err != nil || more || total != 42 || !reflect.DeepEqual(tracks, want) {
		t.Fatalf("track page = %#v, %d, %v, %v", tracks, total, more, err)
	}
}

func TestLibrarySourceTrackPageForbidden(t *testing.T) {
	c := spotifyFixtureCatalog(func(*http.Request) (*http.Response, error) {
		return spotifyFixtureResponse(403, `{"message":"private fixture body"}`), nil
	})
	tracks, total, more, err := c.TrackPage(context.Background(), "spotify:playlist:shared", 0)
	if !errors.Is(err, errPlaylistUnavailable) || strings.Contains(err.Error(), "private fixture body") || more || total != 0 || tracks != nil {
		t.Fatalf("forbidden playlist = %#v, %d, %v, %v", tracks, total, more, err)
	}
}

func TestLibrarySourceFailures(t *testing.T) {
	for _, tt := range []struct {
		name string
		code int
		want error
	}{
		{"server error", http.StatusInternalServerError, errSpotifyCatalog},
		{"redirect", http.StatusTemporaryRedirect, errSpotifyCatalog},
	} {
		t.Run(tt.name, func(t *testing.T) {
			calls := 0
			c := spotifyFixtureCatalog(func(*http.Request) (*http.Response, error) {
				calls++
				resp := spotifyFixtureResponse(tt.code, "arbitrary fixture body")
				resp.Header.Set("Location", "https://api.spotify.com/v1/me/playlists")
				return resp, nil
			})
			playlists, total, more, err := c.PlaylistPage(context.Background(), 0)
			if !errors.Is(err, tt.want) || strings.Contains(err.Error(), "arbitrary fixture body") || playlists != nil || more || total != 0 || calls != 1 {
				t.Fatalf("failed playlist page = %#v, %d, %v, %v; calls=%d", playlists, total, more, err, calls)
			}
		})
	}
}

func TestLibrarySourceRejectsInvalidPlaylist(t *testing.T) {
	calls := 0
	c := spotifyFixtureCatalog(func(*http.Request) (*http.Response, error) {
		calls++
		return spotifyFixtureResponse(200, `{"items":[],"next":null}`), nil
	})
	for _, uri := range []string{"", "spotify:playlist:../items", "spotify:album:mine"} {
		if tracks, total, more, err := c.TrackPage(context.Background(), uri, 0); err == nil || tracks != nil || more || total != 0 {
			t.Fatalf("track page for %q = %#v, %d, %v, %v", uri, tracks, total, more, err)
		}
	}
	if calls != 0 {
		t.Fatalf("an invalid playlist URI reached the network: %d calls", calls)
	}
}
