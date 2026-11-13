package web

import (
	"math"
	"testing"
	"time"

	"github.com/mdpsec/huntdeck/internal/store"
)

func TestBackfillCounted(t *testing.T) {
	day := func(n int) time.Time { return time.Date(2026, 10, 28+n, 10, 0, 0, 0, time.UTC) }
	totals := []store.DayValue{{At: day(0), Value: 1000}, {At: day(1), Value: 1002}, {At: day(2), Value: 1004},
		{At: day(3), Value: 1006}, {At: day(4), Value: 1010}, {At: day(5), Value: 1012}}
	stored := []store.DayValue{{At: day(5).Add(3 * time.Hour), Value: 210}}
	old := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	young := day(-10)
	tests := []struct {
		name    string
		stored  []store.DayValue
		started *time.Time
		want    []float64
	}{
		// From 3 days: 6 over 3 days × 30, 10 over 4 × 22.5; day 5 is stored.
		{"before the first figure", stored, &old, []float64{180, 225}},
		{"nothing stored yet", nil, nil, []float64{180, 225, 216}},
		// Launched 10 days before: every report is in the window.
		{"a young program", stored, &young, []float64{1002, 1004, 1006, 1010}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := backfillCounted(totals, tt.stored, tt.started)
			if len(got) != len(tt.want) {
				t.Fatalf("got %+v, want %v", got, tt.want)
			}
			for i, w := range tt.want {
				if math.Abs(got[i].Value-w) > .01 {
					t.Errorf("point %d = %.2f, want %.2f", i, got[i].Value, w)
				}
			}
		})
	}
	if got := backfillCounted(totals[:1], nil, nil); got != nil {
		t.Errorf("one total backfilled %v", got)
	}
}

func TestValidDays(t *testing.T) {
	day := func(n int) time.Time { return time.Date(2026, 11, 1+n, 6, 0, 0, 0, time.UTC) }
	received := []store.DayValue{{At: day(0), Value: 100}, {At: day(1), Value: 120}, {At: day(2), Value: 110}}
	total := []store.DayValue{{At: day(1).Add(time.Hour), Value: 1000}, {At: day(2), Value: 1000}}
	accepted := []store.DayValue{{At: day(1).Add(time.Hour), Value: 500}, {At: day(2), Value: 400}}
	got := validDays(received, accepted, total)
	// Day 0 takes the earliest share (½); day 2 its own (⅖).
	want := []float64{50, 60, 44}
	if len(got) != len(want) {
		t.Fatalf("got %+v", got)
	}
	for i, w := range want {
		if got[i].Value != w {
			t.Errorf("day %d = %v, want %v", i, got[i].Value, w)
		}
	}
	if validDays(received, nil, total) != nil {
		t.Error("a line without accepted totals")
	}
}
