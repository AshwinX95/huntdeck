package store

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/mdpsec/huntdeck/internal/platform"
)

// Independent is the platform name of programs added by hand (see
// internal/platform/independent, which can't be imported here).
const Independent = "independent"

// OwnProgram is a program added by hand, as its listing has it, with its
// live scope.
type OwnProgram struct {
	ID      int64
	Program platform.Program
	Scope   []platform.ScopeEntry
	InSeeds bool // shared in seeds (see seedPublic)
}

// OwnPrograms returns the live programs added by hand, oldest first: the
// listing an edit to one of them is applied to.
func (s *Store) OwnPrograms(ctx context.Context) ([]OwnProgram, error) {
	rows, err := s.db.R.QueryContext(ctx, `SELECT id, handle, name, url, private, state, offers_bounty, currency, bounty_min, bounty_max,
		policy, in_seeds FROM programs WHERE platform = ? AND removed_at IS NULL ORDER BY id`, Independent)
	if err != nil {
		return nil, fmt.Errorf("own programs: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []OwnProgram
	byID := map[int64]int{}
	for rows.Next() {
		var o OwnProgram
		var lo, hi sql.NullFloat64
		p := &o.Program
		if err := rows.Scan(&o.ID, &p.Handle, &p.Name, &p.URL, &p.Private, &p.State, &p.OffersBounty, &p.Currency, &lo, &hi,
			&p.Policy, &o.InSeeds); err != nil {
			return nil, err
		}
		if lo.Valid {
			p.BountyMin = &lo.Float64
		}
		if hi.Valid {
			p.BountyMax = &hi.Float64
		}
		byID[o.ID] = len(out)
		out = append(out, o)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	entries, err := s.db.R.QueryContext(ctx, `SELECT e.program_id, e.in_scope, e.asset_type, e.platform_asset_type, e.identifier,
		e.bounty_eligible, e.max_severity, e.instruction FROM scope_entries e JOIN programs p ON p.id = e.program_id
		WHERE p.platform = ? AND p.removed_at IS NULL AND e.removed_at IS NULL ORDER BY e.id`, Independent)
	if err != nil {
		return nil, fmt.Errorf("own programs' scope: %w", err)
	}
	defer func() { _ = entries.Close() }()
	for entries.Next() {
		var id int64
		var e platform.ScopeEntry
		if err := entries.Scan(&id, &e.InScope, &e.AssetType, &e.PlatformAssetType, &e.Identifier, &e.BountyEligible,
			&e.MaxSeverity, &e.Instruction); err != nil {
			return nil, err
		}
		if i, ok := byID[id]; ok {
			out[i].Scope = append(out[i].Scope, e)
		}
	}
	return out, entries.Err()
}

// SetOwnProgramExtras saves what an edit sets that a poll doesn't: whether
// the program is shared in seeds, and its policy, which a poll leaves as
// stored when the listing carries none (so clearing it takes this).
func (s *Store) SetOwnProgramExtras(ctx context.Context, programID int64, inSeeds bool, policy string) error {
	return s.Write(ctx, func(tx *Tx) error {
		_, err := tx.tx.ExecContext(ctx, `UPDATE programs SET in_seeds = ?, policy = CASE WHEN ? = '' THEN '' ELSE policy END
			WHERE id = ? AND platform = ?`, b2i(inSeeds), policy, programID, Independent)
		return err
	})
}

// HasPrograms reports whether platform has any programs stored, removed
// ones included.
func (s *Store) HasPrograms(ctx context.Context, platformName string) (bool, error) {
	var ok bool
	err := s.db.R.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM programs WHERE platform = ?)`, platformName).Scan(&ok)
	return ok, err
}
