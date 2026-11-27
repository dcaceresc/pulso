package ui

import (
	"context"
	"net/url"
	"reflect"
	"unicode"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/dcaceresc/pulso/internal/spotify"
)

// CatalogAPI is the read-only Spotify catalog the setup handoff gives the
// player. It lives outside the views because the workspace reads it directly:
// playback has no modal overlay to route through.
type CatalogAPI interface {
	Search(context.Context, string, int) ([]catalogItem, bool, error)
	Playlists(context.Context, int) ([]catalogItem, bool, error)
	PlaylistTracks(context.Context, string, int) ([]catalogItem, bool, error)
}

// playlistWriterOf accepts the catalog only when it can change playlists: a
// read-only catalog, or a typed nil, hides the save action entirely.
func playlistWriterOf(api CatalogAPI) spotify.PlaylistWriter {
	if api == nil {
		return nil
	}
	writer, ok := api.(spotify.PlaylistWriter)
	if !ok {
		return nil
	}
	if value := reflect.ValueOf(writer); value.Kind() == reflect.Pointer && value.IsNil() {
		return nil
	}
	return writer
}

// catalogBrowserURL accepts only a plain https authorization URL whose query
// Pulso itself could have built, so a hostile or malformed value never reaches
// the sidebar as terminal text.
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

// catalogEdit applies one key to the bounded text buffer of a search field:
// printables and one space, Backspace, and Ctrl+U to clear. Paste never edits
// and control characters never enter the buffer.
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
