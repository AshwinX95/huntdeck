package store

import (
	"context"
	"testing"
	"time"

	"github.com/mdpsec/huntdeck/internal/platform"
)

// A program that hides its bounty amounts has its bounty-awarded reports
// counted at its average bounty, marked estimated; one that shows amounts
// keeps only what it shows.
func TestHiddenBountiesAreEstimated(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 11, 10, 12, 0, 0, 0, time.UTC)
	s := newTestStore(t, func() time.Time { return now })
	low, high, shown := 400.0, 600.0, 1000.0
	ids := map[string]int64{}
	err := s.Write(ctx, func(tx *Tx) error {
		for _, h := range []string{"hidden", "shown"} {
			id, err := tx.UpsertProgram(ctx, "hackerone", platform.Program{Handle: h, Name: h, URL: "https://example.com/" + h,
				State: "open", OffersBounty: true, Currency: "USD", Private: h == "hidden"})
			if err != nil {
				return err
			}
			ids[h] = id
			if err := tx.RecordProgramStats(ctx, id, platform.ProgramStats{AvgBountyLow: &low, AvgBountyHigh: &high}); err != nil {
				return err
			}
			for i, action := range []string{platform.ActionBounty, platform.ActionBounty, platform.ActionResolved} {
				r := platform.Resolution{ID: h + string(rune('1'+i)), ActivityAt: now.Add(-time.Duration(1+i) * 24 * time.Hour), Action: action}
				if h == "shown" && i == 0 {
					r.Bounty = &shown
				}
				if _, err := tx.SaveResolution(ctx, id, r); err != nil {
					return err
				}
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	hidden, err := s.ProgramResolutions(ctx, ids["hidden"], now, 12)
	if err != nil {
		t.Fatal(err)
	}
	if hidden.BountyAverage == nil || *hidden.BountyAverage != 500 || hidden.Bounties90d != 1000 || hidden.Rewarded90d != 2 {
		t.Errorf("hidden: average %v, 90 days %v from %d bounties; want 500, 1000 from 2", hidden.BountyAverage, hidden.Bounties90d, hidden.Rewarded90d)
	}
	if m := hidden.Months[len(hidden.Months)-1]; m.Bounties != 1000 || m.Rewarded != 2 {
		t.Errorf("hidden's month: %v from %d", m.Bounties, m.Rewarded)
	}
	shownSum, err := s.ProgramResolutions(ctx, ids["shown"], now, 12)
	if err != nil {
		t.Fatal(err)
	}
	if shownSum.BountyAverage != nil || shownSum.Bounties90d != 1000 {
		t.Errorf("shown: average %v, 90 days %v; want no estimate, the 1000 shown", shownSum.BountyAverage, shownSum.Bounties90d)
	}

	start := time.Date(2025, 12, 1, 0, 0, 0, 0, time.UTC)
	ov, err := s.PlatformOverviews(ctx, now, start, 12, "hackerone", "private")
	if err != nil {
		t.Fatal(err)
	}
	if len(ov) != 1 {
		t.Fatalf("overviews: %d", len(ov))
	}
	if m := ov[0].Months[11]; m.Bounties != 1000 || !m.BountiesEstimated {
		t.Errorf("private Stats month: %v, estimated %v; want 1000, estimated", m.Bounties, m.BountiesEstimated)
	}
	ov, _ = s.PlatformOverviews(ctx, now, start, 12, "hackerone", "public")
	if m := ov[0].Months[11]; m.Bounties != 1000 || m.BountiesEstimated {
		t.Errorf("public Stats month: %v, estimated %v; want the 1000 shown", m.Bounties, m.BountiesEstimated)
	}
	// A program that stops paying counts as it is now, as Paid · 90 days
	// counts it: its reports still count, its bounties no longer.
	if _, err := s.db.W.ExecContext(ctx, `UPDATE programs SET offers_bounty = 0 WHERE id = ?`, ids["shown"]); err != nil {
		t.Fatal(err)
	}
	ov, _ = s.PlatformOverviews(ctx, now, start, 12, "hackerone", "public")
	if m := ov[0].Months[11]; m.Bounties != 0 || m.Valid != 3 {
		t.Errorf("public Stats month after it stopped paying: %v bounties, %d valid; want 0, 3", m.Bounties, m.Valid)
	}
}
