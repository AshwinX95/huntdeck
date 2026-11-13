package store

import (
	"context"
	"database/sql"
	"time"
)

// BoardChange is a change as the hunt board shows it: with its program and,
// for scope changes, the entry it is about.
type BoardChange struct {
	ChangeView
	Identifier string // the scope entry's, for scope changes
	AssetType  string
}

// BoardChanges returns the changes since since on programs the user hasn't
// ignored, newest first, at most limit: program state changes when states
// is set, every other kind when not.
func (s *Store) BoardChanges(ctx context.Context, since time.Time, states bool, limit int) ([]BoardChange, error) {
	op := "<>"
	if states {
		op = "="
	}
	//nolint:gosec // op is one of two constants; values are bound parameters
	rows, err := s.db.R.QueryContext(ctx, `
		SELECT c.id, c.occurred_at, c.program_id, c.kind, c.field, COALESCE(c.old_value, ''), COALESCE(c.new_value, ''),
			p.platform, p.handle, p.name, p.url, p.private, p.state, COALESCE(e.identifier, ''), COALESCE(e.asset_type, '')
		FROM changes c JOIN programs p ON p.id = c.program_id
		LEFT JOIN scope_entries e ON e.id = c.scope_entry_id
		WHERE c.occurred_at >= ? AND c.kind `+op+` ? AND p.ignored = 0
		ORDER BY c.occurred_at DESC, c.id DESC LIMIT ?`, ts(since), ChangeStateChanged, limit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []BoardChange
	for rows.Next() {
		var c BoardChange
		var at string
		p := &c.Program
		if err := rows.Scan(&c.ID, &at, &c.ProgramID, &c.Kind, &c.Field, &c.OldValue, &c.NewValue,
			&p.Platform, &p.Handle, &p.Name, &p.URL, &p.Private, &p.State, &c.Identifier, &c.AssetType); err != nil {
			return nil, err
		}
		c.OccurredAt = parseTS(at)
		p.ID = c.ProgramID
		out = append(out, c)
	}
	return out, rows.Err()
}

// BoardCounts are the hunt board's "this week" numbers.
type BoardCounts struct {
	ScopeAdded    int // in-scope entries added
	ScopePrograms int // programs they were added to
	Wildcards     int // wildcards among them
	Domains       int // domains among them
	Programs      int // programs listed for the first time
	Invites       int // private ones among them
	Bounty        int // bounty changes: paying started or stopped, or the bounty table changed
}

// BoardCounts counts what's new since since, leaving out ignored programs.
func (s *Store) BoardCounts(ctx context.Context, since time.Time) (BoardCounts, error) {
	var c BoardCounts
	var scopePrograms sql.NullInt64
	err := s.db.R.QueryRowContext(ctx, `
		SELECT
			COUNT(*) FILTER (WHERE c.kind = ? AND c.field = 'in_scope'),
			COUNT(DISTINCT CASE WHEN c.kind = ? AND c.field = 'in_scope' THEN c.program_id END),
			COUNT(*) FILTER (WHERE c.kind = ? AND c.field = 'in_scope' AND e.asset_type = 'wildcard'),
			COUNT(*) FILTER (WHERE c.kind = ? AND c.field = 'in_scope' AND e.asset_type = 'domain'),
			COUNT(*) FILTER (WHERE c.kind = ?),
			COUNT(*) FILTER (WHERE c.kind = ? AND c.new_value = 'private'),
			COUNT(*) FILTER (WHERE c.kind IN (?, ?))
		FROM changes c JOIN programs p ON p.id = c.program_id
		LEFT JOIN scope_entries e ON e.id = c.scope_entry_id
		WHERE c.occurred_at >= ? AND p.ignored = 0`,
		ChangeScopeAdded, ChangeScopeAdded, ChangeScopeAdded, ChangeScopeAdded, ChangeProgramAdded, ChangeProgramAdded,
		ChangeBountyChanged, ChangeRewardsChanged, ts(since)).
		Scan(&c.ScopeAdded, &scopePrograms, &c.Wildcards, &c.Domains, &c.Programs, &c.Invites, &c.Bounty)
	c.ScopePrograms = int(scopePrograms.Int64)
	return c, err
}
