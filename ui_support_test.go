package main

import (
	"context"
	"fmt"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

// catalogViewFake is the minimal catalogAPI fixture shared by the setup
// handoff and the workspace. A nil function answers with an empty page, so a
// test wires only the endpoint it exercises.
type catalogViewFake struct {
	search func(context.Context, string, int) ([]catalogItem, bool, error)
	lists  func(context.Context, int) ([]catalogItem, bool, error)
	tracks func(context.Context, string, int) ([]catalogItem, bool, error)
}

var _ catalogAPI = catalogViewFake{}

func (f catalogViewFake) Search(ctx context.Context, query string, offset int) ([]catalogItem, bool, error) {
	if f.search == nil {
		return nil, false, nil
	}
	return f.search(ctx, query, offset)
}

func (f catalogViewFake) Playlists(ctx context.Context, offset int) ([]catalogItem, bool, error) {
	if f.lists == nil {
		return nil, false, nil
	}
	return f.lists(ctx, offset)
}

func (f catalogViewFake) PlaylistTracks(ctx context.Context, id string, offset int) ([]catalogItem, bool, error) {
	if f.tracks == nil {
		return nil, false, nil
	}
	return f.tracks(ctx, id, offset)
}

// catalogViewReply is the authorize seam that hands one catalogAPI to the
// caller without any network work.
func catalogViewReply(api catalogAPI) func(context.Context, string, func(string)) (catalogAPI, error) {
	return func(context.Context, string, func(string)) (catalogAPI, error) { return api, nil }
}

func TestCatalogBrowserURLValidation(t *testing.T) {
	const authorize = "https://accounts.spotify.com/authorize?client_id=demo&response_type=code&redirect_uri=http%3A%2F%2F127.0.0.1%3A8888%2Fcallback&scope=user-read-private&state=abc&code_challenge=xyz&code_challenge_method=S256&show_dialog=false"
	for _, tt := range []struct {
		name string
		raw  string
		want string
	}{
		{"authorization url", authorize, authorize},
		{"minimal url", "https://example.test/authorize?client_id=demo", "https://example.test/authorize?client_id=demo"},
		{"http is rejected", "http://example.test/authorize?client_id=demo", ""},
		{"missing host", "https:///authorize?client_id=demo", ""},
		{"userinfo is rejected", "https://user@example.test/authorize?client_id=demo", ""},
		{"fragment is rejected", "https://example.test/authorize?client_id=demo#token", ""},
		{"unknown query key", "https://example.test/authorize?client_id=demo&evil=1", ""},
		{"control characters", "https://example.test/authorize?client_id=de\x1bmo", ""},
		{"newline", "https://example.test/authorize?client_id=demo\nnext", ""},
		{"too long", "https://example.test/authorize?client_id=" + strings.Repeat("a", 4096), ""},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if got := catalogBrowserURL(tt.raw); got != tt.want {
				t.Fatalf("catalogBrowserURL(%q) = %q, want %q", tt.raw, got, tt.want)
			}
		})
	}
}

func TestCatalogEditBuffer(t *testing.T) {
	abc := catalogEdit("", playerViewKey("abc"))
	if abc != "abc" {
		t.Fatalf("typing = %q", abc)
	}
	for _, tt := range []struct {
		name  string
		input string
		key   tea.KeyMsg
		want  string
	}{
		{"typing appends", "ab", playerViewKey("cd"), "abcd"},
		{"backspace removes one rune", "a界", tea.KeyMsg{Type: tea.KeyBackspace}, "a"},
		{"backspace on an empty buffer", "", tea.KeyMsg{Type: tea.KeyBackspace}, ""},
		{"ctrl+u clears", "abc", tea.KeyMsg{Type: tea.KeyCtrlU}, ""},
		{"space appends", "ab", tea.KeyMsg{Type: tea.KeySpace}, "ab "},
		{"pasted runes append like typing", "ab", tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("pasted"), Paste: true}, "abpasted"},
		{"pasted backspace does not edit", "ab", tea.KeyMsg{Type: tea.KeyBackspace, Paste: true}, "ab"},
		{"control runes never enter the buffer", "ab", tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("c\x00d")}, "abcd"},
		{"whitespace becomes one space", "ab", tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("\tc\nd")}, "ab c d"},
		{"format controls are dropped", "ab", tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("c\u200bd")}, "abcd"},
		{"a zero-width joiner is kept", "ab", tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("c\u200dd")}, "abc\u200dd"},
		{"an unhandled key keeps the buffer", "ab", tea.KeyMsg{Type: tea.KeyEnter}, "ab"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if got := catalogEdit(tt.input, tt.key); got != tt.want {
				t.Fatalf("catalogEdit(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
	// The buffer is bounded so a long paste or a held key cannot grow it.
	full := strings.Repeat("a", 512)
	if got := catalogEdit(full, playerViewKey("b")); got != full {
		t.Fatalf("the buffer grew past its bound: %d runes", len([]rune(got)))
	}
	if got := catalogEdit(full, tea.KeyMsg{Type: tea.KeySpace}); got != full {
		t.Fatal("a full buffer accepted a space")
	}
}

func TestCatalogViewFakeServesCatalogAPI(t *testing.T) {
	api := catalogViewFake{
		search: func(_ context.Context, query string, offset int) ([]catalogItem, bool, error) {
			return []catalogItem{{Name: fmt.Sprintf("%s/%d", query, offset), URI: "spotify:track:1"}}, true, nil
		},
		lists: func(context.Context, int) ([]catalogItem, bool, error) {
			return []catalogItem{{Name: "Playlist", URI: "spotify:playlist:1"}}, false, nil
		},
		tracks: func(_ context.Context, uri string, _ int) ([]catalogItem, bool, error) {
			if uri != "spotify:playlist:1" {
				return nil, false, fmt.Errorf("unexpected uri %q", uri)
			}
			return []catalogItem{{Name: "Track", URI: "spotify:track:2"}}, false, nil
		},
	}
	// The constructor helper returns the catalog through the interface, which
	// is exactly what the setup handoff passes on.
	received, err := catalogViewReply(api)(context.Background(), "client-id", nil)
	if err != nil || received == nil {
		t.Fatalf("catalogViewReply returned %v, %v", received, err)
	}
	items, more, err := received.Search(context.Background(), "tren", 0)
	if err != nil || !more || len(items) != 1 || items[0].Name != "tren/0" {
		t.Fatalf("Search = %v, %v, %v", items, more, err)
	}
	if items, _, err := received.Playlists(context.Background(), 0); err != nil || len(items) != 1 || items[0].Name != "Playlist" {
		t.Fatalf("Playlists = %v, %v", items, err)
	}
	if items, _, err := received.PlaylistTracks(context.Background(), "spotify:playlist:1", 0); err != nil || len(items) != 1 || items[0].Name != "Track" {
		t.Fatalf("PlaylistTracks = %v, %v", items, err)
	}
	// A fake with no endpoints wired answers an empty page instead of panicking.
	empty, err := catalogViewReply(catalogViewFake{})(context.Background(), "client-id", nil)
	if err != nil || empty == nil {
		t.Fatalf("the empty fake is unusable: %v, %v", empty, err)
	}
	if items, more, err := empty.Search(context.Background(), "tren", 0); err != nil || more || items != nil {
		t.Fatalf("the empty fake answered %v, %v, %v", items, more, err)
	}
}
