package main

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

type catalogViewFake struct {
	search func(context.Context, string, int) ([]catalogItem, bool, error)
	lists  func(context.Context, int) ([]catalogItem, bool, error)
	tracks func(context.Context, string, int) ([]catalogItem, bool, error)
}

func (f catalogViewFake) Search(ctx context.Context, query string, offset int) ([]catalogItem, bool, error) {
	return f.search(ctx, query, offset)
}

func (f catalogViewFake) Playlists(ctx context.Context, offset int) ([]catalogItem, bool, error) {
	return f.lists(ctx, offset)
}

func (f catalogViewFake) PlaylistTracks(ctx context.Context, id string, offset int) ([]catalogItem, bool, error) {
	return f.tracks(ctx, id, offset)
}

func catalogViewReply(api catalogAPI) func(context.Context, string, func(string)) (catalogAPI, error) {
	return func(context.Context, string, func(string)) (catalogAPI, error) { return api, nil }
}

func catalogViewDrain(t *testing.T, m *catalogModel, cmd tea.Cmd) {
	t.Helper()
	for steps := 0; cmd != nil; steps++ {
		if steps == 5 {
			t.Fatal("catalog commands did not settle")
		}
		_, cmd = m.Update(playerViewRun(t, cmd))
	}
}

func catalogViewKey(t *testing.T, m *catalogModel, key tea.KeyMsg) {
	t.Helper()
	_, cmd := m.Update(key)
	catalogViewDrain(t, m, cmd)
}

func TestCatalogViewSearchAndPagination(t *testing.T) {
	var calls []string
	api := catalogViewFake{search: func(_ context.Context, query string, offset int) ([]catalogItem, bool, error) {
		calls = append(calls, fmt.Sprintf("%s/%d", query, offset))
		if offset == 0 {
			// An API page can contain fewer than ten usable items after filtering.
			return []catalogItem{{Name: "Track 1", URI: "spotify:track:1"}}, true, nil
		}
		return []catalogItem{{Name: "Final track", Detail: "Artist", URI: "spotify:track:11"}}, false, nil
	}}
	var played []string
	m := newCatalogModel(context.Background(), preferences{}, 80, 24, "client-id", catalogViewReply(api), func(uri string) {
		played = append(played, uri)
	})
	if !m.Closed() || m.View() != "" || m.Init() != nil || len(calls) != 0 {
		t.Fatal("catalog must start closed and idle")
	}
	catalogViewDrain(t, m, m.Open("search"))
	catalogViewKey(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	if m.Closed() || len(calls) != 0 {
		t.Fatal("opening search or submitting an empty query fetched results")
	}
	catalogViewKey(t, m, playerViewKey("jazzx"))
	catalogViewKey(t, m, tea.KeyMsg{Type: tea.KeyBackspace})
	catalogViewKey(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	playerViewText(t, m, "Track 1")
	catalogViewKey(t, m, tea.KeyMsg{Type: tea.KeyPgDown})
	playerViewText(t, m, "Final track", "Artist")
	catalogViewKey(t, m, tea.KeyMsg{Type: tea.KeyPgDown})
	catalogViewKey(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	if !reflect.DeepEqual(calls, []string{"jazz/0", "jazz/10"}) || !reflect.DeepEqual(played, []string{"spotify:track:11"}) {
		t.Fatalf("search calls %v, playback %v", calls, played)
	}
}

func TestCatalogViewPlaylistDetailsAndPlayback(t *testing.T) {
	api := catalogViewFake{
		lists: func(_ context.Context, offset int) ([]catalogItem, bool, error) {
			if offset != 0 {
				return nil, false, fmt.Errorf("unexpected playlist offset")
			}
			return []catalogItem{
				{Name: "Restricted mix", URI: "spotify:playlist:restricted"},
				{Name: "Owned mix", URI: "spotify:playlist:owned"},
			}, false, nil
		},
		tracks: func(_ context.Context, id string, offset int) ([]catalogItem, bool, error) {
			if offset != 0 {
				return nil, false, fmt.Errorf("unexpected track offset")
			}
			switch id {
			case "spotify:playlist:restricted":
				return nil, false, fmt.Errorf("wrapped: %w", errPlaylistUnavailable)
			case "spotify:playlist:owned":
				return []catalogItem{{Name: "Owned track", URI: "spotify:track:owned"}}, false, nil
			default:
				return nil, false, fmt.Errorf("unexpected playlist identifier")
			}
		},
	}
	var played []string
	m := newCatalogModel(context.Background(), preferences{}, 80, 24, "client-id", catalogViewReply(api), func(uri string) {
		played = append(played, uri)
	})
	catalogViewDrain(t, m, m.Open("playlists"))
	playerViewText(t, m, "Restricted mix", "Owned mix")
	catalogViewKey(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	if view := playerViewText(t, m, "Playlist contents unavailable", "p: play playlist"); strings.Contains(view, "No tracks") || len(played) != 0 {
		t.Fatal("unsupported playlist was shown as empty or played by Enter")
	}
	catalogViewKey(t, m, playerViewKey("p"))
	catalogViewKey(t, m, tea.KeyMsg{Type: tea.KeyLeft})
	catalogViewKey(t, m, tea.KeyMsg{Type: tea.KeyDown})
	catalogViewKey(t, m, playerViewKey("p"))
	catalogViewKey(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	playerViewText(t, m, "Owned track")
	catalogViewKey(t, m, playerViewKey("p"))
	if want := []string{"spotify:playlist:restricted", "spotify:playlist:owned", "spotify:track:owned"}; !reflect.DeepEqual(played, want) {
		t.Fatalf("played %v, want %v", played, want)
	}
}

func TestCatalogViewAuthorizationURLAndFailure(t *testing.T) {
	const url = "https://example.test/authorize?client_id=demo"
	const secret = "fake-sensitive-token"
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	finish := make(chan struct{})
	m := newCatalogModel(ctx, preferences{}, 100, 24, "", func(ctx context.Context, id string, publish func(string)) (catalogAPI, error) {
		if id != "demo" {
			return nil, fmt.Errorf("wrong Client ID")
		}
		publish(url)
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-finish:
			return nil, errors.New(secret)
		}
	}, nil)
	catalogViewDrain(t, m, m.Open("playlists"))
	playerViewText(t, m, "Client ID")
	catalogViewKey(t, m, playerViewKey("demo"))
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	_, follow := m.Update(playerViewRun(t, cmd))
	playerViewText(t, m, url, "browser")
	close(finish)
	catalogViewDrain(t, m, follow)
	if view := playerViewText(t, m, "Authorization failed"); strings.Contains(view, secret) {
		t.Fatal("authorization error leaked sensitive text")
	}
}

func TestCatalogViewEscapeCancelsAndIgnoresStaleResults(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	started := make(chan context.Context, 1)
	api := catalogViewFake{search: func(ctx context.Context, query string, _ int) ([]catalogItem, bool, error) {
		if query == "stale" {
			started <- ctx
			<-ctx.Done()
			return []catalogItem{{Name: "Stale result"}}, false, nil
		}
		return []catalogItem{{Name: "Fresh result"}}, false, nil
	}}
	m := newCatalogModel(ctx, preferences{}, 80, 24, "client-id", catalogViewReply(api), nil)
	catalogViewDrain(t, m, m.Open("search"))
	catalogViewKey(t, m, playerViewKey("stale"))
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("submitted query did not schedule a command")
	}
	stale := make(chan tea.Msg, 1)
	go func() { stale <- cmd() }()
	workCtx := playerViewRun(t, func() tea.Msg { return <-started }).(context.Context)
	_, cmd = m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if cmd != nil || !m.Closed() || m.View() != "" || workCtx.Err() != context.Canceled {
		t.Fatal("Esc must cancel pending work and close only the overlay")
	}
	catalogViewDrain(t, m, m.Open("search"))
	catalogViewKey(t, m, playerViewKey("fresh"))
	catalogViewKey(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	_, cmd = m.Update(playerViewRun(t, func() tea.Msg { return <-stale }))
	if view := playerViewText(t, m, "Fresh result"); cmd != nil || strings.Contains(view, "Stale result") {
		t.Fatal("stale result affected reopened search")
	}
}

func TestCatalogViewSpanishChrome(t *testing.T) {
	api := catalogViewFake{lists: func(context.Context, int) ([]catalogItem, bool, error) {
		return nil, false, nil
	}}
	m := newCatalogModel(context.Background(), preferences{Language: "es", Theme: "light"}, 80, 24, "client-id", catalogViewReply(api), nil)
	catalogViewDrain(t, m, m.Open("playlists"))
	playerViewText(t, m, "Listas de reproducción", "No hay listas", "Esc: cerrar")
}

func TestCatalogViewSanitizesMetadataAndFitsWindow(t *testing.T) {
	api := catalogViewFake{lists: func(context.Context, int) ([]catalogItem, bool, error) {
		return []catalogItem{{Name: "\x1b[31m彩虹\x1b[0m\nTitle\x00", Detail: "\x1b]0;UNTRUSTED\x07" + strings.Repeat("界", 60)}}, false, nil
	}}
	m := newCatalogModel(context.Background(), preferences{}, 80, 24, "client-id", catalogViewReply(api), nil)
	catalogViewDrain(t, m, m.Open("playlists"))
	playerViewText(t, m, "彩虹 Title")
	for _, size := range []tea.WindowSizeMsg{{Width: 80, Height: 24}, {Width: 14, Height: 4}, {Width: 1, Height: 1}, {Width: 0, Height: 5}, {Width: 5, Height: 0}} {
		m.Update(size)
		view := m.View()
		if size.Width == 0 || size.Height == 0 {
			if view != "" {
				t.Fatalf("zero-sized view rendered: %q", view)
			}
			continue
		}
		plain := ansi.Strip(view)
		if strings.Contains(view, "UNTRUSTED") || strings.Contains(view, "\x1b[31m") || strings.ContainsAny(plain, "\r\t\x00\x07\x1b") {
			t.Fatalf("metadata injected terminal controls: %q", view)
		}
		rows := strings.Split(plain, "\n")
		if len(rows) > size.Height {
			t.Fatalf("viewport overflow: %d rows in %d", len(rows), size.Height)
		}
		for _, row := range rows {
			if lipgloss.Width(row) > size.Width {
				t.Fatalf("viewport overflow at width %d: %q", size.Width, row)
			}
		}
	}
}

func TestCatalogRemembersClientID(t *testing.T) {
	var saved []string
	api := catalogViewFake{search: func(context.Context, string, int) ([]catalogItem, bool, error) {
		return []catalogItem{{Name: "Track", URI: "spotify:track:1"}}, false, nil
	}}
	m := newCatalogModel(context.Background(), preferences{}, 80, 24, "", catalogViewReply(api), nil)
	m.saveClientID = func(id string) { saved = append(saved, id) }
	catalogViewDrain(t, m, m.Open("search"))
	catalogViewKey(t, m, playerViewKey("client123"))
	catalogViewKey(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	if len(saved) != 1 || saved[0] != "client123" {
		t.Fatalf("saved client ids = %v", saved)
	}
	if !m.Closed() && m.clientID != "client123" {
		t.Fatalf("client id was not kept for this session: %q", m.clientID)
	}
}
