package store

import (
	"context"
	"database/sql"

	"github.com/mdpsec/huntdeck/internal/platform"
)

// HackerOne programs can hide their bounty amounts (team
// hide_bounty_amounts; private programs do), and then Hacktivity says a
// report got a bounty ("bounty", the latest action) but never how much.
// For those programs, the bounties shown per month are estimated: each
// such report at the program's average bounty (the middle of its
// published range), marked estimated wherever they're shown.

// hiddenBountyAverages returns the average bounty, in the program's
// currency, of each program in scope that has bounty-awarded reports but
// shows no amount on any of them. where filters programs p by args.
func (s *Store) hiddenBountyAverages(ctx context.Context, where string, args ...any) (map[int64]float64, error) {
	out := map[int64]float64{}
	err := s.each(ctx, `
		SELECT p.id, (st.avg_bounty_low + st.avg_bounty_high) / 2.0 FROM programs p
		JOIN program_public_stats st ON st.program_id = p.id AND st.captured_at = (
			SELECT MAX(x.captured_at) FROM program_public_stats x WHERE x.program_id = p.id AND x.avg_bounty_low IS NOT NULL)
		WHERE st.avg_bounty_low IS NOT NULL AND st.avg_bounty_high IS NOT NULL
			AND EXISTS (SELECT 1 FROM resolved_reports r WHERE r.program_id = p.id AND r.action = '`+platform.ActionBounty+`')
			AND NOT EXISTS (SELECT 1 FROM resolved_reports r WHERE r.program_id = p.id AND r.bounty IS NOT NULL)
			AND `+where, args, func(rows *sql.Rows) error {
		var id int64
		var avg float64
		if err := rows.Scan(&id, &avg); err != nil {
			return err
		}
		if avg > 0 {
			out[id] = avg
		}
		return nil
	})
	return out, err
}

// estimatedBounty is the bounty to count for a resolved report: the one
// shown, else the program's average when it hides amounts and the report
// got a bounty. est says it's the average.
func estimatedBounty(action string, shown sql.NullFloat64, avg float64, hides bool) (v float64, est bool) {
	switch {
	case shown.Valid:
		return shown.Float64, false
	case hides && action == platform.ActionBounty:
		return avg, true
	}
	return 0, false
}
