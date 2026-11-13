package store

import (
	"context"
	"testing"
	"time"

	"github.com/mdpsec/huntdeck/internal/platform"
)

// The 90-day counts start from the totals saved 90 days ago, else the
// earliest saved; today's snapshot and ones without totals don't count.
func TestTotalsBaseline(t *testing.T) {
	ctx := context.Background()
	start := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	now := start
	s := newTestStore(t, func() time.Time { return now })
	i := func(n int) *int { return &n }
	f := func(v float64) *float64 { return &v }
	var id int64
	base := func() (SavedTotals, bool) {
		t.Helper()
		var b SavedTotals
		var ok bool
		err := s.Write(ctx, func(tx *Tx) error {
			var err error
			b, ok, err = tx.TotalsBaseline(ctx, id, 90*24*time.Hour)
			return err
		})
		if err != nil {
			t.Fatal(err)
		}
		return b, ok
	}
	record := func(at time.Time, st platform.ProgramStats) {
		t.Helper()
		now = at
		if err := s.Write(ctx, func(tx *Tx) error { return tx.RecordProgramStats(ctx, id, st) }); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.Write(ctx, func(tx *Tx) error {
		var err error
		id, err = tx.UpsertProgram(ctx, "fake", platform.Program{Handle: "acme", Name: "Acme", URL: "https://example.com/acme", State: "open"})
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if _, ok := base(); ok {
		t.Fatal("a baseline before anything was saved")
	}
	record(start, platform.ProgramStats{ReportsLast90d: i(5)}) // no totals: not a baseline
	record(start.Add(24*time.Hour), platform.ProgramStats{TotalReports: i(100), BountiesPaid: f(1000)})
	if _, ok := base(); ok {
		t.Fatal("today's snapshot is a baseline")
	}
	record(start.Add(2*24*time.Hour), platform.ProgramStats{TotalReports: i(110)})
	now = start.Add(30 * 24 * time.Hour)
	if b, ok := base(); !ok || b.Reports == nil || *b.Reports != 100 || b.Paid == nil || *b.Paid != 1000 || b.Days != 29 {
		t.Errorf("a month in: %+v %v, want the earliest totals (100, 29 days)", b, ok)
	}
	now = start.Add(92*24*time.Hour + time.Hour)
	if b, ok := base(); !ok || b.Reports == nil || *b.Reports != 110 || b.Paid != nil || b.Days < 90 || b.Days > 91 {
		t.Errorf("92 days in: %+v %v, want the latest totals 90 days back (110)", b, ok)
	}
	if b, _ := base(); b.Age != nil {
		t.Errorf("age %v for a program without a launch date", *b.Age)
	}

	// The program's age comes with it, even before any totals are saved.
	launched := start.Add(-10 * 24 * time.Hour)
	if err := s.Write(ctx, func(tx *Tx) error {
		var err error
		id, err = tx.UpsertProgram(ctx, "fake", platform.Program{Handle: "young", Name: "Young", URL: "https://example.com/young", State: "open",
			StartedAt: &launched})
		return err
	}); err != nil {
		t.Fatal(err)
	}
	now = start
	if b, ok := base(); ok || b.Age == nil || *b.Age != 10 {
		t.Errorf("young program: %+v %v, want no totals and age 10", b, ok)
	}
}
