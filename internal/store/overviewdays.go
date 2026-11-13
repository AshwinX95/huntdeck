package store

import (
	"context"
	"database/sql"
	"fmt"
	"slices"
	"time"
)

// Programs' numbers by day, for the Stats pages' 90-day lines.

// ProgramDay is one program's number on one day.
type ProgramDay struct {
	ProgramID int64
	Day       time.Time // midnight UTC
	Value     float64
	Currency  string // the program's, for paid amounts
}

// Columns PlatformDays can read: a program's reports received and bounties
// paid in the last 90 days, and its average hours to the company's decision
// on a report over the last 90 days (Intigriti; Bugcrowd's "validation
// within").
const (
	DayReports90d    = "reports_90d"
	DayPaid90d       = "bounties_paid_90d"
	DayDecisionHours = "validation_hours"
)

// PlatformDays returns every live program's daily value of column (one of
// the Day* constants) since since, by platform (platformName only, unless
// ""; programs of visibility, as PlatformOverviews), oldest first; the last
// snapshot of a day counts. Paid amounts are in each program's currency
// (ProgramDay.Currency). Where a program's platform publishes no paid
// total (Bugcrowd), its days are worked out as the Programs list's
// estimate is (estimatedPaidDays), after the saved ones; before a
// program's first saved day, from its leaderboards (boardPaidDays).
func (s *Store) PlatformDays(ctx context.Context, column string, since time.Time, platformName, visibility string) (map[string][]ProgramDay, error) {
	if column != DayReports90d && column != DayPaid90d && column != DayDecisionHours {
		return nil, fmt.Errorf("platform days: unknown column %q", column)
	}
	out := map[string][]ProgramDay{}
	//nolint:gosec // column is one of two constants; values are bound parameters
	q := `SELECT p.platform, s.program_id, substr(s.captured_at, 1, 10), s.` + column + `,
			COALESCE(NULLIF(s.currency, ''), p.currency, '') FROM program_public_stats s
		JOIN programs p ON p.id = s.program_id
		WHERE p.removed_at IS NULL AND ` + statsScope + ` AND s.captured_at >= :since AND s.` + column + ` IS NOT NULL
			AND (:paid = 0 OR p.offers_bounty = 1)
		ORDER BY p.platform, substr(s.captured_at, 1, 10), s.program_id, s.captured_at`
	// Paid amounts are those of programs that pay bounties now, as the
	// 90-day Paid figure the line ends at counts them.
	args := []any{sql.Named("since", ts(since)), sql.Named("platform", platformName), sql.Named("visibility", visibility),
		sql.Named("paid", column == DayPaid90d)}
	err := s.each(ctx, q, args, func(rows *sql.Rows) error {
		var p, day string
		var d ProgramDay
		if err := rows.Scan(&p, &d.ProgramID, &day, &d.Value, &d.Currency); err != nil {
			return err
		}
		at, err := time.Parse(time.DateOnly, day)
		if err != nil {
			return err
		}
		d.Day = at
		list := out[p]
		if n := len(list); n > 0 && list[n-1].ProgramID == d.ProgramID && list[n-1].Day.Equal(at) {
			list[n-1] = d // a later snapshot the same day
		} else {
			list = append(list, d)
		}
		out[p] = list
		return nil
	})
	if err != nil || column != DayPaid90d {
		return out, err
	}
	if err := s.estimatedPaidDays(ctx, since, args, out); err != nil {
		return out, err
	}
	return out, s.boardPaidDays(ctx, since, args, out)
}

// estimatedPaidDays adds to out, for each day since since, each live USD
// program's estimated bounties paid in the 90 days to that day's end (now,
// for today) where its platform publishes no paid total: its payable
// accepted reports in those 90 days times its average payout now, as the
// Programs list estimates today's. Only days whose 90 days its accepted
// reports cover in full.
func (s *Store) estimatedPaidDays(ctx context.Context, since time.Time, args []any, out map[string][]ProgramDay) error {
	type prog struct {
		platform string
		from     time.Time // accepted reports listed in full from
		avg      float64
		accepted []time.Time
	}
	progs := map[int64]*prog{}
	var ids []int64
	err := s.each(ctx, `SELECT p.platform, p.id, p.accepted_since, ps.avg_bounty_high FROM programs p
		JOIN program_public_stats ps ON ps.program_id = p.id AND ps.captured_at = p.stats_at
		WHERE p.removed_at IS NULL AND p.accepted_since IS NOT NULL AND ps.bounties_paid_90d IS NULL AND p.offers_bounty = 1
			AND ps.avg_bounty_high IS NOT NULL AND ps.currency IN ('', 'USD') AND `+statsScope+`
		ORDER BY p.id`, args, func(rows *sql.Rows) error {
		var id int64
		var p prog
		var from string
		if err := rows.Scan(&p.platform, &id, &from, &p.avg); err != nil {
			return err
		}
		p.from = parseTS(from)
		progs[id] = &p
		ids = append(ids, id)
		return nil
	})
	if err != nil || len(ids) == 0 {
		return err
	}
	err = s.each(ctx, `SELECT a.program_id, a.accepted_at FROM accepted_reports a JOIN programs p ON p.id = a.program_id
		WHERE p.removed_at IS NULL AND p.accepted_since IS NOT NULL AND `+payablePriority+` AND `+statsScope+`
		ORDER BY a.accepted_at`, args, func(rows *sql.Rows) error {
		var id int64
		var at string
		if err := rows.Scan(&id, &at); err != nil {
			return err
		}
		if p := progs[id]; p != nil {
			p.accepted = append(p.accepted, parseTS(at))
		}
		return nil
	})
	if err != nil {
		return err
	}
	now := s.now().UTC()
	today := now.Truncate(24 * time.Hour)
	for day := since.UTC().Truncate(24 * time.Hour); !day.After(today); day = day.AddDate(0, 0, 1) {
		end := day.AddDate(0, 0, 1)
		if end.After(now) {
			end = now
		}
		start := end.Add(-90 * 24 * time.Hour)
		for _, id := range ids {
			p := progs[id]
			if p.from.After(start) {
				continue // its reports don't cover these 90 days yet
			}
			n, _ := slices.BinarySearchFunc(p.accepted, end, func(a, t time.Time) int { return a.Compare(t) })
			k, _ := slices.BinarySearchFunc(p.accepted, start, func(a, t time.Time) int { return a.Compare(t) })
			out[p.platform] = append(out[p.platform], ProgramDay{ProgramID: id, Day: day, Value: float64(n-k) * p.avg, Currency: "USD"})
		}
	}
	return nil
}
