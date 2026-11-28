package spotify

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

func spotifyFixtureCatalog(f spotifyFixtureTransport) *Catalog {
	return &Catalog{
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
	if err != nil || !more || !reflect.DeepEqual(items, []Item{{Name: "Song", Detail: "A, B", URI: "spotify:track:one"}}) {
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
	if err != nil || more || !reflect.DeepEqual(items, []Item{{Name: "Shared", Detail: "Friend", URI: "spotify:playlist:shared"}, {Name: "Mine", URI: "spotify:playlist:mine"}}) {
		t.Fatalf("playlists = %#v, %v, %v", items, more, err)
	}
	tracks, more, err := c.PlaylistTracks(context.Background(), items[0].URI, 0)
	if !errors.Is(err, ErrPlaylistUnavailable) || strings.Contains(err.Error(), "private fixture body") || more || tracks != nil {
		t.Fatalf("forbidden playlist = %#v, %v, %v", tracks, more, err)
	}
	tracks, more, err = c.PlaylistTracks(context.Background(), items[1].URI, 0)
	if err != nil || more || !reflect.DeepEqual(tracks, []Item{{Name: "Song", Detail: "Artist", URI: "spotify:track:one"}}) {
		t.Fatalf("playlist contents = %#v, %v, %v", tracks, more, err)
	}
	tracks, more, err = c.PlaylistTracks(context.Background(), "spotify:playlist:../items", 0)
	if err == nil || more || tracks != nil || calls != 3 {
		t.Fatalf("invalid URI = %#v, %v, %v; calls=%d", tracks, more, err, calls)
	}
	// El mensaje visible del rechazo no cambia al compartir el error tipado.
	if err.Error() != "Invalid Spotify playlist URI" {
		t.Fatalf("invalid URI message = %q", err.Error())
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

func TestSpotifyCatalogProfile(t *testing.T) {
	for _, tc := range []struct {
		name string
		body string
		want string
	}{
		{"display name", `{"display_name":"  Ana Pérez  "}`, "Ana Pérez"},
		{"empty name", `{"display_name":""}`, ""},
		{"absent name", `{}`, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			c := spotifyFixtureCatalog(func(r *http.Request) (*http.Response, error) {
				calls++
				// El perfil no pagina: ni limit ni offset llegan a la petición.
				if r.Method != http.MethodGet || r.URL.Scheme != "https" || r.URL.Host != "api.spotify.com" || r.URL.Path != "/v1/me" || r.URL.RawQuery != "" || r.Header.Get("Authorization") != "Bearer fixture-access" {
					t.Fatal("unexpected profile request")
				}
				return spotifyFixtureResponse(200, tc.body), nil
			})
			name, err := c.Profile(context.Background())
			if err != nil || name != tc.want || calls != 1 {
				t.Fatalf("profile = %q, %v; calls=%d", name, err, calls)
			}
		})
	}
}

func TestSpotifyCatalogProfileFailure(t *testing.T) {
	for _, code := range []int{401, 500} {
		t.Run(fmt.Sprint(code), func(t *testing.T) {
			c := spotifyFixtureCatalog(func(*http.Request) (*http.Response, error) {
				return spotifyFixtureResponse(code, `{"error":{"message":"fixture secret body"}}`), nil
			})
			name, err := c.Profile(context.Background())
			if err == nil || !errors.Is(err, ErrSpotifyCatalog) || strings.Contains(err.Error(), "fixture secret body") || name != "" {
				t.Fatalf("failed profile = %q, %v", name, err)
			}
		})
	}
}

// TestSpotifyCatalogRecommendations fija la petición de descubrir: la ruta, la
// semilla que viaja y la forma de pista que se lee de la respuesta, la misma de
// los resultados de la búsqueda.
func TestSpotifyCatalogRecommendations(t *testing.T) {
	calls := 0
	c := spotifyFixtureCatalog(func(r *http.Request) (*http.Response, error) {
		calls++
		if r.Method != http.MethodGet || r.URL.Scheme != "https" || r.URL.Host != "api.spotify.com" || r.URL.Path != "/v1/recommendations" || r.Header.Get("Authorization") != "Bearer fixture-access" {
			t.Fatal("unexpected recommendations request")
		}
		q := r.URL.Query()
		// La semilla es el id de la pista en reproducción y el tope es el del endpoint.
		if q.Get("seed_tracks") != "seed1" || q.Get("limit") != "20" || q.Has("offset") || len(q) != 2 {
			t.Fatalf("recommendations query = %v", q)
		}
		return spotifyFixtureResponse(200, `{"tracks":[{"name":"Like it","artists":[{"name":"A"},{"name":"B"}],"duration_ms":210000,"uri":"spotify:track:one","is_playable":true,"album":{"images":[{"url":"https://img/64","width":64,"height":64},{"url":"https://img/20","width":20,"height":20}]}},{"name":"Not playable","uri":"spotify:track:two","is_playable":false},null]}`), nil
	})
	tracks, err := c.Recommendations(context.Background(), "spotify:track:seed1")
	want := []Track{{Name: "Like it", Artist: "A, B", DurationMS: 210000, URI: "spotify:track:one", Cover: "https://img/20"}}
	if err != nil || !reflect.DeepEqual(tracks, want) || calls != 1 {
		t.Fatalf("recommendations = %#v, %v; calls=%d", tracks, err, calls)
	}
}

// TestSpotifyCatalogRecommendationsForbidden fija que un 403 significa que esta
// aplicación de Spotify no conserva acceso al endpoint: una sola petición, sin
// reintento y sin cuerpo de Spotify en el mensaje visible.
func TestSpotifyCatalogRecommendationsForbidden(t *testing.T) {
	calls := 0
	c := spotifyFixtureCatalog(func(*http.Request) (*http.Response, error) {
		calls++
		return spotifyFixtureResponse(403, `{"error":{"message":"fixture secret body"}}`), nil
	})
	tracks, err := c.Recommendations(context.Background(), "spotify:track:seed1")
	if !errors.Is(err, ErrRecommendationsUnavailable) || strings.Contains(err.Error(), "fixture secret body") || tracks != nil || calls != 1 {
		t.Fatalf("forbidden recommendations = %#v, %v; calls=%d", tracks, err, calls)
	}
	if err.Error() != "Spotify recommendations are unavailable for this app" {
		t.Fatalf("forbidden message = %q", err.Error())
	}
}

// TestSpotifyCatalogRecommendationsSeed fija que una semilla que no nombra una
// pista de Spotify no llega a la red: nunca se envía un seed vacío ni ajeno.
func TestSpotifyCatalogRecommendationsSeed(t *testing.T) {
	for _, seed := range []string{"", "spotify:track:", "spotify:album:seed1", "spotify:track:../seed", "seed1"} {
		t.Run(seed, func(t *testing.T) {
			calls := 0
			c := spotifyFixtureCatalog(func(*http.Request) (*http.Response, error) {
				calls++
				return spotifyFixtureResponse(200, `{"tracks":[]}`), nil
			})
			tracks, err := c.Recommendations(context.Background(), seed)
			if !errors.Is(err, errSpotifyTrackURI) || tracks != nil || calls != 0 {
				t.Fatalf("seed %q = %#v, %v; calls=%d", seed, tracks, err, calls)
			}
		})
	}
}

// TestSpotifyCatalogForbiddenWithoutAPlaylistRead conserva el 403 compartido de
// las lecturas que no nombran un rechazo propio: la búsqueda y el perfil siguen
// respondiendo un fallo del catálogo, sin cuerpo de Spotify.
func TestSpotifyCatalogForbiddenWithoutAPlaylistRead(t *testing.T) {
	for _, read := range []struct {
		name string
		path string
		call func(*Catalog) (string, error)
	}{
		{"search", "/v1/search", func(c *Catalog) (string, error) {
			items, _, err := c.Search(context.Background(), "test", 0)
			return fmt.Sprint(items), err
		}},
		{"profile", "/v1/me", func(c *Catalog) (string, error) {
			name, err := c.Profile(context.Background())
			return name, err
		}},
	} {
		t.Run(read.name, func(t *testing.T) {
			calls := 0
			c := spotifyFixtureCatalog(func(r *http.Request) (*http.Response, error) {
				calls++
				if r.URL.Path != read.path {
					t.Fatalf("unexpected path %q", r.URL.Path)
				}
				return spotifyFixtureResponse(403, `{"error":{"message":"fixture secret body"}}`), nil
			})
			if _, err := read.call(c); !errors.Is(err, ErrSpotifyCatalog) || strings.Contains(err.Error(), "fixture secret body") || calls != 1 {
				t.Fatalf("forbidden %s = %v; calls=%d", read.name, err, calls)
			}
		})
	}
}
