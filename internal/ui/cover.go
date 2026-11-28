package ui

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"image"
	"image/jpeg"
	"image/png"
	"io"
	"net/http"
	"net/url"
	"reflect"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// La carátula ocupa la ranura de la caja: medio bloque por celda, con el píxel
// de arriba como color de texto y el de abajo como fondo. Así la ranura de veinte
// columnas por diez filas dibuja una imagen de 20×20 píxeles —cuatro veces los
// 10×10 de la ranura anterior— sin más filas.
const (
	coverUpperHalf = "▀"
	// coverPixelRows son los píxeles que caben en cada fila de la ranura.
	coverPixelRows = 2
	// coverMaxBytes acota la descarga: una carátula más pesada no se lee.
	coverMaxBytes = 4 << 20
	// coverMaxPixels acota lo que la cabecera puede declarar, porque el tope de
	// bytes no acota la memoria que pide decodificar: unos cientos de bytes
	// pueden anunciar millones de píxeles. La carátula más grande que Spotify
	// publica mide 640×640 —410 000 píxeles— y la ranura dibuja 20×20, así que un
	// millón deja sitio de sobra y acota la decodificación a unos 4 MiB en RGBA.
	coverMaxPixels = 1 << 20
	// coverTimeout acota la espera de una descarga que no llega.
	coverTimeout = 10 * time.Second
)

var (
	// errCoverFetch cubre cualquier fallo de la descarga: una carátula que no
	// llega deja las iniciales y nada más, así que no se distingue el motivo.
	errCoverFetch = errors.New("cover download failed")
	// errCoverFormat es el único formato que la caja no sabe leer.
	errCoverFormat = errors.New("cover image format is not supported")
	// errCoverTooLarge es una cabecera que anuncia más píxeles de los que la
	// ranura puede necesitar: se rechaza antes de decodificar.
	errCoverTooLarge = errors.New("cover image is larger than the slot needs")
)

// coverSource lee la carátula de una pista que ninguna página cargada trajo.
// *spotify.Catalog lo cumple con una sola consulta estrecha al objeto de pista.
type coverSource interface {
	TrackCover(ctx context.Context, uri string) (string, error)
}

// coverSourceOf acepta el catálogo solo cuando sabe responder una carátula; un
// catálogo nulo de tipo concreto sigue siendo un catálogo ausente.
func coverSourceOf(api CatalogAPI) coverSource {
	if api == nil {
		return nil
	}
	source, ok := api.(coverSource)
	if !ok {
		return nil
	}
	if value := reflect.ValueOf(source); value.Kind() == reflect.Pointer && value.IsNil() {
		return nil
	}
	return source
}

// coverURLMsg es la respuesta de la consulta de respaldo: la URL que Spotify
// publicó para esa pista, o el fallo que se recuerda.
type coverURLMsg struct {
	uri, url string
	err      error
}

// coverBytesMsg es la respuesta de una descarga: los bytes de la imagen o el
// fallo, que también se recuerda.
type coverBytesMsg struct {
	url  string
	data []byte
	err  error
}

// coverPicture es una carátula lista para dibujar o el recuerdo de que su URL no
// dio ninguna. Un fallo nunca produce aviso ni fila de error.
type coverPicture struct {
	rows []string
	ok   bool
}

// coverArt guarda lo que la caja sabe de las carátulas: el mapa URI → URL que
// alimentan las páginas ya cargadas, las pistas que ya se consultaron, y la
// imagen decodificada por URL. Toda ausencia se recuerda, así un repintado no
// repite una petición.
type coverArt struct {
	source coverSource
	// fetch descarga los bytes de una URL. Es una función para que una prueba
	// inyecte la suya y ninguna prueba toque la red.
	fetch  func(context.Context, string) ([]byte, error)
	urls   map[string]string
	asked  map[string]bool
	images map[string]coverPicture
	// loading marca las URL cuyos bytes van en camino: la misma imagen no se
	// descarga dos veces mientras la primera sigue pendiente.
	loading map[string]bool
}

func newCoverArt(source coverSource) *coverArt {
	return &coverArt{
		source: source, fetch: coverDownload,
		urls: map[string]string{}, asked: map[string]bool{},
		images: map[string]coverPicture{}, loading: map[string]bool{},
	}
}

// installSource adopta el catálogo que la autorización acaba de destrabar y
// olvida las consultas fallidas: otra sesión puede conocer la pista.
func (c *coverArt) installSource(source coverSource) {
	if c == nil {
		return
	}
	c.source, c.asked = source, map[string]bool{}
}

// remember guarda la carátula que cada fila cargada anunció. Las páginas de una
// lista y los resultados de una búsqueda son la única fuente sin costo: la
// respuesta que ya se pidió trae la URL.
func (c *coverArt) remember(tracks []libraryTrack) {
	if c == nil {
		return
	}
	for _, track := range tracks {
		uri, cover := playerText(track.URI), playerText(track.Cover)
		if uri != "" && cover != "" {
			c.urls[uri] = cover
		}
	}
}

// request devuelve el trabajo pendiente para dibujar la carátula de una pista:
// primero la URL que falte y después sus bytes, una sola vez por URL. Se llama
// al llegar un estado o una página, nunca al dibujar, así la primera pintura no
// espera la imagen.
func (c *coverArt) request(ctx context.Context, uri string) tea.Cmd {
	if c == nil || uri == "" || ctx.Err() != nil {
		return nil
	}
	if link, known := c.urls[uri]; known {
		return c.download(ctx, link)
	}
	return c.lookup(ctx, uri)
}

// lookup pregunta una sola vez por la pista que ninguna página cargada trajo y
// recuerda el intento, con o sin resultado: una pista sin carátula no se vuelve
// a preguntar en cada repintado.
func (c *coverArt) lookup(ctx context.Context, uri string) tea.Cmd {
	if c.source == nil || c.asked[uri] {
		return nil
	}
	c.asked[uri] = true
	source := c.source
	return func() tea.Msg {
		if ctx.Err() != nil {
			return nil
		}
		link, err := source.TrackCover(ctx, uri)
		return coverURLMsg{uri: uri, url: playerText(link), err: err}
	}
}

// download descarga los bytes de una URL conocida, también una sola vez: la
// caché guarda la imagen y el fallo por igual.
func (c *coverArt) download(ctx context.Context, link string) tea.Cmd {
	if _, cached := c.images[link]; cached || c.loading[link] {
		return nil
	}
	c.loading[link] = true
	fetch := c.fetch
	return func() tea.Msg {
		if ctx.Err() != nil {
			return nil
		}
		data, err := fetch(ctx, link)
		return coverBytesMsg{url: link, data: data, err: err}
	}
}

// land guarda la respuesta de una descarga: la imagen decodificada o el recuerdo
// del fallo. Ni el error ni el motivo llegan a la pantalla.
func (c *coverArt) land(msg coverBytesMsg) {
	if c == nil {
		return
	}
	delete(c.loading, msg.url)
	if msg.err != nil {
		c.images[msg.url] = coverPicture{}
		return
	}
	rows, err := coverRows(msg.data)
	if err != nil {
		c.images[msg.url] = coverPicture{}
		return
	}
	c.images[msg.url] = coverPicture{rows: rows, ok: true}
}

// rows devuelve las filas ya dibujables de la carátula de una pista: solo
// cuando la imagen llegó y se decodificó.
func (c *coverArt) rows(uri string) ([]string, bool) {
	if c == nil || uri == "" {
		return nil, false
	}
	link, known := c.urls[uri]
	if !known {
		return nil, false
	}
	picture, done := c.images[link]
	return picture.rows, done && picture.ok
}

// coverRows decodifica los bytes de una carátula y los reduce a las filas de la
// ranura. Solo la biblioteca estándar decodifica: JPEG y PNG.
func coverRows(data []byte) ([]string, error) {
	img, err := coverDecode(data)
	if err != nil {
		return nil, err
	}
	width, height := playerArtWidth, playerArtRows*coverPixelRows
	return coverDraw(coverShrink(img, width, height), width, height), nil
}

// coverDecode elige el decodificador por la firma del archivo y no por su
// nombre, y descarta por la cabecera lo que no cabría en la ranura.
func coverDecode(data []byte) (image.Image, error) {
	var decode func(io.Reader) (image.Image, error)
	switch {
	case bytes.HasPrefix(data, []byte("\x89PNG\r\n\x1a\n")):
		decode = png.Decode
	case bytes.HasPrefix(data, []byte{0xFF, 0xD8, 0xFF}):
		decode = jpeg.Decode
	default:
		return nil, errCoverFormat
	}
	config, _, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil || config.Width <= 0 || config.Height <= 0 {
		return nil, errCoverFormat
	}
	if int64(config.Width)*int64(config.Height) > coverMaxPixels {
		return nil, errCoverTooLarge
	}
	return decode(bytes.NewReader(data))
}

// coverPixel es un píxel de la ranura con ocho bits por canal.
type coverPixel struct{ r, g, b uint8 }

// coverShrink reduce la imagen al tamaño de la ranura promediando cada bloque
// de origen, para que una carátula de 640 px conserve su color medio en las dos
// filas de píxeles que le tocan por celda. Los valores llegan premultiplicados
// por el alfa, que es lo que la imagen declara.
func coverShrink(img image.Image, width, height int) []coverPixel {
	bounds := img.Bounds()
	pixels := make([]coverPixel, 0, width*height)
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			pixels = append(pixels, coverAverage(img, bounds, x, y, width, height))
		}
	}
	return pixels
}

func coverAverage(img image.Image, bounds image.Rectangle, x, y, width, height int) coverPixel {
	x0 := bounds.Min.X + x*bounds.Dx()/width
	x1 := max(x0+1, bounds.Min.X+(x+1)*bounds.Dx()/width)
	y0 := bounds.Min.Y + y*bounds.Dy()/height
	y1 := max(y0+1, bounds.Min.Y+(y+1)*bounds.Dy()/height)
	var sumR, sumG, sumB, count uint64
	for py := y0; py < y1; py++ {
		for px := x0; px < x1; px++ {
			r, g, b, _ := img.At(px, py).RGBA()
			sumR, sumG, sumB = sumR+uint64(r>>8), sumG+uint64(g>>8), sumB+uint64(b>>8)
			count++
		}
	}
	if count == 0 {
		return coverPixel{}
	}
	return coverPixel{r: uint8(sumR / count), g: uint8(sumG / count), b: uint8(sumB / count)}
}

// coverDraw arma las filas de la ranura: cada celda pinta el medio bloque
// superior con el píxel de arriba como color de texto y el de abajo como fondo,
// en color de 24 bits.
func coverDraw(pixels []coverPixel, width, height int) []string {
	rows := make([]string, 0, height/coverPixelRows)
	for row := 0; row < height/coverPixelRows; row++ {
		var line strings.Builder
		for x := 0; x < width; x++ {
			top := pixels[(row*coverPixelRows)*width+x]
			bottom := pixels[(row*coverPixelRows+1)*width+x]
			line.WriteString(lipgloss.NewStyle().
				Foreground(lipgloss.Color(coverHex(top))).
				Background(lipgloss.Color(coverHex(bottom))).
				Render(coverUpperHalf))
		}
		rows = append(rows, line.String())
	}
	return rows
}

func coverHex(pixel coverPixel) string {
	return fmt.Sprintf("#%02X%02X%02X", pixel.r, pixel.g, pixel.b)
}

// coverHTTPClient acota la espera y no sigue redirecciones, igual que el
// cliente de la API: una carátula no necesita ninguna de las dos cosas.
var coverHTTPClient = &http.Client{
	Timeout: coverTimeout,
	CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	},
}

// coverDownload descarga los bytes de una carátula. Es la única ruta de red del
// dibujo: el modelo guarda la función, así que una prueba inyecta la suya y
// nunca la usa.
func coverDownload(ctx context.Context, raw string) ([]byte, error) {
	link, err := url.Parse(raw)
	if err != nil || link.Scheme != "https" || link.Hostname() == "" || link.User != nil {
		return nil, errCoverFetch
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, link.String(), nil)
	if err != nil {
		return nil, errCoverFetch
	}
	resp, err := coverHTTPClient.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, errCoverFetch
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, errCoverFetch
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, coverMaxBytes+1))
	if err != nil || len(data) > coverMaxBytes {
		return nil, errCoverFetch
	}
	return data, nil
}
