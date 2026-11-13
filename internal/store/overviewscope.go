package store

import (
	"context"
	"database/sql"
	"time"
)

// The Stats page's scope levels at each month's end: assets in scope and
// wildcards in scope, of today's open programs.

// inScopeAt reports whether a scope entry added at added (bound: there by
// the date a platform's history starts at, so all along) and removed at
// removed was in scope at end.
func inScopeAt(added, removed *time.Time, bound bool, end time.Time) bool {
	return (bound || added == nil || !added.After(end)) && (removed == nil || removed.After(end))
}

// assetsByMonth counts the in-scope assets (scope entries) of today's open
// programs at each of ends: added by then and not yet removed.
func (s *Store) assetsByMonth(ctx context.Context, args []any, ends []time.Time, asset func(platform string, i int)) error {
	return s.each(ctx, `SELECT p.platform, e.added_at, e.added_bound, e.removed_at FROM scope_entries e JOIN programs p ON p.id = e.program_id
		WHERE e.in_scope = 1 AND p.removed_at IS NULL AND p.state = 'open' AND `+statsScope, args,
		func(rows *sql.Rows) error {
			var p string
			var added, removed sql.NullString
			var bound bool
			if err := rows.Scan(&p, &added, &bound, &removed); err != nil {
				return err
			}
			from, gone := parseTSPtr(added), parseTSPtr(removed)
			for i, end := range ends {
				if inScopeAt(from, gone, bound, end) {
					asset(p, i)
				}
			}
			return nil
		})
}

// wildcardsByMonth counts the in-scope wildcards of today's open programs
// at each of ends, as the Targets page and the hunt board count them: the
// wildcard targets their scope entries expand to (an entry of another
// type can list one, and one entry several), once per program and value.
// An entry removed before HuntDeck saw it (from a platform's scope
// history) has no targets; a wildcard one counts as its identifier.
func (s *Store) wildcardsByMonth(ctx context.Context, args []any, ends []time.Time, wildcard func(platform string, i int)) error {
	type key struct {
		program int64
		value   string
	}
	seen := make([]map[key]bool, len(ends))
	for i := range seen {
		seen[i] = map[key]bool{}
	}
	return s.each(ctx, `SELECT p.platform, p.id, COALESCE(t.value, lower(e.identifier)), e.added_at, e.added_bound, e.removed_at
		FROM scope_entries e JOIN programs p ON p.id = e.program_id
		LEFT JOIN scope_targets t ON t.scope_entry_id = e.id AND t.kind = 'wildcard'
		WHERE e.in_scope = 1 AND p.removed_at IS NULL AND p.state = 'open' AND `+statsScope+`
			AND (t.id IS NOT NULL OR (e.asset_type = 'wildcard' AND e.removed_at IS NOT NULL
				AND NOT EXISTS (SELECT 1 FROM scope_targets x WHERE x.scope_entry_id = e.id)))`, args,
		func(rows *sql.Rows) error {
			var p string
			var k key
			var added, removed sql.NullString
			var bound bool
			if err := rows.Scan(&p, &k.program, &k.value, &added, &bound, &removed); err != nil {
				return err
			}
			from, gone := parseTSPtr(added), parseTSPtr(removed)
			for i, end := range ends {
				if inScopeAt(from, gone, bound, end) && !seen[i][k] {
					seen[i][k] = true
					wildcard(p, i)
				}
			}
			return nil
		})
}
