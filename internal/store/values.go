package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"math"
	"time"
)

// Freshness of the numbers the API serves for a program (its v2 shape): for
// each, when it last changed. TrackValues keeps program_values: after every
// poll it works out each program's numbers as the Programs list does and,
// value by value, moves changed_at only where the value differs from the one
// saved. So a value's time says when HuntDeck first saw it as it is now, to
// the poll; a figure re-read unchanged keeps its time.
//
// The first time a program's values are saved (the first run after the
// migration, on an install with history), each time comes from that
// history where it can be dated (see valueBackfill) and is left unknown
// (nil) where it can't. A program first seen later has its values' times
// from when they first appeared.

// Tracked field names, as the API names them.
const (
	ValBounty           = "bounty"
	ValInScope          = "in_scope_entries"
	ValOutOfScope       = "out_of_scope_entries"
	ValWildcards        = "wildcards"
	ValDomains          = "domains"
	ValApps             = "apps"
	ValAPIs             = "apis"
	ValPaid90d          = "paid_90d"
	ValPaidTotal        = "paid_total"
	ValAvgBountyLow     = "average_bounty_low"
	ValAvgBountyHigh    = "average_bounty_high"
	ValRewarded         = "rewarded_reports"
	ValReceived90d      = "reports_received_90d"
	ValResolved90d      = "resolved_90d"
	ValAccepted90d      = "accepted_90d"
	ValReports7d        = "reports_7d"
	ValReportsMonth     = "reports_this_month"
	ValTotalReports     = "total_reports"
	ValAcceptedTotal    = "accepted_total"
	ValHunters          = "hunters"
	ValHunters90d       = "hunters_90d"
	ValFirstResponseHrs = "first_response_hours"
	ValValidationHours  = "validation_hours"
)

// TrackedFields are the tracked numbers' field names.
var TrackedFields = []string{ValBounty, ValInScope, ValOutOfScope, ValWildcards, ValDomains,
	ValApps, ValAPIs, ValPaid90d, ValPaidTotal, ValAvgBountyLow, ValAvgBountyHigh, ValRewarded,
	ValReceived90d, ValResolved90d, ValAccepted90d, ValReports7d, ValReportsMonth, ValTotalReports,
	ValAcceptedTotal, ValHunters, ValHunters90d, ValFirstResponseHrs, ValValidationHours}

// round2 is v to the cent, as the API rounds amounts and hours: a change
// smaller than that isn't one.
func round2(v *float64) *float64 {
	if v == nil {
		return nil
	}
	r := math.Round(*v*100) / 100
	return &r
}

// TrackedValues are a program's tracked numbers, by field, as JSON.
func TrackedValues(it ProgramListItem) (map[string]string, error) {
	var bounty any
	if it.BountyMax != nil {
		bounty = map[string]any{"min": round2(it.BountyMin), "max": round2(it.BountyMax), "currency": it.Currency, "from": it.BountyFrom}
	}
	vals := map[string]any{
		ValBounty: bounty, ValInScope: it.InScope, ValOutOfScope: it.OutOfScope, ValWildcards: it.Wildcards,
		ValDomains: it.Domains, ValApps: it.Apps, ValAPIs: it.APIs,
		ValPaid90d: round2(it.Paid90d), ValPaidTotal: round2(it.PaidAll), ValAvgBountyLow: round2(it.AvgBountyLow),
		ValAvgBountyHigh: round2(it.AvgPayout), ValRewarded: it.Rewarded, ValReceived90d: it.Reports90d,
		ValResolved90d: it.Resolved90d, ValAccepted90d: it.Accepted90d, ValReports7d: it.Reports7d,
		ValReportsMonth: it.ReportsMonth, ValTotalReports: it.TotalReports, ValAcceptedTotal: it.AcceptedAll,
		ValHunters: it.Hunters, ValHunters90d: it.Hunters90d, ValFirstResponseHrs: round2(it.FirstResponseHours),
		ValValidationHours: round2(it.ValidationHours),
	}
	out := make(map[string]string, len(vals))
	for k, v := range vals {
		b, err := json.Marshal(v)
		if err != nil {
			return nil, fmt.Errorf("value %s: %w", k, err)
		}
		out[k] = string(b)
	}
	return out, nil
}

// valueColumns are the tracked fields read straight from a program's
// latest public stats, and the column each is: their history dates them.
var valueColumns = map[string]string{
	ValPaid90d: "bounties_paid_90d", ValPaidTotal: "bounties_paid", ValAvgBountyLow: "avg_bounty_low",
	ValAvgBountyHigh: "avg_bounty_high", ValRewarded: "rewarded_reports", ValReceived90d: "reports_90d",
	ValReports7d: "reports_7d", ValReportsMonth: "reports_month", ValTotalReports: "total_reports",
	ValAcceptedTotal: "accepted_reports", ValHunters: "COALESCE(hunters, hackers_thanked)", ValHunters90d: "hunters_90d",
	ValFirstResponseHrs: "first_response_hours", ValValidationHours: "validation_hours",
}

// TrackValues saves every listed program's tracked numbers, moving each
// one's changed_at only where its value changed (see above). Removed
// programs keep what they had.
func (s *Store) TrackValues(ctx context.Context) error {
	items, _, err := s.Programs(ctx, ProgramFilter{State: "all", Ignored: "include", Limit: 1 << 30})
	if err != nil {
		return fmt.Errorf("track values: list programs: %w", err)
	}
	// Programs without their values saved yet are dated from history
	// first, outside the write: the first time, that reads every snapshot.
	has := map[int64]bool{} // a field added later is dated when it's first saved
	if err := s.eachRow(ctx, `SELECT DISTINCT program_id, NULL FROM program_values`, nil, func(id int64, _ sql.NullString) {
		has[id] = true
	}); err != nil {
		return fmt.Errorf("track values: %w", err)
	}
	var missing []int64
	for _, it := range items {
		if !has[it.ID] {
			missing = append(missing, it.ID)
		}
	}
	backfill := map[int64]map[string]string{}
	if len(missing) > 0 {
		if backfill, err = s.valueBackfill(ctx, missing); err != nil {
			return fmt.Errorf("track values: history: %w", err)
		}
	}
	return s.Write(ctx, func(tx *Tx) error {
		saved := map[int64]map[string]string{}
		rows, err := tx.tx.QueryContext(ctx, `SELECT program_id, field, value FROM program_values`)
		if err != nil {
			return err
		}
		for rows.Next() {
			var id int64
			var field, value string
			if err := rows.Scan(&id, &field, &value); err != nil {
				_ = rows.Close()
				return err
			}
			if saved[id] == nil {
				saved[id] = map[string]string{}
			}
			saved[id][field] = value
		}
		if err := rows.Close(); err != nil {
			return err
		}
		tracking := len(saved) > 0 // values were saved before: new ones appeared now
		for _, it := range items {
			vals, err := TrackedValues(it)
			if err != nil {
				return fmt.Errorf("track values: program %d: %w", it.ID, err)
			}
			mine := saved[it.ID]
			for field, v := range vals {
				old, ok := mine[field]
				switch {
				case ok && old == v:
					continue
				case ok:
					_, err = tx.tx.ExecContext(ctx, `UPDATE program_values SET value = ?, changed_at = ? WHERE program_id = ? AND field = ?`,
						v, tx.now, it.ID, field)
				default:
					var at any
					if b := backfill[it.ID][field]; b != "" && !ownEstimate(it, field) {
						at = b
					} else if tracking {
						at = tx.now
					}
					_, err = tx.tx.ExecContext(ctx, `INSERT INTO program_values (program_id, field, value, changed_at) VALUES (?, ?, ?, ?)`,
						it.ID, field, v, at)
				}
				if err != nil {
					return fmt.Errorf("track values: program %d %s: %w", it.ID, field, err)
				}
			}
		}
		return nil
	})
}

// eachRow runs q on the read connection and calls fn with each row's
// program ID and time.
func (s *Store) eachRow(ctx context.Context, q string, args []any, fn func(id int64, at sql.NullString)) error {
	rows, err := s.db.R.QueryContext(ctx, q, args...)
	if err != nil {
		return err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var id int64
		var at sql.NullString
		if err := rows.Scan(&id, &at); err != nil {
			return err
		}
		fn(id, at)
	}
	return rows.Err()
}

// ownEstimate says whether field's value for it is worked out by HuntDeck
// rather than read from its stats column, so that column's history
// doesn't date it.
func ownEstimate(it ProgramListItem, field string) bool {
	return field == ValPaid90d && it.Paid90dEstimated || field == ValPaidTotal && it.PaidAllEstimated
}

// backfillAll is how many programs' history valueBackfill reads by ID;
// more, and it reads everyone's (the first run).
const backfillAll = 500

// valueBackfill dates the tracked values of programs ids that history can
// date, by program and field: a stats column's value from the first of the
// daily snapshots that has had it since; the in- and out-of-scope entry
// counts from the latest entry added, removed or moved in or out of scope
// (else when the program was first seen); a bounty range from its latest
// bounty table change.
func (s *Store) valueBackfill(ctx context.Context, ids []int64) (map[int64]map[string]string, error) {
	only, args := "", []any{}
	if len(ids) <= backfillAll {
		only = " AND p.id IN (" + placeholders(len(ids)) + ")"
		for _, id := range ids {
			args = append(args, id)
		}
	}
	out := map[int64]map[string]string{}
	add := func(field, q string, first ...any) error {
		err := s.eachRow(ctx, q, append(first, args...), func(id int64, at sql.NullString) {
			if !at.Valid || at.String == "" {
				return
			}
			if out[id] == nil {
				out[id] = map[string]string{}
			}
			out[id][field] = at.String
		})
		if err != nil {
			return fmt.Errorf("%s: %w", field, err)
		}
		return nil
	}
	for field, col := range valueColumns {
		// The start of each program's latest run of one value, up to its
		// latest snapshot (the one the Programs list reads).
		//nolint:gosec // col is one of valueColumns and only placeholders are added, not input
		q := `WITH s AS (
				SELECT s.program_id, s.captured_at, ` + col + ` AS v,
					LAG(` + col + `) OVER w AS pv, ROW_NUMBER() OVER w AS rn
				FROM program_public_stats s JOIN programs p ON p.id = s.program_id AND s.captured_at <= p.stats_at` + only + `
				WINDOW w AS (PARTITION BY s.program_id ORDER BY s.captured_at))
			SELECT program_id, MAX(captured_at) FROM s WHERE rn = 1 OR v IS NOT pv GROUP BY program_id`
		if err := add(field, q); err != nil {
			return nil, err
		}
	}
	for field, in := range map[string]int{ValInScope: 1, ValOutOfScope: 0} {
		//nolint:gosec // only placeholders are added, not input
		q := `SELECT p.id, MAX(p.first_seen_at,
				COALESCE((SELECT MAX(CASE WHEN e.removed_at IS NULL THEN e.first_seen_at ELSE e.removed_at END)
					FROM scope_entries e WHERE e.program_id = p.id AND e.in_scope = ?), ''),
				COALESCE((SELECT MAX(c.occurred_at) FROM changes c WHERE c.program_id = p.id
					AND c.kind = 'scope_updated' AND c.field = 'in_scope'), ''))
			FROM programs p WHERE 1 = 1` + only
		if err := add(field, q, in); err != nil {
			return nil, err
		}
	}
	//nolint:gosec // only placeholders are added, not input
	err := add(ValBounty, `SELECT p.id, MAX(c.occurred_at) FROM changes c JOIN programs p ON p.id = c.program_id
		WHERE c.kind = 'rewards_changed'`+only+` GROUP BY p.id`)
	return out, err
}

// ProgramTimes is when a program and each of its tracked numbers last
// changed, and when HuntDeck last checked its platform for it.
type ProgramTimes struct {
	// UpdatedAt is the latest of: when HuntDeck first saw the program, its
	// removal, its latest recorded change (Changes) and its latest tracked
	// value change.
	UpdatedAt time.Time
	// LastSyncedAt is when HuntDeck last read the program from its
	// platform, changed or not: listed, its scope checked or its stats
	// fetched. Nil for programs added by hand.
	LastSyncedAt *time.Time
	// Fields are the tracked numbers' times, by field; a field missing, or
	// nil, wasn't dated.
	Fields map[string]*time.Time
}

// programUpdatedExpr is ProgramTimes.UpdatedAt for programs p, in SQL.
const programUpdatedExpr = `MAX(p.first_seen_at, COALESCE(p.removed_at, ''),
	COALESCE((SELECT MAX(c.occurred_at) FROM changes c WHERE c.program_id = p.id), ''),
	COALESCE((SELECT MAX(v.changed_at) FROM program_values v WHERE v.program_id = p.id), ''))`

// ProgramTimesOf returns ProgramTimes for the programs ids, by ID.
func (s *Store) ProgramTimesOf(ctx context.Context, ids []int64) (map[int64]ProgramTimes, error) {
	out := make(map[int64]ProgramTimes, len(ids))
	if len(ids) == 0 {
		return out, nil
	}
	args := make([]any, len(ids))
	for i, id := range ids {
		args[i] = id
	}
	in := placeholders(len(ids))
	//nolint:gosec // constant placeholders; values are bound parameters
	rows, err := s.db.R.QueryContext(ctx, `SELECT p.id, `+programUpdatedExpr+`,
			CASE WHEN p.platform <> ? THEN NULLIF(MAX(p.last_seen_at, COALESCE(p.scope_checked_at, ''), COALESCE(p.stats_at, '')), '') END
		FROM programs p WHERE p.id IN (`+in+`)`, append([]any{Independent}, args...)...)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var id int64
		var updated string
		var synced sql.NullString
		if err := rows.Scan(&id, &updated, &synced); err != nil {
			_ = rows.Close()
			return nil, err
		}
		out[id] = ProgramTimes{UpdatedAt: parseTS(updated), LastSyncedAt: parseTSPtr(synced), Fields: map[string]*time.Time{}}
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	//nolint:gosec // constant placeholders; values are bound parameters
	rows, err = s.db.R.QueryContext(ctx, `SELECT program_id, field, changed_at FROM program_values WHERE program_id IN (`+in+`)`, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var id int64
		var field string
		var at sql.NullString
		if err := rows.Scan(&id, &field, &at); err != nil {
			return nil, err
		}
		if pt, ok := out[id]; ok {
			pt.Fields[field] = parseTSPtr(at)
		}
	}
	return out, rows.Err()
}

// changedSinceCond keeps programs (as p) whose ProgramTimes.UpdatedAt is
// after its argument (four times).
const changedSinceCond = `(p.first_seen_at > ? OR p.removed_at > ?
	OR EXISTS (SELECT 1 FROM changes c WHERE c.program_id = p.id AND c.occurred_at > ?)
	OR EXISTS (SELECT 1 FROM program_values v WHERE v.program_id = p.id AND v.changed_at > ?))`
