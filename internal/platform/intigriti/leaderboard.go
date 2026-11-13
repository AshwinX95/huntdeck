package intigriti

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strconv"

	"github.com/mdpsec/huntdeck/internal/platform"
)

// A program's leaderboards (verified live 2026-11-09), logged in:
//
//	GET /api/core/researcher/program/{id}/leaderboard/last90days
//	GET /api/core/researcher/program/{id}/leaderboard/allTime
//	GET /api/core/researcher/program/{id}/leaderboard/month?year=Y&month=M
//	    (a past calendar month; verified live 2026-11-09, as is
//	    quarter?year=Y&quarter=Q, unused)
//
// Each is [{userId, userName, avatarId, reputation, rank, identityChecked,
// streak}], highest first, at most platform.LeaderboardCap entries.
// reputation is the points earned on the program in the window: a fixed
// number per accepted report by severity (low 5, medium 20, high 25,
// critical 40, exceptional 50) and a fifth of that for a duplicate of an
// accepted one. Some programs hide it (null for everyone). Only the sums
// and the count are kept: never who.

type leaderJSON struct {
	Reputation *int `json:"reputation"`
}

// board is one leaderboard in sums: points (nil when hidden), researchers
// with points (or listed, where hidden), and whether it was cut short.
type board struct {
	points  *int
	hunters int
	capped  bool
}

func sumBoard(entries []leaderJSON) board {
	b := board{capped: len(entries) >= platform.LeaderboardCap}
	sum, hidden := 0, false
	for _, e := range entries {
		switch {
		case e.Reputation == nil:
			hidden = true
			b.hunters++
		case *e.Reputation > 0:
			sum += *e.Reputation
			b.hunters++
		}
	}
	if !hidden {
		b.points = &sum
	}
	return b
}

// leaderboards reads program id's 90-day and all-time leaderboards into s:
// Hunters90d, and the points where both boards are whole. A board that
// can't be read leaves s as it was.
func (c *Client) leaderboards(ctx context.Context, id string, s *platform.ProgramStats) error {
	read := func(window string) (board, bool, error) {
		var entries []leaderJSON
		if err := c.webJSON(ctx, "GET", "/program/"+url.PathEscape(id)+"/leaderboard/"+window, nil, &entries); err != nil {
			return board{}, false, ctx.Err() // only a cancelled context stops the stats
		}
		return sumBoard(entries), true, nil
	}
	recent, ok, err := read("last90days")
	if err != nil || !ok {
		return err
	}
	h := recent.hunters
	s.Hunters90d = &h
	all, ok, err := read("allTime")
	if err != nil || !ok {
		return err
	}
	if shareable(recent, all) {
		s.Points90d, s.PointsAll = recent.points, all.points
	}
	return nil
}

// shareable reports whether the boards' points give the share of them
// earned in the last 90 days: both show points, neither is cut short, and
// there are some.
func shareable(recent, all board) bool {
	return recent.points != nil && all.points != nil && !recent.capped && !all.capped && *all.points > 0
}

// LeaderboardMonths implements platform.LeaderboardHistorian: each
// program's monthly leaderboards, one request a month, read in sums. A
// program the account can't open (403) is skipped; any other error stops
// it, since the next run carries on where it stopped. The amount parameter
// the web app sends doesn't lift the 100-entry cap (2026-11), so it isn't
// sent.
func (c *Client) LeaderboardMonths(ctx context.Context, want []platform.LeaderboardWant, save platform.LeaderboardSaver) error {
	for _, w := range want {
		months := make([]platform.LeaderboardMonth, 0, len(w.Months))
		for _, m := range w.Months {
			q := url.Values{"year": {strconv.Itoa(m.Year())}, "month": {strconv.Itoa(int(m.Month()))}}
			var entries []leaderJSON
			err := c.webJSON(ctx, "GET", "/program/"+url.PathEscape(w.Handle)+"/leaderboard/month?"+q.Encode(), nil, &entries)
			if err != nil {
				if errors.Is(err, platform.ErrScopeUnavailable) && ctx.Err() == nil {
					months = nil
					break
				}
				return fmt.Errorf("intigriti: leaderboard of %s for %s: %w", w.Handle, m.Format("2006-01"), err)
			}
			b := sumBoard(entries)
			months = append(months, platform.LeaderboardMonth{Month: m, Points: b.points, Hunters: b.hunters, Capped: b.capped})
		}
		if months == nil {
			continue
		}
		if err := save(w.Handle, months); err != nil {
			return err
		}
	}
	return nil
}
