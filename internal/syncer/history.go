package syncer

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/mdpsec/huntdeck/internal/platform"
	"github.com/mdpsec/huntdeck/internal/store"
)

// historyPerPoll bounds how many programs' scope history one full poll
// reads: it's a one-off per program, spread over a few polls.
const historyPerPoll = 100

// syncHistory reads the scope history of programs that don't have it yet
// and dates their entries. Each program is saved as it arrives.
func (s *Syncer) syncHistory(ctx context.Context, log *slog.Logger, sh platform.ScopeHistorian, platformName string) error {
	progs, ids, err := s.store.ScopeHistoryDue(ctx, platformName, historyPerPoll)
	if err != nil || len(progs) == 0 {
		return err
	}
	log.Info("reading scope history", "programs", len(progs))
	read := 0
	err = sh.ScopeHistory(ctx, progs, func(handle string, versions []platform.ScopeVersion) error {
		id, ok := ids[handle]
		if !ok {
			return nil
		}
		err := s.store.Write(ctx, func(tx *store.Tx) error {
			if err := applyHistory(ctx, tx, id, versions); err != nil {
				return err
			}
			return tx.MarkScopeHistory(ctx, id)
		})
		if err == nil {
			read++
		}
		return err
	})
	log.Info("scope history read", "programs", read, "of", len(progs))
	if err != nil {
		return fmt.Errorf("scope history: %w", err)
	}
	return nil
}

// syncStateHistory reads past state changes (pauses, resumes) of programs
// that don't have them yet. Each program is saved as it arrives.
func (s *Syncer) syncStateHistory(ctx context.Context, log *slog.Logger, sh platform.StateHistorian, platformName string) error {
	progs, ids, err := s.store.StateHistoryDue(ctx, platformName)
	if err != nil || len(progs) == 0 {
		return err
	}
	added := 0
	seen := map[string]bool{}
	err = sh.StateHistory(ctx, progs, func(handle string, changes []platform.StateChange) error {
		id, ok := ids[handle]
		if !ok {
			return nil
		}
		seen[handle] = true
		n := 0
		err := s.store.Write(ctx, func(tx *store.Tx) error {
			n = 0 // Write may retry the function
			for _, c := range changes {
				ok, err := tx.AddPastStateChange(ctx, id, c)
				if err != nil {
					return err
				}
				if ok {
					n++
				}
			}
			return tx.MarkStateHistory(ctx, id)
		})
		if err == nil {
			added += n
		}
		return err
	})
	if err != nil {
		return fmt.Errorf("state history: %w", err)
	}
	// Programs without any past changes are done too.
	err = s.store.Write(ctx, func(tx *store.Tx) error {
		for _, p := range progs {
			if !seen[p.Handle] {
				if err := tx.MarkStateHistory(ctx, ids[p.Handle]); err != nil {
					return err
				}
			}
		}
		return nil
	})
	log.Info("state history read", "programs", len(progs), "changes", added)
	return err
}

// span is one stretch of versions an asset was in scope for.
type span struct {
	entry platform.ScopeEntry
	from  time.Time
	bound bool       // from is the oldest version: added on or before it
	until *time.Time // the first version without it; nil if still there
}

// applyHistory dates a program's entries from its scope versions (oldest
// first). Assets are matched by identifier and side (in or out of scope),
// since entry IDs can change between versions. An asset's added date is the
// start of its latest stretch; one missing from the newest version was
// removed when the next version came out. Entries HuntDeck has as live but the
// history as removed, or the reverse, are left alone.
func applyHistory(ctx context.Context, tx *store.Tx, programID int64, versions []platform.ScopeVersion) error {
	spans := map[string]*span{}
	var prev map[string]bool
	for i, v := range versions {
		cur := map[string]bool{}
		for _, e := range v.Entries {
			id := identity(e)
			if cur[id] {
				continue
			}
			cur[id] = true
			if sp := spans[id]; sp != nil && prev[id] {
				sp.entry = e
			} else {
				spans[id] = &span{entry: e, from: v.At, bound: i == 0}
			}
		}
		for id := range prev {
			if !cur[id] {
				at := v.At
				spans[id].until = &at
			}
		}
		prev = cur
	}

	rows, err := tx.ScopeEntries(ctx, programID)
	if err != nil {
		return err
	}
	keys := map[string]bool{}
	byIdentity := map[string]store.EntryRow{}
	for _, r := range rows {
		keys[r.Key] = true
		id := identity(r.Entry)
		if old, ok := byIdentity[id]; !ok || (old.Removed && !r.Removed) {
			byIdentity[id] = r
		}
	}
	for id, sp := range spans {
		from := sp.from
		row, ok := byIdentity[id]
		switch {
		case ok && !row.Removed && sp.until == nil:
			err = tx.SetEntryDates(ctx, row.ID, &from, sp.bound, nil)
		case ok && row.Removed && sp.until != nil:
			err = tx.SetEntryDates(ctx, row.ID, &from, sp.bound, sp.until)
		case !ok && sp.until != nil:
			k := keyEntries([]platform.ScopeEntry{sp.entry})[0]
			if keys[k.key] {
				k.key = "hist:" + k.hash[:24]
			}
			if keys[k.key] {
				continue
			}
			keys[k.key] = true
			err = tx.InsertHistoryEntry(ctx, programID, k.key, k.hash, sp.entry, &from, sp.bound, *sp.until)
		}
		if err != nil {
			return err
		}
	}
	return nil
}

// identity matches an asset across versions and with stored entries.
func identity(e platform.ScopeEntry) string {
	side := "out"
	if e.InScope {
		side = "in"
	}
	return side + "|" + strings.ToLower(strings.TrimSpace(e.Identifier))
}
