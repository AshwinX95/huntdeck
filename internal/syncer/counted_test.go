package syncer

import (
	"math"
	"testing"

	"github.com/mdpsec/huntdeck/internal/platform"
	"github.com/mdpsec/huntdeck/internal/store"
)

func TestCount90d(t *testing.T) {
	i := func(n int) *int { return &n }
	f := func(v float64) *float64 { return &v }
	tests := []struct {
		name          string
		st            platform.ProgramStats
		base          store.SavedTotals
		ok            bool
		reports, days *int
		paid          *float64
	}{
		{name: "first save, 7-day count", st: platform.ProgramStats{TotalReports: i(500), ReportsLast7d: i(14)},
			reports: i(180), days: i(0)},
		{name: "first save, no 7-day count", st: platform.ProgramStats{TotalReports: i(500), BountiesPaid: f(9000)}},
		{name: "a few days, 7-day count fills the rest", st: platform.ProgramStats{TotalReports: i(530), ReportsLast7d: i(14)},
			base: store.SavedTotals{Days: 20, Reports: i(500)}, ok: true, reports: i(170), days: i(20)},
		{name: "under a week, no 7-day count", st: platform.ProgramStats{TotalReports: i(510), BountiesPaid: f(9500)},
			base: store.SavedTotals{Days: 6, Reports: i(500), Paid: f(9000)}, ok: true},
		{name: "a month, scaled", st: platform.ProgramStats{TotalReports: i(560), BountiesPaid: f(10000)},
			base: store.SavedTotals{Days: 30, Reports: i(500), Paid: f(9000)}, ok: true, reports: i(180), paid: f(3000), days: i(30)},
		{name: "90 days, counted", st: platform.ProgramStats{TotalReports: i(700), BountiesPaid: f(10000), ReportsLast7d: i(1)},
			base: store.SavedTotals{Days: 91, Reports: i(500), Paid: f(4000)}, ok: true, reports: i(200), paid: f(6000), days: i(91)},
		{name: "a total that went down counts as none", st: platform.ProgramStats{TotalReports: i(490), BountiesPaid: f(8000)},
			base: store.SavedTotals{Days: 95, Reports: i(500), Paid: f(9000)}, ok: true, reports: i(0), paid: f(0), days: i(95)},
		{name: "scaled past the total is capped at it", st: platform.ProgramStats{TotalReports: i(1340), BountiesPaid: f(219384)},
			base: store.SavedTotals{Days: 7, Reports: i(1237), Paid: f(194544)}, ok: true, reports: i(1324), paid: f(219384), days: i(7)},
		{name: "launched under 90 days ago: the totals, counted", st: platform.ProgramStats{TotalReports: i(1340), BountiesPaid: f(219384)},
			base: store.SavedTotals{Days: 7, Reports: i(1237), Paid: f(194544), Age: f(76)}, ok: true, reports: i(1340), paid: f(219384), days: i(90)},
		{name: "launched under 90 days ago, first save", st: platform.ProgramStats{TotalReports: i(40), ReportsLast7d: i(30)},
			base: store.SavedTotals{Age: f(10)}, reports: i(40), days: i(90)},
		{name: "launched long ago: estimated as usual", st: platform.ProgramStats{TotalReports: i(560), BountiesPaid: f(10000)},
			base: store.SavedTotals{Days: 30, Reports: i(500), Paid: f(9000), Age: f(400)}, ok: true, reports: i(180), paid: f(3000), days: i(30)},
		// 3HK, 2026-11-09: 835 of its 2,159 points in the last 90 days.
		{name: "first save, leaderboard share", st: platform.ProgramStats{TotalReports: i(241), BountiesPaid: f(13243), Points90d: i(835), PointsAll: i(2159)},
			reports: i(93), paid: f(13243 * 835.0 / 2159), days: i(0)},
		{name: "the share beats a 7-day count and scaling", st: platform.ProgramStats{TotalReports: i(560), BountiesPaid: f(10000), ReportsLast7d: i(70), Points90d: i(10), PointsAll: i(100)},
			base: store.SavedTotals{Days: 30, Reports: i(500), Paid: f(9000)}, ok: true, reports: i(56), paid: f(1000), days: i(30)},
		{name: "90 days counted beat the share", st: platform.ProgramStats{TotalReports: i(700), BountiesPaid: f(10000), Points90d: i(1), PointsAll: i(100)},
			base: store.SavedTotals{Days: 91, Reports: i(500), Paid: f(4000)}, ok: true, reports: i(200), paid: f(6000), days: i(91)},
		{name: "no points all time: no share", st: platform.ProgramStats{TotalReports: i(500), BountiesPaid: f(9000), Points90d: i(0), PointsAll: i(0)}},
		{name: "launched under 90 days ago: the totals, not the share", st: platform.ProgramStats{TotalReports: i(40), Points90d: i(5), PointsAll: i(50)},
			base: store.SavedTotals{Age: f(10)}, reports: i(40), days: i(90)},
		{name: "paid not scaled from under a week", st: platform.ProgramStats{TotalReports: i(500), ReportsLast7d: i(7), BountiesPaid: f(100)},
			base: store.SavedTotals{Days: 3, Reports: i(497), Paid: f(50)}, ok: true, reports: i(90), days: i(3)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			st := tt.st
			if !needsCounting(st) {
				t.Fatal("needsCounting is false")
			}
			count90d(&st, tt.base, tt.ok)
			if !eqInt(st.ReportsLast90d, tt.reports) || !eqInt(st.CountedDays, tt.days) {
				t.Errorf("reports %v days %v, want %v %v", show(st.ReportsLast90d), show(st.CountedDays), show(tt.reports), show(tt.days))
			}
			if (st.BountiesPaidLast90d == nil) != (tt.paid == nil) || (tt.paid != nil && *st.BountiesPaidLast90d != *tt.paid) {
				t.Errorf("paid %v, want %v", show(st.BountiesPaidLast90d), show(tt.paid))
			}
		})
	}
	// A program that hides its report count still gets paid in 90 days.
	st := platform.ProgramStats{BountiesPaid: f(50000)}
	count90d(&st, store.SavedTotals{Days: 10, Paid: f(49000)}, true)
	if st.ReportsLast90d != nil || st.BountiesPaidLast90d == nil || *st.BountiesPaidLast90d != 9000 {
		t.Errorf("paid only: reports %v paid %v, want none and 9000", show(st.ReportsLast90d), show(st.BountiesPaidLast90d))
	}
	published := platform.ProgramStats{TotalReports: i(5), ReportsLast90d: i(3)}
	if needsCounting(published) || needsCounting(platform.ProgramStats{}) {
		t.Error("needsCounting is true for published 90-day figures or no totals")
	}
}

// Bugcrowd programs that keep CrowdStream to themselves: paid in 90 days is
// the rise in rewarded reports × the average payout.
func TestCountRewarded90d(t *testing.T) {
	i := func(n int) *int { return &n }
	f := func(v float64) *float64 { return &v }
	tests := []struct {
		name string
		base store.SavedTotals
		ok   bool
		paid *float64
		days int
	}{
		{name: "first save: nothing yet"},
		{name: "under a week: nothing yet", base: store.SavedTotals{Days: 3, Rewarded: i(313)}, ok: true},
		{name: "9 days, scaled", base: store.SavedTotals{Days: 9, Rewarded: i(313)}, ok: true, paid: f(2 * 90.0 / 9 * 2000), days: 9},
		{name: "90 days, counted", base: store.SavedTotals{Days: 90, Rewarded: i(300)}, ok: true, paid: f(15 * 2000), days: 90},
		{name: "launched under 90 days ago: every reward", base: store.SavedTotals{Age: f(30)}, paid: f(315 * 2000), days: 90},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			st := platform.ProgramStats{RewardedReports: i(315), AvgBountyHigh: f(2000)}
			if !needsRewardedCount(st) {
				t.Fatal("needsRewardedCount is false")
			}
			countRewarded90d(&st, tt.base, tt.ok)
			if (st.BountiesPaidLast90d == nil) != (tt.paid == nil) || (tt.paid != nil && math.Abs(*st.BountiesPaidLast90d-*tt.paid) > 1e-6) {
				t.Fatalf("paid %v, want %v", show(st.BountiesPaidLast90d), show(tt.paid))
			}
			if tt.paid != nil && (st.CountedDays == nil || *st.CountedDays != tt.days) {
				t.Errorf("days %v, want %d", show(st.CountedDays), tt.days)
			}
			if tt.paid != nil && (st.RewardedLast90d == nil || math.Abs(*st.RewardedLast90d*2000-*tt.paid) > 1e-6) {
				t.Errorf("rewarded in 90 days %v, want %v", show(st.RewardedLast90d), *tt.paid/2000)
			}
			if st.ReportsLast90d != nil {
				t.Error("rewarded reports aren't reports received")
			}
		})
	}
	if needsRewardedCount(platform.ProgramStats{RewardedReports: i(1), AvgBountyHigh: f(1), BountiesPaid: f(5)}) {
		t.Error("needsRewardedCount with a published total paid")
	}
}

func TestCleanName(t *testing.T) {
	for in, want := range map[string]string{" Esteé  Lauder": "Esteé Lauder", "MoonPay ": "MoonPay", "Acme\tCorp": "Acme Corp", "Acme": "Acme"} {
		if got := cleanName(in); got != want {
			t.Errorf("cleanName(%q) = %q, want %q", in, got, want)
		}
	}
}

func eqInt(a, b *int) bool { return (a == nil) == (b == nil) && (a == nil || *a == *b) }

func show[T any](p *T) any {
	if p == nil {
		return nil
	}
	return *p
}
