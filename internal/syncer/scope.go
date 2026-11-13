package syncer

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/mdpsec/huntdeck/internal/platform"
	"github.com/mdpsec/huntdeck/internal/scope"
	"github.com/mdpsec/huntdeck/internal/store"
)

// scopePlan is what a poll does about one program's scope.
type scopePlan int

const (
	planSkip      scopePlan = iota // leave it for a full poll
	planUnchanged                  // the listing says it hasn't changed
	planFetch
	planCheck // ask the platform whether it changed; fetch if so
)

// scopeJob is one program whose scope a poll fetches, or checks first.
type scopeJob struct {
	prog    platform.Program
	check   bool      // ask a platform.ScopeChangeChecker first
	fetched time.Time // the last download, for checks
}

// planScope decides what a poll in mode does about a program's scope. Both
// modes fetch new, returning and never-fetched programs, and ones the listing
// says changed. Quick polls leave the rest, including programs whose scope
// the platform refused to show, to the full poll. Full polls skip scopes the
// platform says are unchanged, for up to scopeRecheck since the download.
func planScope(mode string, old *store.ProgramRow, p platform.Program, changesSince *time.Time, now time.Time) scopePlan {
	switch {
	case old == nil, old.RemovedAt != nil, old.ScopeSyncedAt == nil && old.ScopeUnavailableAt == nil:
		return planFetch
	case old.ScopeSyncedAt == nil || old.ScopeUnavailableAt != nil:
		if mode == ModeFull {
			return planFetch
		}
		return planSkip
	}
	fetched := *old.ScopeSyncedAt
	changed, known := listedChange(p, changesSince, fetched)
	switch {
	case known && changed:
		return planFetch
	case mode == ModeQuick && known:
		return planUnchanged
	case mode == ModeQuick:
		return planSkip
	case now.Sub(fetched) >= scopeRecheck:
		return planFetch
	case known:
		return planUnchanged
	}
	return planCheck
}

// listedChange reports whether the listing says p's scope changed after the
// download at fetched, and whether it says at all.
func listedChange(p platform.Program, changesSince *time.Time, fetched time.Time) (changed, known bool) {
	cutoff := fetched.Add(-changeSlack)
	switch {
	case p.ScopeChangedAt != nil:
		return p.ScopeChangedAt.After(cutoff), true
	case changesSince != nil && !cutoff.Before(*changesSince):
		return false, true
	}
	return false, false
}

// syncScopes fetches scope for programs with a bounded worker pool. Each
// program is written in its own transaction as soon as it arrives. Check
// jobs ask the platform first and count as unchanged when it says so.
func (s *Syncer) syncScopes(ctx context.Context, log *slog.Logger, p platform.Platform, runID int64,
	jobs []scopeJob, ids map[string]int64, workers int, force bool) (fetched, failed, unchanged int) {
	work := make(chan scopeJob)
	var mu sync.Mutex
	var wg sync.WaitGroup
	for range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for job := range work {
				prog := job.prog
				same, err := s.scopeUnchanged(ctx, p, job, ids[prog.Handle])
				if err == nil && same {
					mu.Lock()
					unchanged++
					mu.Unlock()
					s.advance(runID, 1)
					continue
				}
				if err == nil {
					err = s.syncOneScope(ctx, p, runID, ids[prog.Handle], prog, force)
				}
				mu.Lock()
				switch {
				case errors.Is(err, platform.ErrScopeUnavailable):
					log.Debug("scope not available; keeping previous scope", "program", prog.Handle, "err", err)
					if werr := s.store.Write(ctx, func(tx *store.Tx) error { return tx.MarkScopeUnavailable(ctx, ids[prog.Handle]) }); werr != nil && ctx.Err() == nil {
						log.Warn("cannot record unavailable scope", "program", prog.Handle, "err", werr)
					}
				case err != nil:
					failed++
					if ctx.Err() == nil {
						log.Warn("scope fetch failed; keeping previous scope", "program", prog.Handle, "err", err)
					}
				default:
					fetched++
				}
				mu.Unlock()
				s.advance(runID, 1)
			}
		}()
	}
	for _, job := range jobs {
		if ctx.Err() != nil {
			break
		}
		work <- job
	}
	close(work)
	wg.Wait()
	return fetched, failed, unchanged
}

// scopeUnchanged asks the platform, for check jobs, whether the program's
// scope changed since its last download, and records the check if not.
func (s *Syncer) scopeUnchanged(ctx context.Context, p platform.Platform, job scopeJob, programID int64) (bool, error) {
	cc, ok := p.(platform.ScopeChangeChecker)
	if !job.check || !ok {
		return false, nil
	}
	at, err := cc.ScopeChangedAt(ctx, job.prog)
	if err != nil || at.IsZero() || at.After(job.fetched.Add(-changeSlack)) {
		return false, err
	}
	return true, s.store.Write(ctx, func(tx *store.Tx) error { return tx.MarkScopeChecked(ctx, programID) })
}

func (s *Syncer) syncOneScope(ctx context.Context, p platform.Platform, runID, programID int64, prog platform.Program, force bool) error {
	var entries []platform.ScopeEntry
	var detail platform.ScopeDetail
	var err error
	if df, ok := p.(platform.ScopeDetailFetcher); ok {
		entries, detail, err = df.FetchScopeDetail(ctx, prog)
	} else {
		entries, err = p.FetchScope(ctx, prog)
	}
	if err != nil {
		return err
	}
	keyed := keyEntries(entries)
	programHash := scopeHash(keyed)

	return s.store.Write(ctx, func(tx *store.Tx) error {
		row, err := tx.Program(ctx, p.Name(), prog.Handle)
		if err != nil {
			return err
		}
		if err := recordDetail(ctx, tx, runID, row, detail); err != nil {
			return err
		}
		cvss := cvssOf(keyed)
		if row.ScopeHash == programHash && row.ScopeSyncedAt != nil {
			if err := tx.SetEntryCVSS(ctx, programID, cvss); err != nil {
				return err
			}
			return tx.SetScopeSynced(ctx, programID, programHash) // unchanged: one tiny write
		}
		baseline := row.ScopeSyncedAt == nil // first sync: store silently, no change events

		stored, err := tx.ScopeEntries(ctx, programID)
		if err != nil {
			return err
		}
		if len(keyed) == 0 && !force {
			if n := liveEntries(stored); n > 0 {
				return fmt.Errorf("platform returned no scope entries; keeping the previous %d (a real emptied scope needs --force-removals)", n)
			}
		}
		byKey := make(map[string]store.EntryRow, len(stored))
		for _, r := range stored {
			byKey[r.Key] = r
		}

		record := func(c store.Change) error {
			if baseline {
				return nil
			}
			c.PollRunID, c.ProgramID = runID, programID
			return tx.AddChange(ctx, c)
		}

		present := map[string]bool{}
		twins := slashTwins(stored, keyed)
		for _, k := range keyed {
			present[k.key] = true
			old, exists := byKey[k.key]
			if twin, ok := twins[twinKey(k)]; !exists && ok {
				// The same asset with a trailing slash added or taken off: an
				// edit of the entry, not a removal and an addition.
				delete(twins, twinKey(k))
				if err := tx.RekeyEntry(ctx, twin.ID, k.key); err != nil {
					return err
				}
				present[twin.Key] = true
				old, exists = twin, true
			}
			switch {
			case !exists:
				id, err := tx.InsertEntry(ctx, programID, k.key, k.hash, k.entry, baseline)
				if err != nil {
					return err
				}
				if err := tx.ReplaceTargets(ctx, id, scope.Targets(k.entry.AssetType, k.entry.Identifier)); err != nil {
					return err
				}
				if err := record(store.Change{ScopeEntryID: id, Kind: store.ChangeScopeAdded, Field: scopeSide(k.entry.InScope), NewValue: k.entry.Identifier}); err != nil {
					return err
				}
			case old.Removed || old.ContentHash != k.hash:
				if err := tx.UpdateEntry(ctx, old.ID, k.hash, k.entry, old.Removed); err != nil {
					return err
				}
				if err := tx.ReplaceTargets(ctx, old.ID, scope.Targets(k.entry.AssetType, k.entry.Identifier)); err != nil {
					return err
				}
				if old.Removed {
					if err := record(store.Change{ScopeEntryID: old.ID, Kind: store.ChangeScopeAdded, Field: scopeSide(k.entry.InScope), OldValue: "removed", NewValue: k.entry.Identifier}); err != nil {
						return err
					}
					continue
				}
				for _, c := range entryChanges(old.Entry, k.entry) {
					c.ScopeEntryID = old.ID
					if err := record(c); err != nil {
						return err
					}
				}
			}
		}
		for _, old := range stored {
			if old.Removed || present[old.Key] {
				continue
			}
			if err := tx.MarkEntryRemoved(ctx, old.ID); err != nil {
				return err
			}
			if err := record(store.Change{ScopeEntryID: old.ID, Kind: store.ChangeScopeRemoved, Field: scopeSide(old.Entry.InScope), OldValue: old.Entry.Identifier}); err != nil {
				return err
			}
		}
		if err := tx.RefreshScopeCounts(ctx, programID); err != nil {
			return err
		}
		if err := tx.SetEntryCVSS(ctx, programID, cvss); err != nil {
			return err
		}
		return tx.SetScopeSynced(ctx, programID, programHash)
	})
}

// cvssOf is the entries' CVSS requirements by key: every entry's when any
// has some (so cleared ones are cleared), else nil.
func cvssOf(keyed []keyedEntry) map[string]platform.CVSSRequirements {
	if !slices.ContainsFunc(keyed, func(k keyedEntry) bool { return k.entry.CVSS != platform.CVSSRequirements{} }) {
		return nil
	}
	out := make(map[string]platform.CVSSRequirements, len(keyed))
	for _, k := range keyed {
		out[k.key] = k.entry.CVSS
	}
	return out
}

// slashTwins are the live stored entries keyed by their identifier (no
// platform ID) that the listing leaves out, by twinKey: one listed again
// with only a trailing slash added or taken off is the same asset.
func slashTwins(stored []store.EntryRow, keyed []keyedEntry) map[string]store.EntryRow {
	listed := make(map[string]bool, len(keyed))
	for _, k := range keyed {
		listed[k.key] = true
	}
	out := map[string]store.EntryRow{}
	for _, r := range stored {
		if !r.Removed && !listed[r.Key] && strings.HasPrefix(r.Key, hashKeyPrefix) {
			out[twinKey(keyedEntry{key: r.Key, entry: r.Entry})] = r
		}
	}
	return out
}

// twinKey is what a keyless entry and its slash twin share; "" for an
// entry with a platform ID.
func twinKey(k keyedEntry) string {
	if !strings.HasPrefix(k.key, hashKeyPrefix) {
		return ""
	}
	return strconv.FormatBool(k.entry.InScope) + "\x00" + k.entry.AssetType + "\x00" + strings.TrimRight(k.entry.Identifier, "/")
}

func liveEntries(rows []store.EntryRow) int {
	n := 0
	for _, r := range rows {
		if !r.Removed {
			n++
		}
	}
	return n
}

func scopeSide(in bool) string {
	if in {
		return "in_scope"
	}
	return "out_of_scope"
}

func entryChanges(old, cur platform.ScopeEntry) []store.Change {
	var out []store.Change
	add := func(field, o, n string) {
		if o != n {
			out = append(out, store.Change{Kind: store.ChangeScopeUpdated, Field: field, OldValue: o, NewValue: n})
		}
	}
	add("identifier", old.Identifier, cur.Identifier)
	add("in_scope", strconv.FormatBool(old.InScope), strconv.FormatBool(cur.InScope))
	add("bounty_eligible", strconv.FormatBool(old.BountyEligible), strconv.FormatBool(cur.BountyEligible))
	add("asset_type", old.AssetType, cur.AssetType)
	add("max_severity", old.MaxSeverity, cur.MaxSeverity)
	add("instruction", old.Instruction, cur.Instruction)
	return out
}

// hashKeyPrefix starts the key of an entry the platform gives no ID.
const hashKeyPrefix = "h:"

type keyedEntry struct {
	key, hash string
	entry     platform.ScopeEntry
}

// keyEntries assigns each entry a stable key (the platform's ID, or a hash
// of its identity when there is none) and a content hash, dropping repeats.
func keyEntries(entries []platform.ScopeEntry) []keyedEntry {
	out := make([]keyedEntry, 0, len(entries))
	seen := map[string]bool{}
	for _, e := range entries {
		key := e.PlatformID
		if key == "" {
			key = hashKeyPrefix + hashOf(strconv.FormatBool(e.InScope), e.AssetType, e.Identifier)[:24]
		}
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, keyedEntry{key: key, entry: e, hash: hashOf(
			strconv.FormatBool(e.InScope), e.AssetType, e.PlatformAssetType, e.Identifier,
			strconv.FormatBool(e.BountyEligible), e.MaxSeverity, e.Instruction)})
	}
	return out
}

func scopeHash(entries []keyedEntry) string {
	lines := make([]string, len(entries))
	for i, k := range entries {
		lines[i] = k.key + "=" + k.hash
	}
	slices.Sort(lines)
	return hashOf(lines...)
}

func hashOf(parts ...string) string {
	h := sha256.New()
	for _, p := range parts {
		_, _ = h.Write([]byte(p))
		_, _ = h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil))
}
