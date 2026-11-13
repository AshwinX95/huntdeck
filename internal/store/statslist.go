package store

import (
	"context"
	"database/sql"
	"time"
)

// ProgramStatsRow is one program in the stats comparison.
type ProgramStatsRow struct {
	ProgramRef
	OffersBounty bool
	InScope      int
	Wildcards    int
	Stats        ProgramStats
	// Resolved90d counts resolved, undisclosed reports of the last 90 days
	// (HackerOne Hacktivity, Bugcrowd CrowdStream); nil where the platform
	// doesn't list them.
	Resolved90d *int
	// Accepted90d counts reports accepted in the last 90 days (Bugcrowd
	// CrowdStream); nil where the platform doesn't list them.
	Accepted90d *int
}

// reportsSince is the CTE counting each program's resolutions (res) and
// accepted reports (acc) since its parameters; see reportsSinceArgs.
const reportsSince = `WITH res AS (SELECT program_id, COUNT(*) AS n FROM (
		SELECT program_id FROM resolved_reports WHERE resolved_at >= ?
		UNION ALL SELECT program_id FROM accepted_reports WHERE resolved_at >= ?) GROUP BY program_id),
	acc AS (SELECT program_id, COUNT(*) AS n FROM accepted_reports WHERE accepted_at >= ? GROUP BY program_id) `

// reportsSinceArgs are reportsSince's parameters for the 90 days to now.
func reportsSinceArgs(now time.Time) []any {
	since := ts(now.Add(-90 * 24 * time.Hour))
	return []any{since, since, since}
}

// joinReports joins reportsSince's counts to programs p.
const joinReports = ` LEFT JOIN res ON res.program_id = p.id LEFT JOIN acc ON acc.program_id = p.id`

// resolved90 is a program's resolutions from res, NULL if it has no
// resolution history.
const resolved90 = `CASE WHEN p.resolutions_synced_at IS NOT NULL OR p.accepted_since IS NOT NULL THEN COALESCE(res.n, 0) END`

// accepted90 is a program's accepted reports from acc, NULL if its feed
// was never read.
const accepted90 = `CASE WHEN p.accepted_since IS NOT NULL THEN COALESCE(acc.n, 0) END`

// PaidPerReport is r.Stats.PaidPerReport.
func (r ProgramStatsRow) PaidPerReport() *float64 { return r.Stats.PaidPerReport() }

// StatsSorts are the orderings ProgramStatsList understands; the first is
// the default.
var StatsSorts = []string{"paid90", "perreport", "reports90", "top", "avg", "total", "resolved", "efficiency", "recent", "quiet",
	"resolved90", "accepted90", "name"}

// statsOrder maps a sort key to an ORDER BY clause. Missing values sort last.
var statsOrder = map[string]string{
	"paid90":     "s.bounties_paid_90d IS NULL, s.bounties_paid_90d DESC",
	"perreport":  "per_report IS NULL, per_report DESC",
	"reports90":  "s.reports_90d IS NULL, s.reports_90d DESC",
	"top":        "s.top_bounty_high IS NULL, s.top_bounty_high DESC, s.top_bounty_low DESC",
	"avg":        "s.avg_bounty_high IS NULL, s.avg_bounty_high DESC, s.avg_bounty_low DESC",
	"total":      "s.bounties_paid IS NULL, s.bounties_paid DESC",
	"resolved":   "s.resolved_reports IS NULL, s.resolved_reports DESC",
	"efficiency": "s.response_efficiency IS NULL, s.response_efficiency DESC, s.bounties_paid_90d DESC",
	"recent":     "s.last_resolved_at IS NULL, s.last_resolved_at DESC",
	"quiet":      "s.last_resolved_at IS NULL, s.last_resolved_at ASC",
	"resolved90": "resolved_90d IS NULL, resolved_90d DESC",
	"accepted90": "accepted_90d IS NULL, accepted_90d DESC",
	"name":       "lower(p.name)",
}

// ProgramStatsList returns one page of programs that have public stats,
// filtered like the programs list and sorted by f.Sort (see StatsSorts), and
// the total matching count.
func (s *Store) ProgramStatsList(ctx context.Context, f ProgramFilter) ([]ProgramStatsRow, int, error) {
	where, args := f.where()
	where += " AND p.stats_at IS NOT NULL"
	var total int
	if err := s.db.R.QueryRowContext(ctx, `SELECT COUNT(*) FROM programs p`+where, args...).Scan(&total); err != nil { //nolint:gosec // constant fragments; values are bound parameters
		return nil, 0, err
	}
	order, ok := statsOrder[f.Sort]
	if !ok {
		order = statsOrder[StatsSorts[0]]
	}
	if f.Reverse {
		order = reverseOrder(order)
	}
	order += ", lower(p.name), p.id" // ties A to Z either way
	limit := f.Limit
	if limit <= 0 {
		limit = 50
	}
	//nolint:gosec // constant fragments; values are bound parameters
	q := reportsSince + `SELECT p.id, p.platform, p.handle, p.name, p.url, p.private, p.state, p.offers_bounty, p.n_in_scope, p.n_wildcards,
			s.bounties_paid_90d / NULLIF(s.reports_90d, 0) AS per_report, ` + resolved90 + ` AS resolved_90d,
			` + accepted90 + ` AS accepted_90d, ` + statsColumns + `
		FROM programs p JOIN program_public_stats s ON s.program_id = p.id AND s.captured_at = p.stats_at` + joinReports +
		where + ` ORDER BY ` + order + ` LIMIT ? OFFSET ?`
	qargs := append(reportsSinceArgs(s.Now()), args...)
	rows, err := s.db.R.QueryContext(ctx, q, append(qargs, limit, max(0, f.Offset))...)
	if err != nil {
		return nil, 0, err
	}
	defer func() { _ = rows.Close() }()
	var out []ProgramStatsRow
	for rows.Next() {
		var r ProgramStatsRow
		var perReport sql.NullFloat64
		var resolved, accepted sql.NullInt64
		st, err := scanStats(func(dest ...any) error {
			return rows.Scan(append([]any{&r.ID, &r.Platform, &r.Handle, &r.Name, &r.URL, &r.Private, &r.State,
				&r.OffersBounty, &r.InScope, &r.Wildcards, &perReport, &resolved, &accepted}, dest...)...)
		})
		if err != nil {
			return nil, 0, err
		}
		r.Stats = *st
		r.Resolved90d, r.Accepted90d = intPtr(resolved), intPtr(accepted)
		out = append(out, r)
	}
	return out, total, rows.Err()
}
