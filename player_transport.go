package main

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
	playerQueueLimit = 10
	playerReadLimit  = 256 << 10 // Includes metadata for the entire queue.
	playerRetryMin   = 200 * time.Millisecond
	playerRetryMax   = 5 * time.Second
)

const (
	playerRetryNotice = "Cannot reach Soloist. Reconnecting…"
	playerPairNotice  = "Soloist is not paired."
	playerErrorNotice = "Soloist reported a playback error."
)

type playerTrack struct {
	Title, Artist, Album string
	DurationMS           int64
}

type playerState struct {
	Track      playerTrack
	Queue      []playerTrack
	Status     string
	Device     string
	PositionMS int64
	Volume     int
	Connected  bool
	Notice     string
}

type playerCommand struct {
	Name   string
	Volume int
}

func newPlayerStream(ctx context.Context, dataDir func() (string, error)) (<-chan playerState, chan<- playerCommand) {
	states := make(chan playerState, 1)
	commands := make(chan playerCommand, 8)
	go func() {
		defer close(states)
		publish := func(state playerState) {
			// A slow UI needs the latest full snapshot, not a backlog.
			select {
			case <-states:
			default:
			}
			states <- state
		}
		input := (<-chan playerCommand)(commands)
		for delay := playerRetryMin; ctx.Err() == nil; delay = min(2*delay, playerRetryMax) {
			if playerSession(ctx, dataDir, &input, publish) {
				delay = playerRetryMin
			}
			if ctx.Err() != nil {
				return
			}
			publish(playerState{Notice: playerRetryNotice})
			if !playerRetry(ctx, delay, &input) {
				return
			}
		}
	}()
	return states, commands
}

func playerRetry(ctx context.Context, delay time.Duration, input *<-chan playerCommand) bool {
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
		return "", errSoloistRuntime
	}
	// Reuse setup's guarded marker reads. Like setup, accept only a literal
	// loopback IP with no zone and a nonzero port, never a host name or URL.
	addr, hasAddr, addrErr := readSoloistMarker(filepath.Join(dir, "ws.addr"))
	port, hasPort, portErr := readSoloistMarker(filepath.Join(dir, "ws.port"))
	ip, ipErr := netip.ParseAddr(addr)
	number, numberErr := strconv.ParseUint(port, 10, 16)
	if addrErr != nil || portErr != nil || !hasAddr || !hasPort || ipErr != nil || !ip.IsLoopback() || ip.Zone() != "" || numberErr != nil || number == 0 {
		return "", errSoloistRuntime
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
	PositionMS *int64           `json:"position_ms"`
	Position   *struct {
		MS *int64 `json:"position_ms"`
	} `json:"position"`
	Upcoming []struct {
		Item *playerWireTrack `json:"item"`
	} `json:"upcoming"`
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

func (item *playerWireTrack) track() playerTrack {
	if item == nil {
		return playerTrack{}
	}
	d := item.Decorations
	artists := make([]string, 0, len(d.Creators))
	for _, creator := range d.Creators {
		if name := creator.Entity.name(); name != "" {
			artists = append(artists, name)
		}
	}
	return playerTrack{
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
	probe, cancel := context.WithTimeout(ctx, soloistProbeTimeout)
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

func playerSession(ctx context.Context, dataDir func() (string, error), input *<-chan playerCommand, publish func(playerState)) (authenticated bool) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	frames := make(chan playerFrame)
	go playerRead(ctx, dataDir, frames)

	var state playerState
	var conn *websocket.Conn
	for ctx.Err() == nil {
		select {
		case <-ctx.Done():
			return authenticated
		case command, ok := <-*input:
			if !ok {
				*input = nil
				continue
			}
			if packet, allowed := playerCommandPacket(command); state.Connected && allowed {
				if !playerWrite(ctx, conn, packet) {
					return authenticated
				}
			}
		case frame, ok := <-frames:
			if !ok {
				return authenticated
			}
			conn = frame.conn
			event := frame.event
			switch event.Type {
			case "auth_state":
				if event.LoggedIn == nil {
					return authenticated
				}
				if !*event.LoggedIn {
					state = playerState{Notice: playerPairNotice}
				} else if !state.Connected {
					limit := playerQueueLimit
					if !playerWrite(ctx, conn, playerRequest{Command: "get_state"}) || !playerWrite(ctx, conn, playerRequest{Command: "get_queue", Limit: limit}) {
						return authenticated
					}
					// Drop input buffered before the new session became ready.
					// There is no retry of commands from a failed write either.
					for n := len(*input); n > 0; n-- {
						<-*input
					}
					state.Connected, state.Notice = true, ""
					authenticated = true
				}
				if event.DeviceName != nil {
					state.Device = playerMetadataText(*event.DeviceName)
				}
			case "logout":
				state = playerState{Notice: playerPairNotice}
			case "error":
				state.Notice = playerErrorNotice
			default:
				if !state.Connected || !state.apply(event) {
					continue
				}
			}
			publish(state)
		}
	}
	return authenticated
}

func (state *playerState) apply(event playerEvent) bool {
	updatePosition := func() {
		if event.Position != nil && event.Position.MS != nil {
			state.PositionMS = max(0, *event.Position.MS)
		} else if event.PositionMS != nil {
			state.PositionMS = max(0, *event.PositionMS)
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
		state.PositionMS = 0
		if event.Type == "playback_state" || event.Item == nil {
			state.Status = ""
		}
		updateStatus()
		updatePosition()
		if event.Volume != nil {
			state.Volume = min(100, max(0, *event.Volume))
		}
	case "playback_changed":
		updateStatus()
		updatePosition()
	case "position_sync":
		updatePosition()
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
		state.Queue = make([]playerTrack, 0, min(len(event.Upcoming), playerQueueLimit))
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
	Command string `json:"command"`
	Limit   int    `json:"limit,omitempty"`
	Volume  *int   `json:"volume,omitempty"`
}

func playerCommandPacket(command playerCommand) (playerRequest, bool) {
	packet := playerRequest{Command: command.Name}
	switch command.Name {
	case "play", "pause", "skip_next", "skip_prev":
		return packet, true
	case "set_volume":
		volume := min(100, max(0, command.Volume))
		packet.Volume = &volume
		return packet, true
	default:
		return playerRequest{}, false
	}
}

func playerWrite(ctx context.Context, conn *websocket.Conn, packet playerRequest) bool {
	ctx, cancel := context.WithTimeout(ctx, soloistProbeTimeout)
	defer cancel()
	data, err := json.Marshal(struct {
		Type string `json:"type"`
		playerRequest
	}{Type: "command", playerRequest: packet})
	return err == nil && conn.Write(ctx, websocket.MessageText, data) == nil
}
