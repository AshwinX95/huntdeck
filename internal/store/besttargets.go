package store

import (
	"cmp"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	"github.com/mdpsec/huntdeck/internal/platform"
	"github.com/mdpsec/huntdeck/internal/scope"
)

// BestTargets returns one page of scored targets matching f (plus an
// optional substring query), best first, one row per program and target
// (per program and root domain for roots, with its best target's score),
// and the total. Only in-scope targets are scored.
func (s *Store) BestTargets(ctx context.Context, f TargetFilter, query string, limit, offset int) ([]TargetRow, int, error) {
	if len(f.AssetTypes) > 0 { // not scored: newest first
		f.Order = TargetsByNewest
		return s.assetRows(ctx, f, query, limit, offset)
	}
	if f.OutOfScope {
		return nil, 0, nil
	}
	from, args := bestFrom(f, query)
	group, value := "ts.program_id, ts.kind, ts.value", "ts.value"
	if f.Kind == scope.KindRoot {
		group, value = "ts.program_id, ts.apex", "ts.apex"
	}
	var total int
	//nolint:gosec // constant fragments; values are bound parameters
	if err := s.db.R.QueryRowContext(ctx, `SELECT COUNT(*) FROM (SELECT 1`+from+` GROUP BY `+group+`)`, args...).Scan(&total); err != nil {
		return nil, 0, err
	}
	if limit <= 0 {
		limit = 100
	}
	// The query below names its columns: sc the score, v the value, a the
	// date added, pid the program.
	order := "sc DESC, v, pid"
	page, pageArgs := ` LIMIT ? OFFSET ?`, []any{limit, max(0, offset)}
	switch f.Order {
	case TargetsByNewest:
		order = "a DESC, " + order
	case TargetsByBounty:
		// A target's bounty is worked out below, so these are sorted and
		// paged there, once every match has one.
		page, pageArgs = "", nil
	}
	if f.Reverse {
		order = reverseOrder(order)
	}
	capped := ""
	if f.JoinedPerProgram > 0 {
		capped = ` WHERE j = 0 OR jn <= ?`
		pageArgs = append([]any{f.JoinedPerProgram}, pageArgs...)
	}
	// With one MAX aggregate, SQLite takes the bare columns from the row
	// holding it: a root shows its best target's date, bounty and reasons.
	// jn ranks a program's targets best first, apart for those new to the
	// user (joined), and jc counts them.
	//nolint:gosec // constant fragments; values are bound parameters
	rows, err := s.db.R.QueryContext(ctx, `SELECT * FROM (
		SELECT ts.kind, `+value+` AS v, ts.bounty, ts.added_at AS a, MAX(ts.score) AS sc, ts.reasons,
			p.id AS pid, p.platform, p.handle, p.name, p.url, p.private, p.state, p.offers_bounty,
			p.rewards, p.bounty_min, p.bounty_max, ps.top_bounty_high, COALESCE(ps.currency, p.currency, ''),
			ps.bounties_paid, `+paidAllExpr+`, ps.rewarded_reports, ps.avg_bounty_high, ts.joined AS j,
			ROW_NUMBER() OVER (PARTITION BY p.id, ts.joined ORDER BY MAX(ts.score) DESC, `+value+`) AS jn,
			COUNT(*) OVER (PARTITION BY p.id, ts.joined) AS jc`+from+`
		GROUP BY `+group+`)`+capped+` ORDER BY `+order+page, append(args, pageArgs...)...)
	if err != nil {
		return nil, 0, err
	}
	defer func() { _ = rows.Close() }()
	var out []TargetRow
	for rows.Next() {
		var r TargetRow
		var added, reasons string
		var rewards sql.NullString
		var listMin, listMax, statsTop, paid, paidAll, avg sql.NullFloat64
		var rewarded sql.NullInt64
		var rank, count int
		p := &r.Program
		if err := rows.Scan(&r.Kind, &r.Value, &r.Bounty, &added, &r.Score, &reasons, &p.ID, &p.Platform, &p.Handle, &p.Name, &p.URL, &p.Private, &p.State, &r.OffersBounty,
			&rewards, &listMin, &listMax, &statsTop, &r.Currency, &paid, &paidAll, &rewarded, &avg, &r.Joined, &rank, &count); err != nil {
			return nil, 0, err
		}
		if r.Joined && f.JoinedPerProgram > 0 && rank == f.JoinedPerProgram {
			r.MoreJoined = count - rank
		}
		r.Scored = true
		var table []platform.RewardRow
		if rewards.Valid {
			_ = json.Unmarshal([]byte(rewards.String), &table) // a bad table reads as none
		}
		if r.Bounty { // what a target that pays nothing "pays" is beside the point
			r.PayMin, r.PayMax, r.PayFrom = targetBounty(table, r.Value, floatPtr(listMin), floatPtr(listMax), floatPtr(statsTop))
		}
		r.PaidAll, r.PaidAllEstimated = floatPtr(paidAll), !paid.Valid && paidAll.Valid
		r.Rewarded, r.AvgPayout = intPtr(rewarded), floatPtr(avg)
		if f.Kind == scope.KindRoot {
			r.Kind = scope.KindRoot
		}
		r.InScope, r.AddedAt = true, parseTSPtr(sql.NullString{String: added, Valid: true})
		if err := json.Unmarshal([]byte(reasons), &r.Reasons); err != nil {
			return nil, 0, fmt.Errorf("target %s reasons: %w", r.Value, err)
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, err
	}
	if f.Order == TargetsByBounty {
		out = byBounty(out, f.Reverse, limit, offset)
	}
	return out, total, nil
}

// byBounty sorts rows highest bounty first (the top of the range, then
// the bottom; turned around if reverse), rows without one last either way,
// and returns the page at offset.
func byBounty(rows []TargetRow, reverse bool, limit, offset int) []TargetRow {
	slices.SortStableFunc(rows, func(a, b TargetRow) int { return cmpBounty(a, b, reverse) })
	rows = rows[min(len(rows), max(0, offset)):]
	return rows[:min(len(rows), limit)]
}

// cmpBounty orders a before b if it pays more (the top of its range, then
// the bottom; less if reverse), rows without a bounty last either way.
func cmpBounty(a, b TargetRow, reverse bool) int {
	pay := func(v *float64) float64 {
		if v == nil {
			return 0
		}
		return *v
	}
	if (a.PayMax == nil) != (b.PayMax == nil) { // no bounty: last either way
		if a.PayMax == nil {
			return 1
		}
		return -1
	}
	c := cmp.Or(cmp.Compare(pay(b.PayMax), pay(a.PayMax)), cmp.Compare(pay(b.PayMin), pay(a.PayMin)))
	if reverse {
		return -c
	}
	return c
}

// EachBestTarget calls fn with each distinct scored target value matching
// f, best first (by its best program).
func (s *Store) EachBestTarget(ctx context.Context, f TargetFilter, fn func(value string) error) error {
	if len(f.AssetTypes) > 0 { // not scored: by identifier
		return s.eachAsset(ctx, f, fn)
	}
	if f.OutOfScope {
		return nil
	}
	from, args := bestFrom(f, f.Query)
	value := "ts.value"
	if f.Kind == scope.KindRoot {
		value = "ts.apex"
	}
	lim, largs := f.page()
	//nolint:gosec // constant fragments; values are bound parameters
	rows, err := s.db.R.QueryContext(ctx, `SELECT `+value+from+` GROUP BY `+value+` ORDER BY MAX(ts.score) DESC, `+value+lim, append(args, largs...)...)
	if err != nil {
		return err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var v string
		if err := rows.Scan(&v); err != nil {
			return err
		}
		if err := fn(v); err != nil {
			return err
		}
	}
	return rows.Err()
}

// bestFrom is the FROM and WHERE of target score queries for f.
func bestFrom(f TargetFilter, query string) (string, []any) {
	conds := []string{"p.removed_at IS NULL"}
	var args []any
	value := "ts.value"
	switch {
	case len(f.Kinds) > 0:
		var kinds []string
		for _, k := range f.Kinds {
			if k == scope.KindApp {
				kinds = append(kinds, scope.AssetAndroid, scope.AssetIOS)
			} else {
				kinds = append(kinds, k)
			}
		}
		conds = append(conds, "ts.kind IN ("+placeholders(len(kinds))+")")
		for _, k := range kinds {
			args = append(args, k)
		}
	case f.Kind == "":
	case f.Kind == scope.KindRoot:
		conds = append(conds, "ts.kind IN "+webKinds, "ts.apex <> ''")
		value = "ts.apex"
	case f.Kind == scope.KindApp:
		conds = append(conds, "ts.kind IN "+appKinds)
	default:
		conds = append(conds, "ts.kind = ?")
		args = append(args, f.Kind)
	}
	if f.Platform != "" {
		conds = append(conds, "p.platform = ?")
		args = append(args, f.Platform)
	}
	if f.BountyOnly {
		conds = append(conds, "ts.bounty = 1")
	}
	if f.PrivateOnly {
		conds = append(conds, "p.private = 1")
	}
	if f.Tag != "" {
		conds = append(conds, tagCond)
		args = append(args, f.Tag)
	}
	if !f.AddedSince.IsZero() {
		conds = append(conds, "ts.added_at >= ?")
		args = append(args, ts(f.AddedSince))
	}
	if !f.ChangedSince.IsZero() {
		conds = append(conds, `EXISTS (SELECT 1 FROM scope_targets t JOIN scope_entries e ON e.id = t.scope_entry_id
			WHERE t.kind = ts.kind AND t.value = ts.value AND e.program_id = ts.program_id AND e.removed_at IS NULL AND e.updated_at > ?)`)
		args = append(args, ts(f.ChangedSince))
	}
	if q := strings.TrimSpace(query); q != "" {
		conds = append(conds, `lower(`+value+`) LIKE ? ESCAPE '\'`)
		args = append(args, likeEscape(q))
	}
	if c, cargs := excludeCond(f.Exclude, value); c != "" {
		conds, args = append(conds, c), append(args, cargs...)
	}
	return ` FROM target_scores ts JOIN programs p ON p.id = ts.program_id
		LEFT JOIN program_public_stats ps ON ps.program_id = p.id AND ps.captured_at = p.stats_at
		WHERE ` + strings.Join(conds, " AND "), args
}

// Where a target's bounty range comes from.
const (
	PayFromTable   = "table"   // the program's bounty table
	PayFromListing = "listing" // the range the platform's listing gives
	PayFromStats   = "stats"   // only the most the program has paid, from its stats
)

// targetBounty is the bounty range a program gives for a target, or for
// the whole program if value is "": from its bounty table (the row for that
// asset if there is one, else every row), else the range its listing gives,
// else up to the most it has paid, else the lowest its listing gives (a
// program added by hand may say only that: hi is nil).
func targetBounty(table []platform.RewardRow, value string, listMin, listMax, statsTop *float64) (lo, hi *float64, from string) {
	rows := table
	for _, r := range table {
		if value != "" && strings.EqualFold(r.Scope, value) {
			rows = []platform.RewardRow{r}
			break
		}
	}
	var low, high float64
	for _, r := range rows {
		for _, rr := range []platform.RewardRange{r.Low, r.Medium, r.High, r.Critical} {
			for _, v := range []float64{rr.Min, rr.Max} {
				if v > 0 && (low == 0 || v < low) {
					low = v
				}
			}
			high = max(high, rr.Max)
		}
	}
	if high > 0 {
		return &low, &high, PayFromTable
	}
	if listMax != nil && *listMax > 0 {
		if listMin != nil && *listMin > 0 {
			return listMin, listMax, PayFromListing
		}
		return nil, listMax, PayFromListing
	}
	if statsTop != nil && *statsTop > 0 {
		return nil, statsTop, PayFromStats
	}
	if listMin != nil && *listMin > 0 {
		return listMin, nil, PayFromListing
	}
	return nil, nil, ""
}
