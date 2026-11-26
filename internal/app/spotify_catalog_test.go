package app

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"reflect"
	"strings"
	"testing"
	"time"
)

type spotifyFixtureTransport func(*http.Request) (*http.Response, error)

func (f spotifyFixtureTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func spotifyFixtureResponse(code int, body string) *http.Response {
	return &http.Response{StatusCode: code, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body))}
}

func spotifyFixtureCatalog(f spotifyFixtureTransport) *spotifyCatalog {
	return &spotifyCatalog{
		client: newSpotifyHTTPClient(f), clientID: "fixture-client",
		token: spotifyToken{access: "fixture-access", refresh: "fixture-refresh", expires: time.Now().Add(time.Hour)},
	}
}

func TestSpotifyCatalogSearch(t *testing.T) {
	calls := 0
	c := spotifyFixtureCatalog(func(r *http.Request) (*http.Response, error) {
		calls++
		if r.Method != http.MethodGet || r.URL.Scheme != "https" || r.URL.Host != "api.spotify.com" || r.URL.Path != "/v1/search" || r.Header.Get("Authorization") != "Bearer fixture-access" {
			t.Fatal("unexpected search request")
		}
		q := r.URL.Query()
		if q.Get("q") != "rock & roll" || q.Get("type") != "track" || q.Get("limit") != "10" || q.Get("offset") != fmt.Sprint((calls-1)*10) {
			t.Fatal("incorrect search pagination or query")
		}
		if calls == 1 {
			return spotifyFixtureResponse(200, `{"tracks":{"items":[{"name":"Song","artists":[{"name":"A"},{"name":"B"}],"uri":"spotify:track:one"}],"next":"https://invalid.example/dont-follow"}}`), nil
		}
		return spotifyFixtureResponse(200, `{"tracks":{"items":[],"next":null}}`), nil
	})
	items, more, err := c.Search(context.Background(), "rock & roll", 0)
	if err != nil || !more || !reflect.DeepEqual(items, []catalogItem{{Name: "Song", Detail: "A, B", URI: "spotify:track:one"}}) {
		t.Fatalf("search = %#v, %v, %v", items, more, err)
	}
	items, more, err = c.Search(context.Background(), "rock & roll", 10)
	if err != nil || more || len(items) != 0 || calls != 2 {
		t.Fatalf("second search = %#v, %v, %v; calls=%d", items, more, err, calls)
	}
}

func TestSpotifyCatalogPlaylists(t *testing.T) {
	calls := 0
	c := spotifyFixtureCatalog(func(r *http.Request) (*http.Response, error) {
		calls++
		if r.Method != http.MethodGet || r.URL.Host != "api.spotify.com" || r.URL.Query().Get("limit") != "10" || r.URL.Query().Get("offset") != "0" {
			t.Fatal("unexpected playlist request")
		}
		switch r.URL.Path {
		case "/v1/me/playlists":
			return spotifyFixtureResponse(200, `{"items":[null,{"name":"Shared","owner":{"display_name":"Friend"},"uri":"spotify:playlist:shared"},{"name":"Mine","uri":"spotify:playlist:mine"}],"next":null}`), nil
		case "/v1/playlists/shared/items":
			return spotifyFixtureResponse(403, `{"message":"private fixture body"}`), nil
		case "/v1/playlists/mine/items":
			return spotifyFixtureResponse(200, `{"items":[null,{"item":null},{"item":{"type":"track","name":"Song","artists":[{"name":"Artist"}],"uri":"spotify:track:one"}}],"next":null}`), nil
		default:
			t.Fatal("unexpected playlist endpoint")
			return nil, nil
		}
	})
	items, more, err := c.Playlists(context.Background(), 0)
	if err != nil || more || !reflect.DeepEqual(items, []catalogItem{{Name: "Shared", Detail: "Friend", URI: "spotify:playlist:shared"}, {Name: "Mine", URI: "spotify:playlist:mine"}}) {
		t.Fatalf("playlists = %#v, %v, %v", items, more, err)
	}
	tracks, more, err := c.PlaylistTracks(context.Background(), items[0].URI, 0)
	if !errors.Is(err, errPlaylistUnavailable) || strings.Contains(err.Error(), "private fixture body") || more || tracks != nil {
		t.Fatalf("forbidden playlist = %#v, %v, %v", tracks, more, err)
	}
	tracks, more, err = c.PlaylistTracks(context.Background(), items[1].URI, 0)
	if err != nil || more || !reflect.DeepEqual(tracks, []catalogItem{{Name: "Song", Detail: "Artist", URI: "spotify:track:one"}}) {
		t.Fatalf("playlist contents = %#v, %v, %v", tracks, more, err)
	}
	tracks, more, err = c.PlaylistTracks(context.Background(), "spotify:playlist:../items", 0)
	if err == nil || more || tracks != nil || calls != 3 {
		t.Fatalf("invalid URI = %#v, %v, %v; calls=%d", tracks, more, err, calls)
	}
}

func TestSpotifyCatalogRateLimitAndRedirect(t *testing.T) {
	for _, tc := range []struct {
		name string
		code int
		want error
	}{{"rate limit", 429, errSpotifyRateLimited}, {"redirect", 307, nil}} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			c := spotifyFixtureCatalog(func(r *http.Request) (*http.Response, error) {
				calls++
				resp := spotifyFixtureResponse(tc.code, "arbitrary fixture body")
				resp.Header.Set("Location", "https://api.spotify.com/other")
				return resp, nil
			})
			items, more, err := c.Search(context.Background(), "test", 0)
			if err == nil || (tc.want != nil && !errors.Is(err, tc.want)) || strings.Contains(err.Error(), "arbitrary fixture body") || items != nil || more || calls != 1 {
				t.Fatalf("failed search = %#v, %v, %v; calls=%d", items, more, err, calls)
			}
		})
	}
}

func TestSpotifyCatalogRefresh(t *testing.T) {
	calls := 0
	c := spotifyFixtureCatalog(func(r *http.Request) (*http.Response, error) {
		calls++
		if calls == 1 {
			if r.Method != http.MethodPost || r.URL.String() != "https://accounts.spotify.com/api/token" || r.Header.Get("Authorization") != "" || r.ParseForm() != nil || r.PostForm.Encode() != "client_id=fixture-client&grant_type=refresh_token&refresh_token=fixture-refresh" {
				t.Fatal("unexpected refresh request")
			}
			return spotifyFixtureResponse(200, `{"access_token":"new-access","token_type":"Bearer","expires_in":3600}`), nil
		}
		if r.URL.Host != "api.spotify.com" || r.Header.Get("Authorization") != "Bearer new-access" {
			t.Fatal("refreshed token not used")
		}
		return spotifyFixtureResponse(200, `{"tracks":{"items":[],"next":null}}`), nil
	})
	c.token.expires = time.Now().Add(-time.Minute)
	for i := 0; i < 2; i++ {
		if items, more, err := c.Search(context.Background(), "test", 0); err != nil || more || len(items) != 0 {
			t.Fatalf("search = %#v, %v, %v", items, more, err)
		}
	}
	if calls != 3 {
		t.Fatalf("got %d calls, want one refresh and two searches", calls)
	}
}
