package yeswehack

import (
	"context"
	"errors"
	"fmt"
	"net/url"

	"github.com/mdpsec/huntdeck/internal/platform"
)

// Program stats come with the program document FetchScope reads, under
// "stats" (verified live 2026-10-26):
//
//	{"max_reward": null, "average_reward": null, "average_first_time_response": 1,
//	 "total_reports": 175, "total_reports_last24_hours": 0,
//	 "total_reports_last7_days": 1, "total_reports_current_month": 3}
//
// average_first_time_response is in days: almost every program shows 1.
// The rewards are in euro cents (a top reward of 150000 on a program paying
// up to €5,000), and null unless the program chose to disclose them. One
// request per program: 56 for the public programs in 2026-10.
//
// A second request reads how many researchers the program's ranking lists
// (those who scored on it), from its first page of one:
//
//	GET /programs/{slug}/ranking?page=1&resultsPerPage=1
//	→ {"pagination": {"nb_results": 379, …}, "items": [one researcher]}
const statsBatch = 25

type rankingJSON struct {
	Pagination struct {
		Results *int `json:"nb_results"`
	} `json:"pagination"`
}

type statsJSON struct {
	Stats *struct {
		MaxReward     *float64 `json:"max_reward"`
		AverageReward *float64 `json:"average_reward"`
		FirstResponse *float64 `json:"average_first_time_response"`
		Total         *int     `json:"total_reports"`
		Last7Days     *int     `json:"total_reports_last7_days"`
		ThisMonth     *int     `json:"total_reports_current_month"`
	} `json:"stats"`
}

// FetchProgramStats implements platform.StatsFetcher. Programs whose
// document can't be read are left out.
func (c *Client) FetchProgramStats(ctx context.Context, handles []string, save platform.StatsSaver) error {
	for start := 0; start < len(handles); start += statsBatch {
		batch := handles[start:min(start+statsBatch, len(handles))]
		out := make(map[string]platform.ProgramStats, len(batch))
		for _, h := range batch {
			var sj statsJSON
			err := c.getJSON(ctx, c.base.String()+"/programs/"+url.PathEscape(h), &sj)
			switch {
			case errors.Is(err, ErrAuth) || ctx.Err() != nil:
				return fmt.Errorf("yeswehack: program stats for %s: %w", h, err)
			case err != nil:
				continue // e.g. a program that just went away
			}
			if sj.Stats == nil {
				continue
			}
			st := sj.Stats
			avg, top := euros(st.AverageReward), euros(st.MaxReward)
			s := platform.ProgramStats{
				Currency:      "EUR",
				AvgBountyLow:  avg,
				AvgBountyHigh: avg,
				TopBountyLow:  top,
				TopBountyHigh: top,
				TotalReports:  st.Total,
				ReportsLast7d: st.Last7Days,
				// The month is the platform's own; HuntDeck saves it daily, so
				// a month's last snapshot is (nearly) its whole count.
				ReportsThisMonth: st.ThisMonth,
			}
			if st.FirstResponse != nil {
				hours := *st.FirstResponse * 24
				s.FirstResponseHours = &hours
			}
			var rj rankingJSON
			switch err := c.getJSON(ctx, c.base.String()+"/programs/"+url.PathEscape(h)+"/ranking?page=1&resultsPerPage=1", &rj); {
			case errors.Is(err, ErrAuth) || ctx.Err() != nil:
				return fmt.Errorf("yeswehack: ranking of %s: %w", h, err)
			case err == nil && rj.Pagination.Results != nil:
				s.Hunters = rj.Pagination.Results
			}
			out[h] = s
		}
		if err := save(batch, out); err != nil {
			return err
		}
	}
	return nil
}

// euros converts an amount in cents.
func euros(cents *float64) *float64 {
	if cents == nil {
		return nil
	}
	v := *cents / 100
	return &v
}
