package ui

import (
	"context"
	"errors"
	"time"

	"github.com/dcaceresc/pulso/internal/soloist"
)

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

func (s *setupPairingSession) run(ctx context.Context, previous <-chan struct{}, service SetupService, recovering bool) {
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
			err := service.Watch(ctx, func(device string) bool {
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
		state, err := service.Inspect(ctx)
		if ctx.Err() != nil {
			return
		}
		if setupDaemonStopped(state, err) {
			publish(setupPairingDaemonStopped, "")
			return
		}
		if err == nil && state == SetupReady {
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

func setupDaemonStopped(state SetupState, err error) bool {
	return errors.Is(err, soloist.ErrNotListening) || err == nil && (state == SetupNeedsKey || state == SetupMissing)
}
