package spotify

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
)

const (
	spotifyAccountsURL          = "https://accounts.spotify.com"
	spotifyTokenURL             = spotifyAccountsURL + "/api/token"
	spotifyCallbackPath         = "/callback"
	spotifyCallbackDefaultAddr  = "127.0.0.1:8888"
	spotifyAuthorizationTimeout = 3 * time.Minute
)

var (
	errSpotifyRateLimited = errors.New("Spotify rate limit reached; try again later")
	errSpotifyToken       = errors.New("Spotify token request failed")
	// A definitive rejection means the saved approval is gone for good.
	errSpotifyTokenRejected = errors.New("Spotify rejected the authorization; approve access again")
	errSpotifyClientID      = errors.New("Spotify rejected the Client ID or redirect URI")
	// Spotify only accepts loopback redirects with an explicit port, so the
	// address is fixed and must match the one registered in the dashboard.
	spotifyCallbackAddr = spotifyCallbackDefaultAddr
	spotifyBearer       = regexp.MustCompile(`^[A-Za-z0-9._~+/\-]+=*$`)
)

type spotifyToken struct {
	access, refresh string
	expires         time.Time
}

// Authorize restores the saved session when possible and otherwise
// displays the authorization link and waits for the local callback.
// showURL should return promptly so the caller can cancel while waiting for Spotify.
func Authorize(ctx context.Context, clientID string, showURL func(string)) (*Catalog, error) {
	path, err := spotifySessionPath()
	if err != nil {
		path = ""
	}
	return authorizeSpotifyAt(ctx, path, clientID, showURL, nil)
}

// authorizeSpotifyWithTransport keeps the interactive-only protocol seam. It
// never reads or writes a stored session.
func authorizeSpotifyWithTransport(ctx context.Context, clientID string, showURL func(string), transport http.RoundTripper) (*Catalog, error) {
	return authorizeSpotifyAt(ctx, "", clientID, showURL, transport)
}

// authorizeSpotifyAt tries the saved session first. A valid access token needs
// no network call, an expired one is refreshed silently, and only a missing or
// rejected approval falls back to the browser and the loopback listener.
// An empty sessionPath means no persistence, which the protocol fixtures use.
func authorizeSpotifyAt(ctx context.Context, sessionPath, clientID string, showURL func(string), transport http.RoundTripper) (*Catalog, error) {
	clientID = strings.TrimSpace(clientID)
	if clientID == "" || showURL == nil {
		return nil, errors.New("Spotify login needs a client ID and an authorization link handler")
	}
	ctx, cancel := context.WithTimeout(ctx, spotifyAuthorizationTimeout)
	defer cancel()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	client := newSpotifyHTTPClient(transport)
	if catalog, ok, err := restoreSpotifyCatalog(ctx, sessionPath, clientID, client); err != nil {
		return nil, err
	} else if ok {
		return catalog, nil
	}
	// Independent 256-bit random values produce a 43-character verifier and state.
	var random [64]byte
	if _, err := rand.Read(random[:]); err != nil {
		return nil, errors.New("Could not initialize Spotify authorization")
	}
	state := base64.RawURLEncoding.EncodeToString(random[:32])
	verifier := base64.RawURLEncoding.EncodeToString(random[32:])
	challenge := sha256.Sum256([]byte(verifier))

	listener, err := net.Listen("tcp4", spotifyCallbackAddr)
	if err != nil {
		return nil, errors.New("Could not listen on " + spotifyCallbackAddr + " for the Spotify callback; free the port and retry")
	}
	host := listener.Addr().String()
	redirect := "http://" + host + spotifyCallbackPath
	params := url.Values{
		"client_id":             {clientID},
		"response_type":         {"code"},
		"redirect_uri":          {redirect},
		"scope":                 {"playlist-read-private playlist-read-collaborative"},
		"state":                 {state},
		"code_challenge_method": {"S256"},
		"code_challenge":        {base64.RawURLEncoding.EncodeToString(challenge[:])},
	}
	code, err := waitSpotifyCallback(ctx, listener, host, state, func() {
		showURL(spotifyAccountsURL + "/authorize?" + params.Encode())
	})
	if err != nil {
		return nil, err
	}

	token, err := requestSpotifyToken(ctx, client, url.Values{
		"grant_type":    {"authorization_code"},
		"client_id":     {clientID},
		"code":          {code},
		"redirect_uri":  {redirect},
		"code_verifier": {verifier},
	})
	if err != nil {
		return nil, err
	}
	// Best effort: the approved session is what makes the next launch silent.
	if sessionPath != "" {
		_ = saveSpotifySessionAt(sessionPath, spotifySession{access: token.access, refresh: token.refresh, expires: token.expires})
	}
	return newSpotifyCatalog(client, clientID, token, sessionPath), nil
}

// restoreSpotifyCatalog uses the saved session without any user interaction.
// A false result with no error means "no usable session"; an error is a
// temporary failure that must not discard the saved approval.
func restoreSpotifyCatalog(ctx context.Context, sessionPath, clientID string, client *http.Client) (*Catalog, bool, error) {
	clientID = strings.TrimSpace(clientID)
	if sessionPath == "" || clientID == "" {
		return nil, false, nil
	}
	if err := ctx.Err(); err != nil {
		return nil, false, err
	}
	session, ok := loadSpotifySessionAt(sessionPath)
	if !ok {
		return nil, false, nil
	}
	if time.Now().Before(session.expires) {
		return newSpotifyCatalog(client, clientID, session.token(), sessionPath), true, nil
	}
	token, err := requestSpotifyToken(ctx, client, url.Values{
		"grant_type":    {"refresh_token"},
		"client_id":     {clientID},
		"refresh_token": {session.refresh},
	})
	if err != nil {
		if errors.Is(err, errSpotifyTokenRejected) {
			_ = deleteSpotifySessionAt(sessionPath)
			return nil, false, nil
		}
		return nil, false, err
	}
	if token.refresh == "" {
		token.refresh = session.refresh
	}
	_ = saveSpotifySessionAt(sessionPath, spotifySession{access: token.access, refresh: token.refresh, expires: token.expires})
	return newSpotifyCatalog(client, clientID, token, sessionPath), true, nil
}

// storedSpotifyCatalog restores the user's saved session for the setup
// wizard's narrow hook, where any failure simply means "no session yet".
func storedSpotifyCatalog(ctx context.Context, clientID string) (*Catalog, bool) {
	path, err := spotifySessionPath()
	if err != nil {
		return nil, false
	}
	catalog, ok, _ := restoreSpotifyCatalog(ctx, path, clientID, newSpotifyHTTPClient(nil))
	return catalog, ok
}

// Restore devuelve la sesión guardada para el hook estrecho del asistente de
// configuración. El segundo resultado es false cuando no hay una sesión
// utilizable.
func Restore(ctx context.Context, clientID string) (*Catalog, bool) {
	return storedSpotifyCatalog(ctx, clientID)
}

func waitSpotifyCallback(ctx context.Context, listener net.Listener, host, state string, showURL func()) (string, error) {
	var accepted = make(chan struct{}, 1)
	type callback struct {
		code string
		err  error
	}
	results := make(chan callback, 1)
	server := &http.Server{
		ReadHeaderTimeout: 3 * time.Second,
		ReadTimeout:       5 * time.Second,
		WriteTimeout:      5 * time.Second,
		MaxHeaderBytes:    8 << 10,
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Cache-Control", "no-store")
			w.Header().Set("Referrer-Policy", "no-referrer")
			if r.URL.EscapedPath() != spotifyCallbackPath || r.Host != host {
				http.NotFound(w, r)
				return
			}
			if r.Method != http.MethodGet {
				w.Header().Set("Allow", http.MethodGet)
				http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
				return
			}
			if len(r.URL.RawQuery) > 4096 {
				http.Error(w, "Callback query too large", http.StatusRequestURITooLong)
				return
			}
			q, err := url.ParseQuery(r.URL.RawQuery)
			if err != nil || len(q["state"]) != 1 || subtle.ConstantTimeCompare([]byte(q.Get("state")), []byte(state)) != 1 {
				http.Error(w, "Invalid Spotify authorization response", http.StatusBadRequest)
				return
			}
			// An unrelated local request must not burn the one real callback.
			select {
			case accepted <- struct{}{}:
			default:
				http.Error(w, "Spotify callback already received", http.StatusConflict)
				return
			}
			if q.Get("error") == "invalid_client" {
				http.Error(w, "Spotify rejected the Client ID", http.StatusBadRequest)
				results <- callback{err: errSpotifyClientID}
				return
			}
			if q.Has("error") || len(q["code"]) != 1 || q.Get("code") == "" {
				http.Error(w, "Spotify authorization did not complete", http.StatusBadRequest)
				results <- callback{err: errors.New("Spotify authorization did not complete")}
				return
			}
			w.Header().Set("Content-Type", "text/plain; charset=utf-8")
			_, _ = io.WriteString(w, "Spotify authorization received. You can return to Pulso.\n")
			results <- callback{code: q.Get("code")}
		}),
	}
	server.SetKeepAlivesEnabled(false)
	served := make(chan struct{})
	go func() {
		_ = server.Serve(listener)
		close(served)
	}()
	defer func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		if err := server.Shutdown(shutdownCtx); err != nil {
			_ = server.Close()
		}
		_ = listener.Close()
		<-served
	}()

	showURL()
	if err := ctx.Err(); err != nil {
		return "", err
	}
	select {
	case result := <-results:
		return result.code, result.err
	case <-ctx.Done():
		return "", ctx.Err()
	case <-served:
		return "", errors.New("Spotify loopback callback stopped")
	}
}

func newSpotifyHTTPClient(transport http.RoundTripper) *http.Client {
	return &http.Client{
		Transport: transport,
		Timeout:   15 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
}

func requestSpotifyToken(ctx context.Context, client *http.Client, form url.Values) (spotifyToken, error) {
	token, status, err := exchangeSpotifyToken(ctx, client, form)
	switch {
	case err != nil:
		return spotifyToken{}, err
	case status == http.StatusOK:
		return token, nil
	case status == http.StatusBadRequest || status == http.StatusUnauthorized:
		return spotifyToken{}, errSpotifyTokenRejected
	default:
		return spotifyToken{}, errSpotifyToken
	}
}

// exchangeSpotifyToken reports the status so a rejected authorization can be
// told apart from a temporary failure.
func exchangeSpotifyToken(ctx context.Context, client *http.Client, form url.Values) (spotifyToken, int, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, spotifyTokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return spotifyToken{}, 0, errSpotifyToken
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return spotifyToken{}, 0, ctx.Err()
		}
		return spotifyToken{}, 0, errSpotifyToken
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusTooManyRequests {
		return spotifyToken{}, resp.StatusCode, errSpotifyRateLimited
	}
	if resp.StatusCode != http.StatusOK {
		// The status alone classifies a rejection; an error body is never surfaced.
		return spotifyToken{}, resp.StatusCode, nil
	}
	var token struct {
		Access  string `json:"access_token"`
		Refresh string `json:"refresh_token"`
		Type    string `json:"token_type"`
		Expires int64  `json:"expires_in"`
	}
	if !readSpotifyJSON(resp.Body, 64<<10, &token) || !strings.EqualFold(token.Type, "Bearer") || !spotifyBearer.MatchString(token.Access) || token.Expires <= 0 || token.Expires > int64((1<<63-1)/time.Second) {
		return spotifyToken{}, resp.StatusCode, errSpotifyToken
	}
	return spotifyToken{access: token.Access, refresh: token.Refresh, expires: time.Now().Add(time.Duration(token.Expires) * time.Second)}, resp.StatusCode, nil
}

func readSpotifyJSON(r io.Reader, limit int64, dst any) bool {
	data, err := io.ReadAll(io.LimitReader(r, limit+1))
	return err == nil && int64(len(data)) <= limit && json.Unmarshal(data, dst) == nil
}

// RedirectHint is the exact value users must register in the dashboard.
func RedirectHint() string {
	addr := spotifyCallbackAddr
	if strings.HasSuffix(addr, ":0") {
		addr = spotifyCallbackDefaultAddr
	}
	return "http://" + addr + spotifyCallbackPath
}

// newSpotifyCatalog keeps the rotated refresh token on disk so an expiring
// access token never forces a new browser approval.
// NewCatalog construye el catálogo que el flujo OAuth devuelve tras la
// aprobación. Quien llama solo aporta un cliente HTTP propio; el token
// permanece dentro de este paquete.
func NewCatalog(client *http.Client, clientID, access, refresh string, expires time.Time, sessionPath string) *Catalog {
	return newSpotifyCatalog(client, clientID, spotifyToken{access: access, refresh: refresh, expires: expires}, sessionPath)
}

func newSpotifyCatalog(client *http.Client, clientID string, token spotifyToken, sessionPath string) *Catalog {
	catalog := &Catalog{client: client, clientID: clientID, token: token}
	if sessionPath != "" {
		catalog.persist = func(refreshed spotifyToken) {
			_ = saveSpotifySessionAt(sessionPath, spotifySession{access: refreshed.access, refresh: refreshed.refresh, expires: refreshed.expires})
		}
	}
	return catalog
}
