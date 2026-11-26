package app

import (
	"context"
	"errors"
	"net/url"
	"strings"
)

var _ librarySource = (*spotifyCatalog)(nil)

// librarySpotifyPage keeps the shared page shape and adds the API's total.
type librarySpotifyPage[T any] struct {
	spotifyPage[T]
	Total int `json:"total"`
}

// librarySpotifyTrack adds the duration the shared search track type omits.
type librarySpotifyTrack struct {
	spotifyTrack
	DurationMS int64 `json:"duration_ms"`
}

// PlaylistPage reads one page of the signed-in user's playlists.
func (c *spotifyCatalog) PlaylistPage(ctx context.Context, offset int) ([]libraryPlaylist, int, bool, error) {
	var page librarySpotifyPage[struct {
		Name  string `json:"name"`
		URI   string `json:"uri"`
		Owner struct {
			DisplayName string `json:"display_name"`
		} `json:"owner"`
		Tracks struct {
			Total int `json:"total"`
		} `json:"tracks"`
	}]
	if err := c.get(ctx, "/v1/me/playlists", url.Values{}, offset, &page, false); err != nil {
		return nil, 0, false, err
	}
	playlists := make([]libraryPlaylist, 0, len(page.Items))
	for _, playlist := range page.Items {
		if playlist == nil || spotifyURIID(playlist.URI, "playlist") == "" {
			continue
		}
		playlists = append(playlists, libraryPlaylist{
			Name: playlist.Name, Owner: playlist.Owner.DisplayName,
			URI: playlist.URI, Total: playlist.Tracks.Total,
		})
	}
	return playlists, page.Total, page.more(offset), nil
}

// TrackPage reads one page of a playlist's items. A restricted playlist keeps
// the shared errPlaylistUnavailable signal so the caller can still play it.
func (c *spotifyCatalog) TrackPage(ctx context.Context, uri string, offset int) ([]libraryTrack, int, bool, error) {
	id := spotifyURIID(uri, "playlist")
	if id == "" {
		return nil, 0, false, errors.New("Invalid Spotify playlist URI")
	}
	var page librarySpotifyPage[struct {
		Item *librarySpotifyTrack `json:"item"`
	}]
	if err := c.get(ctx, "/v1/playlists/"+id+"/items", url.Values{}, offset, &page, true); err != nil {
		return nil, 0, false, err
	}
	tracks := make([]libraryTrack, 0, len(page.Items))
	for _, entry := range page.Items {
		if entry != nil {
			tracks = appendLibraryTrack(tracks, entry.Item)
		}
	}
	return tracks, page.Total, page.more(offset), nil
}

// SearchPage reads one page of track search results. An empty query is not a
// request: it returns no rows and never reaches the network. The API caps a
// search page at ten tracks, which is the shared page size.
func (c *spotifyCatalog) SearchPage(ctx context.Context, query string, offset int) ([]libraryTrack, int, bool, error) {
	query = strings.TrimSpace(query)
	if query == "" {
		return nil, 0, false, nil
	}
	var response struct {
		Tracks *librarySpotifyPage[librarySpotifyTrack] `json:"tracks"`
	}
	params := url.Values{"q": {query}, "type": {"track"}}
	if err := c.get(ctx, "/v1/search", params, offset, &response, false); err != nil {
		return nil, 0, false, err
	}
	if response.Tracks == nil {
		return nil, 0, false, errSpotifyCatalog
	}
	tracks := make([]libraryTrack, 0, len(response.Tracks.Items))
	for _, track := range response.Tracks.Items {
		tracks = appendLibraryTrack(tracks, track)
	}
	return tracks, response.Tracks.Total, response.Tracks.more(offset), nil
}

// appendLibraryTrack reuses the shared track filter and artist join, then adds
// the duration the table column renders.
func appendLibraryTrack(tracks []libraryTrack, track *librarySpotifyTrack) []libraryTrack {
	if track == nil {
		return tracks
	}
	usable := appendSpotifyTrack(nil, &track.spotifyTrack)
	if len(usable) == 0 {
		return tracks
	}
	item := usable[0]
	return append(tracks, libraryTrack{Name: item.Name, Artist: item.Detail, DurationMS: track.DurationMS, URI: item.URI})
}
