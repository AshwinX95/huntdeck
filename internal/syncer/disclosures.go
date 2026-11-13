package syncer

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/mdpsec/huntdeck/internal/platform"
	"github.com/mdpsec/huntdeck/internal/store"
)

// Disclosed reports are kept in two ways:
//   - every poll reads the platform's all-programs feed, newest first, back
//     to where the previous poll stopped (usually one request);
//   - full polls fetch the whole history of programs that have never had it
//     (new programs, and every program the first time), one program at a
//     time.
//
// Reports for programs HuntDeck doesn't know are skipped.
const (
	// feedPages bounds one read of the all-programs feed; firstFeedPages
	// applies when there is no cursor yet (the backfill covers history).
	feedPages      = 20
	firstFeedPages = 2
	// historyPages bounds one program's history.
	historyPages = 40
	// feedOverlap re-reads a little of the feed before the cursor, in case
	// disclosure times shift or ties straddle a page.
	feedOverlap = 24 * time.Hour
)

// syncDisclosures reads new disclosures and, in full polls, the history of
// programs in ids that don't have it yet. Whatever was stored before an
// error is kept.
func (s *Syncer) syncDisclosures(ctx context.Context, log *slog.Logger, df platform.DisclosureFetcher, platformName string,
	ids map[string]int64, full bool) (added int, err error) {
	known, err := s.store.ProgramIDs(ctx, platformName)
	if err != nil {
		return 0, fmt.Errorf("disclosed reports: %w", err)
	}
	n, err := s.readDisclosureFeed(ctx, df, platformName, known)
	added += n
	if err != nil || !full {
		return added, err
	}
	n, err = s.disclosureHistory(ctx, log, df, platformName, ids)
	return added + n, err
}

// disclosureHistory fetches the whole disclosed report history of the programs
// in ids that don't have it yet: in full polls, and when a program is
// refreshed (an invitation just accepted shows its history).
func (s *Syncer) disclosureHistory(ctx context.Context, log *slog.Logger, df platform.DisclosureFetcher, platformName string,
	ids map[string]int64) (added int, err error) {
	due, err := s.store.DisclosuresDue(ctx, platformName)
	if err != nil {
		return added, fmt.Errorf("disclosed reports: %w", err)
	}
	var todo []store.ProgramRef
	for _, p := range due {
		if _, ok := ids[p.Handle]; ok {
			todo = append(todo, p)
		}
	}
	if len(todo) > 0 {
		log.Info("fetching disclosed report history", "programs", len(todo))
	}
	for _, p := range todo {
		if ctx.Err() != nil {
			return added, ctx.Err()
		}
		prog := platform.Program{Handle: p.Handle, Name: p.Name}
		err := df.FetchDisclosures(ctx, &prog, historyPages, func(page []platform.Disclosure) (bool, error) {
			n, err := s.saveDisclosures(ctx, map[string]int64{p.Handle: p.ID}, page)
			added += n
			return true, err
		})
		if err == nil {
			err = s.store.Write(ctx, func(tx *store.Tx) error { return tx.MarkDisclosuresSynced(ctx, p.ID) })
		}
		if err != nil {
			return added, fmt.Errorf("disclosed reports for %s: %w", p.Handle, err)
		}
	}
	return added, nil
}

// readDisclosureFeed reads the all-programs feed back to the stored cursor
// and moves the cursor to the newest disclosure seen.
func (s *Syncer) readDisclosureFeed(ctx context.Context, df platform.DisclosureFetcher, platformName string,
	known map[string]int64) (added int, err error) {
	cursor, err := s.store.DisclosureCursor(ctx, platformName)
	if err != nil {
		return 0, fmt.Errorf("disclosed reports: %w", err)
	}
	pages := feedPages
	if cursor == nil {
		pages = firstFeedPages
	}
	var newest time.Time
	err = df.FetchDisclosures(ctx, nil, pages, func(page []platform.Disclosure) (bool, error) {
		n, err := s.saveDisclosures(ctx, known, page)
		added += n
		if err != nil {
			return false, err
		}
		if len(page) == 0 {
			return true, nil // nothing usable on this page; the fetcher stops at the last one
		}
		oldest := page[0].DisclosedAt
		for _, d := range page {
			if d.DisclosedAt.After(newest) {
				newest = d.DisclosedAt
			}
			if d.DisclosedAt.Before(oldest) {
				oldest = d.DisclosedAt
			}
		}
		return cursor == nil || !oldest.Before(cursor.Add(-feedOverlap)), nil
	})
	if err != nil {
		return added, fmt.Errorf("disclosed reports: %w", err)
	}
	if !newest.IsZero() && (cursor == nil || newest.After(*cursor)) {
		err = s.store.Write(ctx, func(tx *store.Tx) error { return tx.SetDisclosureCursor(ctx, platformName, newest) })
	}
	return added, err
}

// saveDisclosures stores one page against the programs in ids, in one
// transaction, and returns how many reports were new.
func (s *Syncer) saveDisclosures(ctx context.Context, ids map[string]int64, page []platform.Disclosure) (added int, err error) {
	err = s.store.Write(ctx, func(tx *store.Tx) error {
		added = 0 // Write may retry the function
		touched := map[int64]bool{}
		for _, d := range page {
			id, ok := ids[d.ProgramHandle]
			if !ok {
				continue
			}
			isNew, err := tx.SaveDisclosure(ctx, id, d)
			if err != nil {
				return err
			}
			if isNew {
				added++
			}
			touched[id] = true
		}
		for id := range touched {
			if err := tx.RefreshDisclosureCount(ctx, id); err != nil {
				return err
			}
		}
		return nil
	})
	return added, err
}
