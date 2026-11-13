package store

import (
	"context"
	"database/sql"
	"math"
	"strings"
	"time"

	"github.com/mdpsec/huntdeck/internal/platform"
)

// Intigriti shows only a running total of a program's accepted reports,
// so its valid reports per month are counted from the rises between the
// totals HuntDeck saved, and start where saving started. Before that, its
// past monthly leaderboards (leaderboard_months) give an estimate: the
// points researchers earned in a month are a fixed number per accepted
// report by severity, and a fifth of it per duplicate, so
//
//	accepted ≈ the month's points × the program's accepted reports per point
//
// where the program's own ratio is its all-time accepted reports over its
// all-time points, else the platform's over every program showing both.
// A program that hides its points has only how many researchers earned
// any: accepted ≈ those × the platform's accepted reports per researcher a
// month, from the programs that show points. Months are estimated up to
// the first saved total; the month it falls in for the days before it.

// LeaderboardMonthsBack is how many finished months of a program's
// leaderboard are read.
const LeaderboardMonthsBack = 12

// maxPerPoint bounds accepted reports per point: the fewest points an
// accepted report earns is 5.
const maxPerPoint = 0.2

// LeaderboardMonthsDue returns up to about limit months to read of the
// leaderboards of a platform's live programs whose leaderboard HuntDeck
// reads (it saved a 90-day count from it): each finished month of the last
// LeaderboardMonthsBack not saved yet, from the month the program launched.
// Private programs come first. ids maps the handles to program IDs.
func (s *Store) LeaderboardMonthsDue(ctx context.Context, platformName string, limit int) ([]platform.LeaderboardWant, map[string]int64, error) {
	cur := s.Now()
	cur = time.Date(cur.Year(), cur.Month(), 1, 0, 0, 0, 0, time.UTC)
	var out []platform.LeaderboardWant
	ids := map[string]int64{}
	n := 0
	err := s.each(ctx, `
		SELECT p.id, p.handle, p.started_at, (SELECT GROUP_CONCAT(m.month) FROM leaderboard_months m WHERE m.program_id = p.id)
		FROM programs p WHERE p.platform = ? AND p.removed_at IS NULL
			AND EXISTS (SELECT 1 FROM program_public_stats s WHERE s.program_id = p.id AND s.hunters_90d IS NOT NULL)
		ORDER BY p.private DESC, p.id`, []any{platformName}, func(rows *sql.Rows) error {
		var id int64
		var handle string
		var started, have sql.NullString
		if err := rows.Scan(&id, &handle, &started, &have); err != nil {
			return err
		}
		if n >= limit {
			return nil
		}
		saved := map[string]bool{}
		for _, m := range strings.Split(have.String, ",") {
			saved[m] = true
		}
		launched := parseTS(started.String)
		w := platform.LeaderboardWant{Handle: handle}
		for j := LeaderboardMonthsBack; j >= 1; j-- {
			m := cur.AddDate(0, -j, 0)
			if saved[m.Format("2006-01")] || (!launched.IsZero() && !launched.Before(m.AddDate(0, 1, 0))) {
				continue
			}
			w.Months = append(w.Months, m)
		}
		if len(w.Months) > 0 {
			out = append(out, w)
			ids[handle] = id
			n += len(w.Months)
		}
		return nil
	})
	return out, ids, err
}

// SaveLeaderboardMonths stores a program's monthly leaderboards, replacing
// any saved for the same months.
func (t *Tx) SaveLeaderboardMonths(ctx context.Context, programID int64, months []platform.LeaderboardMonth) error {
	for _, m := range months {
		if _, err := t.tx.ExecContext(ctx, `
			INSERT INTO leaderboard_months (program_id, month, points, hunters, capped, fetched_at) VALUES (?, ?, ?, ?, ?, ?)
			ON CONFLICT (program_id, month) DO UPDATE SET points = excluded.points, hunters = excluded.hunters,
				capped = excluded.capped, fetched_at = excluded.fetched_at`,
			programID, m.Month.UTC().Format("2006-01"), m.Points, m.Hunters, m.Capped, t.now); err != nil {
			return err
		}
	}
	return nil
}

// boardRates are a platform's accepted reports per leaderboard point and
// per researcher a month, from its programs that show both, and each
// program's own per point.
type boardRates struct {
	perPoint, perHunter map[string]float64
	program             map[int64]float64
}

// leaderboardRates works out boardRates over every live program with
// saved leaderboards, whatever the Stats page's filter.
func (s *Store) leaderboardRates(ctx context.Context) (boardRates, error) {
	r := boardRates{perPoint: map[string]float64{}, perHunter: map[string]float64{}, program: map[int64]float64{}}
	type sums struct{ accepted, points, estimated, hunters float64 }
	by := map[string]*sums{}
	err := s.each(ctx, `
		SELECT p.platform, p.id,
			(SELECT l.accepted_reports FROM program_public_stats l WHERE l.program_id = p.id AND l.accepted_reports IS NOT NULL ORDER BY l.captured_at DESC LIMIT 1),
			(SELECT l.points_all FROM program_public_stats l WHERE l.program_id = p.id AND l.points_all > 0 ORDER BY l.captured_at DESC LIMIT 1),
			(SELECT SUM(m.points) FROM leaderboard_months m WHERE m.program_id = p.id AND m.points IS NOT NULL AND m.capped = 0),
			(SELECT SUM(m.hunters) FROM leaderboard_months m WHERE m.program_id = p.id AND m.points IS NOT NULL AND m.capped = 0)
		FROM programs p WHERE p.removed_at IS NULL AND EXISTS (SELECT 1 FROM leaderboard_months m WHERE m.program_id = p.id)`, nil,
		func(rows *sql.Rows) error {
			var p string
			var id int64
			var accepted, points, monthPoints, monthHunters sql.NullInt64
			if err := rows.Scan(&p, &id, &accepted, &points, &monthPoints, &monthHunters); err != nil {
				return err
			}
			if by[p] == nil {
				by[p] = &sums{}
			}
			if !accepted.Valid || !points.Valid {
				return nil
			}
			rate := math.Min(maxPerPoint, float64(accepted.Int64)/float64(points.Int64))
			r.program[id] = rate
			b := by[p]
			b.accepted += float64(accepted.Int64)
			b.points += float64(points.Int64)
			if monthHunters.Int64 > 0 {
				b.estimated += float64(monthPoints.Int64) * rate
				b.hunters += float64(monthHunters.Int64)
			}
			return nil
		})
	for p, b := range by {
		if b.points > 0 {
			r.perPoint[p] = math.Min(maxPerPoint, b.accepted/b.points)
		}
		if b.hunters > 0 {
			r.perHunter[p] = b.estimated / b.hunters
		}
	}
	return r, err
}
