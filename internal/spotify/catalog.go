package spotify

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
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
	CatalogPageSize = 10
)

var (
	ErrPlaylistUnavailable = errors.New("Spotify playlist contents are unavailable")
	ErrSpotifyCatalog      = errors.New("Spotify catalog request failed")
	// ErrSpotifyWriteScope significa que la aprobación guardada no incluye el
	// permiso de escritura: se recupera aprobando otra vez, sin cambiar de sesión.
	ErrSpotifyWriteScope = errors.New("Spotify authorization cannot change playlists; authorize again")
	// ErrPlaylistNotEditable significa que esta cuenta no puede modificar la
	// lista elegida; volver a aprobar no lo arregla.
	ErrPlaylistNotEditable = errors.New("Spotify playlist cannot be changed by this account")
	errSpotifyOffset       = errors.New("Invalid Spotify catalog offset")
	errSpotifyPlaylistURI  = errors.New("Invalid Spotify playlist URI")
	errSpotifyTrackURI     = errors.New("Invalid Spotify track URI")
	spotifyID              = regexp.MustCompile(`^[A-Za-z0-9]+$`)
)

type Item struct {
	Name, Detail, URI string
}

type Catalog struct {
	client   *http.Client
	clientID string
	// sessionPath permite que una aprobación nueva reemplace la guardada sin
	// cambiar el formato del archivo de sesión.
	sessionPath string

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
	return len(p.Items) > 0 && p.Next != "" && offset <= math.MaxInt-2*CatalogPageSize
}

type spotifyTrack struct {
	Name    string `json:"name"`
	URI     string `json:"uri"`
	Artists []struct {
		Name string `json:"name"`
	} `json:"artists"`
	Playable *bool `json:"is_playable"`
}

func (c *Catalog) Search(ctx context.Context, query string, offset int) ([]Item, bool, error) {
	params := url.Values{"q": {query}, "type": {"track"}}
	var response struct {
		Tracks *spotifyPage[spotifyTrack] `json:"tracks"`
	}
	if err := c.get(ctx, "/v1/search", params, offset, &response, false); err != nil {
		return nil, false, err
	}
	if response.Tracks == nil {
		return nil, false, ErrSpotifyCatalog
	}
	items := make([]Item, 0, len(response.Tracks.Items))
	for _, track := range response.Tracks.Items {
		items = appendSpotifyTrack(items, track)
	}
	return items, response.Tracks.more(offset), nil
}

func (c *Catalog) Playlists(ctx context.Context, offset int) ([]Item, bool, error) {
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
	items := make([]Item, 0, len(page.Items))
	for _, playlist := range page.Items {
		if playlist != nil && spotifyURIID(playlist.URI, "playlist") != "" {
			items = append(items, Item{Name: playlist.Name, Detail: playlist.Owner.DisplayName, URI: playlist.URI})
		}
	}
	return items, page.more(offset), nil
}

func (c *Catalog) PlaylistTracks(ctx context.Context, uri string, offset int) ([]Item, bool, error) {
	id := spotifyURIID(uri, "playlist")
	if id == "" {
		return nil, false, errSpotifyPlaylistURI
	}
	var page spotifyPage[struct {
		Item *spotifyTrack `json:"item"`
	}]
	if err := c.get(ctx, "/v1/playlists/"+id+"/items", url.Values{}, offset, &page, true); err != nil {
		return nil, false, err
	}
	items := make([]Item, 0, len(page.Items))
	for _, entry := range page.Items {
		if entry != nil {
			items = appendSpotifyTrack(items, entry.Item)
		}
	}
	return items, page.more(offset), nil
}

// Profile devuelve el nombre visible de la cuenta con la sesión abierta. La
// API omite el nombre en algunas cuentas, así que el vacío es una respuesta
// válida y no un fallo.
func (c *Catalog) Profile(ctx context.Context) (string, error) {
	var response struct {
		DisplayName string `json:"display_name"`
	}
	// El perfil no pagina: la petición va sin limit ni offset.
	if err := c.request(ctx, "/v1/me", &response, false); err != nil {
		return "", err
	}
	return strings.TrimSpace(response.DisplayName), nil
}

// PlaylistWriter es la mitad de escritura que el reproductor necesita: guardar
// la pista que suena y, cuando Spotify avisa de que falta el permiso, pedirlo
// otra vez con el mismo enlace de autorización que ya se muestra. *Catalog la
// implementa.
type PlaylistWriter interface {
	AddToPlaylist(ctx context.Context, playlistURI, trackURI string) (string, error)
	ReauthorizeWrites(ctx context.Context, showURL func(string)) (PlaylistWriter, error)
}

var _ PlaylistWriter = (*Catalog)(nil)

// AddToPlaylist añade una pista a la lista elegida y devuelve el snapshot que
// Spotify confirma, vacío si la respuesta no lo trae. Un rechazo por permisos o
// por una lista ajena llega como error tipado, nunca con el texto de Spotify.
func (c *Catalog) AddToPlaylist(ctx context.Context, playlistURI, trackURI string) (string, error) {
	id := spotifyURIID(playlistURI, "playlist")
	if id == "" {
		return "", errSpotifyPlaylistURI
	}
	if spotifyURIID(trackURI, "track") == "" {
		return "", errSpotifyTrackURI
	}
	// La ruta documentada por Spotify para añadir elementos a una lista.
	var response struct {
		SnapshotID string `json:"snapshot_id"`
	}
	payload := struct {
		URIs []string `json:"uris"`
	}{URIs: []string{trackURI}}
	if err := c.write(ctx, "/v1/playlists/"+id+"/tracks", payload, &response); err != nil {
		return "", err
	}
	return response.SnapshotID, nil
}

// write hace una sola petición POST autenticada al host fijado de la API. Igual
// que en las lecturas, ni el cuerpo de la respuesta ni el token llegan al
// llamador, un redirect no se sigue y el cliente acota el tiempo de espera.
// Solo un 2xx documentado con cuerpo JSON cuenta como éxito.
func (c *Catalog) write(ctx context.Context, target string, payload, dst any) error {
	data, err := json.Marshal(payload)
	if err != nil {
		return ErrSpotifyCatalog
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, spotifyAPIURL+target, bytes.NewReader(data))
	if err != nil {
		return ErrSpotifyCatalog
	}
	token, err := c.accessToken(ctx)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.client.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return ErrSpotifyCatalog
	}
	defer resp.Body.Close()
	switch resp.StatusCode {
	case http.StatusForbidden:
		return spotifyWriteForbidden(resp.Body)
	case http.StatusTooManyRequests:
		return errSpotifyRateLimited
	case http.StatusOK, http.StatusCreated:
		if readSpotifyJSON(resp.Body, 1<<20, dst) {
			return nil
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
	}
	return ErrSpotifyCatalog
}

// spotifyWriteForbidden separa los dos 403 de la API de escritura: un permiso
// que falta, recuperable aprobando otra vez, y una lista que esta cuenta no
// puede editar. Solo el mensaje clasifica la respuesta; su texto nunca sale de
// aquí, y un cuerpo sin clasificar se trata como lista no editable para no
// mandar a nadie al navegador sin motivo.
func spotifyWriteForbidden(body io.Reader) error {
	var response struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if readSpotifyJSON(body, 1<<20, &response) && strings.Contains(strings.ToLower(response.Error.Message), "scope") {
		return ErrSpotifyWriteScope
	}
	return ErrPlaylistNotEditable
}

// ReleaseSession impide que el catálogo reemplazado guarde su token viejo.
func (c *Catalog) ReleaseSession() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.persist = nil
}

func spotifyURIID(uri, kind string) string {
	id, ok := strings.CutPrefix(uri, "spotify:"+kind+":")
	if !ok || !spotifyID.MatchString(id) {
		return ""
	}
	return id
}

func appendSpotifyTrack(items []Item, track *spotifyTrack) []Item {
	if track == nil || spotifyURIID(track.URI, "track") == "" || (track.Playable != nil && !*track.Playable) {
		return items
	}
	artists := make([]string, 0, len(track.Artists))
	for _, artist := range track.Artists {
		if artist.Name != "" {
			artists = append(artists, artist.Name)
		}
	}
	return append(items, Item{Name: track.Name, Detail: strings.Join(artists, ", "), URI: track.URI})
}

func (c *Catalog) get(ctx context.Context, path string, params url.Values, offset int, dst any, playlistContents bool) error {
	if offset < 0 || offset > math.MaxInt-CatalogPageSize {
		return errSpotifyOffset
	}
	params.Set("limit", strconv.Itoa(CatalogPageSize))
	params.Set("offset", strconv.Itoa(offset))
	return c.request(ctx, path+"?"+params.Encode(), dst, playlistContents)
}

// request hace una sola petición GET autenticada al host fijado de la API y
// traduce cada fallo a un error seguro: ni el cuerpo de la respuesta ni el
// token llegan al llamador.
func (c *Catalog) request(ctx context.Context, target string, dst any, playlistContents bool) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, spotifyAPIURL+target, nil)
	if err != nil {
		return ErrSpotifyCatalog
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
		return ErrSpotifyCatalog
	}
	defer resp.Body.Close()
	switch resp.StatusCode {
	case http.StatusForbidden:
		if playlistContents {
			return ErrPlaylistUnavailable
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
	return ErrSpotifyCatalog
}

func (c *Catalog) accessToken(ctx context.Context) (string, error) {
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
