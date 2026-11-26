package main

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
	spotifyAuthorizationTimeout = 3 * time.Minute
)

var (
	errSpotifyRateLimited = errors.New("Spotify rate limit reached; try again later")
	errSpotifyToken       = errors.New("Spotify token request failed")
	errSpotifyClientID    = errors.New("Spotify rejected the Client ID or redirect URI")
	spotifyBearer         = regexp.MustCompile(`^[A-Za-z0-9._~+/\-]+=*$`)
)

type spotifyToken struct {
	access, refresh string
	expires         time.Time
}

// authorizeSpotify displays the authorization link and waits for the local callback.
// showURL should return promptly so the caller can cancel while waiting for Spotify.
func authorizeSpotify(ctx context.Context, clientID string, showURL func(string)) (*spotifyCatalog, error) {
	return authorizeSpotifyWithTransport(ctx, clientID, showURL, nil)
}

func authorizeSpotifyWithTransport(ctx context.Context, clientID string, showURL func(string), transport http.RoundTripper) (*spotifyCatalog, error) {
	clientID = strings.TrimSpace(clientID)
	if clientID == "" || showURL == nil {
		return nil, errors.New("Spotify login needs a client ID and an authorization link handler")
	}
	ctx, cancel := context.WithTimeout(ctx, spotifyAuthorizationTimeout)
	defer cancel()
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	// Independent 256-bit random values produce a 43-character verifier and state.
	var random [64]byte
	if _, err := rand.Read(random[:]); err != nil {
		return nil, errors.New("Could not initialize Spotify authorization")
	}
	state := base64.RawURLEncoding.EncodeToString(random[:32])
	verifier := base64.RawURLEncoding.EncodeToString(random[32:])
	challenge := sha256.Sum256([]byte(verifier))

	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		return nil, errors.New("Could not start the Spotify loopback callback")
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

	client := newSpotifyHTTPClient(transport)
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
	return &spotifyCatalog{client: client, clientID: clientID, token: token}, nil
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
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, spotifyTokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return spotifyToken{}, errSpotifyToken
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return spotifyToken{}, ctx.Err()
		}
		return spotifyToken{}, errSpotifyToken
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusTooManyRequests {
		return spotifyToken{}, errSpotifyRateLimited
	}
	var token struct {
		Access  string `json:"access_token"`
		Refresh string `json:"refresh_token"`
		Type    string `json:"token_type"`
		Expires int64  `json:"expires_in"`
	}
	if resp.StatusCode != http.StatusOK || !readSpotifyJSON(resp.Body, 64<<10, &token) || !strings.EqualFold(token.Type, "Bearer") || !spotifyBearer.MatchString(token.Access) || token.Expires <= 0 || token.Expires > int64((1<<63-1)/time.Second) {
		return spotifyToken{}, errSpotifyToken
	}
	return spotifyToken{access: token.Access, refresh: token.Refresh, expires: time.Now().Add(time.Duration(token.Expires) * time.Second)}, nil
}

func readSpotifyJSON(r io.Reader, limit int64, dst any) bool {
	data, err := io.ReadAll(io.LimitReader(r, limit+1))
	return err == nil && int64(len(data)) <= limit && json.Unmarshal(data, dst) == nil
}
