package store

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"
)

// liveInScope limits scope entries e of programs p to live in-scope ones.
const liveInScope = `e.in_scope = 1 AND e.removed_at IS NULL AND p.removed_at IS NULL`

// accessAt is when the user got access to private program p: when they
// joined it, else when HuntDeck first listed it as private (a program in
// the platform's first import has no such change, so it never counts).
const accessAt = `COALESCE(p.joined_at, ` + listedPrivate + `)`

// listedPrivate is when HuntDeck recorded program p as new and private;
// NULL for programs of the platform's first import, or of an account
// newly signed in (see syncer.syncPrograms), which were the user's already.
const listedPrivate = `(SELECT MIN(c.occurred_at) FROM changes c
	WHERE c.program_id = p.id AND c.kind = 'program_added' AND c.old_value = '' AND c.new_value = 'private')`

// recentEntries returns live in-scope entries added since since, with
// their targets, marking those that brought a root domain the program had
// never had in scope. Dates that are only an upper bound ("there by then")
// don't count as recent: the entry may be much older.
func (s *Store) recentEntries(ctx context.Context, since time.Time) ([]ScoreEntry, error) {
	newRoots := map[int64]map[string]bool{}
	rows, err := s.db.R.QueryContext(ctx, `
		SELECT DISTINCT e.program_id, t.apex
		FROM scope_entries e
		JOIN programs p ON p.id = e.program_id
		JOIN scope_targets t ON t.scope_entry_id = e.id
		WHERE e.in_scope = 1 AND e.removed_at IS NULL AND e.added_at >= ?1 AND e.added_bound = 0 AND p.removed_at IS NULL
			AND t.kind IN `+webKinds+` AND t.apex <> ''
			AND NOT EXISTS (SELECT 1 FROM scope_targets t2 JOIN scope_entries e2 ON e2.id = t2.scope_entry_id
				WHERE t2.program_id = t.program_id AND t2.kind IN `+webKinds+` AND t2.apex <> '' AND t2.apex = t.apex
					AND e2.in_scope = 1 AND (e2.added_at IS NULL OR e2.added_at < ?1 OR e2.added_bound = 1))`, ts(since))
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var id int64
		var apex string
		if err := rows.Scan(&id, &apex); err != nil {
			_ = rows.Close()
			return nil, err
		}
		if newRoots[id] == nil {
			newRoots[id] = map[string]bool{}
		}
		newRoots[id][apex] = true
	}
	if err := errors.Join(rows.Err(), rows.Close()); err != nil {
		return nil, err
	}
	rs, err := s.scoreEntries(ctx, `e.added_at >= ?1 AND e.added_bound = 0`, `e.added_at`, ts(since))
	out := make([]ScoreEntry, len(rs))
	for i, e := range rs {
		for _, a := range e.apexes {
			e.NewRoot = e.NewRoot || newRoots[e.ProgramID][a]
		}
		out[i] = e.ScoreEntry
	}
	return out, err
}

// joinedEntries returns the live in-scope entries of private programs the
// user got access to since since (accessAt; not invitations still
// waiting) that were in scope by then, dated when they got access: a
// program can be old and its scope still new to the user. Entries the
// program added after that are recent ones instead.
func (s *Store) joinedEntries(ctx context.Context, since time.Time) ([]ScoreEntry, error) {
	rs, err := s.scoreEntries(ctx, `p.private = 1 AND p.invite_pending = 0 AND `+accessAt+` >= ?1
		AND (e.added_at IS NULL OR e.added_bound = 1 OR e.added_at < `+accessAt+`)`, accessAt, ts(since))
	out := make([]ScoreEntry, len(rs))
	for i, e := range rs {
		e.Joined = true
		out[i] = e.ScoreEntry
	}
	return out, err
}

// scoreEntry is a ScoreEntry with the root domains of its targets.
type scoreEntry struct {
	ScoreEntry
	apexes []string
}

// scoreEntries returns the live in-scope entries matching cond (on scope
// entry e of program p, with args), dated by added, with their targets.
func (s *Store) scoreEntries(ctx context.Context, cond, added string, args ...any) ([]scoreEntry, error) {
	// An app has no targets: it stands for itself, its identifier, with
	// how many other programs list the same app.
	//nolint:gosec // constant fragments; values are bound parameters
	rows, err := s.db.R.QueryContext(ctx, `
		SELECT e.id, e.program_id, e.identifier, e.asset_type, e.bounty_eligible, e.max_severity, `+added+`,
			COALESCE((SELECT GROUP_CONCAT(DISTINCT t.apex) FROM scope_targets t WHERE t.scope_entry_id = e.id AND t.apex <> ''), ''),
			CASE WHEN e.asset_type IN `+appKinds+` THEN (SELECT COUNT(DISTINCT e2.program_id)
				FROM scope_entries e2 JOIN programs p2 ON p2.id = e2.program_id
				WHERE e2.asset_type = e.asset_type AND e2.identifier = e.identifier AND e2.program_id <> e.program_id
					AND e2.in_scope = 1 AND e2.removed_at IS NULL AND p2.removed_at IS NULL) END
		FROM scope_entries e JOIN programs p ON p.id = e.program_id
		WHERE `+liveInScope+` AND `+cond+`
		ORDER BY 7 DESC, e.id`, args...)
	if err != nil {
		return nil, err
	}
	var out []scoreEntry
	byID := map[int64]int{}
	for rows.Next() {
		var e scoreEntry
		var at, apexes string
		var appShared sql.NullInt64
		if err := rows.Scan(&e.ID, &e.ProgramID, &e.Identifier, &e.AssetType, &e.Bounty, &e.MaxSeverity, &at, &apexes, &appShared); err != nil {
			_ = rows.Close()
			return nil, err
		}
		e.AddedAt = parseTS(at)
		if appShared.Valid {
			e.Targets = []ScoreTarget{{Kind: e.AssetType, Value: e.Identifier, Shared: int(appShared.Int64)}}
		}
		if apexes != "" {
			e.apexes = strings.Split(apexes, ",")
		}
		byID[e.ID] = len(out)
		out = append(out, e)
	}
	if err := errors.Join(rows.Err(), rows.Close()); err != nil {
		return nil, err
	}

	// Their targets, and how many other programs list each one.
	//nolint:gosec // constant fragments; values are bound parameters
	rows, err = s.db.R.QueryContext(ctx, `
		SELECT t.scope_entry_id, t.kind, t.value, t.apex,
			(SELECT COUNT(DISTINCT t2.program_id) FROM scope_targets t2 INDEXED BY scope_targets_kind
				JOIN scope_entries e2 ON e2.id = t2.scope_entry_id JOIN programs p2 ON p2.id = t2.program_id
				WHERE t2.kind = t.kind AND t2.value = t.value AND t2.program_id <> t.program_id
					AND e2.in_scope = 1 AND e2.removed_at IS NULL AND p2.removed_at IS NULL)
		FROM scope_targets t JOIN scope_entries e ON e.id = t.scope_entry_id JOIN programs p ON p.id = e.program_id
		WHERE `+liveInScope+` AND `+cond+`
		ORDER BY t.id`, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var id int64
		var t ScoreTarget
		if err := rows.Scan(&id, &t.Kind, &t.Value, &t.Apex, &t.Shared); err != nil {
			return nil, err
		}
		if i, ok := byID[id]; ok {
			out[i].Targets = append(out[i].Targets, t)
		}
	}
	return out, rows.Err()
}
