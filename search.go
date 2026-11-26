package main

import (
	"context"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

// searchPageMsg carries one completed search page, fenced by its generation.
type searchPageMsg struct {
	generation uint64
	tracks     []libraryTrack
	offset     int
	total      int
	more       bool
	err        error
}

// searchState is the top bar's view of one Spotify track search. It reuses the
// library's mechanics: every fetch runs under one cancellable generation, so a
// newer query drops an older reply instead of merging a stale page.
type searchState struct {
	ctx    context.Context
	source librarySource
	query  string

	tracks []libraryTrack
	total  int
	more   bool
	offset int
	cursor int

	loading bool
	loaded  bool
	// active reports whether the table shows search results instead of the
	// playlist or the Soloist queue.
	active bool
	err    error

	work       context.Context
	cancel     context.CancelFunc
	generation uint64
}

func newSearchState(ctx context.Context, source librarySource) *searchState {
	return &searchState{ctx: ctx, source: source}
}

// submit starts a fresh search. An empty or blank query never reaches the
// source, which is what keeps the network quiet while the field is empty.
func (s *searchState) submit(query string) tea.Cmd {
	if s == nil {
		return nil
	}
	query = strings.TrimSpace(query)
	if query == "" {
		return nil
	}
	s.stopWork()
	s.query, s.active, s.loaded, s.err = query, true, false, nil
	s.tracks, s.total, s.more, s.offset, s.cursor = nil, 0, false, 0, 0
	return s.fetch(0)
}

// close leaves the search view and fences off a pending page, so the table
// returns to the playlist it showed before.
func (s *searchState) close() {
	if s == nil {
		return
	}
	s.stopWork()
	s.query, s.active = "", false
	s.tracks, s.total, s.more, s.offset, s.cursor = nil, 0, false, 0, 0
	s.loading, s.loaded, s.err = false, false, nil
}

func (s *searchState) stopWork() {
	if s.cancel != nil {
		s.cancel()
	}
	s.work, s.cancel = nil, nil
	s.generation++
	s.loading = false
}

func (s *searchState) begin() (context.Context, uint64) {
	s.stopWork()
	s.work, s.cancel = context.WithCancel(s.ctx)
	return s.work, s.generation
}

func (s *searchState) current(generation uint64) bool {
	return s.work != nil && s.generation == generation && s.ctx.Err() == nil
}

func (s *searchState) fetch(offset int) tea.Cmd {
	if s.source == nil || s.ctx.Err() != nil {
		return nil
	}
	ctx, generation := s.begin()
	source, query := s.source, s.query
	s.loading = true
	return func() tea.Msg {
		if ctx.Err() != nil {
			return nil
		}
		tracks, total, more, err := source.SearchPage(ctx, query, offset)
		return searchPageMsg{generation: generation, tracks: tracks, offset: offset, total: total, more: more, err: err}
	}
}

// update applies a fenced page and reports whether it changed the table.
func (s *searchState) update(msg tea.Msg) tea.Cmd {
	if s == nil {
		return nil
	}
	page, ok := msg.(searchPageMsg)
	if !ok || !s.current(page.generation) {
		return nil
	}
	s.stopWork()
	s.loaded = true
	if page.err != nil {
		s.err = page.err
		return nil
	}
	if page.offset == 0 {
		s.tracks = nil
	}
	s.tracks = append(s.tracks, page.tracks...)
	s.total, s.more = page.total, page.more
	s.offset = page.offset + spotifyCatalogPageSize
	s.err = nil
	s.clamp()
	return nil
}

// retry repeats the page that failed, so Enter on the error row recovers.
func (s *searchState) retry() tea.Cmd {
	if s == nil || !s.active {
		return nil
	}
	return s.fetch(s.offset)
}

// move pages in one more result when the cursor passes the loaded rows.
func (s *searchState) move(delta int) tea.Cmd {
	if s == nil || !s.active || len(s.tracks) == 0 {
		return nil
	}
	last := len(s.tracks) - 1
	if s.more {
		last++
	}
	s.cursor = min(max(0, s.cursor+delta), last)
	if s.cursor > len(s.tracks)-1 && s.more && !s.loading {
		return s.fetch(s.offset)
	}
	return nil
}

func (s *searchState) clamp() {
	if s == nil {
		return
	}
	s.cursor = playerIndex(s.cursor, len(s.tracks))
}

// selected is the result under the table cursor.
func (s *searchState) selected() (libraryTrack, bool) {
	if s == nil || s.cursor < 0 || s.cursor >= len(s.tracks) {
		return libraryTrack{}, false
	}
	return s.tracks[s.cursor], true
}
