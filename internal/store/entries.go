package store

import (
	"context"
	"database/sql"
	"errors"
	"slices"
	"strconv"
	"time"

	"github.com/mdpsec/huntdeck/internal/platform"
	"github.com/mdpsec/huntdeck/internal/scope"
)

// EntryRow is a stored scope entry.
type EntryRow struct {
	ID          int64
	Key         string
	ContentHash string
	Removed     bool
	Entry       platform.ScopeEntry
}

// ScopeEntries returns every entry of a program, including removed ones.
func (t *Tx) ScopeEntries(ctx context.Context, programID int64) ([]EntryRow, error) {
	rows, err := t.tx.QueryContext(ctx, `
		SELECT id, entry_key, content_hash, removed_at IS NOT NULL, in_scope, asset_type, platform_asset_type, identifier,
			bounty_eligible, max_severity, instruction
		FROM scope_entries WHERE program_id = ?`, programID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []EntryRow
	for rows.Next() {
		var r EntryRow
		e := &r.Entry
		if err := rows.Scan(&r.ID, &r.Key, &r.ContentHash, &r.Removed, &e.InScope, &e.AssetType, &e.PlatformAssetType,
			&e.Identifier, &e.BountyEligible, &e.MaxSeverity, &e.Instruction); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// InsertEntry adds a new scope entry and returns its ID.
// Its added_at is the platform's date, or now unless baseline (the
// program's first scope sync, whose entries were already there).
func (t *Tx) InsertEntry(ctx context.Context, programID int64, key, hash string, e platform.ScopeEntry, baseline bool) (int64, error) {
	added := tsPtr(e.CreatedAt)
	if added == nil && !baseline {
		added = t.now
	}
	var id int64
	err := t.tx.QueryRowContext(ctx, `
		INSERT INTO scope_entries (program_id, entry_key, in_scope, asset_type, platform_asset_type, identifier,
			bounty_eligible, max_severity, instruction, platform_created_at, platform_updated_at, content_hash,
			first_seen_at, updated_at, added_at)
		VALUES (`+placeholders(15)+`) RETURNING id`,
		programID, key, b2i(e.InScope), e.AssetType, e.PlatformAssetType, e.Identifier, b2i(e.BountyEligible),
		e.MaxSeverity, e.Instruction, tsPtr(e.CreatedAt), tsPtr(e.UpdatedAt), hash, t.now, t.now, added).Scan(&id)
	return id, err
}

// UpdateEntry overwrites an entry's content and clears any removal. A
// revived entry (back after a removal) counts as added now, unless the
// platform dates it.
func (t *Tx) UpdateEntry(ctx context.Context, id int64, hash string, e platform.ScopeEntry, revived bool) error {
	added := tsPtr(e.CreatedAt)
	if added == nil && revived {
		added = t.now
	}
	_, err := t.tx.ExecContext(ctx, `
		UPDATE scope_entries SET in_scope = ?, asset_type = ?, platform_asset_type = ?, identifier = ?,
			bounty_eligible = ?, max_severity = ?, instruction = ?, platform_created_at = ?, platform_updated_at = ?,
			content_hash = ?, updated_at = ?, removed_at = NULL, added_at = COALESCE(?, added_at),
			added_bound = CASE WHEN ? IS NULL THEN added_bound ELSE 0 END
		WHERE id = ?`,
		b2i(e.InScope), e.AssetType, e.PlatformAssetType, e.Identifier, b2i(e.BountyEligible), e.MaxSeverity,
		e.Instruction, tsPtr(e.CreatedAt), tsPtr(e.UpdatedAt), hash, t.now, added, added, id)
	return err
}

// RecordArchivedEntry stores a scope entry the platform says the program
// removed at archivedAt, as history: a new removed entry, or a more exact
// removal time for one HuntDeck saw removed. Entries HuntDeck still has as
// live are left to the scope sync (removal rule). No change is recorded.
func (t *Tx) RecordArchivedEntry(ctx context.Context, programID int64, key, hash string, e platform.ScopeEntry, archivedAt time.Time) error {
	var id int64
	var removed sql.NullString
	err := t.tx.QueryRowContext(ctx, `SELECT id, removed_at FROM scope_entries WHERE program_id = ? AND entry_key = ?`,
		programID, key).Scan(&id, &removed)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return t.InsertHistoryEntry(ctx, programID, key, hash, e, e.CreatedAt, false, archivedAt)
	case err != nil:
		return err
	case !removed.Valid:
		return nil
	}
	return t.SetEntryDates(ctx, id, e.CreatedAt, false, &archivedAt)
}

// InsertHistoryEntry stores an entry the program removed before HuntDeck
// saw it, with when it was added (bound: on or before) and removed.
func (t *Tx) InsertHistoryEntry(ctx context.Context, programID int64, key, hash string, e platform.ScopeEntry,
	addedAt *time.Time, bound bool, removedAt time.Time) error {
	_, err := t.tx.ExecContext(ctx, `
		INSERT INTO scope_entries (program_id, entry_key, in_scope, asset_type, platform_asset_type, identifier,
			bounty_eligible, max_severity, instruction, platform_created_at, platform_updated_at, content_hash,
			first_seen_at, updated_at, added_at, added_bound, removed_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		programID, key, b2i(e.InScope), e.AssetType, e.PlatformAssetType, e.Identifier, b2i(e.BountyEligible),
		e.MaxSeverity, e.Instruction, tsPtr(e.CreatedAt), tsPtr(e.UpdatedAt), hash, t.now, t.now,
		tsPtr(addedAt), b2i(bound), ts(removedAt))
	return err
}

// SetEntryDates records when an entry was added (if given) and, for an
// entry HuntDeck has as removed, when it was removed. A date from the
// platform's own record (platform_created_at) is never replaced.
func (t *Tx) SetEntryDates(ctx context.Context, entryID int64, addedAt *time.Time, bound bool, removedAt *time.Time) error {
	if addedAt != nil {
		if _, err := t.tx.ExecContext(ctx, `
			UPDATE scope_entries SET added_at = ?, added_bound = ?
			WHERE id = ? AND (platform_created_at IS NULL OR platform_created_at = ?)`,
			ts(*addedAt), b2i(bound), entryID, ts(*addedAt)); err != nil {
			return err
		}
	}
	if removedAt != nil {
		if _, err := t.tx.ExecContext(ctx, `UPDATE scope_entries SET removed_at = ? WHERE id = ? AND removed_at IS NOT NULL`,
			ts(*removedAt), entryID); err != nil {
			return err
		}
	}
	return nil
}

// MarkEntryRemoved records that an entry disappeared from a complete scope.
// RekeyEntry files an entry under the key it's now listed by: for a
// platform without IDs, whose keys hash the identifier, one that only
// gained or lost a trailing slash.
func (t *Tx) RekeyEntry(ctx context.Context, id int64, key string) error {
	_, err := t.tx.ExecContext(ctx, `UPDATE scope_entries SET entry_key = ? WHERE id = ?`, key, id)
	return err
}

func (t *Tx) MarkEntryRemoved(ctx context.Context, id int64) error {
	_, err := t.tx.ExecContext(ctx, `UPDATE scope_entries SET removed_at = ?, updated_at = ? WHERE id = ?`, t.now, t.now, id)
	return err
}

// ReplaceTargets swaps an entry's derived targets.
func (t *Tx) ReplaceTargets(ctx context.Context, entryID int64, targets []scope.Target) error {
	if _, err := t.tx.ExecContext(ctx, `DELETE FROM scope_targets WHERE scope_entry_id = ?`, entryID); err != nil {
		return err
	}
	for _, tg := range targets {
		if _, err := t.tx.ExecContext(ctx, `
			INSERT INTO scope_targets (scope_entry_id, program_id, kind, value, host, apex)
			VALUES (?1, (SELECT program_id FROM scope_entries WHERE id = ?1), ?2, ?3, ?4, ?5)`,
			entryID, tg.Kind, tg.Value, tg.Host, tg.Apex); err != nil {
			return err
		}
	}
	return nil
}

// ScopeVersionSetting is the settings key holding the scope.Version stored
// targets were derived with.
const ScopeVersionSetting = "scope.version"

// Retarget derives every entry's targets again with derive, the parser of
// scope version, unless that version derived them already. It replaces the
// targets that differ, and the cached target counts of their programs,
// and returns how many entries changed. Removed entries stored without
// targets (from platform history) stay without.
func (s *Store) Retarget(ctx context.Context, version int, derive func(assetType, identifier string) []scope.Target) (int, error) {
	var n int
	err := s.Write(ctx, func(tx *Tx) error {
		n = 0
		var stored string
		err := tx.tx.QueryRowContext(ctx, `SELECT value FROM settings WHERE key = ?`, ScopeVersionSetting).Scan(&stored)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if stored == strconv.Itoa(version) {
			return nil
		}
		old := map[int64][]scope.Target{}
		rows, err := tx.tx.QueryContext(ctx, `SELECT scope_entry_id, kind, value, host, apex FROM scope_targets ORDER BY id`)
		if err != nil {
			return err
		}
		for rows.Next() {
			var id int64
			var t scope.Target
			if err := rows.Scan(&id, &t.Kind, &t.Value, &t.Host, &t.Apex); err != nil {
				_ = rows.Close()
				return err
			}
			old[id] = append(old[id], t)
		}
		if err := errors.Join(rows.Err(), rows.Close()); err != nil {
			return err
		}
		type entry struct {
			id, programID         int64
			assetType, identifier string
			removed               bool
		}
		var entries []entry
		rows, err = tx.tx.QueryContext(ctx, `SELECT id, program_id, asset_type, identifier, removed_at IS NOT NULL FROM scope_entries ORDER BY id`)
		if err != nil {
			return err
		}
		for rows.Next() {
			var e entry
			if err := rows.Scan(&e.id, &e.programID, &e.assetType, &e.identifier, &e.removed); err != nil {
				_ = rows.Close()
				return err
			}
			entries = append(entries, e)
		}
		if err := errors.Join(rows.Err(), rows.Close()); err != nil {
			return err
		}
		var changed []int64 // programs, in order
		for _, e := range entries {
			if e.removed && len(old[e.id]) == 0 {
				continue
			}
			cur := derive(e.assetType, e.identifier)
			if slices.Equal(cur, old[e.id]) {
				continue
			}
			if err := tx.ReplaceTargets(ctx, e.id, cur); err != nil {
				return err
			}
			n++
			if !slices.Contains(changed, e.programID) {
				changed = append(changed, e.programID)
			}
		}
		for _, id := range changed {
			if err := tx.RefreshScopeCounts(ctx, id); err != nil {
				return err
			}
		}
		return tx.PutSetting(ctx, ScopeVersionSetting, strconv.Itoa(version))
	})
	return n, err
}
