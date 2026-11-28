package ui

import (
	"bytes"
	"fmt"
	"image"
	"io"
	"os"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/mattn/go-sixel"
)

// La carátula gráfica: cuando el terminal habla el protocolo Sixel, la caja deja
// su ranura en blanco y la imagen se escribe encima, una sola vez por cambio, en
// lugar de reducirla a medios bloques. El medio bloque sigue siendo el respaldo
// de todo lo que no sea Sixel y de cualquier emisión que falle.
//
// La detección es una heurística y el usuario puede imponer el modo con
// PULSO_COVER: un terminal que no conteste a la consulta no pierde la carátula,
// solo la reduce a bloques.

const (
	// coverOverrideEnv fuerza el modo de la carátula: `sixel` o `blocks`.
	coverOverrideEnv = "PULSO_COVER"
	// coverSixelArm es la espera antes de escribir la imagen. Bubble Tea pinta a
	// 60 cuadros por segundo, así que cincuenta milisegundos son varios cuadros:
	// el que dejó la ranura en blanco ya salió y la imagen no la borra el mismo
	// repintado que la reservó.
	coverSixelArm = 50 * time.Millisecond
	// coverSixelColumn es la columna (desde uno) donde empieza la ranura: el
	// borde de la caja, dos espacios de margen y la primera celda de la imagen.
	coverSixelColumn = 4
	// coverSixelRowOffset son las filas (desde uno) que hay del borde superior de
	// la caja a la primera fila de la imagen: el borde, la fila del título y el
	// hueco que separa la imagen de ese título.
	coverSixelRowOffset = 3
)

// coverRenderer es el modo con el que la caja dibuja la carátula.
type coverRenderer uint8

const (
	// coverBlocks es el medio bloque: cada celda pinta dos píxeles de la imagen
	// reducida. Es el respaldo y el modo de todo terminal sin Sixel.
	coverBlocks coverRenderer = iota
	// coverSixel deja la ranura en blanco y escribe la imagen con el protocolo
	// gráfico del terminal.
	coverSixel
)

// coverCell es el tamaño de una celda del terminal en píxeles: la imagen se
// escala a la ranura en píxeles —columnas por ancho de celda, filas por alto de
// celda— y no al número de celdas, que es lo que hace que una carátula se vea
// nítida en lugar de pixelada.
type coverCell struct{ width, height int }

// coverCellFallback es la celda que se usa cuando el terminal no responde a la
// consulta de tamaño (XTWINOPS 16 y, si tampoco contesta, 14 y 18): diez por
// veinte píxeles, la proporción de una fuente de terminal de paso fijo. Con ella
// la ranura grande de veinte por diez celdas mide 200×200 píxeles y la pequeña
// de diez por cinco mide 100×100.
var coverCellFallback = coverCell{width: 10, height: 20}

// coverSixelMsg es el resultado de una emisión: la clave escrita, el fallo, la cancelación o el desfase.
type coverSixelMsg struct {
	key       string
	err       error
	cancelled bool
	stale     bool
}

// coverSixelReadyMsg es el tic que vuelve después del cuadro que dejó la ranura
// en blanco: con ese cuadro ya pintado se puede escribir la imagen.
type coverSixelReadyMsg struct{}

// coverSixelPlan es la imagen que la caja acaba de dejar en blanco y el lugar
// exacto del terminal donde hay que escribirla.
type coverSixelPlan struct {
	key    string
	image  image.Image
	row    int
	column int
}

// coverGraphics es el estado de la carátula gráfica: el modo decidido una vez,
// la celda medida una vez, el escritor del terminal y las claves que dicen qué
// se dibujó, qué se escribió y qué falló.
type coverGraphics struct {
	renderer coverRenderer
	cell     coverCell
	// out es el terminal. Es un campo para que una prueba escriba en un búfer y
	// ninguna prueba toque uno real.
	out io.Writer
	// painted es la clave de la imagen cuyas filas en blanco ya devolvió la
	// vista: solo con esas filas en pantalla se puede escribir la imagen sin que
	// el repintado que sigue la borre.
	painted string
	// emitted es la clave de la imagen que ya está escrita en el terminal.
	emitted string
	// failed es la clave cuya emisión falló: esa imagen, y solo esa, vuelve al
	// medio bloque.
	failed string
	// pending es la imagen en blanco que espera su emisión, con su lugar.
	pending *coverSixelPlan
	// wanted es la clave que la pantalla espera; la emisión la compara al escribir.
	wanted string
}

// coverRendererFor decide el modo una sola vez al arrancar. Un PULSO_COVER
// explícito manda sobre cualquier detección; sin él, Sixel exige que la salida
// sea un terminal —una imagen escrita en una tubería no la ve nadie— y que el
// terminal declare el protocolo al preguntárselo.
func coverRendererFor(override string, terminal, supported func() bool) coverRenderer {
	switch strings.ToLower(strings.TrimSpace(override)) {
	case "sixel":
		return coverSixel
	case "blocks":
		return coverBlocks
	}
	if !terminal() || !supported() {
		return coverBlocks
	}
	return coverSixel
}

// coverTerminalOutput dice si la salida del programa es un terminal. La consulta
// al terminal abre /dev/tty, así que primero se comprueba esto: sin un terminal
// al que escribir la respuesta no sirve de nada y una prueba no la dispara.
func coverTerminalOutput() bool {
	info, err := os.Stdout.Stat()
	return err == nil && info.Mode()&os.ModeCharDevice != 0
}

// coverSixelSupported pregunta al terminal si dibuja Sixel (DA1).
func coverSixelSupported() bool { return sixel.IsSupported() }

// coverCellSize mide la celda con la consulta del terminal y, cuando no hay
// respuesta o la que llega no sirve, usa la celda de respaldo documentada.
func coverCellSize(measure func() (int, int, error)) coverCell {
	width, height, err := measure()
	if err != nil || width <= 0 || height <= 0 {
		return coverCellFallback
	}
	return coverCell{width: width, height: height}
}

// newCoverSixel construye el estado de arranque: decide el modo y, solo cuando
// hay Sixel, mide la celda del terminal.
func newCoverSixel() coverGraphics {
	renderer := coverRendererFor(os.Getenv(coverOverrideEnv), coverTerminalOutput, coverSixelSupported)
	sixelState := coverGraphics{renderer: renderer, cell: coverCellFallback}
	if renderer == coverSixel {
		sixelState.cell = coverCellSize(sixel.CellSize)
	}
	return sixelState
}

// active distingue el modo gráfico del medio bloque.
func (s *coverGraphics) active() bool { return s.renderer == coverSixel }

// output es el terminal donde se escribe la imagen; sin escritor inyectado es la
// salida del programa.
func (s *coverGraphics) output() io.Writer {
	if s.out != nil {
		return s.out
	}
	return os.Stdout
}

// hidden dice si la ranura de esta clave se deja en blanco: solo cuando hay modo
// gráfico y esa imagen no falló.
func (s *coverGraphics) hidden(key string) bool {
	return s.active() && key != "" && s.failed != key
}

// forget olvida la imagen ya escrita. Una pantalla que la tapa —la ayuda, el
// mensaje de tamaño mínimo— la habrá borrado, así que se vuelve a escribir en
// cuanto la caja regrese.
func (s *coverGraphics) forget() {
	s.painted, s.pending, s.emitted = "", nil, ""
}

// coverSixelBlank son las filas que la caja reserva a la imagen: espacio del
// ancho de la ranura, para que la geometría de la caja no cambie y el terminal
// tenga una superficie limpia donde dibujar.
func coverSixelBlank(slot artSlot) []string {
	rows := make([]string, 0, slot.rows)
	for row := 0; row < slot.rows; row++ {
		rows = append(rows, strings.Repeat(" ", slot.columns))
	}
	return rows
}

// coverSixelArt es la imagen que la caja coloca en su ranura, con la clave que
// identifica lo que muestra.
type coverSixelArt struct {
	key   string
	image image.Image
}

// coverSixelArt devuelve la carátula gráfica de la pista en reproducción: la
// imagen decodificada escalada al tamaño en píxeles de la ranura que la banda
// resuelta pide. La ranura vacía, una caja que no afirma nada de la sesión y una
// imagen ausente o ya fallida no tienen carátula gráfica.
func (m *playerModel) coverSixelArt() (coverSixelArt, bool) {
	if !m.sixel.active() || m.quit || m.help || m.playerBoxCondition() != playerBoxLive {
		return coverSixelArt{}, false
	}
	slot := playerArtSlot(m.width, m.height)
	if !slot.ok() {
		return coverSixelArt{}, false
	}
	uri := playerText(m.state.Track.URI)
	link, img, ok := m.covers.artwork(uri, slot, m.sixel.cell)
	if !ok {
		return coverSixelArt{}, false
	}
	// La clave reúne todo lo que cambia lo que se ve: la pista, la imagen, el
	// tamaño de la ventana y el tamaño en píxeles de la ranura. Cambiar
	// cualquiera obliga a escribir la imagen otra vez.
	key := fmt.Sprintf("%s|%s|%dx%d|%dx%d", uri, link, m.width, m.height,
		slot.columns*m.sixel.cell.width, slot.rows*m.sixel.cell.height)
	if m.sixel.failed == key {
		return coverSixelArt{}, false
	}
	return coverSixelArt{key: key, image: img}, true
}

// coverSixelPaint anota la carátula gráfica que la vista acaba de dejar en
// blanco y dónde empieza la ranura en la pantalla. boxTop es la fila de la
// ventana del borde superior de la caja; el reparto es fijo —borde, título y
// hueco—, así que la primera fila de la imagen se cuenta desde ahí. Una vista
// sin carátula gráfica olvida lo anotado.
func (m *playerModel) coverSixelPaint(boxTop int) {
	art, ok := m.coverSixelArt()
	if !ok || boxTop < 0 {
		m.sixel.painted, m.sixel.pending = "", nil
		return
	}
	m.sixel.painted = art.key
	m.sixel.pending = &coverSixelPlan{
		key:    art.key,
		image:  art.image,
		row:    boxTop + coverSixelRowOffset,
		column: coverSixelColumn,
	}
}

// coverSixelWanted lee la clave que la pantalla espera; el candado nunca cubre la escritura.
func (m *playerModel) coverSixelWanted() string {
	m.coverMu.Lock()
	defer m.coverMu.Unlock()
	return m.sixel.wanted
}

// coverSixelEmit escribe la imagen en la ranura una sola vez por cambio: al
// cambiar de pista, al cambiar el tamaño de la ventana y al cambiar el tamaño
// de la imagen. No se escribe en cada repintado porque Bubble Tea, en la
// pantalla alternativa, vuelve a escribir solo las líneas que cambian: las filas
// en blanco de la ranura no cambian entre un tic y el siguiente, así que la
// imagen sobrevive entre tics y reescribirla cada segundo sería trabajo inútil y
// un parpadeo. La emisión exige además que la vista ya haya devuelto esas filas
// en blanco —lo dice coverSixelPaint—: escribir la imagen y después sustituir su
// región por otro contenido la borraría, y por eso la escritura viaja en el tic
// de coverSixelSoon y no en el mensaje que dejó la ranura vacía.
func (m *playerModel) coverSixelEmit() tea.Cmd {
	if !m.sixel.active() || m.quit || m.ctx.Err() != nil {
		return nil
	}
	plan := m.sixel.pending
	if plan == nil || m.sixel.painted != plan.key || m.sixel.emitted == plan.key || m.sixel.failed == plan.key {
		return nil
	}
	out, key, row, column, img := m.sixel.output(), plan.key, plan.row, plan.column, plan.image
	return func() tea.Msg {
		// Se codifica entero en memoria antes de tocar el terminal: una
		// codificación a medias dejaría media imagen escrita.
		data, err := coverSixelEncode(img)
		if err != nil {
			return coverSixelMsg{key: key, err: err}
		}
		// Antes de escribir: si la pantalla cambió mientras se codificaba, no se escribe.
		if m.coverSixelWanted() != key || m.ctx.Err() != nil {
			return coverSixelMsg{key: key, cancelled: true}
		}
		_, err = out.Write(coverSixelPlace(row, column, data))
		// Después de escribir: si la clave cambió, la imagen es vieja y la ranura se repinta.
		if m.coverSixelWanted() != key {
			return coverSixelMsg{key: key, stale: true}
		}
		return coverSixelMsg{key: key, err: err}
	}
}

// coverSixelSoon pide el tic que escribe la imagen cuando hay una lista y todavía
// no está en el terminal. Es lo que hace que la carátula no dependa de que llegue
// otro mensaje: en pausa no hay tics de reproducción y la imagen llegaría tarde.
func (m *playerModel) coverSixelSoon() tea.Cmd {
	if !m.sixel.active() || m.quit || m.ctx.Err() != nil {
		return nil
	}
	art, ok := m.coverSixelArt()
	m.coverMu.Lock()
	m.sixel.wanted = art.key
	m.coverMu.Unlock()
	if !ok || art.key == m.sixel.emitted {
		return nil
	}
	return tea.Tick(coverSixelArm, func(time.Time) tea.Msg { return coverSixelReadyMsg{} })
}

// coverCommands junta lo que la carátula necesita: pedir la imagen que falta y
// programar la escritura de la que ya está lista.
func (m *playerModel) coverCommands() tea.Cmd {
	return playerCommands(m.coverRequest(), m.coverSixelSoon())
}

// coverSixelEncode codifica la imagen ya escalada al tamaño de la ranura. El
// codificador cuantiza a su paleta y no cambia el tamaño, así que los píxeles que
// llegan son los que se ven.
func coverSixelEncode(img image.Image) ([]byte, error) {
	var buffer bytes.Buffer
	if err := sixel.NewEncoder(&buffer).Encode(img); err != nil {
		return nil, err
	}
	return buffer.Bytes(), nil
}

// coverSixelPlace lleva el cursor a la esquina de la ranura y escribe la imagen
// ahí, en una sola llamada: el repintado de Bubble Tea escribe en el mismo
// terminal desde otra goroutine y no debe meterse entre la posición y la imagen.
// La pantalla alternativa empieza cada repintado en la esquina superior
// izquierda, así que la posición del cursor tras la imagen no descoloca al resto
// de la vista.
func coverSixelPlace(row, column int, data []byte) []byte {
	place := fmt.Sprintf("\x1b[%d;%dH", row, column)
	out := make([]byte, 0, len(place)+len(data))
	out = append(out, place...)
	return append(out, data...)
}
