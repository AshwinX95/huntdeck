package store

import (
	"context"
	"database/sql"
	"fmt"
	"slices"
	"time"
)

// The Stats page's platform overview: each platform's programs and scope
// now, and its numbers per month for a year. Ignored programs count: the
// overview describes the platform, not the user's list.

// PlatformOverviews returns every platform's overview, not Independent's
// (programs added by hand), (platformName's
// only, unless "") with months per month up to now's, starting at start
// (the first of a month, UTC), counting the programs of visibility
// ("public", "private", or "" for both).
func (s *Store) PlatformOverviews(ctx context.Context, now, start time.Time, months int, platformName, visibility string) ([]PlatformOverview, error) {
	got, order, err := s.monthSeries(ctx, now, start, months, platformName, visibility, false)
	if err != nil {
		return nil, err
	}
	out := make([]PlatformOverview, 0, len(order))
	for _, k := range order {
		if got[k].Platform != Independent { // programs added by hand: no platform, and no stats to compare
			out = append(out, *got[k])
		}
	}
	return out, nil
}

// ProgramSeries returns each live program's months on a platform, as
// PlatformOverviews has them for the whole platform, by program ID. Open,
// Paying, Assets and Launched are left at zero: the Programs list has a
// program's own.
func (s *Store) ProgramSeries(ctx context.Context, platformName, visibility string, now, start time.Time, months int) (map[int64]PlatformOverview, error) {
	got, _, err := s.monthSeries(ctx, now, start, months, platformName, visibility, true)
	if err != nil {
		return nil, err
	}
	out := make(map[int64]PlatformOverview, len(got))
	for k, o := range got {
		out[k.ProgramID] = *o
	}
	return out, nil
}

// statsScope limits the Stats pages' queries to program p's platform
// (:platform, "" for all) and visibility (:visibility: "public",
// "private", or "" for both), as it is now.
const statsScope = `(:platform = '' OR p.platform = :platform) AND (:visibility = '' OR p.private = (:visibility = 'private'))`

// launchedExpr is when program p launched: the date its platform gives;
// for YesWeHack, which gives none, the date of its first version, where
// its version history starts (the oldest version's entries are bound to
// it). Bugcrowd's brief history often starts at its move to the new brief
// format instead, so it isn't used.
const launchedExpr = `COALESCE(p.started_at, CASE WHEN p.platform = 'yeswehack' THEN
	(SELECT MIN(e.added_at) FROM scope_entries e WHERE e.program_id = p.id AND e.added_bound = 1) END)`

// paidUSD says program p's bounties count in the months' Bounties: it pays
// in USD (other currencies go by the rises in its total paid), and pays
// bounties now. A program counts as it is now, as for visibility, and the
// 90-day Paid figure leaves out programs that stopped paying too.
const paidUSD = `(p.currency IN ('', 'USD') AND p.offers_bounty = 1)`

// seriesKey is what monthSeries adds up to: a platform, or one of its
// programs.
type seriesKey struct {
	Platform  string
	ProgramID int64
}

// monthSeries reads the numbers behind PlatformOverviews and
// ProgramSeries: by platform, or by program (byProgram) on platformName
// ("" for every platform), in the order first seen.
func (s *Store) monthSeries(ctx context.Context, now, start time.Time, months int, platformName, visibility string, byProgram bool) (map[seriesKey]*PlatformOverview, []seriesKey, error) {
	got := map[seriesKey]*PlatformOverview{}
	var order []seriesKey
	get := func(p string, id int64) *PlatformOverview {
		k := seriesKey{Platform: p}
		if byProgram {
			k.ProgramID = id
		}
		o, ok := got[k]
		if !ok {
			o = &PlatformOverview{Platform: p, Months: make([]PlatformMonth, months)}
			got[k] = o
			order = append(order, k)
		}
		return o
	}
	month := func(at string) int {
		t := parseTS(at)
		if t.IsZero() || t.Before(start) {
			return -1
		}
		i := (t.Year()-start.Year())*12 + int(t.Month()-start.Month())
		if i >= months {
			return -1
		}
		return i
	}
	// :start is start, :platform the platform ("" for all), :visibility
	// the programs' (statsScope), :since30 thirty days before now.
	args := []any{sql.Named("start", ts(start)), sql.Named("platform", platformName), sql.Named("visibility", visibility),
		sql.Named("since30", ts(now.Add(-30*24*time.Hour)))}
	const live = ` p.removed_at IS NULL AND ` + statsScope
	hidden, err := s.hiddenBountyAverages(ctx, `p.removed_at IS NULL AND `+statsScope, args...)
	if err != nil {
		return nil, nil, err
	}
	scoped := map[[2]int64]bool{} // program and month with scope added
	totals := map[int64]int{}     // a program's last accepted reports total
	queries := []struct {
		sql  string
		scan func(rows *sql.Rows) error
	}{
		{`SELECT p.platform, p.id, p.state = 'open', p.offers_bounty, p.n_in_scope, ` + launchedExpr + ` IS NOT NULL,
				EXISTS (SELECT 1 FROM resolved_reports r WHERE r.program_id = p.id), p.accepted_since
			FROM programs p WHERE` + live + ` ORDER BY p.platform, p.id`,
			func(rows *sql.Rows) error {
				var p string
				var id int64
				var open, paying, launches, resolved bool
				var assets int
				var since sql.NullString
				if err := rows.Scan(&p, &id, &open, &paying, &assets, &launches, &resolved, &since); err != nil {
					return err
				}
				o := get(p, id)
				if open && !byProgram {
					o.Open++
					o.Assets += assets
					if paying {
						o.Paying++
					}
				}
				o.Launches = o.Launches || (launches && !byProgram)
				switch {
				case resolved:
					o.ValidKind = "resolved"
				case since.Valid:
					o.ValidKind = "accepted"
					if t := parseTSPtr(since); o.ValidSince == nil || t.Before(*o.ValidSince) {
						o.ValidSince = t
					}
				}
				return nil
			}},
		{`SELECT p.platform, p.id, COALESCE(SUM(e.added_at >= :since30 AND e.added_bound = 0), 0), COALESCE(SUM(e.removed_at >= :since30), 0)
			FROM scope_entries e JOIN programs p ON p.id = e.program_id
			WHERE e.in_scope = 1 AND` + live + ` AND ((e.added_at >= :since30 AND e.added_bound = 0) OR e.removed_at >= :since30)
			GROUP BY p.platform, p.id`,
			func(rows *sql.Rows) error {
				var p string
				var id int64
				var added, removed int
				if err := rows.Scan(&p, &id, &added, &removed); err != nil {
					return err
				}
				o := get(p, id)
				o.Added30, o.Removed30 = o.Added30+added, o.Removed30+removed
				return nil
			}},
		{`SELECT p.platform, p.id, e.added_at FROM scope_entries e JOIN programs p ON p.id = e.program_id
			WHERE e.in_scope = 1 AND e.added_bound = 0 AND e.added_at >= :start AND` + live,
			func(rows *sql.Rows) error {
				var p, at string
				var id int64
				if err := rows.Scan(&p, &id, &at); err != nil {
					return err
				}
				if i := month(at); i >= 0 {
					get(p, id).Months[i].ScopeAdded++
					if k := [2]int64{id, int64(i)}; !byProgram && !scoped[k] {
						scoped[k] = true
						get(p, id).Months[i].ScopePrograms++
					}
				}
				return nil
			}},
		{`SELECT p.platform, p.id, d.disclosed_at FROM disclosed_reports d JOIN programs p ON p.id = d.program_id
			WHERE d.disclosed_at >= :start AND` + live,
			func(rows *sql.Rows) error {
				var p, at string
				var id int64
				if err := rows.Scan(&p, &id, &at); err != nil {
					return err
				}
				if i := month(at); i >= 0 && !byProgram {
					get(p, id).Months[i].Disclosed++
				}
				return nil
			}},
		{`SELECT platform, id, launched FROM (SELECT p.platform, p.id, ` + launchedExpr + ` AS launched FROM programs p WHERE` + live + `)
			WHERE launched >= :start`,
			func(rows *sql.Rows) error {
				var p, at string
				var id int64
				if err := rows.Scan(&p, &id, &at); err != nil {
					return err
				}
				if i := month(at); i >= 0 && !byProgram {
					get(p, id).Months[i].Launched++
				}
				return nil
			}},
		{`SELECT p.platform, p.id, r.resolved_at, r.submitted_at, r.action, r.bounty, ` + paidUSD + ` FROM resolved_reports r
			JOIN programs p ON p.id = r.program_id WHERE r.resolved_at >= :start AND` + live,
			func(rows *sql.Rows) error {
				var p, at, action string
				var id int64
				var sub sql.NullString
				var bounty sql.NullFloat64
				var usd bool
				if err := rows.Scan(&p, &id, &at, &sub, &action, &bounty, &usd); err != nil {
					return err
				}
				i := month(at)
				if i < 0 {
					return nil
				}
				m := &get(p, id).Months[i]
				m.Valid++
				if usd {
					avg, hides := hidden[id]
					v, est := estimatedBounty(action, bounty, avg, hides)
					m.Bounties += v
					m.BountiesEstimated = m.BountiesEstimated || est
				}
				if sub.Valid && action == "resolved" {
					if d := parseTS(at).Sub(parseTS(sub.String)).Hours() / 24; d >= 0 {
						m.ResolveDays = append(m.ResolveDays, d)
					}
				}
				return nil
			}},
		{`SELECT p.platform, p.id, a.accepted_at, a.submitted_at, a.resolved_at, a.bounty, ` + paidUSD + ` FROM accepted_reports a
			JOIN programs p ON p.id = a.program_id WHERE (a.accepted_at >= :start OR a.resolved_at >= :start)
				AND p.accepted_since IS NOT NULL AND` + live, // as the programs' own rows count them (MarkAcceptedListed)
			func(rows *sql.Rows) error {
				var p, at string
				var id int64
				var sub, resolved sql.NullString
				var bounty sql.NullFloat64
				var usd bool
				if err := rows.Scan(&p, &id, &at, &sub, &resolved, &bounty, &usd); err != nil {
					return err
				}
				o := get(p, id)
				if i := month(at); i >= 0 {
					o.Months[i].Valid++
					if usd {
						o.Months[i].Bounties += bounty.Float64
					}
				}
				if resolved.Valid && sub.Valid {
					if i := month(resolved.String); i >= 0 {
						if d := parseTS(resolved.String).Sub(parseTS(sub.String)).Hours() / 24; d >= 0 {
							o.Months[i].ResolveDays = append(o.Months[i].ResolveDays, d)
						}
					}
				}
				return nil
			}},
		// Where a platform publishes only a running total of a program's
		// accepted reports (Intigriti), the rises between saved days are
		// its accepted reports, counted from the first day saved.
		{`SELECT p.platform, p.id, s.captured_at, s.accepted_reports FROM program_public_stats s JOIN programs p ON p.id = s.program_id
			WHERE s.accepted_reports IS NOT NULL AND p.accepted_since IS NULL
				AND NOT EXISTS (SELECT 1 FROM resolved_reports r WHERE r.program_id = p.id) AND` + live + `
			ORDER BY p.id, s.captured_at`,
			func(rows *sql.Rows) error {
				var p, at string
				var id int64
				var n int
				if err := rows.Scan(&p, &id, &at, &n); err != nil {
					return err
				}
				o := get(p, id)
				if o.ValidKind == "" {
					o.ValidKind = "accepted"
				}
				if last, ok := totals[id]; ok {
					if i := month(at); i >= 0 && n > last {
						o.Months[i].Valid += n - last
					}
				} else if t := parseTS(at); o.ValidSince == nil || t.Before(*o.ValidSince) {
					o.ValidSince = &t
				}
				totals[id] = n
				return nil
			}},
	}
	for _, q := range queries {
		if err := s.each(ctx, q.sql, args, q.scan); err != nil {
			return nil, nil, fmt.Errorf("platform overview: %w", err)
		}
	}
	if err := s.estimateValid(ctx, args, live, month, get, start); err != nil {
		return nil, nil, fmt.Errorf("platform overview: estimated valid reports: %w", err)
	}
	if err := s.countPaidRises(ctx, args, live, month, get); err != nil {
		return nil, nil, fmt.Errorf("platform overview: paid per month: %w", err)
	}
	if err := s.estimateFromLeaderboards(ctx, args, live, month, get, start); err != nil {
		return nil, nil, fmt.Errorf("platform overview: valid reports from leaderboards: %w", err)
	}
	if !byProgram {
		ends := make([]time.Time, months)
		for i := range ends {
			ends[i] = start.AddDate(0, i+1, 0)
		}
		ends[months-1] = now
		err := s.openByMonth(ctx, args, ends, func(p string, i int, paying bool) {
			m := &get(p, 0).Months[i]
			m.Open++
			if paying {
				m.Paying++
			}
		})
		if err != nil {
			return nil, nil, fmt.Errorf("platform overview: open programs: %w", err)
		}
		err = s.assetsByMonth(ctx, args, ends, func(p string, i int) { get(p, 0).Months[i].Assets++ })
		if err != nil {
			return nil, nil, fmt.Errorf("platform overview: assets: %w", err)
		}
		err = s.wildcardsByMonth(ctx, args, ends, func(p string, i int) { get(p, 0).Months[i].Wildcards++ })
		if err != nil {
			return nil, nil, fmt.Errorf("platform overview: wildcards: %w", err)
		}
	}
	return got, order, nil
}

// openByMonth counts each program open at each of ends, working back from
// its state now through its status changes (Intigriti's go back years;
// the rest start when HuntDeck first polled), its removal and when it was
// added or launched, and whether it paid bounties then, back through its
// bounty changes. A program without a launch date or an "added" change
// counts as there all along.
func (s *Store) openByMonth(ctx context.Context, args []any, ends []time.Time, open func(platform string, i int, paying bool)) error {
	type event struct {
		at time.Time
		to string // the state before it, or for a bounty change whether it paid: "true", "false"
		// bounty marks a change to whether it pays bounties.
		bounty bool
	}
	type prog struct {
		platform, state string
		paying          bool
		started         *time.Time
		events          []event // newest first
	}
	progs := map[int64]*prog{}
	var ids []int64
	err := s.each(ctx, `SELECT p.id, p.platform, p.state, p.offers_bounty, p.removed_at, `+launchedExpr+` FROM programs p
		WHERE `+statsScope, args, func(rows *sql.Rows) error {
		var id int64
		var p prog
		var removed, started sql.NullString
		if err := rows.Scan(&id, &p.platform, &p.state, &p.paying, &removed, &started); err != nil {
			return err
		}
		p.started = parseTSPtr(started)
		if removed.Valid {
			p.events = append(p.events, event{at: parseTS(removed.String), to: p.state})
			p.state = "removed"
		}
		progs[id] = &p
		ids = append(ids, id)
		return nil
	})
	if err != nil {
		return err
	}
	err = s.each(ctx, `SELECT c.program_id, c.occurred_at, c.kind, c.old_value FROM changes c JOIN programs p ON p.id = c.program_id
		WHERE c.kind IN ('state_changed', 'program_added', 'bounty_changed') AND c.occurred_at >= :start AND `+statsScope, args,
		func(rows *sql.Rows) error {
			var id int64
			var at, kind, old string
			if err := rows.Scan(&id, &at, &kind, &old); err != nil {
				return err
			}
			if p := progs[id]; p != nil {
				e := event{at: parseTS(at), to: old, bounty: kind == ChangeBountyChanged}
				if kind == ChangeProgramAdded {
					e.to = "absent"
				}
				p.events = append(p.events, e)
			}
			return nil
		})
	if err != nil {
		return err
	}
	for _, id := range ids {
		p := progs[id]
		slices.SortFunc(p.events, func(a, b event) int { return b.at.Compare(a.at) })
		for i, end := range ends {
			state, paying := p.state, p.paying
			for _, e := range p.events {
				if !e.at.After(end) {
					break
				}
				if e.bounty {
					paying = e.to == "true"
				} else {
					state = e.to
				}
			}
			if state == "open" && (p.started == nil || !p.started.After(end)) {
				open(p.platform, i, paying)
			}
		}
	}
	return nil
}

// each runs a query and calls scan for every row.
func (s *Store) each(ctx context.Context, q string, args []any, scan func(*sql.Rows) error) error {
	rows, err := s.db.R.QueryContext(ctx, q, args...)
	if err != nil {
		return err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		if err := scan(rows); err != nil {
			return err
		}
	}
	return rows.Err()
}

// OpenByVisibility counts platformName's open programs ("" for every
// platform's, leaving out programs added by hand as Stats does), public
// and private.
func (s *Store) OpenByVisibility(ctx context.Context, platformName string) (public, private int, err error) {
	err = s.each(ctx, `SELECT p.private, COUNT(*) FROM programs p
		WHERE p.removed_at IS NULL AND p.state = 'open' AND (:platform = '' AND p.platform <> :independent OR p.platform = :platform)
		GROUP BY p.private`, []any{sql.Named("platform", platformName), sql.Named("independent", Independent)}, func(rows *sql.Rows) error {
		var priv bool
		var n int
		if err := rows.Scan(&priv, &n); err != nil {
			return err
		}
		if priv {
			private = n
		} else {
			public = n
		}
		return nil
	})
	return public, private, err
}
