package ui

import (
	"bytes"
	"fmt"
	"image"
	"os"
	"strings"

	"github.com/mattn/go-sixel"
)

// La carátula gráfica: cuando el terminal habla el protocolo Sixel, la caja deja
// su ranura en blanco y la imagen se escribe encima, una sola vez por cambio, en
// lugar de reducirla a medios bloques. El medio bloque sigue siendo el respaldo
// de todo lo que no sea Sixel y de cualquier emisión que falle.
//
// La imagen viaja dentro del cuadro: la secuencia se antepone a la última línea de
// la vista, así el renderizador la escribe después de todas las filas que cubre y
// ninguna de ellas la pinta encima. Es de ancho cero, así que no cambia el tamaño
// de la vista.
//
// La detección es una heurística y el usuario puede imponer el modo con
// PULSO_COVER: un terminal que no conteste a la consulta no pierde la carátula,
// solo la reduce a bloques.

const (
	// coverOverrideEnv fuerza el modo de la carátula: `sixel` o `blocks`.
	coverOverrideEnv = "PULSO_COVER"
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

// coverSixelPlan es la imagen que este cuadro lleva y el lugar exacto del
// terminal donde hay que dibujarla.
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
	// emitted es la clave de la imagen que ya viaja en el cuadro.
	emitted string
	// failed es la clave cuya codificación falló: esa imagen, y solo esa, no se
	// vuelve a intentar.
	failed string
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

// forget olvida la imagen que viajaba en el cuadro. Una pantalla que la tapa —la
// ayuda, el mensaje de tamaño mínimo— o una ranura que deja de dibujarse la habrá
// borrado, así que la misma pista y el mismo tamaño que vuelvan la escriben otra vez.
func (s *coverGraphics) forget() {
	s.emitted = ""
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

// coverSixelPlan es la imagen que este cuadro debe llevar y dónde: la ranura que la
// banda resuelta pide, contada desde el borde superior de la caja.
func (m *playerModel) coverSixelPlan(boxTop int) (coverSixelPlan, bool) {
	art, ok := m.coverSixelArt()
	if !ok || boxTop < 0 {
		return coverSixelPlan{}, false
	}
	return coverSixelPlan{
		key:    art.key,
		image:  art.image,
		row:    boxTop + coverSixelRowOffset,
		column: coverSixelColumn,
	}, true
}

// coverSixelFrame mete la imagen en el cuadro: la secuencia se antepone a la última
// línea, que se escribe después de todas las filas que la imagen cubre, y vuelve el
// cursor a esa línea para que su texto no caiga sobre la ranura. Sin ranura, la
// imagen escrita se olvida; con la misma clave, el cuadro no la repite.
func (m *playerModel) coverSixelFrame(last string, lastRow, boxTop int) string {
	plan, ok := m.coverSixelPlan(boxTop)
	if !ok {
		m.sixel.forget()
		return last
	}
	if m.sixel.emitted == plan.key || m.sixel.failed == plan.key {
		return last
	}
	data, err := coverSixelEncode(plan.image)
	if err != nil {
		m.sixel.failed = plan.key
		return last
	}
	m.sixel.emitted = plan.key
	return coverSixelSequence(plan.row, plan.column, data, lastRow) + last
}

// coverSixelSequence es la imagen precedida de su esquina y seguida de la vuelta a
// la última fila, donde va el texto de la línea que la transporta.
func coverSixelSequence(row, column int, data []byte, lastRow int) string {
	return fmt.Sprintf("\x1b[%d;%dH%s\x1b[%d;1H", row, column, data, lastRow)
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
