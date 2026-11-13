package web

import (
	"strconv"
	"time"

	"github.com/mdpsec/huntdeck/internal/store"
)

// acceptedJSON summarises a program's accepted, undisclosed reports.
type acceptedJSON struct {
	Total            int                 `json:"total"`
	Open             int                 `json:"open"`
	CompleteSince    *time.Time          `json:"complete_since"` // months before it have no data
	ReadAt           *time.Time          `json:"read_at"`
	Last90d          int                 `json:"accepted_90d"`
	Prev90d          *int                `json:"accepted_prev_90d"` // null unless fully known
	Resolved90d      int                 `json:"resolved_90d"`
	MedianAccept90d  *float64            `json:"median_days_to_accept_90d"`
	MedianResolve90d *float64            `json:"median_days_to_resolve_90d"`
	ShownBounties90d float64             `json:"shown_bounties_90d"`
	Months           []acceptedMonthJSON `json:"months"`
}

type acceptedMonthJSON struct {
	Month      string         `json:"month"` // "2026-10"
	Known      bool           `json:"known"` // false before complete_since's month
	Accepted   int            `json:"accepted"`
	ByPriority map[string]int `json:"accepted_by_priority"` // "P1".."P5", "" without one
	Resolved   int            `json:"resolved"`
	Submitted  int            `json:"submitted"`
	Bounties   float64        `json:"shown_bounties"`
}

func acceptedReportsJSON(s store.AcceptedSummary) *acceptedJSON {
	if s.Total == 0 && s.Since == nil {
		return nil
	}
	out := &acceptedJSON{Total: s.Total, Open: s.Open, CompleteSince: s.Since, ReadAt: s.SyncedAt, Last90d: s.Last90d,
		Resolved90d: s.Resolved90d, MedianAccept90d: s.MedianAccept90d, MedianResolve90d: s.MedianResolve90d,
		ShownBounties90d: s.Bounties90d, Months: []acceptedMonthJSON{}}
	if s.PrevKnown {
		out.Prev90d = &s.Prev90d
	}
	for _, m := range s.Months {
		known, _ := monthKnown(m.Month, s.Since)
		by := map[string]int{}
		for p, n := range m.ByPriority {
			if n == 0 {
				continue
			}
			key := ""
			if p > 0 {
				key = "P" + strconv.Itoa(p)
			}
			by[key] = n
		}
		out.Months = append(out.Months, acceptedMonthJSON{m.Month.Format("2006-01"), known, m.Accepted, by, m.Resolved,
			m.Submitted, m.Bounties})
	}
	return out
}

// resolvedJSON summarises a program's resolved, undisclosed reports. A
// program that hides its bounty amounts (HackerOne) has each of its
// bounty-awarded reports counted at its average bounty
// (estimated_bounty_average) in the shown bounties, which are then
// flagged estimated, as the UI marks them ≈.
type resolvedJSON struct {
	Total                     int                 `json:"total"`
	First                     *time.Time          `json:"first_resolved_at,omitempty"`
	Last                      *time.Time          `json:"last_resolved_at,omitempty"`
	Last90d                   int                 `json:"resolved_90d"`
	Prev90d                   int                 `json:"resolved_prev_90d"`
	MedianDays90d             *float64            `json:"median_days_to_resolve_90d"`
	MedianDaysPrev            *float64            `json:"median_days_to_resolve_prev_90d"`
	ShownBounties90d          float64             `json:"shown_bounties_90d"`
	ShownBounties90dEstimated bool                `json:"shown_bounties_90d_estimated"`
	BountyAverage             *float64            `json:"estimated_bounty_average"` // null unless the program hides its amounts
	Months                    []resolvedMonthJSON `json:"months"`
}

type resolvedMonthJSON struct {
	Month             string   `json:"month"` // "2026-10"
	Resolved          int      `json:"resolved"`
	MedianDays        *float64 `json:"median_days_to_resolve"`
	Bounties          float64  `json:"shown_bounties"`
	BountiesEstimated bool     `json:"shown_bounties_estimated"`
}

func resolutionsJSON(s store.ResolutionSummary) *resolvedJSON {
	if s.Total == 0 && s.HistoryFetch == nil {
		return nil
	}
	hidden := s.BountyAverage != nil
	out := &resolvedJSON{Total: s.Total, First: s.First, Last: s.Last, Last90d: s.Last90d, Prev90d: s.Prev90d,
		MedianDays90d: s.Median90d, MedianDaysPrev: s.MedianPrev, ShownBounties90d: s.Bounties90d,
		ShownBounties90dEstimated: hidden && s.Rewarded90d > 0, BountyAverage: rounded(s.BountyAverage), Months: []resolvedMonthJSON{}}
	for _, m := range s.Months {
		out.Months = append(out.Months, resolvedMonthJSON{m.Month.Format("2006-01"), m.Resolved, m.MedianDays, m.Bounties,
			hidden && m.Rewarded > 0})
	}
	return out
}

// apiDisclosures caps the disclosed reports in one program's JSON.
const apiDisclosures = 5000

type disclosedJSON struct {
	Total      int                `json:"total"`
	BySeverity map[string]int     `json:"by_severity"` // "" is unrated
	Weaknesses []store.NamedCount `json:"top_weaknesses"`
	Paid       int                `json:"with_bounty"`
	Bounties   float64            `json:"bounties_total"`
	Reports    []disclosureJSON   `json:"reports"`
}

type disclosureJSON struct {
	ID          string     `json:"id"`
	Title       string     `json:"title"`
	URL         string     `json:"url"`
	Severity    string     `json:"severity,omitempty"`
	Weakness    string     `json:"weakness,omitempty"`
	CVEIDs      []string   `json:"cve_ids,omitempty"`
	Substate    string     `json:"substate,omitempty"`
	Bounty      *float64   `json:"bounty,omitempty"`
	SubmittedAt *time.Time `json:"submitted_at,omitempty"`
	DisclosedAt time.Time  `json:"disclosed_at"`
	Votes       int        `json:"votes"`
	Reporter    string     `json:"reporter,omitempty"`
	Summary     string     `json:"summary,omitempty"`
}

func disclosuresJSON(reports []store.DisclosureView, sum store.DisclosureSummary) *disclosedJSON {
	if sum.Total == 0 {
		return nil
	}
	out := &disclosedJSON{Total: sum.Total, BySeverity: map[string]int{}, Weaknesses: sum.Weaknesses, Paid: sum.Paid,
		Bounties: sum.Bounties, Reports: []disclosureJSON{}}
	for _, c := range sum.BySeverity {
		out.BySeverity[c.Name] = c.Count
	}
	for _, d := range reports {
		out.Reports = append(out.Reports, disclosureJSON{d.ReportID, d.Title, d.URL, d.Severity, d.Weakness, d.CVEIDs,
			d.Substate, d.Bounty, d.SubmittedAt, d.DisclosedAt, d.Votes, d.Reporter, d.Summary})
	}
	return out
}
