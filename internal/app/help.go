package app

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// helpEntry is one keyboard binding on the help screen: the key as it is
// pressed and the localized action it performs.
type helpEntry struct {
	key   string
	label string
}

// helpSection groups the bindings by what they act on.
type helpSection struct {
	title   string
	entries []helpEntry
}

// helpSections lists every control once, grouped as Navigation, Playback,
// Library, Search, Appearance and Exit. The footer draws from the same
// bindings, so a key cannot be advertised there and be missing here.
func (m *playerModel) helpSections() []helpSection {
	return []helpSection{
		{m.text("Navigation", "Navegación"), []helpEntry{
			{"Tab", m.text("next panel", "siguiente panel")},
			{"Shift+Tab", m.text("previous panel", "panel anterior")},
			{"↑↓/jk", m.text("move the cursor", "mover el cursor")},
			{"Enter", m.text("open or play", "abrir o reproducir")},
		}},
		{m.text("Playback", "Reproducción"), []helpEntry{
			{"Space", m.text("play or pause", "reproducir o pausar")},
			{"←→", m.text("seek 10 s", "salto de 10 s")},
			{"n/p", m.text("next or previous track", "pista siguiente o anterior")},
			{"s", m.text("shuffle on or off", "aleatorio activado o apagado")},
			{"r", m.text("repeat off, all, one", "repetir apagado, todo, una")},
			{"+/-", m.text("volume", "volumen")},
		}},
		{m.text("Library", "Biblioteca"), []helpEntry{
			{"l", m.text("focus the library", "enfocar la biblioteca")},
			{"Enter", m.text("open the playlist", "abrir la lista")},
			{"p", m.text("play the playlist", "reproducir la lista")},
		}},
		{m.text("Search", "Búsqueda"), []helpEntry{
			{"/", m.text("search", "buscar")},
			{"Enter", m.text("search", "buscar")},
			{"Backspace", m.text("delete", "borrar")},
			{"Esc", m.text("leave the field", "salir del campo")},
		}},
		{m.text("Appearance", "Apariencia"), []helpEntry{
			{"F1", m.text("help", "ayuda")},
			{"F2", m.text("language", "idioma")},
			{"F3", m.text("theme", "tema")},
		}},
		{m.text("Exit", "Salir"), []helpEntry{
			{"Esc", m.text("back, then quit", "volver, luego salir")},
			{"q", m.text("quit", "salir")},
			{"Ctrl+C", m.text("quit", "salir")},
		}},
	}
}

// handleHelpKey closes the help view on its own keys. Every other key is
// swallowed, so the panel underneath never acts by accident.
func (m *playerModel) handleHelpKey(key tea.KeyMsg) {
	if key.Paste || key.Alt {
		return
	}
	switch {
	case key.Type == tea.KeyF1, key.Type == tea.KeyEsc, key.String() == "?", key.String() == "q":
		m.help = false
	}
}

// helpView is the full-screen help screen. It replaces the workspace, keeps the
// same single-width glyph discipline as the rest of the chrome, and never
// renders past the window.
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
				m.text("F1/?/Esc/q: close", "F1/?/Esc/q: cerrar"), m.width),
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
