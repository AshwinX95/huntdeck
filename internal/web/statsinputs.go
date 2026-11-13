package web

import (
	"context"
	"time"

	"github.com/mdpsec/huntdeck/internal/store"
)

// What the Stats pages are built from takes a second or more to read, so
// it's memoised (store.Memo) and Warm works it out again after each poll,
// before anyone asks.

// statsInputs are the Stats pages' numbers for every platform: each
// platform's overview, its open programs as the Programs list has them,
// and their daily numbers by column (statsDays), then platform.
type statsInputs struct {
	Start     time.Time // the first month charted
	Overviews []store.PlatformOverview
	Items     []store.ProgramListItem
	Days      map[string]map[string][]store.ProgramDay
}

// statsStart is the first month the Stats pages chart, now being now.
func statsStart(now time.Time) time.Time {
	return time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC).AddDate(0, -(overviewMonths - 1), 0)
}

// statsInputs reads statsInputs for the programs of visibility ("public",
// "private", or "" for both; see statsVisibility), memoised.
func (s *server) statsInputs(ctx context.Context, visibility string) (statsInputs, error) {
	now := time.Now().UTC()
	start := statsStart(now)
	return store.Memo(ctx, s.Store, "stats:"+visibility+":"+start.Format(time.DateOnly), func(ctx context.Context) (statsInputs, error) {
		in := statsInputs{Start: start, Days: map[string]map[string][]store.ProgramDay{}}
		var err error
		if in.Overviews, err = s.Store.PlatformOverviews(ctx, now, start, overviewMonths, "", visibility); err != nil {
			return in, err
		}
		if in.Items, _, err = s.Store.Programs(ctx, store.ProgramFilter{State: "open", Ignored: "include", Visibility: visibility, Limit: 1 << 20}); err != nil {
			return in, err
		}
		for _, col := range statsDays {
			if in.Days[col], err = s.Store.PlatformDays(ctx, col, now.AddDate(-1, 0, 0), "", visibility); err != nil {
				return in, err
			}
		}
		return in, nil
	})
}

// programSeries reads every live program's months (store.ProgramSeries)
// of visibility, memoised: a platform's stats page lists its programs with
// them.
func (s *server) programSeries(ctx context.Context, visibility string) (map[int64]store.PlatformOverview, error) {
	now := time.Now().UTC()
	start := statsStart(now)
	return store.Memo(ctx, s.Store, "stats-series:"+visibility+":"+start.Format(time.DateOnly), func(ctx context.Context) (map[int64]store.PlatformOverview, error) {
		return s.Store.ProgramSeries(ctx, "", visibility, now, start, overviewMonths)
	})
}

// warmEvery is how often Warm looks for changes.
const warmEvery = 30 * time.Second

// Warm keeps the Stats pages' numbers worked out, so the first view after
// a poll needn't wait for them: once the database has been still for
// warmEvery (a poll writes all the way through), it reads them, which
// works them out again if anything changed. It returns when ctx is done.
func Warm(ctx context.Context, d Deps) {
	s := &server{Deps: d}
	t := time.NewTicker(warmEvery)
	defer t.Stop()
	last := int64(-1)
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		v, err := s.Store.DataVersion(ctx)
		if err != nil || v != last {
			last = v
			continue
		}
		_, err = s.statsInputs(ctx, "") // the default view, All
		if err == nil {
			_, err = s.programSeries(ctx, "")
		}
		if err != nil && ctx.Err() == nil {
			s.Log.Warn("cannot work out the stats pages' numbers ahead", "err", err)
		}
	}
}
