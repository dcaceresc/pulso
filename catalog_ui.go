package main

import (
	"context"
	"errors"
	"net/url"
	"strings"
	"sync"
	"unicode"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

type catalogAPI interface {
	Search(context.Context, string, int) ([]catalogItem, bool, error)
	Playlists(context.Context, int) ([]catalogItem, bool, error)
	PlaylistTracks(context.Context, string, int) ([]catalogItem, bool, error)
}

// The Web API requests ten slots even when it filters unusable entries.
const catalogViewPageSize = 10

type catalogPage struct {
	items            []catalogItem
	offset, selected int
	more             bool
}

type catalogNotice uint8

const (
	catalogNoNotice catalogNotice = iota
	catalogAuthFailed
	catalogLoadFailed
	catalogUnavailable
)

type catalogAuthMsg struct {
	generation uint64
	url        string
	api        catalogAPI
	done       bool
}

type catalogPageMsg struct {
	generation uint64
	page       catalogPage
	err        error
}

type catalogModel struct {
	ctx           context.Context
	preferences   preferences
	width, height int
	authorize     func(context.Context, string, func(string)) (catalogAPI, error)
	play          func(string)
	clientID      string
	saveClientID  func(string)
	api           catalogAPI

	view               string
	query, draft       string
	editing            bool
	playlist           catalogItem
	playlistPage, page catalogPage
	loaded             bool
	notice             catalogNotice
	authURL            string
	authEvents         <-chan catalogAuthMsg
	work               context.Context
	cancel             context.CancelFunc
	generation         uint64
}

var _ tea.Model = (*catalogModel)(nil)

func newCatalogModel(ctx context.Context, p preferences, width, height int, clientID string, authorize func(context.Context, string, func(string)) (catalogAPI, error), play func(string)) *catalogModel {
	return &catalogModel{ctx: ctx, preferences: p, width: width, height: height, clientID: strings.TrimSpace(clientID), authorize: authorize, play: play}
}

func (m *catalogModel) Init() tea.Cmd { return nil }

func (m *catalogModel) Closed() bool { return m.view == "" || m.ctx.Err() != nil }

func (m *catalogModel) Open(view string) tea.Cmd {
	if m.ctx.Err() != nil || (view != "search" && view != "playlists") {
		return nil
	}
	m.close()
	m.view = view
	if m.api != nil {
		return m.openAuthorized()
	}
	if m.clientID == "" {
		return nil
	}
	return m.startAuthorization()
}

func (m *catalogModel) close() {
	m.stopWork()
	m.view, m.query, m.draft = "", "", ""
	m.page, m.playlistPage, m.playlist = catalogPage{}, catalogPage{}, catalogItem{}
	m.loaded, m.editing, m.notice = false, false, catalogNoNotice
}

func (m *catalogModel) stopWork() {
	if m.cancel != nil {
		m.cancel()
	}
	m.work, m.cancel, m.authEvents, m.authURL = nil, nil, nil, ""
	m.generation++
}

func (m *catalogModel) startWork() (context.Context, uint64) {
	m.stopWork()
	m.notice = catalogNoNotice
	m.work, m.cancel = context.WithCancel(m.ctx)
	return m.work, m.generation
}

func (m *catalogModel) startAuthorization() tea.Cmd {
	if m.authorize == nil {
		m.notice = catalogAuthFailed
		return nil
	}
	ctx, generation := m.startWork()
	authorize, clientID := m.authorize, m.clientID
	// One URL and one completion fit without blocking the OAuth callback or
	// tying it to a Bubble Tea program. Commands never mutate model fields.
	events := make(chan catalogAuthMsg, 2)
	m.authEvents = events
	return func() tea.Msg {
		if ctx.Err() != nil {
			return nil
		}
		go func() {
			var published sync.Once
			api, err := authorize(ctx, clientID, func(raw string) {
				if link := catalogBrowserURL(raw); link != "" && ctx.Err() == nil {
					published.Do(func() { events <- catalogAuthMsg{generation: generation, url: link} })
				}
			})
			if err != nil {
				api = nil
			}
			events <- catalogAuthMsg{generation: generation, api: api, done: true}
		}()
		return catalogAwaitAuth(ctx, events)()
	}
}

func catalogAwaitAuth(ctx context.Context, events <-chan catalogAuthMsg) tea.Cmd {
	return func() tea.Msg {
		select {
		case <-ctx.Done():
			return nil
		case msg := <-events:
			return msg
		}
	}
}

func catalogBrowserURL(raw string) string {
	if len(raw) > 4096 || playerText(raw) != raw {
		return ""
	}
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || u.Fragment != "" {
		return ""
	}
	query, err := url.ParseQuery(u.RawQuery)
	if err != nil {
		return ""
	}
	for key := range query {
		switch key {
		case "client_id", "response_type", "redirect_uri", "scope", "state", "code_challenge", "code_challenge_method", "show_dialog":
		default:
			return ""
		}
	}
	return raw
}

func (m *catalogModel) openAuthorized() tea.Cmd {
	if m.view == "search" {
		m.editing = true
		return nil
	}
	return m.fetch(0)
}

func (m *catalogModel) fetch(offset int) tea.Cmd {
	ctx, generation := m.startWork()
	api, view, query, playlistURI := m.api, m.view, m.query, m.playlist.URI
	return func() tea.Msg {
		if ctx.Err() != nil {
			return nil
		}
		page := catalogPage{offset: offset}
		var err error
		switch {
		case view == "search":
			page.items, page.more, err = api.Search(ctx, query, offset)
		case playlistURI != "":
			page.items, page.more, err = api.PlaylistTracks(ctx, playlistURI, offset)
		default:
			page.items, page.more, err = api.Playlists(ctx, offset)
		}
		return catalogPageMsg{generation: generation, page: page, err: err}
	}
}

func (m *catalogModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
	case tea.KeyMsg:
		if !m.Closed() {
			return m, m.handleKey(msg)
		}
	case catalogAuthMsg:
		if !m.current(msg.generation) {
			break
		}
		if !msg.done {
			m.authURL = msg.url
			return m, catalogAwaitAuth(m.work, m.authEvents)
		}
		m.stopWork()
		if msg.api == nil {
			m.notice = catalogAuthFailed
			break
		}
		m.api = msg.api
		return m, m.openAuthorized()
	case catalogPageMsg:
		if !m.current(msg.generation) {
			break
		}
		m.stopWork()
		switch {
		case msg.err == nil:
			m.page, m.loaded = msg.page, true
		case m.playlist.URI != "" && errors.Is(msg.err, errPlaylistUnavailable):
			m.page, m.notice = catalogPage{}, catalogUnavailable
		default:
			m.notice = catalogLoadFailed
		}
	}
	return m, nil
}

func (m *catalogModel) current(generation uint64) bool {
	return !m.Closed() && m.work != nil && m.generation == generation
}

func (m *catalogModel) handleKey(key tea.KeyMsg) tea.Cmd {
	if key.Alt {
		return nil
	}
	if (key.Type == tea.KeyEsc || key.Type == tea.KeyCtrlC) && !key.Paste {
		m.close()
		return nil
	}
	if m.api == nil {
		if m.work != nil {
			return nil
		}
		if m.clientID == "" {
			if key.Type != tea.KeyEnter || key.Paste {
				m.draft = catalogEdit(m.draft, key)
				return nil
			}
			m.clientID = strings.TrimSpace(m.draft)
			if m.clientID == "" {
				return nil
			}
			if m.saveClientID != nil {
				m.saveClientID(m.clientID)
			}
			m.draft = ""
			return m.startAuthorization()
		}
		if !key.Paste {
			switch key.String() {
			case "enter":
				return m.startAuthorization()
			case "e":
				m.draft, m.clientID, m.notice = m.clientID, "", catalogNoNotice
			}
		}
		return nil
	}
	if m.view == "search" && m.editing {
		if key.Type != tea.KeyEnter || key.Paste {
			m.draft = catalogEdit(m.draft, key)
			return nil
		}
		if query := strings.TrimSpace(m.draft); query != "" {
			m.query, m.editing, m.page, m.loaded = query, false, catalogPage{}, false
			return m.fetch(0)
		}
		return nil
	}
	if key.Paste {
		return nil
	}
	// These keys can abandon the current fetch rather than waiting for it.
	if m.view == "search" && key.String() == "/" {
		m.stopWork()
		m.draft, m.editing, m.notice = m.query, true, catalogNoNotice
		return nil
	}
	if m.playlist.URI != "" && key.Type == tea.KeyLeft {
		m.stopWork()
		m.page, m.playlist, m.playlistPage = m.playlistPage, catalogItem{}, catalogPage{}
		m.loaded, m.notice = true, catalogNoNotice
		return nil
	}
	if m.work != nil {
		return nil
	}
	var item catalogItem
	if len(m.page.items) != 0 {
		item = m.page.items[m.page.selected]
	}
	switch key.String() {
	case "up":
		m.page.selected = max(0, m.page.selected-1)
	case "down":
		m.page.selected = max(0, min(len(m.page.items)-1, m.page.selected+1))
	case "pgdown":
		if m.page.more {
			return m.fetch(m.page.offset + catalogViewPageSize)
		}
	case "pgup":
		if m.page.offset > 0 {
			return m.fetch(max(0, m.page.offset-catalogViewPageSize))
		}
	case "enter":
		if m.notice == catalogLoadFailed && !m.loaded {
			return m.fetch(0)
		}
		if m.view == "playlists" && m.playlist.URI == "" && item.URI != "" {
			m.playlist, m.playlistPage = item, m.page
			m.page, m.loaded = catalogPage{}, false
			return m.fetch(0)
		}
		if m.view == "search" {
			m.playItem(item)
		}
	case "p":
		if m.notice == catalogUnavailable {
			item = m.playlist
		}
		m.playItem(item)
	}
	return nil
}

func (m *catalogModel) playItem(item catalogItem) {
	if m.play != nil && item.URI != "" && !m.Closed() {
		m.play(item.URI)
	}
}

func catalogEdit(input string, key tea.KeyMsg) string {
	runes := []rune(input)
	switch key.Type {
	case tea.KeyBackspace:
		if !key.Paste && len(runes) > 0 {
			return string(runes[:len(runes)-1])
		}
	case tea.KeyCtrlU:
		if !key.Paste {
			return ""
		}
	case tea.KeySpace:
		if len(runes) < 512 {
			return input + " "
		}
	case tea.KeyRunes:
		for _, r := range key.Runes {
			if len(runes) >= 512 {
				break
			}
			if unicode.IsSpace(r) {
				r = ' '
			}
			if !unicode.IsControl(r) && (!unicode.Is(unicode.Cf, r) || r == '\u200d') {
				runes = append(runes, r)
			}
		}
		return string(runes)
	}
	return input
}

func (m *catalogModel) View() string {
	if m.Closed() || m.width < 1 || m.height < 1 {
		return ""
	}
	text, palette := m.preferences.text, m.preferences.palette()
	title := text("Search tracks", "Buscar canciones")
	if m.view == "playlists" {
		title = text("Playlists", "Listas de reproducción")
	}
	if m.playlist.URI != "" {
		title = text("Playlist tracks", "Canciones de la lista") + " / " + playerText(m.playlist.Name)
	}
	accent := lipgloss.NewStyle().Foreground(palette.AccentText).Background(palette.Accent).Bold(true)
	muted := lipgloss.NewStyle().Foreground(palette.Muted)
	failure := lipgloss.NewStyle().Foreground(palette.Error)
	rows := []string{accent.Render(playerFit("Pulso / "+title, m.width))}
	body := func(style lipgloss.Style, value string) {
		if len(rows) < m.height {
			rows = append(rows, style.Render(playerFit(value, m.width)))
		}
	}
	plain := lipgloss.NewStyle()
	hint := text("Esc: close", "Esc: cerrar")
	if m.api == nil {
		switch {
		case m.authURL != "":
			body(plain, text("Open this URL manually in your browser:", "Abre esta URL manualmente en tu navegador:"))
			for _, line := range strings.Split(ansi.Hardwrap(m.authURL, m.width, false), "\n") {
				body(plain, line)
			}
		case m.work != nil:
			body(plain, text("Waiting for Spotify authorization…", "Esperando autorización de Spotify…"))
		case m.clientID == "":
			body(plain, text("Spotify Client ID: ", "ID de cliente de Spotify: ")+m.draft+"_")
			hint += text(" | Enter: authorize", " | Enter: autorizar")
		case m.notice == catalogAuthFailed:
			body(failure, text("Authorization failed.", "No se pudo autorizar."))
			hint += text(" | Enter: retry | e: edit Client ID", " | Enter: reintentar | e: cambiar ID de cliente")
		}
	} else {
		switch {
		case m.editing:
			body(plain, text("Query: ", "Consulta: ")+m.draft+"_")
			hint += text(" | Enter: search", " | Enter: buscar")
		case m.notice == catalogUnavailable:
			body(failure, text("Playlist contents unavailable. You can still play the playlist.", "Contenido de la lista no disponible. Aún puedes reproducirla."))
			hint += text(" | p: play playlist | ←: back", " | p: reproducir lista | ←: volver")
		default:
			hint += m.listHint()
			if m.view == "search" && m.query != "" {
				body(muted, text("Query: ", "Consulta: ")+m.query)
			}
			switch {
			case m.work != nil:
				body(muted, text("Loading…", "Cargando…"))
			case m.notice == catalogLoadFailed:
				body(failure, text("Could not load Spotify results.", "No se pudieron cargar los resultados de Spotify."))
				if !m.loaded {
					hint = text("Esc: close | Enter: retry", "Esc: cerrar | Enter: reintentar")
					if m.playlist.URI != "" {
						hint += text(" | ←: back", " | ←: volver")
					}
				}
			case m.loaded && len(m.page.items) == 0:
				if m.view == "playlists" && m.playlist.URI == "" {
					body(plain, text("No playlists on this page.", "No hay listas en esta página."))
				} else {
					body(plain, text("No tracks on this page.", "No hay canciones en esta página."))
				}
			}
			room := max(0, m.height-len(rows)-1)
			if room == 0 && len(rows) < m.height {
				room = 1
			}
			start := max(0, m.page.selected-room+1)
			for i := start; i < min(len(m.page.items), start+room); i++ {
				item := m.page.items[i]
				name := playerText(item.Name)
				if name == "" {
					name = text("Unknown name", "Nombre desconocido")
				}
				if detail := playerText(item.Detail); detail != "" {
					name += " — " + detail
				}
				if i == m.page.selected {
					body(accent, "> "+name)
				} else {
					body(plain, "  "+name)
				}
			}
		}
	}
	body(muted, hint)
	return strings.Join(rows, "\n")
}

func (m *catalogModel) listHint() string {
	text := m.preferences.text
	hint := text(" | ↑↓: select", " | ↑↓: elegir")
	switch {
	case m.view == "search":
		hint += text(" | Enter: play | /: edit query", " | Enter: reproducir | /: editar consulta")
	case m.playlist.URI != "":
		hint += text(" | p: play track | ←: back", " | p: reproducir canción | ←: volver")
	default:
		hint += text(" | Enter: tracks | p: play playlist", " | Enter: canciones | p: reproducir lista")
	}
	if m.page.more || m.page.offset > 0 {
		hint += text(" | PgUp/PgDn: page", " | RePág/AvPág: página")
	}
	return hint
}
