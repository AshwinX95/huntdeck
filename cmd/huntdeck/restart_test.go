package main

import (
	"context"
	"log/slog"
	"path/filepath"
	"testing"
	"time"

	"github.com/mdpsec/huntdeck/internal/db"
	"github.com/mdpsec/huntdeck/internal/store"
)

// A restart waits for this server's running polls (not a crashed
// process's leftover row), unless it's asked for now.
func TestRestartWaitsForRunningPolls(t *testing.T) {
	ctx := context.Background()
	d, err := db.Open(ctx, filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = d.Close() })
	migs, _ := db.Migrations()
	log := slog.New(slog.DiscardHandler)
	if _, err := d.Migrate(ctx, migs, t.TempDir(), log); err != nil {
		t.Fatal(err)
	}
	clock := time.Date(2026, 11, 9, 12, 0, 0, 0, time.UTC)
	st := store.New(d, func() time.Time { return clock })

	// A crashed process's poll, still "running".
	if _, err := st.StartRun(ctx, "bugcrowd", "full", "schedule", time.Hour); err != nil {
		t.Fatal(err)
	}
	clock = clock.Add(time.Minute)
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	r := newRestarter(runCtx, cancel, log, st)
	r.every, r.grace = 10*time.Millisecond, 10*time.Millisecond
	clock = clock.Add(time.Minute)
	id, err := st.StartRun(ctx, "hackerone", "quick", "schedule", time.Hour)
	if err != nil {
		t.Fatal(err)
	}

	if pending, _ := r.Pending(ctx); pending {
		t.Fatal("pending before any restart was asked for")
	}
	r.Restart(false)
	pending, waiting := r.Pending(ctx)
	if !pending || len(waiting) != 1 || waiting[0].Platform != "hackerone" {
		t.Fatalf("pending %v, waiting for %+v; want hackerone's poll only", pending, waiting)
	}
	time.Sleep(50 * time.Millisecond)
	if runCtx.Err() != nil {
		t.Fatal("restarted during a running poll")
	}
	if err := st.FinishRun(ctx, store.Run{ID: id, Status: store.RunOK}); err != nil {
		t.Fatal(err)
	}
	select {
	case <-runCtx.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("didn't restart once the poll finished")
	}
	if !r.requested() {
		t.Error("the restart isn't recorded as asked for")
	}

	// Now: at once, polls or not.
	ctx2, cancel2 := context.WithCancel(ctx)
	defer cancel2()
	r2 := newRestarter(ctx2, cancel2, log, st)
	r2.grace = 10 * time.Millisecond
	if _, err := st.StartRun(ctx, "intigriti", "full", "schedule", time.Hour); err != nil {
		t.Fatal(err)
	}
	r2.Restart(true)
	select {
	case <-ctx2.Done():
	case <-time.After(5 * time.Second):
		t.Error("Restart(true) didn't restart without waiting for the poll")
	}
}
