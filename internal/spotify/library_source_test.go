package spotify

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
	want := []Playlist{{Name: "Mine", Owner: "ana", URI: "spotify:playlist:mine", Total: 42}}
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
	want := []Track{{Name: "Tren al Sur", Artist: "Los Prisioneros", DurationMS: 336000, URI: "spotify:track:one"}}
	if err != nil || more || total != 42 || !reflect.DeepEqual(tracks, want) {
		t.Fatalf("track page = %#v, %d, %v, %v", tracks, total, more, err)
	}
}

func TestLibrarySourceSearchPage(t *testing.T) {
	calls := 0
	c := spotifyFixtureCatalog(func(r *http.Request) (*http.Response, error) {
		calls++
		if r.Method != http.MethodGet || r.URL.Host != "api.spotify.com" || r.URL.Path != "/v1/search" {
			t.Fatalf("unexpected request %s", r.URL.String())
		}
		query := r.URL.Query()
		if query.Get("q") != "rock & roll" || query.Get("type") != "track" || query.Get("limit") != "10" || query.Get("offset") != "10" {
			t.Fatalf("unexpected search parameters %s", r.URL.RawQuery)
		}
		return spotifyFixtureResponse(200, `{"tracks":{"total":42,"items":[null,`+
			`{"name":"Tren al Sur","artists":[{"name":"Los Prisioneros"}],"duration_ms":336000,"uri":"spotify:track:one"},`+
			`{"name":"Blocked","uri":"spotify:track:two","is_playable":false}],`+
			`"next":"https://invalid.example/never"}}`), nil
	})
	tracks, total, more, err := c.SearchPage(context.Background(), "rock & roll", 10)
	want := []Track{{Name: "Tren al Sur", Artist: "Los Prisioneros", DurationMS: 336000, URI: "spotify:track:one"}}
	if err != nil || !more || total != 42 || !reflect.DeepEqual(tracks, want) || calls != 1 {
		t.Fatalf("search page = %#v, %d, %v, %v; calls=%d", tracks, total, more, err, calls)
	}
}

func TestLibrarySourceSearchEmptyQuery(t *testing.T) {
	calls := 0
	c := spotifyFixtureCatalog(func(*http.Request) (*http.Response, error) {
		calls++
		return spotifyFixtureResponse(200, `{"tracks":{"items":[],"next":null}}`), nil
	})
	for _, query := range []string{"", "   ", "\t\n"} {
		tracks, total, more, err := c.SearchPage(context.Background(), query, 0)
		if err != nil || tracks != nil || more || total != 0 {
			t.Fatalf("empty query %q = %#v, %d, %v, %v", query, tracks, total, more, err)
		}
	}
	if calls != 0 {
		t.Fatalf("an empty query reached the network: %d calls", calls)
	}
}

func TestLibrarySourceSearchFailures(t *testing.T) {
	for _, tt := range []struct {
		name string
		code int
		body string
		want error
	}{
		{"missing tracks", http.StatusOK, `{"albums":{}}`, ErrSpotifyCatalog},
		{"server error", http.StatusInternalServerError, "arbitrary fixture body", ErrSpotifyCatalog},
		{"rate limited", http.StatusTooManyRequests, "arbitrary fixture body", errSpotifyRateLimited},
	} {
		t.Run(tt.name, func(t *testing.T) {
			c := spotifyFixtureCatalog(func(*http.Request) (*http.Response, error) {
				return spotifyFixtureResponse(tt.code, tt.body), nil
			})
			tracks, total, more, err := c.SearchPage(context.Background(), "rock", 0)
			if !errors.Is(err, tt.want) || strings.Contains(err.Error(), "arbitrary fixture body") || tracks != nil || more || total != 0 {
				t.Fatalf("failed search page = %#v, %d, %v, %v", tracks, total, more, err)
			}
		})
	}
}

func TestLibrarySourceTrackPageForbidden(t *testing.T) {
	c := spotifyFixtureCatalog(func(*http.Request) (*http.Response, error) {
		return spotifyFixtureResponse(403, `{"message":"private fixture body"}`), nil
	})
	tracks, total, more, err := c.TrackPage(context.Background(), "spotify:playlist:shared", 0)
	if !errors.Is(err, ErrPlaylistUnavailable) || strings.Contains(err.Error(), "private fixture body") || more || total != 0 || tracks != nil {
		t.Fatalf("forbidden playlist = %#v, %d, %v, %v", tracks, total, more, err)
	}
}

func TestLibrarySourceFailures(t *testing.T) {
	for _, tt := range []struct {
		name string
		code int
		want error
	}{
		{"server error", http.StatusInternalServerError, ErrSpotifyCatalog},
		{"redirect", http.StatusTemporaryRedirect, ErrSpotifyCatalog},
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

// TestLibrarySourcePlaylistItemsSnapshot prueba la lectura que alimenta una
// retirada: la misma ruta y la misma paginación que TrackPage, con el snapshot
// que Spotify devuelve para esa página.
func TestLibrarySourcePlaylistItemsSnapshot(t *testing.T) {
	calls := 0
	c := spotifyFixtureCatalog(func(r *http.Request) (*http.Response, error) {
		calls++
		if r.Method != http.MethodGet || r.URL.Path != "/v1/playlists/mine/items" || r.Header.Get("Authorization") != "Bearer fixture-access" {
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.String())
		}
		if r.URL.Query().Get("limit") != "10" || r.URL.Query().Get("offset") != "10" {
			t.Fatalf("unexpected pagination %s", r.URL.RawQuery)
		}
		return spotifyFixtureResponse(200, `{"total":42,"snapshot_id":"snapshot-page","items":[null,{"item":null},`+
			`{"item":{"name":"Tren al Sur","artists":[{"name":"Los Prisioneros"}],"duration_ms":336000,"uri":"spotify:track:one"}},`+
			`{"item":{"name":"Blocked","uri":"spotify:track:two","is_playable":false}}],"next":"https://invalid.example/never"}`), nil
	})
	page, err := c.PlaylistItems(context.Background(), "spotify:playlist:mine", 10)
	want := PlaylistItemsPage{
		Tracks:   []Track{{Name: "Tren al Sur", Artist: "Los Prisioneros", DurationMS: 336000, URI: "spotify:track:one"}},
		Total:    42,
		More:     true,
		Snapshot: "snapshot-page",
	}
	if err != nil || !reflect.DeepEqual(page, want) || calls != 1 {
		t.Fatalf("playlist items = %#v, %v; calls=%d", page, err, calls)
	}
}

// TestLibrarySourcePlaylistItemsKeepsRestrictedSignal conserva la señal de
// lista restringida que ya usa TrackPage, sin texto de Spotify.
func TestLibrarySourcePlaylistItemsKeepsRestrictedSignal(t *testing.T) {
	c := spotifyFixtureCatalog(func(*http.Request) (*http.Response, error) {
		return spotifyFixtureResponse(403, `{"message":"private fixture body"}`), nil
	})
	page, err := c.PlaylistItems(context.Background(), "spotify:playlist:shared", 0)
	if !errors.Is(err, ErrPlaylistUnavailable) || strings.Contains(err.Error(), "private fixture body") {
		t.Fatalf("restricted playlist items = %#v, %v", page, err)
	}
	if page.Tracks != nil || page.Total != 0 || page.More || page.Snapshot != "" {
		t.Fatalf("restricted playlist items kept data: %#v", page)
	}
}

// TestLibrarySourcePlaylistItemsRejectsInvalidPlaylist no gasta una petición
// cuando el URI no es de una lista.
func TestLibrarySourcePlaylistItemsRejectsInvalidPlaylist(t *testing.T) {
	calls := 0
	c := spotifyFixtureCatalog(func(*http.Request) (*http.Response, error) {
		calls++
		return spotifyFixtureResponse(200, `{"items":[],"next":null}`), nil
	})
	for _, uri := range []string{"", "spotify:playlist:../items", "spotify:album:mine"} {
		page, err := c.PlaylistItems(context.Background(), uri, 0)
		if err == nil || page.Tracks != nil || page.Total != 0 || page.More || page.Snapshot != "" {
			t.Fatalf("playlist items for %q = %#v, %v", uri, page, err)
		}
	}
	if calls != 0 {
		t.Fatalf("an invalid playlist URI reached the network: %d calls", calls)
	}
}

// TestLibrarySourceTrackPageIgnoresTheSnapshot prueba que la lectura que ya
// existía conserva sus datos y su paginación aunque Spotify mande el snapshot.
func TestLibrarySourceTrackPageIgnoresTheSnapshot(t *testing.T) {
	c := spotifyFixtureCatalog(func(*http.Request) (*http.Response, error) {
		return spotifyFixtureResponse(200, `{"total":7,"snapshot_id":"snapshot-page","items":[`+
			`{"item":{"name":"Tren al Sur","artists":[{"name":"Los Prisioneros"}],"duration_ms":336000,"uri":"spotify:track:one"}}],"next":null}`), nil
	})
	tracks, total, more, err := c.TrackPage(context.Background(), "spotify:playlist:mine", 0)
	want := []Track{{Name: "Tren al Sur", Artist: "Los Prisioneros", DurationMS: 336000, URI: "spotify:track:one"}}
	if err != nil || more || total != 7 || !reflect.DeepEqual(tracks, want) {
		t.Fatalf("track page = %#v, %d, %v, %v", tracks, total, more, err)
	}
}
