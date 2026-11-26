package main

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"sync"

	tea "github.com/charmbracelet/bubbletea"
)

// libraryTrack is one playable row of a playlist page.
type libraryTrack struct {
	Name, Artist string
	DurationMS   int64
	URI          string
}

// libraryPlaylist is one sidebar row; Total is the playlist's track count.
type libraryPlaylist struct {
	Name, Owner, URI string
	Total            int
}

// librarySource is the paginated Spotify library the workspace reads. Offsets
// advance by spotifyCatalogPageSize, not by the number of usable rows.
type librarySource interface {
	PlaylistPage(ctx context.Context, offset int) ([]libraryPlaylist, int, bool, error)
	TrackPage(ctx context.Context, uri string, offset int) ([]libraryTrack, int, bool, error)
}

// librarySourceOf accepts the setup handoff only when the catalog can serve
// library pages; a typed nil catalog stays absent.
func librarySourceOf(api catalogAPI) librarySource {
	if api == nil {
		return nil
	}
	source, ok := api.(librarySource)
	if !ok {
		return nil
	}
	if value := reflect.ValueOf(source); value.Kind() == reflect.Pointer && value.IsNil() {
		return nil
	}
	return source
}

type libraryNotice uint8

const (
	libraryNoticeNone libraryNotice = iota
	libraryNoticeLoadFailed
	libraryNoticeUnavailable
	libraryNoticeAuthFailed
)

// libraryPageMsg carries one completed page fetch, fenced by its generation.
type libraryPageMsg struct {
	generation   uint64
	playlists    []libraryPlaylist
	tracks       []libraryTrack
	offset       int
	total        int
	more         bool
	playlistPage bool
	err          error
}

// libraryAuthMsg reports the authorization URL and its eventual result.
type libraryAuthMsg struct {
	generation uint64
	url        string
	api        catalogAPI
	done       bool
}

// libraryState is the workspace's read-only view of the Spotify library. Every
// fetch runs under one cancellable generation, so a newer request that
// supersedes an older one drops its reply instead of merging a stale page.
type libraryState struct {
	ctx       context.Context
	source    librarySource
	authorize func(context.Context, string, func(string)) (catalogAPI, error)
	clientID  string

	playlists []libraryPlaylist
	tracks    []libraryTrack
	playlist  libraryPlaylist

	playlistTotal, trackTotal      int
	playlistMore, trackMore        bool
	playlistOffset, trackOffset    int
	sidebarSelected, tableSelected int

	loadingPlaylists, loadingTracks bool
	loadedPlaylists, loadedTracks   bool
	notice                          libraryNotice
	restricted                      bool
	err                             error
	authURL                         string

	work       context.Context
	cancel     context.CancelFunc
	authEvents <-chan libraryAuthMsg
	generation uint64
	started    bool
}

func newLibraryState(ctx context.Context, source librarySource, authorize func(context.Context, string, func(string)) (catalogAPI, error), clientID string) *libraryState {
	return &libraryState{ctx: ctx, source: source, authorize: authorize, clientID: strings.TrimSpace(clientID)}
}

// open reports whether the table shows a playlist instead of the Soloist queue.
func (l *libraryState) open() bool {
	return l != nil && l.playlist.URI != ""
}

// canAuthorize reports whether the sidebar offers the one-line call to action.
func (l *libraryState) canAuthorize() bool {
	return l != nil && l.source == nil && l.authorize != nil
}

// authorizing reports whether the authorization flow owns the sidebar.
func (l *libraryState) authorizing() bool {
	return l != nil && l.source == nil && l.work != nil
}

// init loads the first playlist page when the setup handoff already supplied a
// library source.
func (l *libraryState) init() tea.Cmd {
	if l == nil || l.source == nil || l.started || l.ctx.Err() != nil {
		return nil
	}
	l.started = true
	return l.fetchPlaylists(0)
}

// stopWork cancels the pending fetch and fences off its reply.
func (l *libraryState) stopWork() {
	if l.cancel != nil {
		l.cancel()
	}
	l.work, l.cancel, l.authEvents = nil, nil, nil
	l.generation++
	l.loadingPlaylists, l.loadingTracks = false, false
}

func (l *libraryState) begin() (context.Context, uint64) {
	l.stopWork()
	l.work, l.cancel = context.WithCancel(l.ctx)
	return l.work, l.generation
}

func (l *libraryState) current(generation uint64) bool {
	return l.work != nil && l.generation == generation && l.ctx.Err() == nil
}

func (l *libraryState) fetchPlaylists(offset int) tea.Cmd {
	if l.source == nil || l.ctx.Err() != nil {
		return nil
	}
	ctx, generation := l.begin()
	source := l.source
	l.loadingPlaylists = true
	return func() tea.Msg {
		if ctx.Err() != nil {
			return nil
		}
		playlists, total, more, err := source.PlaylistPage(ctx, offset)
		return libraryPageMsg{generation: generation, playlistPage: true, playlists: playlists, offset: offset, total: total, more: more, err: err}
	}
}

func (l *libraryState) fetchTracks(offset int) tea.Cmd {
	if l.source == nil || l.playlist.URI == "" || l.ctx.Err() != nil {
		return nil
	}
	ctx, generation := l.begin()
	source, uri := l.source, l.playlist.URI
	l.loadingTracks = true
	return func() tea.Msg {
		if ctx.Err() != nil {
			return nil
		}
		tracks, total, more, err := source.TrackPage(ctx, uri, offset)
		return libraryPageMsg{generation: generation, tracks: tracks, offset: offset, total: total, more: more, err: err}
	}
}

// startAuth requests authorization and publishes the URL before the result.
func (l *libraryState) startAuth() tea.Cmd {
	if l.authorize == nil || l.source != nil || l.ctx.Err() != nil {
		return nil
	}
	ctx, generation := l.begin()
	authorize, clientID := l.authorize, l.clientID
	l.notice, l.authURL = libraryNoticeNone, ""
	// One URL and one completion fit without blocking the OAuth callback.
	events := make(chan libraryAuthMsg, 2)
	l.authEvents = events
	return func() tea.Msg {
		if ctx.Err() != nil {
			return nil
		}
		go func() {
			var published sync.Once
			api, err := authorize(ctx, clientID, func(raw string) {
				if link := catalogBrowserURL(raw); link != "" && ctx.Err() == nil {
					published.Do(func() { events <- libraryAuthMsg{generation: generation, url: link} })
				}
			})
			if err != nil {
				api = nil
			}
			events <- libraryAuthMsg{generation: generation, api: api, done: true}
		}()
		return libraryAwaitAuth(ctx, events)()
	}
}

// libraryAwaitAuth reads the next authorization event without blocking the
// Bubble Tea program.
func libraryAwaitAuth(ctx context.Context, events <-chan libraryAuthMsg) tea.Cmd {
	return func() tea.Msg {
		select {
		case <-ctx.Done():
			return nil
		case msg := <-events:
			return msg
		}
	}
}

// update applies a fenced library reply and returns any follow-up request.
func (l *libraryState) update(msg tea.Msg) tea.Cmd {
	if l == nil {
		return nil
	}
	switch msg := msg.(type) {
	case libraryAuthMsg:
		if !l.current(msg.generation) {
			return nil
		}
		if !msg.done {
			l.authURL = msg.url
			return libraryAwaitAuth(l.work, l.authEvents)
		}
		l.stopWork()
		source := librarySourceOf(msg.api)
		if source == nil {
			l.notice = libraryNoticeAuthFailed
			return nil
		}
		l.source, l.authURL, l.notice, l.started = source, "", libraryNoticeNone, true
		return l.fetchPlaylists(0)
	case libraryPageMsg:
		if !l.current(msg.generation) {
			return nil
		}
		l.stopWork()
		if msg.playlistPage {
			l.applyPlaylistPage(msg)
		} else {
			l.applyTrackPage(msg)
		}
	}
	return nil
}

func (l *libraryState) applyPlaylistPage(msg libraryPageMsg) {
	if msg.err != nil {
		l.err, l.notice = msg.err, libraryNoticeLoadFailed
		return
	}
	if msg.offset == 0 {
		l.playlists = nil
	}
	l.playlists = append(l.playlists, msg.playlists...)
	l.playlistTotal, l.playlistMore = msg.total, msg.more
	l.playlistOffset = msg.offset + spotifyCatalogPageSize
	l.loadedPlaylists, l.notice, l.err = true, libraryNoticeNone, nil
	l.sidebarSelected = playerIndex(l.sidebarSelected, len(l.playlists))
}

func (l *libraryState) applyTrackPage(msg libraryPageMsg) {
	l.loadedTracks = true
	if msg.err != nil {
		l.err = msg.err
		if errors.Is(msg.err, errPlaylistUnavailable) {
			l.restricted, l.notice = true, libraryNoticeUnavailable
		} else {
			l.notice = libraryNoticeLoadFailed
		}
		l.clamp()
		return
	}
	if msg.offset == 0 {
		l.tracks = nil
	}
	l.tracks = append(l.tracks, msg.tracks...)
	if msg.total > 0 {
		l.trackTotal = msg.total
	}
	l.trackMore = msg.more
	l.trackOffset = msg.offset + spotifyCatalogPageSize
	l.notice, l.err, l.restricted = libraryNoticeNone, nil, false
	l.clamp()
}

// openPlaylist loads the first track page of the playlist under the cursor.
func (l *libraryState) openPlaylist() tea.Cmd {
	if l == nil || len(l.playlists) == 0 {
		return nil
	}
	l.stopWork()
	l.playlist = l.playlists[playerIndex(l.sidebarSelected, len(l.playlists))]
	l.tracks = nil
	l.trackTotal, l.trackMore = l.playlist.Total, false
	l.trackOffset, l.tableSelected = 0, 0
	l.loadedTracks, l.restricted = false, false
	l.notice, l.err, l.authURL = libraryNoticeNone, nil, ""
	return l.fetchTracks(0)
}

// moveSidebar moves the library cursor and pages in more playlists.
func (l *libraryState) moveSidebar(delta int) tea.Cmd {
	if l == nil || len(l.playlists) == 0 {
		return nil
	}
	last := len(l.playlists) - 1
	if l.playlistMore {
		last++
	}
	l.sidebarSelected = min(max(0, l.sidebarSelected+delta), last)
	if l.sidebarSelected > len(l.playlists)-1 && l.playlistMore && !l.loadingPlaylists {
		return l.fetchPlaylists(l.playlistOffset)
	}
	return nil
}

// moveTracks moves the table cursor and pages in more tracks. A cursor that
// reaches the first unloaded row lands on it once its page arrives.
func (l *libraryState) moveTracks(delta int) tea.Cmd {
	if l == nil || !l.open() || len(l.tracks) == 0 {
		return nil
	}
	last := len(l.tracks) - 1
	if l.trackMore {
		last++
	}
	l.tableSelected = min(max(0, l.tableSelected+delta), last)
	if l.tableSelected > len(l.tracks)-1 && l.trackMore && !l.loadingTracks {
		return l.fetchTracks(l.trackOffset)
	}
	return nil
}

func (l *libraryState) clamp() {
	if l == nil {
		return
	}
	l.sidebarSelected = playerIndex(l.sidebarSelected, len(l.playlists))
	l.tableSelected = playerIndex(l.tableSelected, len(l.tracks))
}

// selectedPlaylist is the playlist under the sidebar cursor.
func (l *libraryState) selectedPlaylist() (libraryPlaylist, bool) {
	if l == nil || len(l.playlists) == 0 {
		return libraryPlaylist{}, false
	}
	return l.playlists[playerIndex(l.sidebarSelected, len(l.playlists))], true
}

// selectedTrack is the track under the table cursor.
func (l *libraryState) selectedTrack() (libraryTrack, bool) {
	if l == nil || l.tableSelected < 0 || l.tableSelected >= len(l.tracks) {
		return libraryTrack{}, false
	}
	return l.tracks[l.tableSelected], true
}
