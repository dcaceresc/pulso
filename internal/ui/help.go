package ui

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// helpEntry es un enlace de teclado de la ayuda: la tecla tal como se pulsa y la
// acción localizada que ejecuta.
type helpEntry struct {
	key   string
	label string
}

// helpSection agrupa los enlaces por aquello sobre lo que actúan.
type helpSection struct {
	title   string
	entries []helpEntry
}

// helpSections enumera cada control una vez, zona por zona: el transporte y la
// salida globales, las tres letras de zona y las teclas de cada zona. `?` es la
// única referencia de teclas: el pie ya no anuncia ninguna.
func (m *playerModel) helpSections() []helpSection {
	return []helpSection{
		{m.text("Global", "Global"), []helpEntry{
			{"q / Ctrl+C", m.text("quit", "salir")},
			{"Space", m.text("play or pause", "reproducir o pausar")},
			{"←→", m.text("seek 10 s", "salto de 10 s")},
			{"n / b", m.text("next or previous track", "pista siguiente o anterior")},
			{"+/-", m.text("volume", "volumen")},
			{"s", m.text("shuffle playback", "aleatorio de la reproducción")},
			{"r", m.text("repeat off, all, one", "repetir apagado, todo, una")},
			{"F2 / F3", m.text("language or theme", "idioma o tema")},
			{"?", m.text("help", "ayuda")},
			{"Esc", m.text("back to the table or cancel", "volver a la tabla o cancelar")},
		}},
		{m.text("Zones", "Zonas"), []helpEntry{
			{"l", m.text("library", "biblioteca")},
			{"/", m.text("search: q s r + - are text", "búsqueda: q s r + - son texto")},
			{"c", m.text("queue", "cola")},
		}},
		{m.text("Library", "Biblioteca"), []helpEntry{
			{"↑↓/jk", m.text("move the cursor", "mover el cursor")},
			{"Enter", m.text("open the playlist or play it", "abrir la lista o reproducirla")},
			{"s", m.text("shuffle the selected playlist", "reproducir la lista al azar")},
		}},
		{m.text("Table", "Tabla"), []helpEntry{
			{"↑↓/jk", m.text("move the cursor", "mover el cursor")},
			{"Enter", m.text("play the row", "reproducir la fila")},
			{"a", m.text("save the playing track to a playlist", "guardar la pista en una lista")},
			{"x", m.text("remove the highlighted track from the open playlist", "quitar la pista marcada de la lista abierta")},
		}},
		{m.text("Search", "Búsqueda"), []helpEntry{
			{"Enter", m.text("search", "buscar")},
			{"Backspace", m.text("delete", "borrar")},
			{"Esc", m.text("leave the field", "salir del campo")},
		}},
	}
}

// handleHelpKey cierra la ayuda con sus propias teclas y sale con las globales.
// Cualquier otra tecla se descarta, así el panel de debajo nunca actúa por
// accidente.
func (m *playerModel) handleHelpKey(key tea.KeyMsg) tea.Cmd {
	if key.Paste || key.Alt {
		return nil
	}
	switch {
	case key.Type == tea.KeyCtrlC, key.String() == "q":
		return m.quitApp()
	case key.Type == tea.KeyEsc, key.String() == "?":
		m.help = false
	}
	return nil
}

// helpView es la ayuda a pantalla completa. Reemplaza el espacio de trabajo,
// mantiene la disciplina de glifos de un solo ancho del resto del cromo y nunca
// dibuja más allá de la ventana.
func (m *playerModel) helpView() string {
	styles := m.styles()
	sections := m.helpSections()
	keyWidth := 0
	for _, section := range sections {
		for _, entry := range section.entries {
			keyWidth = max(keyWidth, lipgloss.Width(entry.key))
		}
	}
	rows := []string{
		playerRenderRow(playerRow{
			text: playerSpread(m.text("Pulso · Help", "Pulso · Ayuda"),
				m.text("Esc/? close", "Esc/?: cerrar"), m.width),
			style: styles.banner,
		}, m.width),
	}
	for _, section := range sections {
		rows = append(rows, playerRenderRow(playerRow{text: section.title, style: styles.label}, m.width))
		for _, entry := range section.entries {
			rows = append(rows, playerRenderRow(playerRow{
				text:  "  " + playerPad(entry.key, keyWidth) + "  " + entry.label,
				style: styles.muted,
			}, m.width))
		}
	}
	if len(rows) > m.height {
		rows = rows[:m.height]
	}
	return strings.Join(rows, "\n")
}
