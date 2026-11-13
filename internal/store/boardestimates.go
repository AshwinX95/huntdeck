package store

import (
	"context"
	"database/sql"
	"math"
	"slices"
	"time"
)

// What the leaderboards (leaderboards.go) and a program's published
// totals give the Stats page, for programs whose valid reports are counted
// from a running total of accepted reports (Intigriti):
//
//   - valid reports per month before the first saved total, estimated;
//   - bounties per month: the rises in the published total paid where it
//     was saved, and before that the month's estimated accepted reports ×
//     the program's paid per accepted report (total paid over accepted
//     reports, all time), estimated;
//   - the 90-day paid line before its first sound saved day: at each
//     month's start, the three months before's estimated accepted reports
//     × paid per accepted report. A saved 90-day paid is sound once it is
//     counted (90 days of totals) or from the leaderboards' share; before
//     that it is the change in the total so far, scaled up, too rough to
//     join the estimates to (a program whose total hasn't moved in two
//     weeks shows 0), so those days are left out where the estimates
//     replace them. Programs with no sound day yet keep their saved days
//     and get no estimates.
//
// Programs that don't publish their total paid, or don't pay bounties now,
// get no bounty estimates.

// boardProgram is one program's estimates from its leaderboards.
type boardProgram struct {
	platform, currency string
	// When the program's accepted total, paid total and a sound 90-day
	// paid were first saved; zero if never.
	firstAccepted, firstPaid, firstSound time.Time
	perAccepted                          float64               // paid per accepted report; 0 unknown, or it doesn't pay now
	months                               map[time.Time]float64 // estimated accepted reports, by month
}

// boardEstimates reads the leaderboard estimates of the programs live
// picks (with args), whose valid reports are counted from saved totals.
func (s *Store) boardEstimates(ctx context.Context, args []any, live string) (map[int64]*boardProgram, error) {
	rates, err := s.leaderboardRates(ctx)
	if err != nil || len(rates.perPoint)+len(rates.perHunter) == 0 {
		return nil, err
	}
	out := map[int64]*boardProgram{}
	err = s.each(ctx, `
		WITH firsts AS (SELECT program_id,
				MIN(CASE WHEN accepted_reports IS NOT NULL THEN captured_at END) AS accepted,
				MIN(CASE WHEN bounties_paid IS NOT NULL THEN captured_at END) AS paid,
				MIN(CASE WHEN bounties_paid_90d IS NOT NULL AND (counted_days >= 90 OR points_90d IS NOT NULL) THEN captured_at END) AS sound
			FROM program_public_stats WHERE program_id IN (SELECT program_id FROM leaderboard_months) GROUP BY program_id)
		SELECT p.platform, p.id, p.offers_bounty, COALESCE(NULLIF(st.currency, ''), p.currency, ''), m.month, m.points, m.hunters,
			f.accepted, f.paid, f.sound, st.bounties_paid, st.accepted_reports
		FROM leaderboard_months m JOIN programs p ON p.id = m.program_id
		LEFT JOIN firsts f ON f.program_id = p.id
		LEFT JOIN program_public_stats st ON st.program_id = p.id AND st.captured_at = (SELECT MAX(y.captured_at)
			FROM program_public_stats y WHERE y.program_id = p.id AND y.bounties_paid IS NOT NULL AND y.accepted_reports > 0)
		WHERE p.accepted_since IS NULL AND NOT EXISTS (SELECT 1 FROM resolved_reports r WHERE r.program_id = p.id) AND`+live+`
		ORDER BY p.id, m.month`, args,
		func(rows *sql.Rows) error {
			var p, currency, mon string
			var id int64
			var paying bool
			var points sql.NullInt64
			var hunters int
			var accepted, paid, sound sql.NullString
			var paidTotal sql.NullFloat64
			var acceptedTotal sql.NullInt64
			if err := rows.Scan(&p, &id, &paying, &currency, &mon, &points, &hunters, &accepted, &paid, &sound, &paidTotal, &acceptedTotal); err != nil {
				return err
			}
			b := out[id]
			if b == nil {
				b = &boardProgram{platform: p, currency: currency, firstAccepted: parseTS(accepted.String),
					firstPaid: parseTS(paid.String), firstSound: parseTS(sound.String), months: map[time.Time]float64{}}
				if paying && paidTotal.Valid && acceptedTotal.Int64 > 0 {
					b.perAccepted = paidTotal.Float64 / float64(acceptedTotal.Int64)
				}
				out[id] = b
			}
			from, err := time.Parse("2006-01", mon)
			if err != nil {
				return nil
			}
			var v float64
			switch rate, own := rates.program[id]; {
			case hunters == 0:
			case points.Valid && own:
				v = float64(points.Int64) * rate
			case points.Valid && rates.perPoint[p] > 0:
				v = float64(points.Int64) * rates.perPoint[p]
			case rates.perHunter[p] > 0:
				v = float64(hunters) * rates.perHunter[p]
			default:
				return nil // nothing to estimate it by
			}
			b.months[from] = v
			return nil
		})
	return out, err
}

// shareBefore is the share of the month from from that comes before at,
// 0 to 1; 0 when at is zero (nothing saved: nothing to estimate up to).
func shareBefore(from, at time.Time) float64 {
	to := from.AddDate(0, 1, 0)
	switch {
	case at.IsZero() || !at.After(from):
		return 0
	case !at.Before(to):
		return 1
	}
	return at.Sub(from).Hours() / to.Sub(from).Hours()
}

// estimateFromLeaderboards adds to got's months, for each program, its
// estimated valid reports before its first saved accepted total and its
// estimated bounties before its first saved total paid, marked Estimated
// and BountiesEstimated; it moves each overview's ValidSince back to the
// first month with valid reports estimated.
func (s *Store) estimateFromLeaderboards(ctx context.Context, args []any, live string, month func(string) int,
	get func(string, int64) *PlatformOverview, start time.Time) error {
	boards, err := s.boardEstimates(ctx, args, live)
	if err != nil {
		return err
	}
	sums := map[*PlatformOverview]map[int]float64{}
	for id, b := range boards {
		o := get(b.platform, id)
		for from, v := range b.months {
			i := month(ts(from))
			if i < 0 {
				continue
			}
			if share := shareBefore(from, b.firstAccepted); share > 0 {
				if sums[o] == nil {
					sums[o] = map[int]float64{}
				}
				sums[o][i] += v * share
			}
			if share := shareBefore(from, b.firstPaid); share > 0 && b.perAccepted > 0 {
				o.Months[i].AddBounty(b.currency, v*share*b.perAccepted)
				o.Months[i].BountiesEstimated = true
			}
		}
	}
	for o, months := range sums {
		first := -1
		for i, v := range months {
			o.Months[i].Valid += int(math.Round(v))
			o.Months[i].Estimated = true
			if first < 0 || i < first {
				first = i
			}
		}
		if from := start.AddDate(0, first, 0); o.ValidSince == nil || from.Before(*o.ValidSince) {
			o.ValidSince = &from
		}
	}
	return nil
}

// countPaidRises adds the rises in programs' saved total paid to the
// months they were saved in, for programs whose valid reports are counted
// from saved totals: what they paid in the month, from when saving began.
func (s *Store) countPaidRises(ctx context.Context, args []any, live string, month func(string) int,
	get func(string, int64) *PlatformOverview) error {
	last := map[int64]float64{}
	return s.each(ctx, `
		SELECT p.platform, p.id, s.captured_at, s.bounties_paid, COALESCE(NULLIF(s.currency, ''), p.currency, '')
		FROM program_public_stats s JOIN programs p ON p.id = s.program_id
		WHERE s.bounties_paid IS NOT NULL AND s.accepted_reports IS NOT NULL AND p.accepted_since IS NULL AND p.offers_bounty = 1
			AND NOT EXISTS (SELECT 1 FROM resolved_reports r WHERE r.program_id = p.id) AND`+live+`
		ORDER BY p.id, s.captured_at`, args, func(rows *sql.Rows) error {
		var p, at, currency string
		var id int64
		var paid float64
		if err := rows.Scan(&p, &id, &at, &paid, &currency); err != nil {
			return err
		}
		if before, ok := last[id]; ok && paid > before {
			if i := month(at); i >= 0 {
				get(p, id).Months[i].AddBounty(currency, paid-before)
			}
		}
		last[id] = paid
		return nil
	})
}

// boardPaidDays adds to out, for each program with leaderboard estimates,
// a paid per accepted report and a sound saved day, its estimated paid in
// 90 days at the start of each month since since and before that day: the
// three months before's estimated accepted reports × paid per accepted
// report. Its saved days before the sound one are taken out.
func (s *Store) boardPaidDays(ctx context.Context, since time.Time, args []any, out map[string][]ProgramDay) error {
	boards, err := s.boardEstimates(ctx, args, ` p.removed_at IS NULL AND `+statsScope)
	if err != nil {
		return err
	}
	sound := map[int64]time.Time{}
	for id, b := range boards {
		if b.perAccepted > 0 && !b.firstSound.IsZero() {
			sound[id] = b.firstSound.Truncate(24 * time.Hour)
		}
	}
	for p, days := range out {
		out[p] = slices.DeleteFunc(days, func(d ProgramDay) bool {
			first, ok := sound[d.ProgramID]
			return ok && d.Day.Before(first)
		})
	}
	since = time.Date(since.Year(), since.Month(), 1, 0, 0, 0, 0, time.UTC)
	for id, b := range boards {
		first, ok := sound[id]
		if !ok {
			continue
		}
		for at := since; at.Before(first); at = at.AddDate(0, 1, 0) {
			sum, whole := 0.0, true
			for k := 1; k <= 3; k++ {
				v, ok := b.months[at.AddDate(0, -k, 0)]
				whole = whole && ok
				sum += v
			}
			if whole {
				out[b.platform] = append(out[b.platform], ProgramDay{ProgramID: id, Day: at, Value: sum * b.perAccepted, Currency: b.currency})
			}
		}
	}
	return nil
}
