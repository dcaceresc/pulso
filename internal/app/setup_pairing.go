package app

import (
	"context"
	"encoding/json"
	"errors"
	"time"
)

// watchPairing owns one socket until an auth_state confirms login, the socket
// fails, or the pairing context is canceled. It never issues Soloist commands.
func (s *localSetup) watchPairing(ctx context.Context, report func(string) bool) error {
	dir, err := s.dataDir()
	if err != nil {
		return err
	}
	endpoint, err := soloistRuntimeEndpoint(dir)
	if err != nil || endpoint == "" {
		return errSoloistRuntime
	}
	conn, event, err := openSoloistAuth(ctx, endpoint)
	if err != nil {
		return err
	}
	defer conn.CloseNow()

	var name string
	waiting := false
	for {
		switch event.Type {
		case "auth_state", "logout", "logged_out":
			if event.Type == "auth_state" {
				if event.LoggedIn == nil {
					return errSoloistRuntime
				}
				if *event.LoggedIn {
					return nil
				}
			}
			var advertised string
			if json.Unmarshal(event.DeviceName, &advertised) == nil {
				advertised = playerText(advertised)
			}
			if !waiting || (advertised != "" && advertised != name) {
				name, waiting = advertised, true
				if !report(name) {
					return ctx.Err()
				}
			}
		}
		event, err = readSoloistAuthEvent(ctx, conn)
		if err != nil {
			return err
		}
	}
}

const (
	setupPairingRetryMin = 500 * time.Millisecond
	setupPairingRetryMax = 4 * time.Second
)

type setupPairingStatus uint8

const (
	setupPairingWaiting setupPairingStatus = iota
	setupPairingReconnecting
	setupPairingDaemonStopped
	setupPairingReady
)

// A session pointer is also the generation token on every watcher message.
type setupPairingSession struct {
	cancel  context.CancelFunc
	updates chan setupPairingUpdate
	done    chan struct{}
}

type setupPairingUpdate struct {
	session *setupPairingSession
	status  setupPairingStatus
	device  string
}

func (s *setupPairingSession) run(ctx context.Context, previous <-chan struct{}, service setupService, recovering bool) {
	defer close(s.done)
	defer close(s.updates)
	if previous != nil {
		select {
		case <-previous:
		case <-ctx.Done():
			// Keep the completion chain intact even if this session was
			// canceled before its predecessor finished canceling.
			<-previous
			return
		}
	}
	publish := func(status setupPairingStatus, device string) bool {
		select {
		case s.updates <- setupPairingUpdate{s, status, device}:
			return true
		case <-ctx.Done():
			return false
		}
	}
	delay := setupPairingRetryMin
	for ctx.Err() == nil {
		if !recovering {
			err := service.watch(ctx, func(device string) bool {
				return publish(setupPairingWaiting, device)
			})
			if ctx.Err() != nil {
				return
			}
			if err == nil {
				publish(setupPairingReady, "")
				return
			}
			if !publish(setupPairingReconnecting, "") {
				return
			}
		}
		if !waitSetupPairingRetry(ctx, delay) {
			return
		}
		delay = min(delay*2, setupPairingRetryMax)
		state, err := service.inspect(ctx)
		if ctx.Err() != nil {
			return
		}
		if setupDaemonStopped(state, err) {
			publish(setupPairingDaemonStopped, "")
			return
		}
		if err == nil && state == setupReady {
			publish(setupPairingReady, "")
			return
		}
		// Keep inspecting when the daemon is present but not responding
		// correctly. Only open the watcher after a successful fresh probe.
		recovering = err != nil
	}
}

func waitSetupPairingRetry(ctx context.Context, delay time.Duration) bool {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return ctx.Err() == nil
	}
}

func setupDaemonStopped(state setupState, err error) bool {
	return errors.Is(err, errSoloistNotListening) || err == nil && (state == setupNeedsKey || state == setupMissing)
}
