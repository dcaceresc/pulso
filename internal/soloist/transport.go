package soloist

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/coder/websocket"
)

const (
	playerQueueLimit       = 10
	playerReadLimit        = 256 << 10 // Includes metadata for the entire queue.
	playerRetryMin         = 200 * time.Millisecond
	playerRetryMax         = 5 * time.Second
	playlistShuffleTimeout = 3 * time.Second
)

const (
	playerRetryNotice = "Cannot reach Soloist. Reconnecting…"
	playerPairNotice  = "Soloist is not paired."
	playerErrorNotice = "Soloist reported a playback error."
)

type Track struct {
	Title, Artist, Album string
	DurationMS           int64
}

type PlaylistShuffleStatus uint8

const (
	PlaylistShuffleIdle PlaylistShuffleStatus = iota
	PlaylistShuffleWaiting
	PlaylistShuffleUnconfirmed
)

const PlayPlaylistShuffle = "play_playlist_shuffle"

type State struct {
	Track  Track
	Queue  []Track
	Status string
	Device string
	// PlaylistContext contiene el nombre o URI de la lista reportada por Soloist.
	PlaylistContext        string
	PlaylistShuffle        PlaylistShuffleStatus
	playlistDeviceInactive bool
	// PositionMS is the playhead Soloist reported at AnchorAt; the UI adds the
	// elapsed time at Speed on top of it. AnchorAt is zero when unknown.
	PositionMS   int64
	AnchorAt     time.Time
	Speed        float64
	Volume       int
	Shuffle      bool
	Repeat       string // "off", "context" or "track", empty until Soloist says.
	OptionsKnown bool   // True once Soloist reported shuffle and repeat.
	Connected    bool
	Notice       string
	SessionID    uint64
}

type Command struct {
	Name       string
	Volume     int
	URI        string
	PositionMS int64
	Shuffle    bool
	Repeat     string
	SessionID  uint64
}

func NewStream(ctx context.Context, dataDir func() (string, error)) (<-chan State, chan<- Command) {
	states := make(chan State, 1)
	commands := make(chan Command, 8)
	go func() {
		defer close(states)
		publish := func(state State) {
			// A slow UI needs the latest full snapshot, not a backlog.
			select {
			case <-states:
			default:
			}
			states <- state
		}
		input := (<-chan Command)(commands)
		var nextSessionID uint64
		for delay := playerRetryMin; ctx.Err() == nil; delay = min(2*delay, playerRetryMax) {
			authenticated, retry := playerSession(ctx, dataDir, &input, &nextSessionID, publish)
			if authenticated {
				delay = playerRetryMin
			}
			if ctx.Err() != nil {
				return
			}
			if retry.Notice == "" {
				retry.Notice = playerRetryNotice
			}
			publish(retry)
			if !playerRetry(ctx, delay, &input) {
				return
			}
		}
	}()
	return states, commands
}

func playerRetry(ctx context.Context, delay time.Duration, input *<-chan Command) bool {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	for ctx.Err() == nil {
		select {
		case <-ctx.Done():
			return false
		case <-timer.C:
			return true
		case _, ok := <-*input:
			if !ok {
				*input = nil
			}
		}
	}
	return false
}

func playerEndpoint(dataDir func() (string, error)) (string, error) {
	dir, err := dataDir()
	if err != nil || !filepath.IsAbs(dir) {
		return "", ErrRuntime
	}
	// Reuse setup's guarded marker reads. Like setup, accept only a literal
	// loopback IP with no zone and a nonzero port, never a host name or URL.
	addr, hasAddr, addrErr := readMarker(filepath.Join(dir, "ws.addr"))
	port, hasPort, portErr := readMarker(filepath.Join(dir, "ws.port"))
	ip, ipErr := netip.ParseAddr(addr)
	number, numberErr := strconv.ParseUint(port, 10, 16)
	if addrErr != nil || portErr != nil || !hasAddr || !hasPort || ipErr != nil || !ip.IsLoopback() || ip.Zone() != "" || numberErr != nil || number == 0 {
		return "", ErrRuntime
	}
	endpoint := url.URL{Scheme: "ws", Host: net.JoinHostPort(ip.String(), strconv.FormatUint(number, 10)), Path: "/"}
	return endpoint.String(), nil
}

type playerEvent struct {
	Type       string           `json:"type"`
	LoggedIn   *bool            `json:"logged_in"`
	DeviceName *string          `json:"device_name"`
	Status     *string          `json:"status"`
	Volume     *int             `json:"volume"`
	Item       *playerWireTrack `json:"item"`
	Context    *playerContext   `json:"context"`
	IsActive   *bool            `json:"is_active"`
	Command    string           `json:"command"`
	PositionMS *int64           `json:"position_ms"`
	Position   *struct {
		MS    *int64   `json:"position_ms"`
		Speed *float64 `json:"speed"`
	} `json:"position"`
	Options *struct {
		Shuffle *bool   `json:"shuffle"`
		Repeat  *string `json:"repeat"`
	} `json:"options"`
	Upcoming []struct {
		Item *playerWireTrack `json:"item"`
	} `json:"upcoming"`
}

func (event playerEvent) deviceActivity() *bool {
	if event.Type == "playback_state" || event.Type == "device_changed" {
		return event.IsActive
	}
	return nil
}

type playerContext struct {
	URI        string `json:"uri"`
	EntityType string `json:"entity_type"`
	playerEntity
}

func (entity *playerContext) playlistLabel() string {
	if uri := entity.playlistURI(); uri != "" {
		if name := entity.name(); name != "" {
			return name
		}
		return uri
	}
	return ""
}

func (entity *playerContext) playlistURI() string {
	if entity != nil && entity.EntityType == "playlist" && IsPlaylistURI(entity.URI) {
		return entity.URI
	}
	return ""
}

type playerEntity struct {
	Name        string `json:"name"`
	Decorations struct {
		Identity struct {
			Name string `json:"name"`
		} `json:"identity"`
	} `json:"decorations"`
}

func (entity playerEntity) name() string {
	if entity.Name != "" {
		return playerMetadataText(entity.Name)
	}
	return playerMetadataText(entity.Decorations.Identity.Name)
}

type playerWireTrack struct {
	Decorations struct {
		Identity struct {
			Name string `json:"name"`
		} `json:"identity"`
		Parent struct {
			Entity playerEntity `json:"entity"`
		} `json:"parent"`
		Creators []struct {
			Entity playerEntity `json:"entity"`
		} `json:"creators"`
		Playback struct {
			DurationMS int64 `json:"duration_ms"`
		} `json:"playback"`
	} `json:"decorations"`
}

func (item *playerWireTrack) track() Track {
	if item == nil {
		return Track{}
	}
	d := item.Decorations
	artists := make([]string, 0, len(d.Creators))
	for _, creator := range d.Creators {
		if name := creator.Entity.name(); name != "" {
			artists = append(artists, name)
		}
	}
	return Track{
		Title: playerMetadataText(d.Identity.Name), Artist: strings.Join(artists, ", "),
		Album: d.Parent.Entity.name(), DurationMS: max(0, d.Playback.DurationMS),
	}
}

func playerMetadataText(text string) string {
	// Escape sequences and terminal control characters are not display data.
	return strings.TrimSpace(strings.Map(func(r rune) rune {
		if unicode.IsControl(r) || r == '\u2028' || r == '\u2029' {
			return -1
		}
		return r
	}, text))
}

type playerFrame struct {
	conn  *websocket.Conn
	event playerEvent
}

func playerRead(ctx context.Context, dataDir func() (string, error), frames chan<- playerFrame) {
	defer close(frames)
	endpoint, err := playerEndpoint(dataDir)
	if err != nil || ctx.Err() != nil {
		return
	}
	// Match the setup probe: direct HTTP transport, no redirects, and a
	// bounded upgrade. The stream has its own larger text-frame limit.
	transport := &http.Transport{MaxResponseHeaderBytes: 8 << 10, DisableCompression: true}
	defer transport.CloseIdleConnections()
	client := &http.Client{
		Transport:     transport,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	probe, cancel := context.WithTimeout(ctx, probeTimeout)
	defer cancel()
	conn, _, err := websocket.Dial(probe, endpoint, &websocket.DialOptions{HTTPClient: client})
	if err != nil {
		return
	}
	defer conn.CloseNow()
	conn.SetReadLimit(playerReadLimit)
	read := func(ctx context.Context) (playerEvent, bool) {
		kind, data, err := conn.Read(ctx)
		var event playerEvent
		ok := err == nil && kind == websocket.MessageText && json.Unmarshal(data, &event) == nil && event.Type != ""
		return event, ok
	}
	event, ok := read(probe)
	cancel()
	if !ok || event.Type != "auth_state" || event.LoggedIn == nil {
		return
	}
	for {
		select {
		case frames <- playerFrame{conn: conn, event: event}:
		case <-ctx.Done():
			return
		}
		if event, ok = read(ctx); !ok {
			return
		}
	}
}

func playerSession(ctx context.Context, dataDir func() (string, error), input *<-chan Command, nextSessionID *uint64, publish func(State)) (authenticated bool, retry State) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	frames := make(chan playerFrame)
	go playerRead(ctx, dataDir, frames)

	var state State
	var conn *websocket.Conn
	var target string
	var matched bool
	var deadline time.Time
	var timeout *time.Timer
	var timeoutC <-chan time.Time
	// La espera de resultados sobrevive a la cancelación de la intención.
	var ackDeadline time.Time
	var ackTimer *time.Timer
	var ackTimeout <-chan time.Time
	stopAckTimer := func() {
		if ackTimer != nil {
			ackTimer.Stop()
		}
		ackTimeout = nil
	}
	defer stopAckTimer()
	ackExpired := func() bool { return ackTimeout != nil && !time.Now().Before(ackDeadline) }
	// Los resultados de play no tienen identificador. No se atribuye una
	// aceptación a la solicitud más reciente si quedan respuestas anteriores.
	var playsAwaitingResult uint64
	finishShuffle := func(status PlaylistShuffleStatus) {
		if timeout != nil {
			timeout.Stop()
		}
		target, matched, timeoutC = "", false, nil
		state.PlaylistShuffle = status
	}
	defer func() { finishShuffle(PlaylistShuffleIdle) }()
	for ctx.Err() == nil {
		if ackExpired() {
			return authenticated, State{PlaylistShuffle: PlaylistShuffleUnconfirmed}
		}
		select {
		case <-ctx.Done():
			return
		case <-ackTimeout:
			return authenticated, State{PlaylistShuffle: PlaylistShuffleUnconfirmed}
		case <-timeoutC:
			finishShuffle(PlaylistShuffleUnconfirmed)
			publish(state)
		case command, ok := <-*input:
			if !ok {
				*input = nil
				continue
			}
			if !state.Connected || command.SessionID != state.SessionID {
				continue
			}
			playlistShuffle := command.Name == PlayPlaylistShuffle
			if playlistShuffle {
				if !IsPlaylistURI(command.URI) {
					continue
				}
				command.Name = "play"
			}
			packets, allowed := playerCommandPacket(command)
			if !allowed {
				continue
			}
			if playlistShuffle {
				finishShuffle(PlaylistShuffleWaiting)
				target = command.URI
				deadline = time.Now().Add(playlistShuffleTimeout)
				timeout = time.NewTimer(time.Until(deadline))
				timeoutC = timeout.C
				publish(state)
			} else if command.Name != "get_state" && command.Name != "set_volume" && state.PlaylistShuffle != PlaylistShuffleIdle {
				finishShuffle(PlaylistShuffleIdle)
				publish(state)
			}
			// Frames of one command go out in order; a failed write ends the
			// session instead of replaying it.
			for _, packet := range packets {
				if !playerWrite(ctx, conn, packet) {
					return
				}
			}
			if command.Name == "play" {
				playsAwaitingResult++
			}
			if playlistShuffle && ackTimeout == nil {
				ackDeadline = deadline
				ackTimer = time.NewTimer(time.Until(ackDeadline))
				ackTimeout = ackTimer.C
			}
		case frame, ok := <-frames:
			if !ok {
				return
			}
			conn = frame.conn
			event := frame.event
			if target != "" && !time.Now().Before(deadline) {
				finishShuffle(PlaylistShuffleUnconfirmed)
				publish(state)
			}
			switch event.Type {
			case "auth_state":
				if event.LoggedIn == nil {
					return
				}
				if !*event.LoggedIn {
					if playsAwaitingResult != 0 {
						return authenticated, State{Notice: playerPairNotice}
					}
					finishShuffle(PlaylistShuffleIdle)
					state = State{Notice: playerPairNotice}
				} else if !state.Connected {
					limit := playerQueueLimit
					if !playerWrite(ctx, conn, playerRequest{Command: "get_state"}) || !playerWrite(ctx, conn, playerRequest{Command: "get_queue", Limit: limit}) {
						return
					}
					// Drop input buffered before the new session became ready.
					// There is no retry of commands from a failed write either.
					for n := len(*input); n > 0; n-- {
						<-*input
					}
					// A keypress from a view of an older session must not control this socket.
					*nextSessionID = *nextSessionID + 1
					state.SessionID = *nextSessionID
					state.Connected, state.Notice = true, ""
					authenticated = true
				}
				if event.DeviceName != nil {
					state.Device = playerMetadataText(*event.DeviceName)
				}
			case "logout":
				if playsAwaitingResult != 0 {
					return authenticated, State{Notice: playerPairNotice}
				}
				finishShuffle(PlaylistShuffleIdle)
				state = State{Notice: playerPairNotice}
			case "error":
				// El error no identifica la orden; no se puede descartar solo un ACK.
				if playsAwaitingResult != 0 {
					return authenticated, State{Notice: playerErrorNotice}
				}
				if target != "" {
					finishShuffle(PlaylistShuffleUnconfirmed)
				}
				state.Notice = playerErrorNotice
			case "command_result":
				if event.Command != "play" || playsAwaitingResult == 0 {
					continue
				}
				if ackExpired() {
					return authenticated, State{PlaylistShuffle: PlaylistShuffleUnconfirmed}
				}
				playsAwaitingResult--
				if playsAwaitingResult == 0 {
					stopAckTimer()
					if target != "" && !matched && !playerWrite(ctx, conn, playerRequest{Command: "get_state"}) {
						return
					}
				}
				if target == "" {
					continue
				}
			default:
				if !state.Connected || !state.apply(event) {
					continue
				}
				if target != "" {
					if active := event.deviceActivity(); active != nil && !*active {
						finishShuffle(PlaylistShuffleUnconfirmed)
					} else if event.Type == "context_changed" || event.Type == "playback_state" {
						if !state.playlistDeviceInactive && event.Context.playlistURI() == target {
							matched = true
						} else if matched {
							finishShuffle(PlaylistShuffleUnconfirmed)
						}
					}
				}
			}
			if target != "" && matched && playsAwaitingResult == 0 {
				if !time.Now().Before(deadline) {
					finishShuffle(PlaylistShuffleUnconfirmed)
				} else {
					if !playerWrite(ctx, conn, playerShufflePacket(true)) || !playerWrite(ctx, conn, playerRequest{Command: "get_state"}) {
						return
					}
					finishShuffle(PlaylistShuffleIdle)
				}
			}
			publish(state)
		}
	}
	return
}

// playerRepeatMode normalizes the repeat modes Soloist reports and accepts.
func playerRepeatMode(raw string) (string, bool) {
	switch mode := strings.ToLower(strings.TrimSpace(raw)); mode {
	case "off", "context", "track":
		return mode, true
	}
	return "", false
}

func (state *State) apply(event playerEvent) bool {
	if active := event.deviceActivity(); active != nil {
		state.playlistDeviceInactive = !*active
		if !*active {
			state.PlaylistContext = ""
		}
	}
	now := time.Now()
	// Every reported position starts a new interpolation anchor.
	updatePosition := func() {
		if event.Position != nil && event.Position.MS != nil {
			state.PositionMS = max(0, *event.Position.MS)
		} else if event.PositionMS != nil {
			state.PositionMS = max(0, *event.PositionMS)
		} else {
			return
		}
		state.AnchorAt = now
	}
	updateSpeed := func() {
		if event.Position != nil && event.Position.Speed != nil {
			state.Speed = max(0, *event.Position.Speed)
		}
	}
	// A mode outside the three is ignored instead of guessed.
	updateOptions := func() {
		if event.Options == nil {
			return
		}
		state.OptionsKnown = true
		if event.Options.Shuffle != nil {
			state.Shuffle = *event.Options.Shuffle
		}
		if event.Options.Repeat != nil {
			if repeat, ok := playerRepeatMode(*event.Options.Repeat); ok {
				state.Repeat = repeat
			}
		}
	}
	updateStatus := func() {
		if event.Status != nil {
			status := strings.ToLower(*event.Status)
			switch status {
			case "playing", "paused", "stopped", "buffering", "loading", "idle":
				state.Status = status
			default:
				state.Status = ""
			}
		}
	}
	switch event.Type {
	case "playback_state", "track_changed":
		state.Track = event.Item.track()
		state.PositionMS, state.AnchorAt = 0, now
		if event.Type == "playback_state" && !state.playlistDeviceInactive {
			state.PlaylistContext = event.Context.playlistLabel()
		}
		if event.Type == "playback_state" || event.Item == nil {
			state.Status = ""
		}
		updateStatus()
		updateSpeed()
		updatePosition()
		updateOptions()
		if event.Volume != nil {
			state.Volume = min(100, max(0, *event.Volume))
		}
	case "context_changed":
		if !state.playlistDeviceInactive {
			state.PlaylistContext = event.Context.playlistLabel()
		}
	case "playback_changed":
		updateStatus()
		updatePosition()
	case "position_sync":
		updatePosition()
		updateSpeed()
	case "options_changed":
		updateOptions()
	case "volume_changed":
		if event.Volume != nil {
			state.Volume = min(100, max(0, *event.Volume))
		}
	case "device_changed":
		if event.DeviceName != nil {
			state.Device = playerMetadataText(*event.DeviceName)
		}
	case "queue_changed", "queue_state":
		// Allocate a new slice; a previous snapshot may still be rendered.
		state.Queue = make([]Track, 0, min(len(event.Upcoming), playerQueueLimit))
		for _, next := range event.Upcoming {
			if next.Item != nil {
				state.Queue = append(state.Queue, next.Item.track())
				if len(state.Queue) == playerQueueLimit {
					break
				}
			}
		}
	default:
		return false
	}
	return true
}

type playerRequest struct {
	Command    string `json:"command"`
	URI        string `json:"uri,omitempty"`
	Limit      int    `json:"limit,omitempty"`
	Volume     *int   `json:"volume,omitempty"`
	PositionMS *int64 `json:"position_ms,omitempty"`
	Enabled    *bool  `json:"enabled,omitempty"`
}

// playerCommandPacket translates one keypress into the frames Soloist expects in
// order, or rejects the command when it is not on the allowlist.
func playerCommandPacket(command Command) ([]playerRequest, bool) {
	switch command.Name {
	case "play", "pause", "skip_next", "skip_prev":
		packet := playerRequest{Command: command.Name}
		if command.URI == "" {
			return []playerRequest{packet}, true
		}
		// Only play accepts a target, and only a playable Spotify URI.
		uri, ok := playerSpotifyURI(command.URI)
		if command.Name != "play" || !ok {
			return nil, false
		}
		packet.URI = uri
		return []playerRequest{packet}, true
	case "set_volume":
		volume := min(100, max(0, command.Volume))
		return []playerRequest{{Command: "set_volume", Volume: &volume}}, true
	case "get_state":
		return []playerRequest{{Command: "get_state"}}, true
	case "seek":
		position := max(0, command.PositionMS)
		return []playerRequest{{Command: "seek", PositionMS: &position}}, true
	case "set_shuffle":
		return []playerRequest{playerShufflePacket(command.Shuffle)}, true
	case "set_repeat":
		repeat, ok := playerRepeatMode(command.Repeat)
		if !ok {
			return nil, false
		}
		// Repeat is two independent flags. Soloist wants the one being cleared
		// first, so a mode never re-enables its other half by accident.
		track, context := repeat == "track", repeat == "context"
		trackPacket := playerRequest{Command: "set_repeat_track", Enabled: &track}
		contextPacket := playerRequest{Command: "set_repeat_context", Enabled: &context}
		if track {
			return []playerRequest{contextPacket, trackPacket}, true
		}
		return []playerRequest{trackPacket, contextPacket}, true
	}
	return nil, false
}

func playerShufflePacket(enabled bool) playerRequest {
	return playerRequest{Command: "set_shuffle", Enabled: &enabled}
}

func playerSpotifyURI(raw string) (string, bool) {
	if rest, ok := strings.CutPrefix(raw, "spotify:"); ok {
		kind, id, found := strings.Cut(rest, ":")
		if !found || !playerPlayableKind(kind) || !playerURIID(id) {
			return "", false
		}
		return "spotify:" + kind + ":" + id, true
	}
	return "", false
}

func IsPlaylistURI(uri string) bool {
	_, ok := playerSpotifyURI(uri)
	return ok && strings.HasPrefix(uri, "spotify:playlist:")
}

func playerPlayableKind(kind string) bool {
	switch kind {
	case "track", "album", "playlist", "episode":
		return true
	}
	return false
}

// Spotify ids are base-62. Reject anything that could change the command shape.
func playerURIID(id string) bool {
	if id == "" || len(id) > 64 {
		return false
	}
	for _, r := range id {
		if !(r >= '0' && r <= '9' || r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z') {
			return false
		}
	}
	return true
}

func playerWrite(ctx context.Context, conn *websocket.Conn, packet playerRequest) bool {
	ctx, cancel := context.WithTimeout(ctx, probeTimeout)
	defer cancel()
	data, err := json.Marshal(struct {
		Type string `json:"type"`
		playerRequest
	}{Type: "command", playerRequest: packet})
	return err == nil && conn.Write(ctx, websocket.MessageText, data) == nil
}

// Pause stops audio when Pulso exits. The daemon stays running and keeps
// its pairing, so the next launch starts playing only when you ask for it.
func Pause(ctx context.Context, dataDir func() (string, error)) {
	ctx, cancel := context.WithTimeout(ctx, probeTimeout)
	defer cancel()
	dir, err := dataDir()
	if err != nil {
		return
	}
	endpoint, err := runtimeEndpoint(dir)
	if err != nil || endpoint == "" {
		return
	}
	conn, event, err := openAuth(ctx, endpoint)
	if err != nil {
		return
	}
	defer conn.CloseNow()
	if event.Type != "auth_state" || event.LoggedIn == nil || !*event.LoggedIn {
		return
	}
	playerWrite(ctx, conn, playerRequest{Command: "pause"})
}
