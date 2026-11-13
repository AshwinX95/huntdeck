package syncer

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/mdpsec/huntdeck/internal/platform"
	"github.com/mdpsec/huntdeck/internal/store"
)

// leaderboardMonthsPerRun bounds how many monthly leaderboards one browser
// sync reads, a request each: the first year of every program is spread
// over a few days, after which a run reads only each program's month just
// finished.
const leaderboardMonthsPerRun = 600

// syncLeaderboards reads the past monthly leaderboards of programs that
// don't have them yet (store.LeaderboardMonthsDue). Each program is saved
// as it arrives.
func (s *Syncer) syncLeaderboards(ctx context.Context, log *slog.Logger, lh platform.LeaderboardHistorian, platformName string) error {
	want, ids, err := s.store.LeaderboardMonthsDue(ctx, platformName, leaderboardMonthsPerRun)
	if err != nil || len(want) == 0 {
		return err
	}
	log.Info("reading past leaderboards", "programs", len(want))
	read := 0
	wctx := context.WithoutCancel(ctx) // a program that arrived is saved even during shutdown
	err = lh.LeaderboardMonths(ctx, want, func(handle string, months []platform.LeaderboardMonth) error {
		id, ok := ids[handle]
		if !ok {
			return nil
		}
		err := s.store.Write(wctx, func(tx *store.Tx) error { return tx.SaveLeaderboardMonths(wctx, id, months) })
		if err == nil {
			read++
		}
		return err
	})
	log.Info("past leaderboards read", "programs", read, "of", len(want))
	if err != nil {
		return fmt.Errorf("past leaderboards: %w", err)
	}
	return nil
}
