package syncer

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/mdpsec/huntdeck/internal/platform"
	"github.com/mdpsec/huntdeck/internal/store"
)

// Accepted reports (Bugcrowd's CrowdStream): every poll reads the
// all-programs feed (latest accepted first) until a page brings nothing
// new. About 170 reports a day across Bugcrowd (2026-10), so a quick poll
// usually needs a page or two. A report accepted long ago and resolved
// today doesn't come back to the top of that feed, so full polls also read
// each program's own feed: once for its history (the platform's window,
// six months on Bugcrowd), then again weekly, a batch at a time.
const (
	acceptedFeedPages    = 10
	acceptedProgramPages = 100 // 20 a page
	acceptedRefresh      = 7 * 24 * time.Hour
	// acceptedRefreshPerPoll bounds the weekly re-reads one full poll does;
	// programs never read are always read.
	acceptedRefreshPerPoll = 100
)

// syncAccepted reads new accepted reports and, in full polls, the feeds of
// programs in ids that were never read or not for a week. Whatever was
// stored before an error is kept.
func (s *Syncer) syncAccepted(ctx context.Context, log *slog.Logger, af platform.AcceptedFetcher, platformName string,
	ids map[string]int64, full bool) (added int, err error) {
	known, err := s.store.ProgramIDs(ctx, platformName)
	if err != nil {
		return 0, fmt.Errorf("accepted reports: %w", err)
	}
	err = af.FetchAccepted(ctx, nil, acceptedFeedPages, func(page platform.AcceptedPage) (bool, error) {
		n, seen, err := s.saveAccepted(ctx, known, page.Reports)
		added += n
		// A page of reports already stored means the rest are too.
		return seen == 0 || n > 0, err
	})
	if added > 0 { // whatever was saved, even before an error
		err = errors.Join(err, s.store.Write(ctx, func(tx *store.Tx) error { return tx.MarkAcceptedListed(ctx, platformName) }))
	}
	if err != nil || !full {
		return added, wrapAccepted(err)
	}

	due, err := s.store.AcceptedDue(ctx, platformName, s.store.Now().Add(-acceptedRefresh), acceptedRefreshPerPoll)
	if err != nil {
		return added, wrapAccepted(err)
	}
	var todo []store.ProgramRef
	for _, p := range due {
		if _, ok := ids[p.Handle]; ok {
			todo = append(todo, p)
		}
	}
	if len(todo) > 0 {
		log.Info("reading accepted reports per program", "programs", len(todo))
	}
	for _, p := range todo {
		if ctx.Err() != nil {
			return added, ctx.Err()
		}
		prog := platform.Program{Handle: p.Handle, Name: p.Name}
		var since *time.Time
		err := af.FetchAccepted(ctx, &prog, acceptedProgramPages, func(page platform.AcceptedPage) (bool, error) {
			if since == nil && !page.Since.IsZero() {
				since = &page.Since
			}
			n, _, err := s.saveAccepted(ctx, map[string]int64{p.Handle: p.ID}, page.Reports)
			added += n
			return true, err
		})
		if err == nil {
			err = s.store.Write(ctx, func(tx *store.Tx) error { return tx.MarkAcceptedSynced(ctx, p.ID, since) })
		}
		if err != nil {
			return added, fmt.Errorf("accepted reports for %s: %w", p.Handle, err)
		}
	}
	return added, nil
}

func wrapAccepted(err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("accepted reports: %w", err)
}

// saveAccepted stores one page against the programs in ids, in one
// transaction, and returns how many reports were new and how many belonged
// to those programs.
func (s *Syncer) saveAccepted(ctx context.Context, ids map[string]int64, page []platform.AcceptedReport) (added, seen int, err error) {
	err = s.store.Write(ctx, func(tx *store.Tx) error {
		added, seen = 0, 0 // Write may retry the function
		for _, r := range page {
			id, ok := ids[r.ProgramHandle]
			if !ok {
				continue
			}
			seen++
			isNew, err := tx.SaveAccepted(ctx, id, r)
			if err != nil {
				return err
			}
			if isNew {
				added++
			}
		}
		return nil
	})
	return added, seen, err
}
