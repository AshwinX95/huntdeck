package syncer

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/mdpsec/huntdeck/internal/platform"
	"github.com/mdpsec/huntdeck/internal/store"
)

// Resolved, undisclosed reports are kept like disclosed ones: every poll
// reads the all-programs feed (latest activity first) back to where the
// previous one stopped, and full polls fetch the whole history of programs
// that don't have it yet. About 180 reports a day across HackerOne
// (2026-10), so a quick poll usually needs one request.
const (
	resolutionFeedPages      = 20
	resolutionFirstFeedPages = 2
	// resolutionHistoryPages bounds one program's history (50 a page).
	resolutionHistoryPages = 100
)

// syncResolutions reads new resolutions and, in full polls, the history of
// programs in ids that don't have it yet. Whatever was stored before an
// error is kept.
func (s *Syncer) syncResolutions(ctx context.Context, log *slog.Logger, rf platform.ResolutionFetcher, platformName string,
	ids map[string]int64, full bool) (added int, err error) {
	known, err := s.store.ProgramIDs(ctx, platformName)
	if err != nil {
		return 0, fmt.Errorf("resolved reports: %w", err)
	}
	n, err := s.readResolutionFeed(ctx, rf, platformName, known)
	added += n
	if err != nil || !full {
		return added, err
	}
	n, err = s.resolutionHistory(ctx, log, rf, platformName, ids)
	return added + n, err
}

// resolutionHistory fetches the whole resolved report history of the programs
// in ids that don't have it yet: in full polls, and when a program is
// refreshed (an invitation just accepted shows its history).
func (s *Syncer) resolutionHistory(ctx context.Context, log *slog.Logger, rf platform.ResolutionFetcher, platformName string,
	ids map[string]int64) (added int, err error) {
	due, err := s.store.ResolutionsDue(ctx, platformName)
	if err != nil {
		return added, fmt.Errorf("resolved reports: %w", err)
	}
	var todo []store.ProgramRef
	for _, p := range due {
		if _, ok := ids[p.Handle]; ok {
			todo = append(todo, p)
		}
	}
	if len(todo) > 0 {
		log.Info("fetching resolved report history", "programs", len(todo))
	}
	for _, p := range todo {
		if ctx.Err() != nil {
			return added, ctx.Err()
		}
		prog := platform.Program{Handle: p.Handle, Name: p.Name}
		err := rf.FetchResolutions(ctx, &prog, resolutionHistoryPages, func(page []platform.Resolution) (bool, error) {
			n, err := s.saveResolutions(ctx, map[string]int64{p.Handle: p.ID}, page)
			added += n
			return true, err
		})
		if err == nil {
			err = s.store.Write(ctx, func(tx *store.Tx) error { return tx.MarkResolutionsSynced(ctx, p.ID) })
		}
		if err != nil {
			return added, fmt.Errorf("resolved reports for %s: %w", p.Handle, err)
		}
	}
	return added, nil
}

// readResolutionFeed reads the all-programs feed back to the stored cursor
// and moves the cursor to the latest activity seen.
func (s *Syncer) readResolutionFeed(ctx context.Context, rf platform.ResolutionFetcher, platformName string,
	known map[string]int64) (added int, err error) {
	cursor, err := s.store.ResolutionCursor(ctx, platformName)
	if err != nil {
		return 0, fmt.Errorf("resolved reports: %w", err)
	}
	pages := resolutionFeedPages
	if cursor == nil {
		pages = resolutionFirstFeedPages
	}
	var newest time.Time
	err = rf.FetchResolutions(ctx, nil, pages, func(page []platform.Resolution) (bool, error) {
		n, err := s.saveResolutions(ctx, known, page)
		added += n
		if err != nil {
			return false, err
		}
		if len(page) == 0 {
			return true, nil // nothing usable on this page; the fetcher stops at the last one
		}
		oldest := page[0].ActivityAt
		for _, r := range page {
			if r.ActivityAt.After(newest) {
				newest = r.ActivityAt
			}
			if r.ActivityAt.Before(oldest) {
				oldest = r.ActivityAt
			}
		}
		return cursor == nil || !oldest.Before(cursor.Add(-feedOverlap)), nil
	})
	if err != nil {
		return added, fmt.Errorf("resolved reports: %w", err)
	}
	if !newest.IsZero() && (cursor == nil || newest.After(*cursor)) {
		err = s.store.Write(ctx, func(tx *store.Tx) error { return tx.SetResolutionCursor(ctx, platformName, newest) })
	}
	return added, err
}

// saveResolutions stores one page against the programs in ids, in one
// transaction, and returns how many reports were new.
func (s *Syncer) saveResolutions(ctx context.Context, ids map[string]int64, page []platform.Resolution) (added int, err error) {
	err = s.store.Write(ctx, func(tx *store.Tx) error {
		added = 0 // Write may retry the function
		for _, r := range page {
			id, ok := ids[r.ProgramHandle]
			if !ok {
				continue
			}
			isNew, err := tx.SaveResolution(ctx, id, r)
			if err != nil {
				return err
			}
			if isNew {
				added++
			}
		}
		return nil
	})
	return added, err
}
