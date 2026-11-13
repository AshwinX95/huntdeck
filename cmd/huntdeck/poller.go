package main

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"time"

	"github.com/mdpsec/huntdeck/internal/platform"
	"github.com/mdpsec/huntdeck/internal/scheduler"
	"github.com/mdpsec/huntdeck/internal/store"
	"github.com/mdpsec/huntdeck/internal/syncer"
	"github.com/mdpsec/huntdeck/internal/web"
)

// backgroundPoller starts polls from the web UI/API without blocking the
// request. Polls use the server's context, so shutdown cancels them and
// serve waits for them to record their outcome.
type backgroundPoller struct {
	ctx       context.Context
	log       *slog.Logger
	store     *store.Store
	syncer    *syncer.Syncer
	sched     *scheduler.Schedule
	platforms []configured
	wg        sync.WaitGroup
}

func (b *backgroundPoller) find(platformName string) *configured {
	for i := range b.platforms {
		if b.platforms[i].Name() == platformName {
			return &b.platforms[i]
		}
	}
	return nil
}

func (b *backgroundPoller) PollNow(platformName, mode string) error {
	target := b.find(platformName)
	if target == nil {
		return web.ErrUnknownPlatform
	}
	if ws, ok := target.Platform.(platform.WebSyncer); mode == syncer.ModeWeb && (!ok || !ws.WebSyncConfigured()) {
		return web.ErrUnknownPlatform
	}
	if !b.sched.Enabled(platformName) {
		return web.ErrPlatformDisabled
	}
	// Fast, friendly check; the lease in StartRun is the real guard.
	if running, err := b.store.RunningRun(b.ctx, platformName); err != nil {
		return err
	} else if running != nil {
		return store.ErrRunInProgress
	}
	b.wg.Add(1)
	go func() {
		defer b.wg.Done()
		_, err := b.syncer.Poll(b.ctx, target.Platform, syncer.Options{Mode: mode, Trigger: "manual", Concurrency: target.concurrency})
		if err != nil && b.ctx.Err() == nil {
			b.log.Warn("manual poll did not run", "platform", platformName, "err", err)
		}
	}()
	return nil
}

// How long accepting an invitation may take (a browser sign-in included),
// and how long the request waits for it before saying it's still going.
const (
	acceptTimeout = 5 * time.Minute
	acceptWait    = 40 * time.Second
	// While another refresh of the platform runs, the accepted program's
	// refresh tries again this often, for up to refreshPatience.
	refreshRetry    = 30 * time.Second
	refreshPatience = 2 * time.Hour
)

// AcceptInvite accepts the invitation on the platform, then refreshes that
// one program (listing, scope and stats) in the background. It waits for
// the accept itself up to acceptWait, and returns web.ErrStillAccepting if
// it takes longer (a browser sign-in): it carries on regardless.
func (b *backgroundPoller) AcceptInvite(platformName, handle string) error {
	target := b.find(platformName)
	if target == nil {
		return web.ErrUnknownPlatform
	}
	if !syncer.CanAcceptInvites(target.Platform) {
		return web.ErrCannotAccept
	}
	if !b.sched.Enabled(platformName) {
		return web.ErrPlatformDisabled
	}
	done := make(chan error, 1)
	b.wg.Add(1)
	go func() {
		defer b.wg.Done()
		ctx, cancel := context.WithTimeout(b.ctx, acceptTimeout)
		err := b.syncer.AcceptInvite(ctx, target.Platform, handle)
		cancel()
		done <- err
		if err != nil {
			if b.ctx.Err() == nil {
				b.log.Warn("invitation not accepted", "platform", platformName, "program", handle, "err", err)
			}
			return
		}
		b.refreshProgram(target, handle)
	}()
	select {
	case err := <-done:
		switch {
		case errors.Is(err, store.ErrNotFound):
			return web.ErrNoInvite
		case errors.Is(err, syncer.ErrNoInvite):
			return web.ErrNoInvite
		}
		return err
	case <-time.After(acceptWait):
		return web.ErrStillAccepting
	}
}

// refreshProgram polls just the program and its stats now, beside any
// poll of the platform that's running, waiting only for another refresh
// of the platform to finish.
func (b *backgroundPoller) refreshProgram(target *configured, handle string) {
	giveUp := time.Now().Add(refreshPatience)
	for {
		run, err := b.syncer.RefreshPrograms(b.ctx, target.Platform, []string{handle})
		switch {
		case err == nil:
			b.log.Info("accepted program refreshed", "platform", target.Name(), "program", handle, "status", run.Status)
			return
		case !errors.Is(err, store.ErrRunInProgress) || time.Now().After(giveUp):
			if b.ctx.Err() == nil {
				b.log.Warn("accepted program not refreshed; the next poll will", "platform", target.Name(), "program", handle, "err", err)
			}
			return
		}
		select {
		case <-b.ctx.Done():
			return
		case <-time.After(refreshRetry):
		}
	}
}
