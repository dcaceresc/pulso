package spotify

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

const spotifyWriteFixtureScope = "playlist-read-private playlist-read-collaborative playlist-modify-private playlist-modify-public"

// spotifyTestTransport envía cada petición al servidor local de pruebas
// mientras el código sigue hablando con los hosts de producción.
type spotifyTestTransport struct{ target *url.URL }

func (s spotifyTestTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	clone := r.Clone(r.Context())
	clone.URL.Scheme, clone.URL.Host = s.target.Scheme, s.target.Host
	clone.Host = ""
	return http.DefaultTransport.RoundTrip(clone)
}

// spotifyWriteCatalog arma un catálogo con sesión válida que habla con el
// servidor de pruebas y cuenta las peticiones recibidas.
func spotifyWriteCatalog(t *testing.T, handler http.HandlerFunc) (*Catalog, *atomic.Int32) {
	t.Helper()
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		handler(w, r)
	}))
	t.Cleanup(server.Close)
	target, err := url.Parse(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	return &Catalog{
		client:   newSpotifyHTTPClient(spotifyTestTransport{target: target}),
		clientID: "fixture-client",
		token:    spotifyToken{access: "fixture-access", refresh: "fixture-refresh", expires: time.Now().Add(time.Hour)},
	}, &calls
}

func TestSpotifyAddToPlaylistRequest(t *testing.T) {
	catalog, calls := spotifyWriteCatalog(t, func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("read body: %v", err)
			return
		}
		if r.Method != http.MethodPost || r.URL.Path != "/v1/playlists/mine/tracks" || r.URL.RawQuery != "" {
			t.Errorf("request = %s %s?%s", r.Method, r.URL.Path, r.URL.RawQuery)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer fixture-access" {
			t.Errorf("authorization = %q", got)
		}
		if got := r.Header.Get("Content-Type"); got != "application/json" {
			t.Errorf("content type = %q", got)
		}
		if got := r.Header.Get("Accept"); got != "application/json" {
			t.Errorf("accept = %q", got)
		}
		if got := string(body); got != `{"uris":["spotify:track:one"]}` {
			t.Errorf("body = %q", got)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_, _ = io.WriteString(w, `{"snapshot_id":"snapshot-one"}`)
	})
	snapshot, err := catalog.AddToPlaylist(context.Background(), "spotify:playlist:mine", "spotify:track:one")
	if err != nil || snapshot != "snapshot-one" || calls.Load() != 1 {
		t.Fatalf("add to playlist = %q, %v; calls=%d", snapshot, err, calls.Load())
	}
}

func TestSpotifyAddToPlaylistAcceptedStatuses(t *testing.T) {
	for _, tc := range []struct {
		name, body, snapshot string
		code                 int
	}{
		{name: "created", code: http.StatusCreated, body: `{"snapshot_id":"snapshot-one"}`, snapshot: "snapshot-one"},
		{name: "ok", code: http.StatusOK, body: `{"snapshot_id":"snapshot-two"}`, snapshot: "snapshot-two"},
		{name: "sin snapshot", code: http.StatusCreated, body: `{}`, snapshot: ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			catalog, calls := spotifyWriteCatalog(t, func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(tc.code)
				_, _ = io.WriteString(w, tc.body)
			})
			snapshot, err := catalog.AddToPlaylist(context.Background(), "spotify:playlist:mine", "spotify:track:one")
			if err != nil || snapshot != tc.snapshot || calls.Load() != 1 {
				t.Fatalf("add to playlist = %q, %v; calls=%d", snapshot, err, calls.Load())
			}
		})
	}
}

func TestSpotifyAddToPlaylistRejectedResponses(t *testing.T) {
	const secret = "fixture secret body"
	for _, tc := range []struct {
		name     string
		body     string
		location string
		want     error
		code     int
	}{
		{name: "server error", code: http.StatusInternalServerError, body: `{"error":{"status":500,"message":"` + secret + `"}}`, want: ErrSpotifyCatalog},
		{name: "bad request", code: http.StatusBadRequest, body: `{"error":{"status":400,"message":"` + secret + `"}}`, want: ErrSpotifyCatalog},
		{name: "malformed body", code: http.StatusCreated, body: secret, want: ErrSpotifyCatalog},
		{name: "accepted sin confirmar", code: http.StatusAccepted, body: `{"snapshot_id":"snapshot-one"}`, want: ErrSpotifyCatalog},
		{name: "no content", code: http.StatusNoContent, want: ErrSpotifyCatalog},
		{name: "rate limit", code: http.StatusTooManyRequests, body: secret, want: errSpotifyRateLimited},
		{name: "redirect", code: http.StatusTemporaryRedirect, body: secret, want: ErrSpotifyCatalog, location: "/redirected"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			catalog, calls := spotifyWriteCatalog(t, func(w http.ResponseWriter, r *http.Request) {
				if tc.location != "" {
					w.Header().Set("Location", tc.location)
				}
				w.WriteHeader(tc.code)
				_, _ = io.WriteString(w, tc.body)
			})
			snapshot, err := catalog.AddToPlaylist(context.Background(), "spotify:playlist:mine", "spotify:track:one")
			if err == nil || !errors.Is(err, tc.want) || snapshot != "" {
				t.Fatalf("rejected write = %q, %v", snapshot, err)
			}
			if strings.Contains(err.Error(), secret) {
				t.Fatalf("error leaked the response body: %v", err)
			}
			// Un redirect no se sigue: la petición original es la única.
			if calls.Load() != 1 {
				t.Fatalf("calls = %d, want 1", calls.Load())
			}
		})
	}
}

func TestSpotifyAddToPlaylistForbiddenMapping(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		want       error
	}{
		{
			name: "sin permiso de escritura",
			body: `{"error":{"status":403,"message":"Insufficient client scope"}}`,
			want: ErrSpotifyWriteScope,
		},
		{
			name: "lista no editable",
			body: `{"error":{"status":403,"message":"You cannot add tracks to a playlist you don't own."}}`,
			want: ErrPlaylistNotEditable,
		},
		{
			name: "cuerpo sin clasificar",
			body: `{"message":"fixture secret body"}`,
			want: ErrPlaylistNotEditable,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			catalog, calls := spotifyWriteCatalog(t, func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusForbidden)
				_, _ = io.WriteString(w, tc.body)
			})
			snapshot, err := catalog.AddToPlaylist(context.Background(), "spotify:playlist:mine", "spotify:track:one")
			if err == nil || !errors.Is(err, tc.want) || snapshot != "" || calls.Load() != 1 {
				t.Fatalf("forbidden write = %q, %v; calls=%d", snapshot, err, calls.Load())
			}
			// Cada 403 conserva su propio error: una lista ajena no se confunde
			// con un permiso que falta ni con un fallo de transporte.
			for _, other := range []error{ErrSpotifyWriteScope, ErrPlaylistNotEditable, ErrSpotifyCatalog, errSpotifyRateLimited} {
				if other != tc.want && errors.Is(err, other) {
					t.Fatalf("error %v also matches %v", err, other)
				}
			}
			if strings.Contains(err.Error(), "fixture secret body") {
				t.Fatalf("error leaked the response body: %v", err)
			}
		})
	}
}

func TestSpotifyAddToPlaylistTransportFailure(t *testing.T) {
	catalog := spotifyFixtureCatalog(func(*http.Request) (*http.Response, error) {
		return nil, errors.New("fixture transport failure")
	})
	snapshot, err := catalog.AddToPlaylist(context.Background(), "spotify:playlist:mine", "spotify:track:one")
	if !errors.Is(err, ErrSpotifyCatalog) || snapshot != "" {
		t.Fatalf("transport failure = %q, %v", snapshot, err)
	}
	if errors.Is(err, ErrSpotifyWriteScope) || errors.Is(err, ErrPlaylistNotEditable) || strings.Contains(err.Error(), "fixture transport failure") {
		t.Fatalf("transport failure was misclassified: %v", err)
	}
}

func TestSpotifyAddToPlaylistRejectsInvalidInput(t *testing.T) {
	for _, tc := range []struct{ name, playlist, track string }{
		{name: "playlist vacía", track: "spotify:track:one"},
		{name: "playlist de pista", playlist: "spotify:track:one", track: "spotify:track:one"},
		{name: "playlist con ruta", playlist: "spotify:playlist:../items", track: "spotify:track:one"},
		{name: "playlist como URL", playlist: "https://open.spotify.com/playlist/mine", track: "spotify:track:one"},
		{name: "playlist con sufijo", playlist: "spotify:playlist:mine:tracks", track: "spotify:track:one"},
		{name: "pista vacía", playlist: "spotify:playlist:mine"},
		{name: "pista de playlist", playlist: "spotify:playlist:mine", track: "spotify:playlist:mine"},
		{name: "pista con ruta", playlist: "spotify:playlist:mine", track: "spotify:track:one/other"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			catalog, calls := spotifyWriteCatalog(t, func(w http.ResponseWriter, r *http.Request) {
				t.Errorf("invalid input reached the network: %s %s", r.Method, r.URL.Path)
			})
			snapshot, err := catalog.AddToPlaylist(context.Background(), tc.playlist, tc.track)
			if err == nil || snapshot != "" || calls.Load() != 0 {
				t.Fatalf("invalid input = %q, %v; calls=%d", snapshot, err, calls.Load())
			}
		})
	}
}

func TestSpotifyAddToPlaylistRefreshesExpiredToken(t *testing.T) {
	catalog, calls := spotifyWriteCatalog(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/token":
			if r.Method != http.MethodPost || r.Header.Get("Authorization") != "" || r.ParseForm() != nil ||
				r.PostForm.Encode() != "client_id=fixture-client&grant_type=refresh_token&refresh_token=fixture-refresh" {
				t.Errorf("unexpected refresh request: %s %s", r.Method, r.URL.Path)
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"access_token":"new-access","token_type":"Bearer","expires_in":3600}`)
		case "/v1/playlists/mine/tracks":
			if r.Header.Get("Authorization") != "Bearer new-access" {
				t.Errorf("refreshed token not used: %q", r.Header.Get("Authorization"))
			}
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusCreated)
			_, _ = io.WriteString(w, `{"snapshot_id":"snapshot-one"}`)
		default:
			t.Errorf("unexpected request path %q", r.URL.Path)
		}
	})
	catalog.token.expires = time.Now().Add(-time.Minute)
	snapshot, err := catalog.AddToPlaylist(context.Background(), "spotify:playlist:mine", "spotify:track:one")
	if err != nil || snapshot != "snapshot-one" || calls.Load() != 2 {
		t.Fatalf("add to playlist = %q, %v; calls=%d", snapshot, err, calls.Load())
	}
}

// spotifyWriteApproval responde al enlace de autorización como lo haría el
// navegador: comprueba los parámetros del enlace y devuelve el código al
// callback local.
func spotifyWriteApproval(t *testing.T, links *int, code, errCode string) func(string) {
	t.Helper()
	showURL := func(link string) {
		*links++
		u, err := url.Parse(link)
		if err != nil {
			t.Errorf("invalid authorization URL: %v", err)
			return
		}
		q := u.Query()
		if u.Scheme != "https" || u.Host != "accounts.spotify.com" || u.Path != "/authorize" || q.Get("response_type") != "code" || q.Get("code_challenge_method") != "S256" || q.Get("client_id") != "fixture-client" {
			t.Errorf("invalid authorization URL parameters: %s", link)
		}
		callback, err := url.Parse(q.Get("redirect_uri"))
		if err != nil {
			t.Errorf("invalid redirect URI: %v", err)
			return
		}
		values := url.Values{"state": {q.Get("state")}}
		if errCode != "" {
			values.Set("error", errCode)
		} else {
			values.Set("code", code)
		}
		callback.RawQuery = values.Encode()
		client := &http.Client{Timeout: 2 * time.Second, Transport: &http.Transport{DisableKeepAlives: true}}
		resp, err := client.Get(callback.String())
		if err != nil {
			t.Errorf("callback request: %v", err)
			return
		}
		resp.Body.Close()
	}
	return showURL
}

func TestSpotifyReauthorizeWritesRequestsWriteScopes(t *testing.T) {
	path := spotifySessionTestPath(t)
	if err := saveSpotifySessionAt(path, spotifySession{access: "stale-access", refresh: "stale-refresh", expires: time.Now().Add(time.Hour)}); err != nil {
		t.Fatal(err)
	}
	calls, links := 0, 0
	var scope string
	catalog := spotifyFixtureCatalog(func(r *http.Request) (*http.Response, error) {
		calls++
		if r.Method != http.MethodPost || r.URL.String() != "https://accounts.spotify.com/api/token" || r.ParseForm() != nil ||
			r.PostForm.Get("grant_type") != "authorization_code" || r.PostForm.Get("code") != "fixture-code" || r.PostForm.Get("client_id") != "fixture-client" {
			t.Fatalf("unexpected code exchange: %s %s", r.Method, r.URL)
		}
		return spotifyFixtureResponse(200, `{"access_token":"write-access","refresh_token":"write-refresh","token_type":"Bearer","expires_in":3600}`), nil
	})
	catalog.sessionPath = path
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	showURL := spotifyWriteApproval(t, &links, "fixture-code", "")
	writer, err := catalog.ReauthorizeWrites(ctx, func(link string) {
		u, err := url.Parse(link)
		if err != nil {
			t.Fatal(err)
		}
		scope = u.Query().Get("scope")
		showURL(link)
	})
	if err != nil || writer == nil || calls != 1 || links != 1 {
		t.Fatalf("reauthorization = %v, err=%v, calls=%d, links=%d", writer, err, calls, links)
	}
	// La sesión guardada pedía solo lectura: la aprobación nueva debe incluir
	// las dos variantes de escritura y conservar la lectura.
	if scope != spotifyWriteFixtureScope {
		t.Fatalf("scope = %q", scope)
	}
	refreshed, ok := writer.(*Catalog)
	if !ok || refreshed.token.access != "write-access" {
		t.Fatalf("reauthorized catalog = %#v", writer)
	}
	stored, ok := loadSpotifySessionAt(path)
	if !ok || stored.access != "write-access" || stored.refresh != "write-refresh" {
		t.Fatalf("stored session = %+v, %v", stored, ok)
	}
}

func TestSpotifyReleaseSessionKeepsTheApprovedSession(t *testing.T) {
	path := spotifySessionTestPath(t)
	stale := NewCatalog(newSpotifyHTTPClient(spotifyFixtureTransport(func(*http.Request) (*http.Response, error) {
		return spotifyFixtureResponse(200, `{"access_token":"stale-access","refresh_token":"stale-refresh","token_type":"Bearer","expires_in":3600}`), nil
	})), "fixture-client", "read-access", "read-refresh", time.Now().Add(-time.Minute), path)
	// La aprobación nueva guarda su sesión de escritura en el mismo archivo.
	if err := saveSpotifySessionAt(path, spotifySession{access: "write-access", refresh: "write-refresh", expires: time.Now().Add(time.Hour)}); err != nil {
		t.Fatal(err)
	}
	stale.ReleaseSession()
	if _, err := stale.accessToken(context.Background()); err != nil {
		t.Fatalf("refresh after release = %v", err)
	}
	if stored, ok := loadSpotifySessionAt(path); !ok || stored.access != "write-access" || stored.refresh != "write-refresh" {
		t.Fatalf("stored session = %+v, %v", stored, ok)
	}
}

func TestSpotifyReauthorizeWritesDeclined(t *testing.T) {
	path := spotifySessionTestPath(t)
	if err := saveSpotifySessionAt(path, spotifySession{access: "stale-access", refresh: "stale-refresh", expires: time.Now().Add(time.Hour)}); err != nil {
		t.Fatal(err)
	}
	calls, links := 0, 0
	catalog := spotifyFixtureCatalog(func(*http.Request) (*http.Response, error) {
		calls++
		return spotifyFixtureResponse(500, ""), nil
	})
	catalog.sessionPath = path
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	showURL := spotifyWriteApproval(t, &links, "", "access_denied")
	writer, err := catalog.ReauthorizeWrites(ctx, showURL)
	if writer != nil || !errors.Is(err, ErrSpotifyApprovalDeclined) || calls != 0 || links != 1 {
		t.Fatalf("declined reauthorization = %v, err=%v, calls=%d, links=%d", writer, err, calls, links)
	}
	// El texto que el asistente ya mostraba al rechazar la aprobación no cambia.
	if err.Error() != "Spotify authorization did not complete" {
		t.Fatalf("declined message = %q", err.Error())
	}
	// Rechazar el permiso nuevo no borra la aprobación que ya existía.
	if stored, ok := loadSpotifySessionAt(path); !ok || stored.access != "stale-access" {
		t.Fatalf("stored session = %+v, %v", stored, ok)
	}
}

func TestSpotifyReauthorizeWritesRejectedBySpotify(t *testing.T) {
	calls, links := 0, 0
	catalog := spotifyFixtureCatalog(func(r *http.Request) (*http.Response, error) {
		calls++
		if r.Method != http.MethodPost {
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL)
		}
		return spotifyFixtureResponse(400, `{"error":"invalid_grant","error_description":"fixture secret body"}`), nil
	})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	showURL := spotifyWriteApproval(t, &links, "fixture-code", "")
	writer, err := catalog.ReauthorizeWrites(ctx, showURL)
	if writer != nil || !errors.Is(err, ErrSpotifyApprovalDeclined) || calls != 1 || links != 1 {
		t.Fatalf("rejected reauthorization = %v, err=%v, calls=%d, links=%d", writer, err, calls, links)
	}
	if strings.Contains(err.Error(), "fixture secret body") {
		t.Fatalf("error leaked the response body: %v", err)
	}
}

func TestSpotifyReauthorizeWritesRequiresApprovalHandler(t *testing.T) {
	catalog := spotifyFixtureCatalog(func(*http.Request) (*http.Response, error) {
		t.Fatal("a nil approval handler made a network call")
		return nil, nil
	})
	if writer, err := catalog.ReauthorizeWrites(context.Background(), nil); err == nil || writer != nil {
		t.Fatalf("nil handler = %v, %v", writer, err)
	}
	var missing *Catalog
	if writer, err := missing.ReauthorizeWrites(context.Background(), func(string) {}); err == nil || writer != nil {
		t.Fatalf("nil catalog = %v, %v", writer, err)
	}
}

// TestSpotifyRemoveFromPlaylistRequest fija la petición documentada de
// retirada: DELETE, ruta de elementos de la lista, cuerpo con el snapshot y una
// sola aparición de la pista.
func TestSpotifyRemoveFromPlaylistRequest(t *testing.T) {
	catalog, calls := spotifyWriteCatalog(t, func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("read body: %v", err)
			return
		}
		if r.Method != http.MethodDelete || r.URL.Path != "/v1/playlists/mine/tracks" || r.URL.RawQuery != "" {
			t.Errorf("request = %s %s?%s", r.Method, r.URL.Path, r.URL.RawQuery)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer fixture-access" {
			t.Errorf("authorization = %q", got)
		}
		if got := r.Header.Get("Content-Type"); got != "application/json" {
			t.Errorf("content type = %q", got)
		}
		if got := r.Header.Get("Accept"); got != "application/json" {
			t.Errorf("accept = %q", got)
		}
		if got := string(body); got != `{"tracks":[{"uri":"spotify:track:one"}],"snapshot_id":"snapshot-one"}` {
			t.Errorf("body = %q", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"snapshot_id":"snapshot-two"}`)
	})
	err := catalog.RemoveFromPlaylist(context.Background(), "spotify:playlist:mine", "spotify:track:one", "snapshot-one")
	if err != nil || calls.Load() != 1 {
		t.Fatalf("remove from playlist = %v; calls=%d", err, calls.Load())
	}
}

func TestSpotifyRemoveFromPlaylistAcceptedStatuses(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		code       int
	}{
		{name: "ok con snapshot", code: http.StatusOK, body: `{"snapshot_id":"snapshot-two"}`},
		{name: "ok sin cuerpo", code: http.StatusOK},
		{name: "sin contenido", code: http.StatusNoContent},
		{name: "aceptado", code: http.StatusAccepted, body: `{"snapshot_id":"snapshot-two"}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			catalog, calls := spotifyWriteCatalog(t, func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tc.code)
				_, _ = io.WriteString(w, tc.body)
			})
			err := catalog.RemoveFromPlaylist(context.Background(), "spotify:playlist:mine", "spotify:track:one", "snapshot-one")
			if err != nil || calls.Load() != 1 {
				t.Fatalf("remove from playlist = %v; calls=%d", err, calls.Load())
			}
		})
	}
}

// TestSpotifyRemoveFromPlaylistSnapshotConflict separa un snapshot viejo de
// cualquier otro fallo: la lista cambió y hay que volver a leerla.
func TestSpotifyRemoveFromPlaylistSnapshotConflict(t *testing.T) {
	const secret = "fixture secret body"
	catalog, calls := spotifyWriteCatalog(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = io.WriteString(w, `{"error":{"status":400,"message":"`+secret+`"}}`)
	})
	err := catalog.RemoveFromPlaylist(context.Background(), "spotify:playlist:mine", "spotify:track:one", "snapshot-stale")
	if !errors.Is(err, ErrPlaylistSnapshotConflict) || calls.Load() != 1 {
		t.Fatalf("snapshot conflict = %v; calls=%d", err, calls.Load())
	}
	for _, other := range []error{ErrSpotifyCatalog, ErrSpotifyWriteScope, ErrPlaylistNotEditable, errSpotifyRateLimited} {
		if errors.Is(err, other) {
			t.Fatalf("conflict also matches %v", other)
		}
	}
	if strings.Contains(err.Error(), secret) {
		t.Fatalf("error leaked the response body: %v", err)
	}
}

func TestSpotifyRemoveFromPlaylistForbiddenMapping(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		want       error
	}{
		{
			name: "sin permiso de escritura",
			body: `{"error":{"status":403,"message":"Insufficient client scope"}}`,
			want: ErrSpotifyWriteScope,
		},
		{
			name: "lista no editable",
			body: `{"error":{"status":403,"message":"You cannot remove tracks from a playlist you don't own."}}`,
			want: ErrPlaylistNotEditable,
		},
		{
			name: "cuerpo sin clasificar",
			body: `{"message":"fixture secret body"}`,
			want: ErrPlaylistNotEditable,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			catalog, calls := spotifyWriteCatalog(t, func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusForbidden)
				_, _ = io.WriteString(w, tc.body)
			})
			err := catalog.RemoveFromPlaylist(context.Background(), "spotify:playlist:mine", "spotify:track:one", "snapshot-one")
			if err == nil || !errors.Is(err, tc.want) || calls.Load() != 1 {
				t.Fatalf("forbidden removal = %v; calls=%d", err, calls.Load())
			}
			for _, other := range []error{ErrSpotifyWriteScope, ErrPlaylistNotEditable, ErrSpotifyCatalog, errSpotifyRateLimited, ErrPlaylistSnapshotConflict} {
				if other != tc.want && errors.Is(err, other) {
					t.Fatalf("error %v also matches %v", err, other)
				}
			}
			if strings.Contains(err.Error(), "fixture secret body") {
				t.Fatalf("error leaked the response body: %v", err)
			}
		})
	}
}

func TestSpotifyRemoveFromPlaylistRejectedResponses(t *testing.T) {
	const secret = "fixture secret body"
	for _, tc := range []struct {
		name     string
		body     string
		location string
		want     error
		code     int
	}{
		{name: "server error", code: http.StatusInternalServerError, body: secret, want: ErrSpotifyCatalog},
		{name: "lista ausente", code: http.StatusNotFound, body: secret, want: ErrSpotifyCatalog},
		{name: "rate limit", code: http.StatusTooManyRequests, body: secret, want: errSpotifyRateLimited},
		{name: "redirect", code: http.StatusTemporaryRedirect, body: secret, want: ErrSpotifyCatalog, location: "/redirected"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			catalog, calls := spotifyWriteCatalog(t, func(w http.ResponseWriter, r *http.Request) {
				if tc.location != "" {
					w.Header().Set("Location", tc.location)
				}
				w.WriteHeader(tc.code)
				_, _ = io.WriteString(w, tc.body)
			})
			err := catalog.RemoveFromPlaylist(context.Background(), "spotify:playlist:mine", "spotify:track:one", "snapshot-one")
			if err == nil || !errors.Is(err, tc.want) || errors.Is(err, ErrPlaylistSnapshotConflict) {
				t.Fatalf("rejected removal = %v", err)
			}
			if strings.Contains(err.Error(), secret) {
				t.Fatalf("error leaked the response body: %v", err)
			}
			// Un redirect no se sigue: la petición original es la única.
			if calls.Load() != 1 {
				t.Fatalf("calls = %d, want 1", calls.Load())
			}
		})
	}
}

func TestSpotifyRemoveFromPlaylistTransportFailure(t *testing.T) {
	catalog := spotifyFixtureCatalog(func(*http.Request) (*http.Response, error) {
		return nil, errors.New("fixture transport failure")
	})
	err := catalog.RemoveFromPlaylist(context.Background(), "spotify:playlist:mine", "spotify:track:one", "snapshot-one")
	if !errors.Is(err, ErrSpotifyCatalog) {
		t.Fatalf("transport failure = %v", err)
	}
	for _, other := range []error{ErrSpotifyWriteScope, ErrPlaylistNotEditable, ErrPlaylistSnapshotConflict, errSpotifyRateLimited} {
		if errors.Is(err, other) {
			t.Fatalf("transport failure was misclassified as %v", other)
		}
	}
	if strings.Contains(err.Error(), "fixture transport failure") {
		t.Fatalf("transport failure leaked its cause: %v", err)
	}
}

// TestSpotifyRemoveFromPlaylistRejectsInvalidInput no gasta ninguna petición
// con datos que no puede nombrar sin ambigüedad, incluido un snapshot vacío.
func TestSpotifyRemoveFromPlaylistRejectsInvalidInput(t *testing.T) {
	for _, tc := range []struct{ name, playlist, track, snapshot string }{
		{name: "playlist vacía", track: "spotify:track:one", snapshot: "snapshot-one"},
		{name: "playlist de pista", playlist: "spotify:track:one", track: "spotify:track:one", snapshot: "snapshot-one"},
		{name: "playlist con ruta", playlist: "spotify:playlist:../items", track: "spotify:track:one", snapshot: "snapshot-one"},
		{name: "playlist como URL", playlist: "https://open.spotify.com/playlist/mine", track: "spotify:track:one", snapshot: "snapshot-one"},
		{name: "pista vacía", playlist: "spotify:playlist:mine", snapshot: "snapshot-one"},
		{name: "pista de playlist", playlist: "spotify:playlist:mine", track: "spotify:playlist:mine", snapshot: "snapshot-one"},
		{name: "pista con ruta", playlist: "spotify:playlist:mine", track: "spotify:track:one/other", snapshot: "snapshot-one"},
		{name: "snapshot vacío", playlist: "spotify:playlist:mine", track: "spotify:track:one"},
		{name: "snapshot en blanco", playlist: "spotify:playlist:mine", track: "spotify:track:one", snapshot: "  \t "},
		{name: "snapshot con espacios", playlist: "spotify:playlist:mine", track: "spotify:track:one", snapshot: " snapshot-one "},
	} {
		t.Run(tc.name, func(t *testing.T) {
			catalog, calls := spotifyWriteCatalog(t, func(w http.ResponseWriter, r *http.Request) {
				t.Errorf("invalid input reached the network: %s %s", r.Method, r.URL.Path)
			})
			err := catalog.RemoveFromPlaylist(context.Background(), tc.playlist, tc.track, tc.snapshot)
			if err == nil || calls.Load() != 0 {
				t.Fatalf("invalid input = %v; calls=%d", err, calls.Load())
			}
			// Un dato inválido no se confunde con un snapshot viejo ni con un
			// permiso que falta.
			for _, other := range []error{ErrSpotifyCatalog, ErrSpotifyWriteScope, ErrPlaylistNotEditable, ErrPlaylistSnapshotConflict, errSpotifyRateLimited} {
				if errors.Is(err, other) {
					t.Fatalf("invalid input was classified as %v", other)
				}
			}
		})
	}
}

func TestSpotifyRemoveFromPlaylistRefreshesExpiredToken(t *testing.T) {
	catalog, calls := spotifyWriteCatalog(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/token":
			if r.Method != http.MethodPost || r.Header.Get("Authorization") != "" || r.ParseForm() != nil ||
				r.PostForm.Encode() != "client_id=fixture-client&grant_type=refresh_token&refresh_token=fixture-refresh" {
				t.Errorf("unexpected refresh request: %s %s", r.Method, r.URL.Path)
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"access_token":"new-access","token_type":"Bearer","expires_in":3600}`)
		case "/v1/playlists/mine/tracks":
			if r.Method != http.MethodDelete || r.Header.Get("Authorization") != "Bearer new-access" {
				t.Errorf("refreshed token not used: %s %s %q", r.Method, r.URL.Path, r.Header.Get("Authorization"))
			}
			w.WriteHeader(http.StatusOK)
			_, _ = io.WriteString(w, `{"snapshot_id":"snapshot-two"}`)
		default:
			t.Errorf("unexpected request path %q", r.URL.Path)
		}
	})
	catalog.token.expires = time.Now().Add(-time.Minute)
	err := catalog.RemoveFromPlaylist(context.Background(), "spotify:playlist:mine", "spotify:track:one", "snapshot-one")
	if err != nil || calls.Load() != 2 {
		t.Fatalf("remove from playlist = %v; calls=%d", err, calls.Load())
	}
}
