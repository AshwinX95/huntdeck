package syncer

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/mdpsec/huntdeck/internal/platform"
	"github.com/mdpsec/huntdeck/internal/store"
)

// recordAnnouncements stores a program's announcements, if they were read.
// Those there the first time are history; later ones are news of the poll.
func recordAnnouncements(ctx context.Context, tx *store.Tx, runID, programID int64, as []platform.Announcement) error {
	if as == nil {
		return nil
	}
	read, err := tx.AnnouncementsRead(ctx, programID)
	if err != nil {
		return err
	}
	for _, a := range as {
		if a.ID == "" {
			continue
		}
		if _, err := tx.AddAnnouncement(ctx, programID, runID, a, !read); err != nil {
			return err
		}
	}
	return tx.MarkAnnouncements(ctx, programID)
}

// recordArchived stores scope entries the program removed as history,
// keyed like live entries so one HuntDeck saw removed gets the exact time.
func recordArchived(ctx context.Context, tx *store.Tx, programID int64, archived []platform.ArchivedScope) error {
	seen := map[string]bool{}
	for _, a := range archived {
		k := keyEntries([]platform.ScopeEntry{a.Entry})[0]
		if seen[k.key] {
			continue
		}
		seen[k.key] = true
		if err := tx.RecordArchivedEntry(ctx, programID, k.key, k.hash, k.entry, a.ArchivedAt); err != nil {
			return err
		}
	}
	return nil
}

// syncPrograms upserts the listing in one transaction and returns program
// IDs by handle, the scope fetches to do, and how many scopes the listing
// says are unchanged. held is non-nil when the mass-removal guard skipped
// removals. account is the listing's account hash (accountHash), "" if
// the platform can't say.
func (s *Syncer) syncPrograms(ctx context.Context, platformName, account string, runID int64, programs []platform.Program,
	changesSince *time.Time, complete bool, o Options) (ids map[string]int64, jobs []scopeJob, unchanged int, held error, err error) {
	ids = make(map[string]int64, len(programs))
	mode := o.Mode
	now := s.store.Now()
	err = s.store.Write(ctx, func(tx *store.Tx) error {
		held, jobs, unchanged = nil, nil, 0 // Write may retry the function
		hadComplete, err := tx.HadCompleteListing(ctx, platformName)
		if err != nil {
			return err
		}
		// Until one full listing has been stored, every program is part of the
		// initial import. --limit runs don't count, or the rest would later
		// show up as hundreds of "new" programs.
		firstImport := !hadComplete
		// A listing from another account than the last complete one brings
		// that account's private programs: the user's already, like a first
		// import's, not invitations just received. The account is settled
		// by its first complete listing; listings before that count as
		// another account's too.
		listed, err := tx.ListedAccount(ctx, platformName)
		if err != nil {
			return err
		}
		newAccount := account != "" && listed != "" && listed != account
		if account != "" && complete && listed != account {
			if err := tx.SetListedAccount(ctx, platformName, account); err != nil {
				return err
			}
		}

		live, err := tx.LivePrograms(ctx, platformName)
		if err != nil {
			return err
		}
		seen := map[string]bool{}
		for _, p := range programs {
			if seen[p.Handle] {
				continue // platforms occasionally repeat an item across pages
			}
			seen[p.Handle] = true
			p.Name = cleanName(p.Name)

			old, err := tx.Program(ctx, platformName, p.Handle)
			if err != nil {
				return err
			}
			if p.PauseUnknown && old != nil && old.State == platform.StatePaused && p.State == platform.StateOpen {
				p.State = old.State // the listing can't tell; the brief said paused
			}
			id, err := tx.UpsertProgram(ctx, platformName, p)
			if err != nil {
				return err
			}
			if p.Requirements != nil {
				if err := tx.SetRequirements(ctx, id, *p.Requirements); err != nil {
					return err
				}
			}
			ids[p.Handle] = id
			for _, c := range programChanges(old, p) {
				// A first import records nothing, except a stored program
				// changing visibility: seeds go by the visibility changes
				// to keep out every program that was ever private.
				if firstImport && c.Kind != store.ChangeVisibilityChanged {
					continue
				}
				if newAccount && c.Kind == store.ChangeProgramAdded && p.Private {
					continue
				}
				c.PollRunID, c.ProgramID = runID, id
				if err := tx.AddChange(ctx, c); err != nil {
					return err
				}
			}
			if p.UserStats != nil {
				if err := tx.RecordUserStats(ctx, id, *p.UserStats); err != nil {
					return err
				}
			}
			plan := planScope(mode, old, p, changesSince, now)
			if o.RefetchScope && mode == ModeFull && (plan == planCheck || plan == planUnchanged) {
				plan = planFetch
			}
			switch plan {
			case planFetch:
				jobs = append(jobs, scopeJob{prog: p})
			case planCheck:
				jobs = append(jobs, scopeJob{prog: p, check: true, fetched: *old.ScopeSyncedAt})
			case planUnchanged:
				if err := tx.MarkScopeChecked(ctx, id); err != nil {
					return err
				}
				unchanged++
			}
		}

		if complete {
			var gone []int64
			for handle, id := range live {
				if !seen[handle] {
					gone = append(gone, id)
				}
			}
			if massRemoval(len(gone), len(live), len(seen)) && !o.ForceRemovals {
				held = fmt.Errorf("the listing would remove %d of %d programs, which looks like a platform problem; removals skipped "+
					"(if it's real, run: huntdeck poll --platform %s --force-removals)", len(gone), len(live), platformName)
				return nil
			}
			for _, id := range gone {
				if err := tx.MarkProgramRemoved(ctx, id); err != nil {
					return err
				}
				if err := tx.AddChange(ctx, store.Change{PollRunID: runID, ProgramID: id, Kind: store.ChangeProgramRemoved}); err != nil {
					return err
				}
			}
		}
		return nil
	})
	return ids, jobs, unchanged, held, err
}

// massRemoval reports whether removing remove of live programs, with listed
// programs in the new listing, is implausible.
func massRemoval(remove, live, listed int) bool {
	if remove == 0 {
		return false
	}
	return listed == 0 || (remove >= massRemovalMin && remove*2 > live)
}

func programChanges(old *store.ProgramRow, p platform.Program) []store.Change {
	var out []store.Change
	if old != nil && old.Private != p.Private {
		// Recorded for a program coming back too ("removed→public" alone
		// would lose that it was private, and seeds would take it).
		out = append(out, store.Change{Kind: store.ChangeVisibilityChanged, Field: "visibility",
			OldValue: visibility(old.Private), NewValue: visibility(p.Private)})
	}
	if old == nil || old.RemovedAt != nil {
		var prev string
		if old != nil {
			prev = "removed"
		}
		return append(out, store.Change{Kind: store.ChangeProgramAdded, Field: "visibility", OldValue: prev, NewValue: visibility(p.Private)})
	}
	if old.State != p.State {
		out = append(out, store.Change{Kind: store.ChangeStateChanged, Field: "state", OldValue: old.State, NewValue: p.State})
	}
	if old.OffersBounty != p.OffersBounty && !p.BountyUnknown {
		out = append(out, store.Change{Kind: store.ChangeBountyChanged, Field: "offers_bounty",
			OldValue: strconv.FormatBool(old.OffersBounty), NewValue: strconv.FormatBool(p.OffersBounty)})
	}
	if old.Name != p.Name {
		out = append(out, store.Change{Kind: store.ChangeProgramRenamed, Field: "name", OldValue: old.Name, NewValue: p.Name})
	}
	return append(out, policyChange(old.Policy, p.Policy)...)
}

func visibility(private bool) string {
	if private {
		return "private"
	}
	return "public"
}

// cleanName drops the spaces some platforms send around a program's name
// or doubled inside it (" Esteé  Lauder"), which would sort it first.
func cleanName(name string) string { return strings.Join(strings.Fields(name), " ") }

// accountHash is a hash of the account p lists programs for
// (platform.AccountNamer), "" if it can't say. Only the hash is stored.
func accountHash(p platform.Platform) string {
	an, ok := p.(platform.AccountNamer)
	if !ok || an.Account() == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(p.Name() + "\x00" + an.Account()))
	return hex.EncodeToString(sum[:16])
}
