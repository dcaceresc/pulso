package main

import (
	"context"
	"errors"
	"math"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	spotifyAPIURL = "https://api.spotify.com"
	// Consumers advance offsets by this page size, not by the number of playable items.
	spotifyCatalogPageSize = 10
)

var (
	errPlaylistUnavailable = errors.New("Spotify playlist contents are unavailable")
	errSpotifyCatalog      = errors.New("Spotify catalog request failed")
	errSpotifyOffset       = errors.New("Invalid Spotify catalog offset")
	spotifyID              = regexp.MustCompile(`^[A-Za-z0-9]+$`)
)

type catalogItem struct {
	Name, Detail, URI string
}

type spotifyCatalog struct {
	client   *http.Client
	clientID string

	mu    sync.Mutex
	token spotifyToken
	// persist stores a rotated refresh token so the next launch stays signed in.
	persist func(spotifyToken)
}

type spotifyPage[T any] struct {
	Items []*T   `json:"items"`
	Next  string `json:"next"`
}

// next is only a pagination signal. Requests always use a locally supplied offset.
func (p spotifyPage[T]) more(offset int) bool {
	return len(p.Items) > 0 && p.Next != "" && offset <= math.MaxInt-2*spotifyCatalogPageSize
}

type spotifyTrack struct {
	Name    string `json:"name"`
	URI     string `json:"uri"`
	Artists []struct {
		Name string `json:"name"`
	} `json:"artists"`
	Playable *bool `json:"is_playable"`
}

func (c *spotifyCatalog) Search(ctx context.Context, query string, offset int) ([]catalogItem, bool, error) {
	params := url.Values{"q": {query}, "type": {"track"}}
	var response struct {
		Tracks *spotifyPage[spotifyTrack] `json:"tracks"`
	}
	if err := c.get(ctx, "/v1/search", params, offset, &response, false); err != nil {
		return nil, false, err
	}
	if response.Tracks == nil {
		return nil, false, errSpotifyCatalog
	}
	items := make([]catalogItem, 0, len(response.Tracks.Items))
	for _, track := range response.Tracks.Items {
		items = appendSpotifyTrack(items, track)
	}
	return items, response.Tracks.more(offset), nil
}

func (c *spotifyCatalog) Playlists(ctx context.Context, offset int) ([]catalogItem, bool, error) {
	var page spotifyPage[struct {
		Name  string `json:"name"`
		URI   string `json:"uri"`
		Owner struct {
			DisplayName string `json:"display_name"`
		} `json:"owner"`
	}]
	if err := c.get(ctx, "/v1/me/playlists", url.Values{}, offset, &page, false); err != nil {
		return nil, false, err
	}
	items := make([]catalogItem, 0, len(page.Items))
	for _, playlist := range page.Items {
		if playlist != nil && spotifyURIID(playlist.URI, "playlist") != "" {
			items = append(items, catalogItem{Name: playlist.Name, Detail: playlist.Owner.DisplayName, URI: playlist.URI})
		}
	}
	return items, page.more(offset), nil
}

func (c *spotifyCatalog) PlaylistTracks(ctx context.Context, uri string, offset int) ([]catalogItem, bool, error) {
	id := spotifyURIID(uri, "playlist")
	if id == "" {
		return nil, false, errors.New("Invalid Spotify playlist URI")
	}
	var page spotifyPage[struct {
		Item *spotifyTrack `json:"item"`
	}]
	if err := c.get(ctx, "/v1/playlists/"+id+"/items", url.Values{}, offset, &page, true); err != nil {
		return nil, false, err
	}
	items := make([]catalogItem, 0, len(page.Items))
	for _, entry := range page.Items {
		if entry != nil {
			items = appendSpotifyTrack(items, entry.Item)
		}
	}
	return items, page.more(offset), nil
}

func spotifyURIID(uri, kind string) string {
	id, ok := strings.CutPrefix(uri, "spotify:"+kind+":")
	if !ok || !spotifyID.MatchString(id) {
		return ""
	}
	return id
}

func appendSpotifyTrack(items []catalogItem, track *spotifyTrack) []catalogItem {
	if track == nil || spotifyURIID(track.URI, "track") == "" || (track.Playable != nil && !*track.Playable) {
		return items
	}
	artists := make([]string, 0, len(track.Artists))
	for _, artist := range track.Artists {
		if artist.Name != "" {
			artists = append(artists, artist.Name)
		}
	}
	return append(items, catalogItem{Name: track.Name, Detail: strings.Join(artists, ", "), URI: track.URI})
}

func (c *spotifyCatalog) get(ctx context.Context, path string, params url.Values, offset int, dst any, playlistContents bool) error {
	if offset < 0 || offset > math.MaxInt-spotifyCatalogPageSize {
		return errSpotifyOffset
	}
	params.Set("limit", strconv.Itoa(spotifyCatalogPageSize))
	params.Set("offset", strconv.Itoa(offset))
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, spotifyAPIURL+path+"?"+params.Encode(), nil)
	if err != nil {
		return errSpotifyCatalog
	}
	token, err := c.accessToken(ctx)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/json")
	resp, err := c.client.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return errSpotifyCatalog
	}
	defer resp.Body.Close()
	switch resp.StatusCode {
	case http.StatusForbidden:
		if playlistContents {
			return errPlaylistUnavailable
		}
	case http.StatusTooManyRequests:
		return errSpotifyRateLimited
	case http.StatusOK:
		if readSpotifyJSON(resp.Body, 1<<20, dst) {
			return nil
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
	}
	return errSpotifyCatalog
}

func (c *spotifyCatalog) accessToken(ctx context.Context) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if time.Now().Before(c.token.expires) {
		return c.token.access, nil
	}
	if c.token.refresh == "" {
		return "", errors.New("Spotify authorization expired; sign in again")
	}
	token, err := requestSpotifyToken(ctx, c.client, url.Values{
		"grant_type":    {"refresh_token"},
		"client_id":     {c.clientID},
		"refresh_token": {c.token.refresh},
	})
	if err != nil {
		return "", err
	}
	if token.refresh == "" {
		token.refresh = c.token.refresh
	}
	c.token = token
	if c.persist != nil {
		c.persist(token)
	}
	return token.access, nil
}
