package store

import (
	"context"
	"errors"
	"testing"
	"time"
)

// A platform's polls and its single-program refreshes hold separate
// leases: a refresh runs beside a poll, two of a kind take turns, and a
// stale run is reclaimed only by its own kind.
func TestRefreshLease(t *testing.T) {
	now := time.Date(2026, 11, 10, 12, 0, 0, 0, time.UTC)
	s := newTestStore(t, func() time.Time { return now })
	ctx := context.Background()
	const stale = 2 * time.Hour

	poll, err := s.StartRun(ctx, "bugcrowd", "full", "schedule", stale)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.StartRun(ctx, "bugcrowd", "quick", "manual", stale); !errors.Is(err, ErrRunInProgress) {
		t.Errorf("second poll: err = %v, want ErrRunInProgress", err)
	}
	refresh, err := s.StartRun(ctx, "bugcrowd", "full", TriggerProgram, stale)
	if err != nil {
		t.Fatalf("refresh beside a poll: %v", err)
	}
	if _, err := s.StartRun(ctx, "bugcrowd", "full", TriggerProgram, stale); !errors.Is(err, ErrRunInProgress) {
		t.Errorf("second refresh: err = %v, want ErrRunInProgress", err)
	}
	if _, err := s.StartRun(ctx, "hackerone", "full", TriggerProgram, stale); err != nil {
		t.Errorf("another platform's refresh: %v", err)
	}
	if r, err := s.RunningRun(ctx, "bugcrowd"); err != nil || r == nil || r.ID != poll {
		t.Errorf("RunningRun = %+v, %v; want the poll %d, not the refresh", r, err, poll)
	}

	// Hours later a new refresh reclaims the stale refresh, not the poll.
	now = now.Add(3 * time.Hour)
	if _, err := s.StartRun(ctx, "bugcrowd", "full", TriggerProgram, stale); err != nil {
		t.Fatalf("stale refresh not reclaimed: %v", err)
	}
	runs, err := s.queryRuns(ctx, `WHERE id IN (?, ?) ORDER BY id`, poll, refresh)
	if err != nil || len(runs) != 2 {
		t.Fatal(runs, err)
	}
	if runs[0].Status != RunRunning || runs[1].Status != RunFailed {
		t.Errorf("after reclaiming: poll %s, refresh %s; want running, failed", runs[0].Status, runs[1].Status)
	}
}
