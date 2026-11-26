package main

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestSpotifyCatalogOAuth(t *testing.T) {
	for _, tc := range []struct {
		name          string
		stateMismatch bool
		redirectToken bool
	}{
		{name: "success"}, {name: "mismatched state", stateMismatch: true}, {name: "token redirect", redirectToken: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var redirect, challenge string
			calls := 0
			fixture := spotifyFixtureTransport(func(r *http.Request) (*http.Response, error) {
				calls++
				if r.Method != http.MethodPost || r.URL.String() != "https://accounts.spotify.com/api/token" || r.Header.Get("Authorization") != "" || r.Header.Get("Content-Type") != "application/x-www-form-urlencoded" || r.ParseForm() != nil {
					t.Fatal("unexpected token exchange")
				}
				form := r.PostForm
				verifier := form.Get("code_verifier")
				digest := sha256.Sum256([]byte(verifier))
				if len(verifier) < 43 || len(verifier) > 128 || base64.RawURLEncoding.EncodeToString(digest[:]) != challenge || form.Get("redirect_uri") != redirect || form.Get("grant_type") != "authorization_code" || form.Get("code") != "fixture-code" || form.Get("client_id") != "fixture-client" || len(form) != 5 {
					t.Fatal("incorrect PKCE exchange form")
				}
				if tc.redirectToken {
					resp := spotifyFixtureResponse(307, "arbitrary token body")
					resp.Header.Set("Location", "https://accounts.spotify.com/other")
					return resp, nil
				}
				return spotifyFixtureResponse(200, `{"access_token":"fixture-access","refresh_token":"fixture-refresh","token_type":"Bearer","expires_in":3600}`), nil
			})
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			catalog, err := authorizeSpotifyWithTransport(ctx, "fixture-client", func(link string) {
				u, err := url.Parse(link)
				if err != nil || u.Scheme != "https" || u.Host != "accounts.spotify.com" || u.Path != "/authorize" {
					t.Fatal("invalid authorization URL")
				}
				q := u.Query()
				state := q.Get("state")
				challenge, redirect = q.Get("code_challenge"), q.Get("redirect_uri")
				cb, err := url.Parse(redirect)
				if err != nil || cb.Scheme != "http" || cb.Hostname() != "127.0.0.1" || cb.Port() == "" || cb.Path != "/callback" || q.Get("scope") != "playlist-read-private playlist-read-collaborative" || q.Get("response_type") != "code" || q.Get("code_challenge_method") != "S256" || q.Get("client_id") != "fixture-client" || len(state) < 43 || len(challenge) != 43 || q.Has("code_verifier") {
					t.Fatal("invalid authorization parameters")
				}
				if tc.stateMismatch {
					replacement := "A"
					if state[0] == 'A' {
						replacement = "B"
					}
					state = replacement + state[1:]
				}
				cb.RawQuery = url.Values{"code": {"fixture-code"}, "state": {state}}.Encode()
				client := &http.Client{Timeout: 2 * time.Second, Transport: &http.Transport{DisableKeepAlives: true}}
				for _, method := range []string{http.MethodPost, http.MethodGet} {
					req, err := http.NewRequestWithContext(ctx, method, cb.String(), nil)
					if err != nil {
						t.Fatal(err)
					}
					resp, err := client.Do(req)
					if err != nil {
						t.Fatal(err)
					}
					body, readErr := io.ReadAll(resp.Body)
					resp.Body.Close()
					want := http.StatusOK
					if method == http.MethodPost {
						want = http.StatusMethodNotAllowed
					} else if tc.stateMismatch {
						want = http.StatusBadRequest
					}
					if readErr != nil || resp.StatusCode != want || strings.Contains(string(body), "fixture-code") || strings.Contains(string(body), state) {
						t.Fatalf("callback status = %d, want %d", resp.StatusCode, want)
					}
				}
			}, fixture)
			if tc.stateMismatch {
				if err == nil || catalog != nil || calls != 0 {
					t.Fatalf("mismatched state was not rejected; calls=%d, err=%v", calls, err)
				}
			} else if tc.redirectToken {
				if err == nil || strings.Contains(err.Error(), "arbitrary token body") || catalog != nil || calls != 1 {
					t.Fatalf("token redirect was not rejected; calls=%d, err=%v", calls, err)
				}
			} else if err != nil || catalog == nil || calls != 1 {
				t.Fatalf("authorization did not succeed; calls=%d, err=%v", calls, err)
			}
			client := &http.Client{Timeout: time.Second, Transport: &http.Transport{DisableKeepAlives: true}}
			if resp, err := client.Get(redirect); err == nil {
				resp.Body.Close()
				t.Fatal("callback listener was not closed")
			}
		})
	}
}

func TestSpotifyCatalogOAuthCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	called := false
	catalog, err := authorizeSpotifyWithTransport(ctx, "fixture-client", func(string) { cancel() }, spotifyFixtureTransport(func(*http.Request) (*http.Response, error) {
		called = true
		return spotifyFixtureResponse(500, ""), nil
	}))
	if catalog != nil || err != context.Canceled || called {
		t.Fatalf("canceled authorization = %v; exchanged=%v", err, called)
	}
}

func TestWaitSpotifyCallbackIgnoresStrayState(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Skipf("loopback listener unavailable: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	host := listener.Addr().String()
	urlCh := make(chan string, 1)
	type result struct {
		code string
		err  error
	}
	done := make(chan result, 1)
	go func() {
		code, err := waitSpotifyCallback(ctx, listener, host, "expected-state", func() {
			urlCh <- "http://" + host + spotifyCallbackPath + "?state=expected-state&code=good-code"
		})
		done <- result{code, err}
	}()
	<-urlCh
	base := "http://" + host + spotifyCallbackPath
	stray, err := http.Get(base + "?state=wrong&code=stray")
	if err != nil {
		t.Fatal(err)
	}
	stray.Body.Close()
	if stray.StatusCode != http.StatusBadRequest {
		t.Fatalf("stray state status = %d", stray.StatusCode)
	}
	good, err := http.Get(base + "?state=expected-state&code=good-code")
	if err != nil {
		t.Fatal(err)
	}
	good.Body.Close()
	if good.StatusCode != http.StatusOK {
		t.Fatalf("valid callback status = %d, want 200 after a stray request", good.StatusCode)
	}
	select {
	case got := <-done:
		if got.err != nil || got.code != "good-code" {
			t.Fatalf("callback = %+v", got)
		}
	case <-ctx.Done():
		t.Fatal("authorization did not finish")
	}
}
