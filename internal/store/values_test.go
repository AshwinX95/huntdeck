package store

import (
	"context"
	"testing"
	"time"

	"github.com/mdpsec/huntdeck/internal/platform"
)

// A tracked value's time moves only when the value does: a poll that reads
// the same figure again keeps it, the poll that sees it change moves it,
// and the program's updated_at and the since filter follow.
func TestTrackValuesMoveOnlyOnChange(t *testing.T) {
	ctx := context.Background()
	day := func(d, h int) time.Time { return time.Date(2026, 11, d, h, 0, 0, 0, time.UTC) }
	now := day(1, 9)
	s := newTestStore(t, func() time.Time { return now })
	ptr := func(v int) *int { return &v }
	fptr := func(v float64) *float64 { return &v }

	var id int64
	stats := func(received int, avg float64) {
		t.Helper()
		err := s.Write(ctx, func(tx *Tx) error {
			var err error
			if id, err = tx.UpsertProgram(ctx, "fake", platform.Program{Handle: "acme", Name: "acme", URL: "https://example.com/acme", State: "open"}); err != nil {
				return err
			}
			return tx.RecordProgramStats(ctx, id, platform.ProgramStats{Currency: "USD", ReportsLast90d: ptr(received), AvgBountyHigh: fptr(avg)})
		})
		if err != nil {
			t.Fatal(err)
		}
		if err := s.TrackValues(ctx); err != nil {
			t.Fatal(err)
		}
	}
	times := func() ProgramTimes {
		t.Helper()
		got, err := s.ProgramTimesOf(ctx, []int64{id})
		if err != nil {
			t.Fatal(err)
		}
		return got[id]
	}
	at := func(pt ProgramTimes, field string) string {
		if v := pt.Fields[field]; v != nil {
			return v.Format(time.RFC3339)
		}
		return "unknown"
	}
	changedSince := func(since time.Time) int {
		t.Helper()
		items, _, err := s.Programs(ctx, ProgramFilter{ChangedSince: since})
		if err != nil {
			t.Fatal(err)
		}
		return len(items)
	}

	steps := []struct {
		name               string
		now                time.Time
		received           int
		avg                float64
		wantReceived       string
		wantAvg            string
		wantUpdated        string
		wantSynced         string
		sinceDay2, sinceD3 int
	}{
		{"first poll", day(1, 9), 10, 500, "2026-11-01T09:00:00Z", "2026-11-01T09:00:00Z", "2026-11-01T09:00:00Z", "2026-11-01T09:00:00Z", 0, 0},
		{"same figures next day", day(2, 9), 10, 500, "2026-11-01T09:00:00Z", "2026-11-01T09:00:00Z", "2026-11-01T09:00:00Z", "2026-11-02T09:00:00Z", 0, 0},
		{"received changes", day(3, 9), 12, 500, "2026-11-03T09:00:00Z", "2026-11-01T09:00:00Z", "2026-11-03T09:00:00Z", "2026-11-03T09:00:00Z", 1, 0},
		{"read again the same day", day(3, 15), 12, 500, "2026-11-03T09:00:00Z", "2026-11-01T09:00:00Z", "2026-11-03T09:00:00Z", "2026-11-03T15:00:00Z", 1, 0},
		{"average changes", day(4, 9), 12, 650.004, "2026-11-03T09:00:00Z", "2026-11-04T09:00:00Z", "2026-11-04T09:00:00Z", "2026-11-04T09:00:00Z", 1, 1},
		{"change under a cent", day(5, 9), 12, 650.001, "2026-11-03T09:00:00Z", "2026-11-04T09:00:00Z", "2026-11-04T09:00:00Z", "2026-11-05T09:00:00Z", 1, 1},
	}
	for _, st := range steps {
		now = st.now
		stats(st.received, st.avg)
		pt := times()
		if got := at(pt, ValReceived90d); got != st.wantReceived {
			t.Errorf("%s: reports_received_90d changed at %s, want %s", st.name, got, st.wantReceived)
		}
		if got := at(pt, ValAvgBountyHigh); got != st.wantAvg {
			t.Errorf("%s: average_bounty_high changed at %s, want %s", st.name, got, st.wantAvg)
		}
		if got := pt.UpdatedAt.Format(time.RFC3339); got != st.wantUpdated {
			t.Errorf("%s: updated_at %s, want %s", st.name, got, st.wantUpdated)
		}
		if pt.LastSyncedAt == nil || pt.LastSyncedAt.Format(time.RFC3339) != st.wantSynced {
			t.Errorf("%s: last_synced_at %v, want %s", st.name, pt.LastSyncedAt, st.wantSynced)
		}
		if n := changedSince(day(2, 12)); n != st.sinceDay2 {
			t.Errorf("%s: since day 2 lists %d programs, want %d", st.name, n, st.sinceDay2)
		}
		if n := changedSince(day(3, 12)); n != st.sinceD3 {
			t.Errorf("%s: since day 3 lists %d programs, want %d", st.name, n, st.sinceD3)
		}
	}
}

// The first tracking dates what history can: a stats figure from the first
// daily snapshot that has had it since, scope counts from their entries.
// What it can't date is left unknown, and a program first seen once
// tracking has begun has its values dated from then.
func TestTrackValuesBackfill(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 11, 1, 9, 0, 0, 0, time.UTC)
	s := newTestStore(t, func() time.Time { return now })
	ptr := func(v int) *int { return &v }
	var acme int64
	poll := func(handle string, received int) int64 {
		t.Helper()
		var id int64
		err := s.Write(ctx, func(tx *Tx) error {
			var err error
			if id, err = tx.UpsertProgram(ctx, "fake", platform.Program{Handle: handle, Name: handle, URL: "https://example.com/" + handle, State: "open"}); err != nil {
				return err
			}
			return tx.RecordProgramStats(ctx, id, platform.ProgramStats{ReportsLast90d: ptr(received)})
		})
		if err != nil {
			t.Fatal(err)
		}
		return id
	}
	for d, n := range []int{5, 7, 7, 7} { // days 1–4, before tracking existed
		now = time.Date(2026, 11, 1+d, 9, 0, 0, 0, time.UTC)
		acme = poll("acme", n)
	}
	if err := s.TrackValues(ctx); err != nil {
		t.Fatal(err)
	}
	got, err := s.ProgramTimesOf(ctx, []int64{acme})
	if err != nil {
		t.Fatal(err)
	}
	if v := got[acme].Fields[ValReceived90d]; v == nil || !v.Equal(time.Date(2026, 11, 2, 9, 0, 0, 0, time.UTC)) {
		t.Errorf("reports_received_90d dated %v, want 2026-11-02 09:00 (the first snapshot with 7)", v)
	}
	if v := got[acme].Fields[ValResolved90d]; v != nil {
		t.Errorf("resolved_90d dated %v; history can't date it", v)
	}

	now = time.Date(2026, 11, 6, 9, 0, 0, 0, time.UTC)
	globex := poll("globex", 3)
	if err := s.TrackValues(ctx); err != nil {
		t.Fatal(err)
	}
	if got, err = s.ProgramTimesOf(ctx, []int64{globex}); err != nil {
		t.Fatal(err)
	}
	if v := got[globex].Fields[ValResolved90d]; v == nil || !v.Equal(now) {
		t.Errorf("a new program's resolved_90d dated %v, want %v (when it appeared)", v, now)
	}
}
