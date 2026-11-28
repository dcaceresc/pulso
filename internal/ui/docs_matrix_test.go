package ui

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// Los tres documentos de usuario describen la misma interfaz que el código. Las
// pruebas de este archivo fallan en cuanto uno de ellos se separa de los otros
// dos, del mapa de bandas o del modelo de teclas, que es como se colaron las
// frases que prometían más de lo que la aplicación hace.

const (
	docsSpanish = "INSTRUCTIONS.md"
	docsEnglish = "INSTRUCTIONS.en.md"
	docsPlan    = "docs/UI-PLAN.md"
)

// docsBandProbe nombra cada banda de la matriz con un tamaño que la dibuja y el
// nombre con que la llaman los documentos de cada lengua.
type docsBandProbe struct {
	band           int
	width, height  int
	nameES, nameEN string
}

var docsBands = []docsBandProbe{
	{playerBandWide, 140, 40, "Amplio", "Wide"},
	{playerBandMedium, 100, 30, "Medio", "Medium"},
	{playerBandCompact, 80, 24, "Compacto", "Compact"},
	{playerBandMinimum, 40, 12, "Mínimo", "Minimum"},
	{playerBandInsufficient, 39, 11, "Insuficiente", "Insufficient"},
}

func docsText(t *testing.T, name string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "..", name))
	if err != nil {
		t.Fatalf("reading %s: %v", name, err)
	}
	return string(data)
}

// docsTableRows devuelve las filas de la primera tabla Markdown que empieza por
// la cabecera que contiene marker, sin la cabecera ni la regla de separación.
func docsTableRows(t *testing.T, name, marker string) [][]string {
	t.Helper()
	lines := strings.Split(docsText(t, name), "\n")
	for i, line := range lines {
		if !strings.HasPrefix(line, "|") || !strings.Contains(line, marker) {
			continue
		}
		var rows [][]string
		for j := i + 2; j < len(lines) && strings.HasPrefix(lines[j], "|"); j++ {
			rows = append(rows, docsCells(lines[j]))
		}
		return rows
	}
	t.Fatalf("%s: no table starts with the header %q", name, marker)
	return nil
}

func docsCells(line string) []string {
	line = strings.TrimSuffix(strings.TrimPrefix(strings.TrimSpace(line), "|"), "|")
	cells := strings.Split(line, "|")
	for i := range cells {
		cells[i] = strings.TrimSpace(cells[i])
	}
	return cells
}

// docsNumbers devuelve los enteros de una celda en su orden, que es la
// firma numérica con la que se comparan las matrices entre lenguas.
func docsNumbers(text string) []int {
	numbers := []int{}
	for _, match := range regexp.MustCompile(`[0-9]+`).FindAllString(text, -1) {
		if value, err := strconv.Atoi(match); err == nil {
			numbers = append(numbers, value)
		}
	}
	return numbers
}

// docsCover reduce la celda de la carátula a sí, no o ninguno, para que las dos
// lenguas se comparen sin traducir la tabla.
func docsCover(cell string) string {
	switch cell {
	case "sí", "yes":
		return "yes"
	case "no":
		return "no"
	case "—":
		return "none"
	}
	return cell
}

// docsCondition construye la condición que la matriz debe declarar para la
// banda index, con los umbrales del código y la lengua del documento.
func docsCondition(lang string, index int) string {
	width, height := "ancho", "alto"
	and, or := "y", "o"
	if lang == "en" {
		width, height = "width", "height"
		and, or = "and", "or"
	}
	switch index {
	case 0:
		return fmt.Sprintf("%s ≥ %d %s %s ≥ %d", width, playerFullWidth, and, height, playerFullHeight)
	case 1:
		// El ancho es un piso: la banda media también recoge las ventanas anchas
		// cuyo alto no llega a la banda amplia, y la matriz lo dice.
		medium := fmt.Sprintf("%s ≥ %d %s %s ≥ %d", width, playerMediumWidth, and, height, playerMediumHeight)
		if lang == "es" {
			return medium + fmt.Sprintf(", también con más de %d columnas si el alto no llega a la banda amplia", playerFullWidth)
		}
		return medium + fmt.Sprintf(", also above %d columns when the height misses the wide band", playerFullWidth)
	case 2:
		return fmt.Sprintf("%s ≥ %d %s %s ≥ %d", width, playerCompactWidth, and, height, playerCompactHeight)
	case 3:
		return fmt.Sprintf("%s ≥ %d %s %s ≥ %d", width, playerMinWidth, and, height, playerMinHeight)
	}
	return fmt.Sprintf("%s < %d %s %s < %d", width, playerMinWidth, or, height, playerMinHeight)
}

func docsCoverWord(lang string, slot bool) string {
	if lang == "es" {
		if slot {
			return "sí"
		}
		return "no"
	}
	if slot {
		return "yes"
	}
	return "no"
}

// docsMatrix declara cómo se lee la matriz de cada documento: la cabecera que la
// identifica, la lengua con la que la escribe y las columnas de condición,
// reproductor, carátula y nombre de banda (-1 cuando no la lleva).
type docsMatrix struct {
	name, lang    string
	header        string
	condition     int
	player, cover int
	band          int
}

var docsMatrices = []docsMatrix{
	{docsSpanish, "es", "| Tamaño |", 0, 2, 3, -1},
	{docsEnglish, "en", "| Size |", 0, 2, 3, -1},
	{docsPlan, "en", "| Band |", 1, 3, 4, 0},
}

// TestDocsDeclareTheSameMatrixAndControls fija que los tres documentos declaren
// las mismas cinco bandas y la misma tabla de dieciséis controles: la matriz de
// tamaños se compara por su firma numérica y la de controles por sus teclas, así
// que una fila añadida, quitada o reordenada en uno solo de ellos falla.
func TestDocsDeclareTheSameMatrixAndControls(t *testing.T) {
	type docsRow struct {
		condition, player []int
		cover             string
	}
	var reference []docsRow
	for _, doc := range docsMatrices {
		rows := docsTableRows(t, doc.name, doc.header)
		if len(rows) != len(docsBands) {
			t.Fatalf("%s: %d matrix rows, want %d", doc.name, len(rows), len(docsBands))
		}
		var got []docsRow
		for i, row := range rows {
			if len(row) <= doc.cover {
				t.Fatalf("%s: matrix row %d has %d cells, want at least %d", doc.name, i+1, len(row), doc.cover+1)
			}
			got = append(got, docsRow{
				condition: docsNumbers(row[doc.condition]),
				player:    docsNumbers(row[doc.player]),
				cover:     docsCover(row[doc.cover]),
			})
		}
		if reference == nil {
			reference = got
			continue
		}
		if !reflect.DeepEqual(reference, got) {
			t.Errorf("%s declares a different size matrix:\n got %+v\nwant %+v", doc.name, got, reference)
		}
	}

	controls := []struct{ name, header string }{
		{docsSpanish, "| Ámbito |"},
		{docsEnglish, "| Scope |"},
		{docsPlan, "| Scope |"},
	}
	var keys []string
	for _, doc := range controls {
		rows := docsTableRows(t, doc.name, doc.header)
		if len(rows) != 16 {
			t.Fatalf("%s: %d control rows, want 16", doc.name, len(rows))
		}
		got := make([]string, len(rows))
		for i, row := range rows {
			if len(row) < 2 {
				t.Fatalf("%s: control row %d has %d cells, want at least 2", doc.name, i+1, len(row))
			}
			got[i] = row[1]
		}
		if keys == nil {
			keys = got
			continue
		}
		if !reflect.DeepEqual(keys, got) {
			t.Errorf("%s declares different controls:\n got %v\nwant %v", doc.name, got, keys)
		}
	}
}

// TestDocsMatrixMatchesTheLayout fija que cada fila de la matriz sea la que
// playerBand, playerBoxBand y playerArtSlot resuelven, y que la escalera de
// bandas baje a la forma siguiente cuando el alto no alcanza.
func TestDocsMatrixMatchesTheLayout(t *testing.T) {
	for _, doc := range docsMatrices {
		rows := docsTableRows(t, doc.name, doc.header)
		if len(rows) != len(docsBands) {
			t.Fatalf("%s: %d matrix rows, want %d", doc.name, len(rows), len(docsBands))
		}
		for i, probe := range docsBands {
			row := rows[i]
			if got := playerBand(probe.width, probe.height); got != probe.band {
				t.Fatalf("%s: row %d describes a band that %dx%d does not resolve to (got %d)",
					doc.name, i+1, probe.width, probe.height, got)
			}
			if doc.band >= 0 {
				want := probe.nameEN
				if doc.lang == "es" {
					want = probe.nameES
				}
				if row[doc.band] != want {
					t.Errorf("%s: row %d names the band %q, want %q", doc.name, i+1, row[doc.band], want)
				}
			}
			if want := docsCondition(doc.lang, i); row[doc.condition] != want {
				t.Errorf("%s: row %d declares %q, want %q", doc.name, i+1, row[doc.condition], want)
			}
			if probe.band == playerBandInsufficient {
				if row[doc.player] != "—" || row[doc.cover] != "—" {
					t.Errorf("%s: row %d promises a player box or a cover the size message does not have", doc.name, i+1)
				}
				continue
			}
			if got := docsNumbers(row[doc.player]); len(got) == 0 || got[0] != playerBoxBand(probe.width) {
				t.Errorf("%s: row %d declares %q interior rows, want %d", doc.name, i+1, row[doc.player], playerBoxBand(probe.width))
			}
			slot := playerArtSlot(probe.width, playerBoxInterior(probe.width, probe.height))
			if want := docsCoverWord(doc.lang, slot); row[doc.cover] != want {
				t.Errorf("%s: row %d declares cover %q, want %q", doc.name, i+1, row[doc.cover], want)
			}
		}
	}

	// La escalera: el ancho es un piso y el alto decide, así que un alto que no
	// llega a la banda de su ancho baja a la siguiente forma más rica que cabe,
	// nunca a la más pobre. Los tres tamaños casi-límite son los que la matriz no
	// describe con una cota superior y los documentos nombran como ejemplo.
	for _, probe := range []struct {
		width, height, band int
	}{
		{120, 30, playerBandWide},
		{120, 29, playerBandMedium},
		{119, 40, playerBandMedium},
		{100, 25, playerBandCompact},
		{99, 40, playerBandCompact},
		{80, 19, playerBandMinimum},
		{79, 24, playerBandMinimum},
		{40, 12, playerBandMinimum},
		{39, 12, playerBandInsufficient},
		{40, 11, playerBandInsufficient},
	} {
		if got := playerBand(probe.width, probe.height); got != probe.band {
			t.Errorf("playerBand(%d, %d) = %d, want %d", probe.width, probe.height, got, probe.band)
		}
	}
}

// TestDocsCoverMatchesTheSlot fija que el plan y las dos INSTRUCTIONS declaren
// la ranura que el código dibuja: las veinte columnas por diez filas y la
// reducción de 20×20 píxeles, para que un número suelto no se quede atrás.
func TestDocsCoverMatchesTheSlot(t *testing.T) {
	pixels := playerArtWidth * playerArtRows * coverPixelRows
	for _, want := range []string{
		fmt.Sprintf("slot of %d columns by %d rows", playerArtWidth, playerArtRows),
		fmt.Sprintf("a %d×%d reduction", playerArtWidth, playerArtRows*coverPixelRows),
		fmt.Sprintf("%d pixels", pixels),
		// La regla de la ranura se escribe con los mismos umbrales que
		// playerArtSlot, así un cambio de código no deja el texto atrás.
		fmt.Sprintf("at least %d columns and %d interior rows", playerBoxGutter, playerArtRows+2),
	} {
		if !strings.Contains(docsText(t, docsPlan), want) {
			t.Errorf("%s does not declare %q", docsPlan, want)
		}
	}
	for _, doc := range []struct{ name, phrase string }{
		{docsSpanish, "veinte columnas por diez filas"},
		{docsEnglish, "twenty columns by ten rows"},
	} {
		if !strings.Contains(docsText(t, doc.name), doc.phrase) {
			t.Errorf("%s does not state the cover slot as %q", doc.name, doc.phrase)
		}
	}
}

// TestDocsAnnounceNoRetiredControl fija que ningún documento de usuario anuncie
// una tecla retirada, el ciclo de foco ni la línea de pistas del pie: `?` es la
// única referencia de teclas y el pie ya no existe.
func TestDocsAnnounceNoRetiredControl(t *testing.T) {
	retired := []string{"`Tab`", "`Shift+Tab`", "`F1`", "`S`", "`p`"}
	footer := []string{
		"Tab focus", "Tab: library", "Tab: biblioteca", "F1 help", "q quit",
		"Esc back/quit", "Esc volver/salir", "S shuffle-list",
		"**Pie:**", "**Footer:**", "`key action`", "`tecla acción`",
	}
	for _, name := range []string{docsSpanish, docsEnglish, docsPlan} {
		text := docsText(t, name)
		for _, gone := range append(append([]string{}, retired...), footer...) {
			if strings.Contains(text, gone) {
				t.Errorf("%s still announces %q", name, gone)
			}
		}
	}
}
