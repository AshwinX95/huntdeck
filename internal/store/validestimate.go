package store

import (
	"context"
	"database/sql"
	"math"
	"time"
)

// Where a platform's valid reports are counted from rises in a running
// total (YesWeHack's Thanks lists) and it also says how many reports a
// program received this calendar month (reports_month), the months before
// the first saved total are estimated: the month's reports received times
// the program's valid share (its valid reports over all its reports, from
// its latest totals). The month the totals start gets the same estimate
// for the days before them, from the reports received by then.

// estimateCut is how late in a month its last saved reports_month must be
// for that month to be estimated: earlier, too much of it is missing.
const estimateCut = 25

// estimateValid adds those estimates to got's months and marks them
// Estimated, moving each overview's ValidSince back to the first month
// estimated.
func (s *Store) estimateValid(ctx context.Context, args []any, live string, month func(string) int,
	get func(string, int64) *PlatformOverview, start time.Time) error {
	sums := map[*PlatformOverview]map[int]float64{}
	add := func(o *PlatformOverview, i int, v float64) {
		if sums[o] == nil {
			sums[o] = map[int]float64{}
		}
		sums[o][i] += v
	}
	var (
		cur     int64 = -1
		started bool
		lastIn  map[int]float64 // a month's last reports_month, late enough in it
		curP    string
		share   float64
	)
	flush := func() {
		if cur < 0 {
			return
		}
		for i, n := range lastIn {
			add(get(curP, cur), i, n*share)
		}
	}
	err := s.each(ctx, `
		SELECT p.platform, p.id, s.captured_at, s.reports_month, s.accepted_reports IS NOT NULL,
			(SELECT MIN(1.0, 1.0 * l.accepted_reports / l.total_reports) FROM program_public_stats l
				WHERE l.program_id = p.id AND l.accepted_reports IS NOT NULL AND l.total_reports > 0
				ORDER BY l.captured_at DESC LIMIT 1)
		FROM program_public_stats s JOIN programs p ON p.id = s.program_id
		WHERE s.reports_month IS NOT NULL AND p.accepted_since IS NULL
			AND NOT EXISTS (SELECT 1 FROM resolved_reports r WHERE r.program_id = p.id)
			AND EXISTS (SELECT 1 FROM program_public_stats a WHERE a.program_id = p.id AND a.accepted_reports IS NOT NULL) AND`+live+`
		ORDER BY p.id, s.captured_at`, args, func(rows *sql.Rows) error {
		var p, at string
		var id int64
		var n int
		var counted bool
		var sh sql.NullFloat64
		if err := rows.Scan(&p, &id, &at, &n, &counted, &sh); err != nil {
			return err
		}
		if id != cur {
			flush()
			cur, curP, started, lastIn, share = id, p, false, map[int]float64{}, sh.Float64
		}
		if started || !sh.Valid {
			return nil
		}
		i := month(at)
		if counted { // the first saved total: the rises count from here
			started = true
			if i >= 0 {
				add(get(p, id), i, float64(n)*share)
			}
			for j := range lastIn {
				if j >= i {
					delete(lastIn, j)
				}
			}
			return nil
		}
		if i >= 0 && parseTS(at).Day() >= estimateCut {
			lastIn[i] = float64(n)
		}
		return nil
	})
	if err != nil {
		return err
	}
	flush()
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
