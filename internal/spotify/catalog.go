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
	// RecommendationLimit es cuántas pistas pide de una vez la tecla de
	// descubrir: el máximo documentado para una sola semilla de pista.
	RecommendationLimit = 20
	// CoverWidth es el ancho de la carátula en celdas de la caja del reproductor.
	// Elegir con él la imagen que la cubre evita descargar 640 px para dibujar
	// veinte columnas; la caja toma su ranura de aquí, así no hay dos números.
	CoverWidth = 20
)

var (
	ErrPlaylistUnavailable = errors.New("Spotify playlist contents are unavailable")
	ErrSpotifyCatalog      = errors.New("Spotify catalog request failed")
	// ErrSpotifyWriteScope significa que la aprobación guardada no incluye el
	// permiso de escritura, o que Spotify rechazó el cambio sin nombrar una lista
	// que esta cuenta no pueda editar: se recupera aprobando otra vez, sin cambiar
	// de sesión.
	ErrSpotifyWriteScope = errors.New("Spotify authorization cannot change playlists; authorize again")
	// ErrPlaylistNotEditable significa que Spotify afirmó que esta cuenta no
	// puede modificar la lista elegida; volver a aprobar no lo arregla.
	ErrPlaylistNotEditable = errors.New("Spotify playlist cannot be changed by this account")
	// ErrPlaylistSnapshotConflict significa que la lista cambió desde que se
	// leyó: hay que volver a leerla antes de reintentar la retirada.
	ErrPlaylistSnapshotConflict = errors.New("Spotify playlist changed since it was read; read it again")
	// ErrRecommendationsUnavailable significa que esta aplicación de Spotify no
	// conserva acceso a /v1/recommendations. No hay lista que ofrecer y la tecla
	// que lo pidió no reintenta.
	ErrRecommendationsUnavailable = errors.New("Spotify recommendations are unavailable for this app")
	errSpotifyOffset              = errors.New("Invalid Spotify catalog offset")
	errSpotifyPlaylistURI         = errors.New("Invalid Spotify playlist URI")
	errSpotifyTrackURI            = errors.New("Invalid Spotify track URI")
	errSpotifySnapshot            = errors.New("Invalid Spotify playlist snapshot")
	spotifyID                     = regexp.MustCompile(`^[A-Za-z0-9]+$`)
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

// Image es una carátula de álbum con el tamaño que Spotify publica, en píxeles.
// La misma forma se lee en la pista y en la consulta estrecha de respaldo.
type Image struct {
	URL    string `json:"url"`
	Width  int    `json:"width"`
	Height int    `json:"height"`
}

// spotifyAlbum son las carátulas del álbum de una pista. Es el único campo del
// álbum que el reproductor dibuja, así que el resto no se declara.
type spotifyAlbum struct {
	Images []Image `json:"images"`
}

type spotifyTrack struct {
	Name    string `json:"name"`
	URI     string `json:"uri"`
	Artists []struct {
		Name string `json:"name"`
	} `json:"artists"`
	Album    spotifyAlbum `json:"album"`
	Playable *bool        `json:"is_playable"`
}

// CoverImage elige la carátula de una pista entre las imágenes que la respuesta
// ya trae: la más pequeña que alcanza el ancho de la ranura en celdas y, cuando
// ninguna llega, la más grande. Ante dos anchos iguales gana la primera imagen
// que los declara, para que la elección sea siempre la misma.
// coverSourceWidth es el ancho de origen que se prefiere pedir a Spotify. La
// ranura dibuja pocos píxeles, pero reducir desde una imagen grande conserva el
// detalle; partir de un original diminuto lo pierde para siempre.
const coverSourceWidth = 640

// CoverImage elige la imagen de la que se reduce la carátula: la más cercana por
// encima de coverSourceWidth y, si ninguna llega, la mayor publicada. Antes se
// pedía la más pequeña que cubriera la ranura, que es exactamente lo que se veía
// pixelado.
func CoverImage(images []Image) string {
	best, bestWidth := "", 0
	largest, largestWidth := "", 0
	for _, image := range images {
		if image.URL == "" {
			continue
		}
		if largest == "" || image.Width > largestWidth {
			largest, largestWidth = image.URL, image.Width
		}
		if image.Width < coverSourceWidth {
			continue
		}
		if best == "" || image.Width < bestWidth {
			best, bestWidth = image.URL, image.Width
		}
	}
	if best != "" {
		return best
	}
	return largest
}

// TrackCover lee la carátula de una pista que ninguna página cargada trajo. Es
// una sola consulta estrecha al objeto de la pista, con los permisos de lectura
// que la sesión ya tiene: la caja la usa cuando lo que suena no está ni en la
// lista abierta ni en los resultados de una búsqueda.
func (c *Catalog) TrackCover(ctx context.Context, uri string) (string, error) {
	id := spotifyURIID(uri, "track")
	if id == "" {
		return "", errSpotifyTrackURI
	}
	var response struct {
		Album spotifyAlbum `json:"album"`
	}
	if err := c.request(ctx, "/v1/tracks/"+id+"?fields=album.images", &response, false); err != nil {
		return "", err
	}
	return CoverImage(response.Album.Images), nil
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

// Recommendations pide a Spotify pistas parecidas a la semilla, que es la pista
// en reproducción. Devuelve la misma forma de pista que los resultados de la
// búsqueda y usa los permisos de lista que la sesión ya tiene: ningún permiso
// nuevo. Un 403 significa que esta aplicación de Spotify no conserva acceso al
// endpoint, así que se reporta tal cual y no se reintenta; una semilla que no es
// una pista de Spotify no llega a la red.
func (c *Catalog) Recommendations(ctx context.Context, seedURI string) ([]Track, error) {
	id := spotifyURIID(seedURI, "track")
	if id == "" {
		return nil, errSpotifyTrackURI
	}
	var response struct {
		Tracks []*librarySpotifyTrack `json:"tracks"`
	}
	params := url.Values{"seed_tracks": {id}, "limit": {strconv.Itoa(RecommendationLimit)}}
	if err := c.requestWith(ctx, "/v1/recommendations?"+params.Encode(), &response, ErrRecommendationsUnavailable); err != nil {
		return nil, err
	}
	tracks := make([]Track, 0, len(response.Tracks))
	for _, track := range response.Tracks {
		tracks = appendLibraryTrack(tracks, track)
	}
	return tracks, nil
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
// la pista que suena, quitar una aparición concreta de una lista y, cuando
// Spotify avisa de que falta el permiso, pedirlo otra vez con el mismo enlace de
// autorización que ya se muestra. *Catalog la implementa, así que quitar está
// disponible donde está guardar.
type PlaylistWriter interface {
	AddToPlaylist(ctx context.Context, playlistURI, trackURI string) (string, error)
	RemoveFromPlaylist(ctx context.Context, playlistURI, trackURI, snapshot string) error
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
	// La ruta vigente de Spotify para añadir elementos a una lista; su cuerpo
	// conserva la forma documentada con las URI que se agregan.
	var response struct {
		SnapshotID string `json:"snapshot_id"`
	}
	payload := struct {
		URIs []string `json:"uris"`
	}{URIs: []string{trackURI}}
	if err := c.write(ctx, "/v1/playlists/"+id+"/items", payload, &response); err != nil {
		return "", err
	}
	return response.SnapshotID, nil
}

// spotifyPlaylistItem names one occurrence of a track inside a playlist.
type spotifyPlaylistItem struct {
	URI string `json:"uri"`
}

// RemoveFromPlaylist quita una sola aparición de una pista de la lista elegida,
// con el snapshot de la lectura donde se eligió. Un snapshot viejo, un permiso
// que falta y una lista ajena llegan como errores tipados distintos, nunca con
// el texto de Spotify; sin snapshot no hay petición.
func (c *Catalog) RemoveFromPlaylist(ctx context.Context, playlistURI, trackURI, snapshot string) error {
	id := spotifyURIID(playlistURI, "playlist")
	if id == "" {
		return errSpotifyPlaylistURI
	}
	if spotifyURIID(trackURI, "track") == "" {
		return errSpotifyTrackURI
	}
	// El snapshot nombra una versión exacta de la lista: vacío o con espacios
	// que Spotify nunca devuelve no hay petición.
	if spotifySnapshotValue(snapshot) == "" {
		return errSpotifySnapshot
	}
	// La misma ruta vigente de elementos, con el cuerpo documentado para quitar:
	// una sola aparición en `items` y el snapshot de la lectura que la eligió.
	payload := struct {
		Items      []spotifyPlaylistItem `json:"items"`
		SnapshotID string                `json:"snapshot_id"`
	}{Items: []spotifyPlaylistItem{{URI: trackURI}}, SnapshotID: snapshot}
	return c.remove(ctx, "/v1/playlists/"+id+"/items", payload)
}

// write hace una sola petición POST autenticada al host fijado de la API. Igual
// que en las lecturas, ni el cuerpo de la respuesta ni el token llegan al
// llamador, un redirect no se sigue y el cliente acota el tiempo de espera.
// Solo un 2xx documentado con cuerpo JSON cuenta como éxito.
func (c *Catalog) write(ctx context.Context, target string, payload, dst any) error {
	resp, err := c.writeRequest(ctx, http.MethodPost, target, payload)
	if err != nil {
		return err
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

// remove hace una sola petición DELETE autenticada al host fijado de la API.
// Como en write, ni el cuerpo de la respuesta ni el token llegan al llamador, un
// redirect no se sigue y el cliente acota el tiempo de espera. Para esta
// operación cualquier 2xx cuenta como éxito, y un 400 es la señal documentada de
// que el snapshot ya no vale.
func (c *Catalog) remove(ctx context.Context, target string, payload any) error {
	resp, err := c.writeRequest(ctx, http.MethodDelete, target, payload)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	switch {
	case resp.StatusCode == http.StatusBadRequest:
		return ErrPlaylistSnapshotConflict
	case resp.StatusCode == http.StatusForbidden:
		return spotifyWriteForbidden(resp.Body)
	case resp.StatusCode == http.StatusTooManyRequests:
		return errSpotifyRateLimited
	case resp.StatusCode >= 200 && resp.StatusCode < 300:
		return nil
	}
	return ErrSpotifyCatalog
}

// writeRequest arma y envía una sola petición autenticada con cuerpo JSON al
// host fijado de la API. Cada camino de escritura clasifica el estado y cierra
// el cuerpo que recibe.
func (c *Catalog) writeRequest(ctx context.Context, method, target string, payload any) (*http.Response, error) {
	data, err := json.Marshal(payload)
	if err != nil {
		return nil, ErrSpotifyCatalog
	}
	req, err := http.NewRequestWithContext(ctx, method, spotifyAPIURL+target, bytes.NewReader(data))
	if err != nil {
		return nil, ErrSpotifyCatalog
	}
	token, err := c.accessToken(ctx)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.client.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, ErrSpotifyCatalog
	}
	return resp, nil
}

// spotifyPlaylistEditRefusals nombra las frases con las que Spotify afirma que
// esta cuenta no puede cambiar esa lista concreta. Solo ellas producen
// ErrPlaylistNotEditable.
var spotifyPlaylistEditRefusals = []string{
	"you cannot add tracks to a playlist you don't own",
	"you cannot remove tracks from a playlist you don't own",
	"you can't add tracks to a playlist you don't own",
	"you can't remove tracks from a playlist you don't own",
	"you don't own this playlist",
	"you do not own this playlist",
	"not editable by this user",
}

// spotifyWriteForbidden separa los dos 403 de la API de escritura. Un rechazo
// que nombra la falta de permiso sobre esa lista se reporta como lista no
// editable, porque volver a aprobar no lo arregla. Cualquier otro, incluido uno
// sin cuerpo, sin mensaje o sin clasificar, se trata como permiso de escritura
// que falta: así la persona recibe la misma aprobación única en vez de que se le
// diga que no puede cambiar su propia lista. Solo el mensaje clasifica la
// respuesta; su texto nunca sale de aquí.
func spotifyWriteForbidden(body io.Reader) error {
	var response struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if !readSpotifyJSON(body, 1<<20, &response) {
		return ErrSpotifyWriteScope
	}
	message := strings.ToLower(response.Error.Message)
	for _, refusal := range spotifyPlaylistEditRefusals {
		if strings.Contains(message, refusal) {
			return ErrPlaylistNotEditable
		}
	}
	return ErrSpotifyWriteScope
}

// spotifySnapshotValue devuelve el snapshot cuando es un valor que Spotify pudo
// haber escrito, y vacío cuando no: solo un texto no vacío y sin espacios
// sobrantes nombra una versión de la lista. Es la misma regla que usan la
// lectura y la retirada, así que un snapshot ausente nunca se fabrica ni se
// envía.
func spotifySnapshotValue(snapshot string) string {
	if trimmed := strings.TrimSpace(snapshot); trimmed == "" || trimmed != snapshot {
		return ""
	}
	return snapshot
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
// token llegan al llamador. Un 403 se reporta como lista restringida cuando la
// lectura son los elementos de una lista.
func (c *Catalog) request(ctx context.Context, target string, dst any, playlistContents bool) error {
	forbidden := error(nil)
	if playlistContents {
		forbidden = ErrPlaylistUnavailable
	}
	return c.requestWith(ctx, target, dst, forbidden)
}

// requestWith es la misma petición GET; cada lectura nombra con forbidden el
// rechazo por permisos que sabe explicar. Sin ese error, un 403 es un fallo más
// del catálogo, que es lo que hacen las lecturas que no dependen de un permiso
// concreto.
func (c *Catalog) requestWith(ctx context.Context, target string, dst any, forbidden error) error {
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
		if forbidden != nil {
			return forbidden
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
