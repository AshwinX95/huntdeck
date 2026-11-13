package main

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/mdpsec/huntdeck/internal/store"
)

// restartPoll is how often a waiting restart looks for running polls.
const restartPoll = 5 * time.Second

// restartGrace is how long a restart waits first, so the page the save
// redirects to loads (and its script waits for the restart) rather than
// finding the server gone.
const restartGrace = 1500 * time.Millisecond

// restarter restarts the server in-process (serve builds everything again,
// with the settings saved meanwhile) once no poll is running, so a restart
// never cuts one short, or at once on request.
type restarter struct {
	ctx   context.Context // the server's: a restart cancels it
	stop  context.CancelFunc
	log   *slog.Logger
	store *store.Store
	since time.Time // when this server started: older running rows are a crashed process's
	every time.Duration
	grace time.Duration

	mu      sync.Mutex
	pending bool
}

func newRestarter(ctx context.Context, stop context.CancelFunc, log *slog.Logger, st *store.Store) *restarter {
	return &restarter{ctx: ctx, stop: stop, log: log, store: st, since: st.Now(), every: restartPoll, grace: restartGrace}
}

// Restart asks for a restart: once no poll is running, or now.
func (r *restarter) Restart(now bool) {
	r.mu.Lock()
	first := !r.pending
	r.pending = true
	r.mu.Unlock()
	if now {
		r.log.Info("restarting now to apply saved settings")
		go func() {
			if r.pause() {
				r.stop()
			}
		}()
		return
	}
	if first {
		go r.wait()
	}
}

// pause waits out the grace period; false if the server stopped meanwhile.
func (r *restarter) pause() bool {
	select {
	case <-r.ctx.Done():
		return false
	case <-time.After(r.grace):
		return true
	}
}

// Pending reports whether a restart is waiting, and for which polls.
func (r *restarter) Pending(ctx context.Context) (bool, []store.Run) {
	r.mu.Lock()
	pending := r.pending
	r.mu.Unlock()
	if !pending {
		return false, nil
	}
	runs, err := r.store.RunningSince(ctx, r.since)
	if err != nil {
		return true, nil
	}
	return true, runs
}

// requested reports whether a restart was asked for.
func (r *restarter) requested() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.pending
}

func (r *restarter) wait() {
	if !r.pause() {
		return
	}
	logged := false
	for {
		runs, err := r.store.RunningSince(r.ctx, r.since)
		switch {
		case r.ctx.Err() != nil:
			return
		case err != nil:
			r.log.Warn("restart: cannot check for running polls", "err", err)
		case len(runs) == 0:
			r.log.Info("restarting to apply saved settings")
			r.stop()
			return
		case !logged:
			r.log.Info("restart to apply saved settings waits for running polls", "polls", len(runs))
			logged = true
		}
		select {
		case <-r.ctx.Done():
			return
		case <-time.After(r.every):
		}
	}
}
