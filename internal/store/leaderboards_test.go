package store

import (
	"context"
	"fmt"
	"slices"
	"testing"
	"time"

	"github.com/mdpsec/huntdeck/internal/platform"
)

// Past monthly leaderboards are read for the finished months of the last
// year from a program's launch, those not saved yet; and they estimate
// valid reports per month up to the first saved accepted total: by the
// program's points where it shows them, else by its researchers.
func TestLeaderboardEstimates(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 10, 15, 12, 0, 0, 0, time.UTC)
	s := newTestStore(t, func() time.Time { return now })
	n := func(v int) *int { return &v }
	month := func(y int, m time.Month) time.Time { return time.Date(y, m, 1, 0, 0, 0, 0, time.UTC) }
	launched := func(t time.Time) *time.Time { return &t }
	ptr := func(v float64) *float64 { return &v }
	ids := map[string]int64{}
	err := s.Write(ctx, func(tx *Tx) error {
		for _, p := range []struct {
			handle  string
			private bool
			started time.Time
			stats   platform.ProgramStats
		}{
			// 100 accepted of 1,000 points: 0.1 a point; €1,000 paid.
			{"shows", false, month(2024, 1), platform.ProgramStats{AcceptedReports: n(100), PointsAll: n(1000), Points90d: n(500), Hunters90d: n(5),
				BountiesPaid: ptr(1000.0), BountiesPaidLast90d: ptr(300.0)}},
			{"hides", true, month(2024, 1), platform.ProgramStats{AcceptedReports: n(50), Hunters90d: n(3), BountiesPaid: ptr(500.0),
				BountiesPaidLast90d: ptr(0.0), CountedDays: n(7)}},
			{"new", false, time.Date(2026, 8, 20, 0, 0, 0, 0, time.UTC), platform.ProgramStats{Hunters90d: n(1)}},
			{"unread", false, month(2024, 1), platform.ProgramStats{AcceptedReports: n(7)}},
		} {
			id, err := tx.UpsertProgram(ctx, "intigriti", platform.Program{Handle: p.handle, Name: p.handle, URL: "https://example.com/" + p.handle,
				State: "open", OffersBounty: true, Currency: "EUR", Private: p.private, StartedAt: launched(p.started)})
			if err != nil {
				return err
			}
			ids[p.handle] = id
			if err := tx.RecordProgramStats(ctx, id, p.stats); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	// November: 3 more accepted and €300 more paid.
	now = time.Date(2026, 11, 5, 12, 0, 0, 0, time.UTC)
	err = s.Write(ctx, func(tx *Tx) error {
		return tx.RecordProgramStats(ctx, ids["shows"], platform.ProgramStats{AcceptedReports: n(103), PointsAll: n(1030), Points90d: n(500),
			Hunters90d: n(5), BountiesPaid: ptr(1300.0), BountiesPaidLast90d: ptr(600.0)})
	})
	if err != nil {
		t.Fatal(err)
	}

	now = time.Date(2026, 11, 10, 12, 0, 0, 0, time.UTC)
	err = s.Write(ctx, func(tx *Tx) error {
		if err := tx.SaveLeaderboardMonths(ctx, ids["shows"], []platform.LeaderboardMonth{
			{Month: month(2026, 7), Points: n(100), Hunters: 2}, {Month: month(2026, 8), Points: n(100), Hunters: 2},
			{Month: month(2026, 9), Points: n(200), Hunters: 4}, {Month: month(2026, 10), Points: n(300), Hunters: 6}}); err != nil {
			return err
		}
		return tx.SaveLeaderboardMonths(ctx, ids["hides"], []platform.LeaderboardMonth{{Month: month(2026, 9), Hunters: 5}})
	})
	if err != nil {
		t.Fatal(err)
	}

	want, got, err := s.LeaderboardMonthsDue(ctx, "intigriti", 100)
	if err != nil {
		t.Fatal(err)
	}
	due := map[string]int{}
	for _, w := range want {
		due[w.Handle] = len(w.Months)
		if got[w.Handle] != ids[w.Handle] {
			t.Errorf("%s: id %d, want %d", w.Handle, got[w.Handle], ids[w.Handle])
		}
	}
	// hides (private) first; new from August, the month it launched.
	if len(want) != 3 || want[0].Handle != "hides" || due["hides"] != 11 || due["shows"] != 8 || due["new"] != 3 ||
		!want[2].Months[0].Equal(month(2026, 8)) {
		t.Errorf("due %v, first %s", due, want[0].Handle)
	}
	if want, _, _ := s.LeaderboardMonthsDue(ctx, "intigriti", 5); len(want) != 1 {
		t.Errorf("a limit of 5 months gave %d programs, want the first only", len(want))
	}

	ov, err := s.PlatformOverviews(ctx, now, month(2025, 12), 12, "intigriti", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(ov) != 1 {
		t.Fatalf("overviews: %d", len(ov))
	}
	// The program's own rate (1030 points now, 103 accepted) is about
	// 0.1. September: shows 200 × 0.1 = 20, hides 5 researchers × the
	// platform's 5 reports each (70 from 14 researchers) = 25. October:
	// shows's 30 for the 14 days before its first total, of 31. November:
	// the 3 counted.
	jul, sep, oct, nov := ov[0].Months[7], ov[0].Months[9], ov[0].Months[10], ov[0].Months[11]
	if jul.Valid != 10 || sep.Valid != 45 || !sep.Estimated || oct.Valid != 14 || !oct.Estimated || ov[0].Months[6].Estimated || nov.Valid != 3 {
		t.Errorf("July %d, September %d (estimated %v), October %d (%v), November %d", jul.Valid, sep.Valid, sep.Estimated, oct.Valid, oct.Estimated, nov.Valid)
	}
	if v := ov[0].ValidSince; v == nil || !v.Equal(month(2026, 7)) {
		t.Errorf("valid since %v, want July", v)
	}
	// Bounties, in euros at €1,300 / 103 accepted a report: September's
	// 20 estimated (and hides's 25 at its €10 a report), October's 14 days
	// of 30, November's €300 rise counted.
	per := 1300.0 / 103
	near := func(a, b float64) bool { return a > b-0.5 && a < b+0.5 }
	if !near(sep.BountiesOther["EUR"], 20*per+25*10) || !sep.BountiesEstimated || sep.Bounties != 0 ||
		!near(oct.BountiesOther["EUR"], 30*per*14.5/31) || !near(nov.BountiesOther["EUR"], 300) || nov.BountiesEstimated {
		t.Errorf("bounties: September %v (%v), October %v, November %v (%v)", sep.BountiesOther, sep.BountiesEstimated,
			oct.BountiesOther, nov.BountiesOther, nov.BountiesEstimated)
	}

	// The 90-day paid line: on October 1, July to September's 40 × €12.62,
	// then the saved days (sound: from the leaderboards' share), in
	// euros. hides's saved day is too rough to join (no points share,
	// fewer than 90 days): it stays, without estimates before it.
	days, err := s.PlatformDays(ctx, DayPaid90d, month(2026, 6), "intigriti", "")
	if err != nil {
		t.Fatal(err)
	}
	var line []string
	for _, d := range days["intigriti"] {
		line = append(line, fmt.Sprintf("%s %.0f %s", d.Day.Format("Jan 2"), d.Value, d.Currency))
	}
	if want := []string{"Oct 15 300 EUR", "Oct 15 0 EUR", "Nov 5 600 EUR", fmt.Sprintf("Oct 1 %.0f EUR", 40*per)}; !slices.Equal(line, want) {
		t.Errorf("paid days %q, want %q", line, want)
	}
}
