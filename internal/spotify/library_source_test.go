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
	var requests []string
	c := spotifyFixtureCatalog(func(r *http.Request) (*http.Response, error) {
		requests = append(requests, r.Method+" "+r.URL.RequestURI())
		switch r.URL.Path {
		case "/v1/playlists/mine/items":
			if r.URL.Query().Get("limit") != "10" || r.URL.Query().Get("offset") != "10" {
				t.Fatalf("unexpected pagination %s", r.URL.RawQuery)
			}
			return spotifyFixtureResponse(200, `{"total":42,"items":[null,{"item":null},`+
				`{"item":{"name":"Tren al Sur","artists":[{"name":"Los Prisioneros"},{"name":""}],"duration_ms":336000,"uri":"spotify:track:one"}},`+
				`{"item":{"name":"Blocked","uri":"spotify:track:two","is_playable":false}},`+
				`{"item":{"name":"No URI","duration_ms":1000}}],"next":null}`), nil
		case "/v1/playlists/mine":
			if r.URL.RawQuery != "fields=snapshot_id" {
				t.Fatalf("unexpected snapshot query %q", r.URL.RawQuery)
			}
			return spotifyFixtureResponse(200, `{"snapshot_id":"snapshot-page"}`), nil
		}
		t.Fatalf("unexpected request %s", r.URL.String())
		return nil, nil
	})
	tracks, total, more, err := c.TrackPage(context.Background(), "spotify:playlist:mine", 10)
	want := []Track{{Name: "Tren al Sur", Artist: "Los Prisioneros", DurationMS: 336000, URI: "spotify:track:one"}}
	if err != nil || more || total != 42 || !reflect.DeepEqual(tracks, want) {
		t.Fatalf("track page = %#v, %d, %v, %v", tracks, total, more, err)
	}
}

// TestLibrarySourcePlaylistItemsSnapshot prueba la lectura que alimenta una
// retirada: la misma ruta y la misma paginación que TrackPage para la página de
// elementos, y el snapshot leído del objeto de la lista, porque Spotify no lo
// devuelve en la página de elementos.
func TestLibrarySourcePlaylistItemsSnapshot(t *testing.T) {
	var requests []string
	c := spotifyFixtureCatalog(func(r *http.Request) (*http.Response, error) {
		requests = append(requests, r.Method+" "+r.URL.RequestURI())
		if r.Header.Get("Authorization") != "Bearer fixture-access" {
			t.Fatalf("unexpected authorization on %s", r.URL.String())
		}
		switch r.URL.Path {
		case "/v1/playlists/mine/items":
			if r.URL.Query().Get("limit") != "10" || r.URL.Query().Get("offset") != "10" {
				t.Fatalf("unexpected pagination %s", r.URL.RawQuery)
			}
			// La página de elementos real no trae snapshot.
			return spotifyFixtureResponse(200, `{"total":42,"items":[null,{"item":null},`+
				`{"item":{"name":"Tren al Sur","artists":[{"name":"Los Prisioneros"}],"duration_ms":336000,"uri":"spotify:track:one"}},`+
				`{"item":{"name":"Blocked","uri":"spotify:track:two","is_playable":false}}],"next":"https://invalid.example/never"}`), nil
		case "/v1/playlists/mine":
			if r.URL.RawQuery != "fields=snapshot_id" {
				t.Fatalf("unexpected snapshot query %q", r.URL.RawQuery)
			}
			return spotifyFixtureResponse(200, `{"snapshot_id":"snapshot-page"}`), nil
		}
		t.Fatalf("unexpected request %s", r.URL.String())
		return nil, nil
	})
	page, err := c.PlaylistItems(context.Background(), "spotify:playlist:mine", 10)
	want := PlaylistItemsPage{
		Tracks:   []Track{{Name: "Tren al Sur", Artist: "Los Prisioneros", DurationMS: 336000, URI: "spotify:track:one"}},
		Total:    42,
		More:     true,
		Snapshot: "snapshot-page",
	}
	wantRequests := []string{
		"GET /v1/playlists/mine/items?limit=10&offset=10",
		"GET /v1/playlists/mine?fields=snapshot_id",
	}
	if err != nil || !reflect.DeepEqual(page, want) || !reflect.DeepEqual(requests, wantRequests) {
		t.Fatalf("playlist items = %#v, %v; requests=%v", page, err, requests)
	}
}

// TestLibrarySourcePlaylistItemsNeverFabricatesSnapshot deja la retirada sin
// snapshot cuando Spotify no lo devuelve: un campo ausente, un valor vacío o
// una respuesta truncada no producen una versión de la lista.
func TestLibrarySourcePlaylistItemsNeverFabricatesSnapshot(t *testing.T) {
	for _, tt := range []struct{ name, body string }{
		{"campo ausente", `{}`},
		{"snapshot vacío", `{"snapshot_id":""}`},
		{"respuesta truncada", `{"snapshot_id":"snapshot-pa`},
	} {
		t.Run(tt.name, func(t *testing.T) {
			c := spotifyFixtureCatalog(func(r *http.Request) (*http.Response, error) {
				switch r.URL.Path {
				case "/v1/playlists/mine":
					return spotifyFixtureResponse(200, tt.body), nil
				case "/v1/playlists/mine/items":
					return spotifyFixtureResponse(200, `{"total":1,"items":[`+
						`{"item":{"name":"Tren al Sur","artists":[{"name":"Los Prisioneros"}],"duration_ms":336000,"uri":"spotify:track:one"}}],"next":null}`), nil
				}
				t.Fatalf("unexpected request %s", r.URL.String())
				return nil, nil
			})
			page, err := c.PlaylistItems(context.Background(), "spotify:playlist:mine", 0)
			want := []Track{{Name: "Tren al Sur", Artist: "Los Prisioneros", DurationMS: 336000, URI: "spotify:track:one"}}
			if err != nil || page.Snapshot != "" || !reflect.DeepEqual(page.Tracks, want) || page.Total != 1 || page.More {
				t.Fatalf("playlist items without snapshot = %#v, %v", page, err)
			}
		})
	}
}

// TestLibrarySourcePlaylistItemsSnapshotFailureIsNotFatal conserva las pistas
// cuando la lectura del snapshot falla: sin snapshot no hay retirada, pero la
// lista se sigue mostrando y el texto de Spotify no sale de aquí.
func TestLibrarySourcePlaylistItemsSnapshotFailureIsNotFatal(t *testing.T) {
	for _, status := range []int{http.StatusForbidden, http.StatusInternalServerError} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			c := spotifyFixtureCatalog(func(r *http.Request) (*http.Response, error) {
				switch r.URL.Path {
				case "/v1/playlists/mine":
					return spotifyFixtureResponse(status, `{"message":"private fixture body"}`), nil
				case "/v1/playlists/mine/items":
					return spotifyFixtureResponse(200, `{"total":1,"items":[`+
						`{"item":{"name":"Tren al Sur","artists":[{"name":"Los Prisioneros"}],"duration_ms":336000,"uri":"spotify:track:one"}}],"next":null}`), nil
				}
				t.Fatalf("unexpected request %s", r.URL.String())
				return nil, nil
			})
			page, err := c.PlaylistItems(context.Background(), "spotify:playlist:mine", 0)
			want := []Track{{Name: "Tren al Sur", Artist: "Los Prisioneros", DurationMS: 336000, URI: "spotify:track:one"}}
			if err != nil || page.Snapshot != "" || !reflect.DeepEqual(page.Tracks, want) {
				t.Fatalf("playlist items with a failed snapshot = %#v, %v", page, err)
			}
		})
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
	calls := 0
	c := spotifyFixtureCatalog(func(*http.Request) (*http.Response, error) {
		calls++
		return spotifyFixtureResponse(403, `{"message":"private fixture body"}`), nil
	})
	tracks, total, more, err := c.TrackPage(context.Background(), "spotify:playlist:shared", 0)
	if !errors.Is(err, ErrPlaylistUnavailable) || strings.Contains(err.Error(), "private fixture body") || more || total != 0 || tracks != nil || calls != 1 {
		t.Fatalf("forbidden playlist = %#v, %d, %v, %v; calls=%d", tracks, total, more, err, calls)
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

// TestLibrarySourcePlaylistItemsKeepsRestrictedSignal conserva la señal de
// lista restringida que ya usa TrackPage, sin texto de Spotify y sin pedir el
// snapshot de una lista cuyos elementos no se pueden leer.
func TestLibrarySourcePlaylistItemsKeepsRestrictedSignal(t *testing.T) {
	calls := 0
	c := spotifyFixtureCatalog(func(*http.Request) (*http.Response, error) {
		calls++
		return spotifyFixtureResponse(403, `{"message":"private fixture body"}`), nil
	})
	page, err := c.PlaylistItems(context.Background(), "spotify:playlist:shared", 0)
	if !errors.Is(err, ErrPlaylistUnavailable) || strings.Contains(err.Error(), "private fixture body") {
		t.Fatalf("restricted playlist items = %#v, %v", page, err)
	}
	if page.Tracks != nil || page.Total != 0 || page.More || page.Snapshot != "" || calls != 1 {
		t.Fatalf("restricted playlist items kept data: %#v; calls=%d", page, calls)
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

// TestLibrarySourceTrackPageKeepsItemsPathAndPaging conserva la lectura que ya
// existía: misma ruta de elementos, misma paginación y las pistas visibles
// aunque la lectura del snapshot falle.
func TestLibrarySourceTrackPageKeepsItemsPathAndPaging(t *testing.T) {
	var requests []string
	c := spotifyFixtureCatalog(func(r *http.Request) (*http.Response, error) {
		requests = append(requests, r.Method+" "+r.URL.RequestURI())
		switch r.URL.Path {
		case "/v1/playlists/mine/items":
			if r.URL.Query().Get("limit") != "10" || r.URL.Query().Get("offset") != "0" {
				t.Fatalf("unexpected pagination %s", r.URL.RawQuery)
			}
			return spotifyFixtureResponse(200, `{"total":7,"items":[`+
				`{"item":{"name":"Tren al Sur","artists":[{"name":"Los Prisioneros"}],"duration_ms":336000,"uri":"spotify:track:one"}}],"next":null}`), nil
		case "/v1/playlists/mine":
			return spotifyFixtureResponse(500, "arbitrary fixture body"), nil
		}
		t.Fatalf("unexpected request %s", r.URL.String())
		return nil, nil
	})
	tracks, total, more, err := c.TrackPage(context.Background(), "spotify:playlist:mine", 0)
	want := []Track{{Name: "Tren al Sur", Artist: "Los Prisioneros", DurationMS: 336000, URI: "spotify:track:one"}}
	wantRequests := []string{
		"GET /v1/playlists/mine/items?limit=10&offset=0",
		"GET /v1/playlists/mine?fields=snapshot_id",
	}
	if err != nil || more || total != 7 || !reflect.DeepEqual(tracks, want) || !reflect.DeepEqual(requests, wantRequests) {
		t.Fatalf("track page = %#v, %d, %v, %v; requests=%v", tracks, total, more, err, requests)
	}
}
