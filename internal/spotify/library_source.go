package spotify

import (
	"context"
	"errors"
	"net/url"
	"strings"
)

// Track es una fila reproducible de una página de lista o de búsqueda.
// Snapshot es el snapshot de la lista de la que se leyó la fila: una retirada lo
// nombra para no borrar sobre una lista que cambió. Solo la lectura que arma la
// tabla de una lista abierta lo completa; una búsqueda lo deja vacío y por eso no
// se puede quitar desde ahí.
type Track struct {
	Name, Artist string
	DurationMS   int64
	URI          string
	Snapshot     string
	// Cover es la URL de la carátula del álbum, elegida por CoverImage entre las
	// imágenes que la misma respuesta ya trae. Queda vacía cuando la pista no
	// publica ninguna, y entonces la caja dibuja sus iniciales.
	Cover string
}

// Playlist es una fila de la barra lateral; Total es el número de pistas de la
// lista.
type Playlist struct {
	Name, Owner, URI string
	Total            int
}

// librarySpotifyPage keeps the shared page shape and adds the API's total.
type librarySpotifyPage[T any] struct {
	spotifyPage[T]
	Total int `json:"total"`
}

// PlaylistItemsPage es una página de pistas de una lista junto con el snapshot
// de la lista que se leyó: la retirada de una aparición concreta nombra ese
// snapshot para no borrar sobre una lista que cambió.
type PlaylistItemsPage struct {
	Tracks   []Track
	Total    int
	More     bool
	Snapshot string
}

// librarySpotifyTrack adds the duration the shared search track type omits.
type librarySpotifyTrack struct {
	spotifyTrack
	DurationMS int64 `json:"duration_ms"`
}

// PlaylistPage reads one page of the signed-in user's playlists.
func (c *Catalog) PlaylistPage(ctx context.Context, offset int) ([]Playlist, int, bool, error) {
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
	playlists := make([]Playlist, 0, len(page.Items))
	for _, playlist := range page.Items {
		if playlist == nil || spotifyURIID(playlist.URI, "playlist") == "" {
			continue
		}
		playlists = append(playlists, Playlist{
			Name: playlist.Name, Owner: playlist.Owner.DisplayName,
			URI: playlist.URI, Total: playlist.Tracks.Total,
		})
	}
	return playlists, page.Total, page.more(offset), nil
}

// TrackPage reads one page of a playlist's items. A restricted playlist keeps
// the shared ErrPlaylistUnavailable signal so the caller can still play it.
func (c *Catalog) TrackPage(ctx context.Context, uri string, offset int) ([]Track, int, bool, error) {
	page, err := c.PlaylistItems(ctx, uri, offset)
	return page.Tracks, page.Total, page.More, err
}

// PlaylistItems lee una página de elementos de una lista y el snapshot de esa
// lista. La ruta, la página y el filtro de pistas son los mismos que usa
// TrackPage; el snapshot se lee aparte, del objeto de la lista, porque la página
// de elementos no lo trae.
func (c *Catalog) PlaylistItems(ctx context.Context, uri string, offset int) (PlaylistItemsPage, error) {
	id := spotifyURIID(uri, "playlist")
	if id == "" {
		return PlaylistItemsPage{}, errors.New("Invalid Spotify playlist URI")
	}
	var page librarySpotifyPage[struct {
		Item *librarySpotifyTrack `json:"item"`
	}]
	if err := c.get(ctx, "/v1/playlists/"+id+"/items", url.Values{}, offset, &page, true); err != nil {
		return PlaylistItemsPage{}, err
	}
	tracks := make([]Track, 0, len(page.Items))
	for _, entry := range page.Items {
		if entry != nil {
			tracks = appendLibraryTrack(tracks, entry.Item)
		}
	}
	return PlaylistItemsPage{Tracks: tracks, Total: page.Total, More: page.more(offset), Snapshot: c.playlistSnapshot(ctx, id)}, nil
}

// playlistSnapshot lee el snapshot de la lista en su propio objeto con una
// consulta estrecha. Un fallo aquí no impide mostrar las pistas: deja el
// snapshot vacío y, con él, la retirada no disponible en vez de inventar una
// versión de la lista.
func (c *Catalog) playlistSnapshot(ctx context.Context, id string) string {
	var response struct {
		SnapshotID string `json:"snapshot_id"`
	}
	if err := c.request(ctx, "/v1/playlists/"+id+"?fields=snapshot_id", &response, false); err != nil {
		return ""
	}
	return spotifySnapshotValue(response.SnapshotID)
}

// SearchPage reads one page of track search results. An empty query is not a
// request: it returns no rows and never reaches the network. The API caps a
// search page at ten tracks, which is the shared page size.
func (c *Catalog) SearchPage(ctx context.Context, query string, offset int) ([]Track, int, bool, error) {
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
		return nil, 0, false, ErrSpotifyCatalog
	}
	tracks := make([]Track, 0, len(response.Tracks.Items))
	for _, track := range response.Tracks.Items {
		tracks = appendLibraryTrack(tracks, track)
	}
	return tracks, response.Tracks.Total, response.Tracks.more(offset), nil
}

// appendLibraryTrack reuses the shared track filter and artist join, then adds
// the duration the table column renders.
func appendLibraryTrack(tracks []Track, track *librarySpotifyTrack) []Track {
	if track == nil {
		return tracks
	}
	usable := appendSpotifyTrack(nil, &track.spotifyTrack)
	if len(usable) == 0 {
		return tracks
	}
	item := usable[0]
	return append(tracks, Track{Name: item.Name, Artist: item.Detail, DurationMS: track.DurationMS, URI: item.URI,
		Cover: CoverImage(track.Album.Images, CoverWidth)})
}
