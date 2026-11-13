package web

import (
	"net/http"

	"github.com/mdpsec/huntdeck/internal/store"
)

// /api/v1/board is the hunt board's summary: its headline numbers, the
// week's counts and status moves, and the top hunts. Its fresh targets
// are /api/v1/targets?sort=best, its changes /api/v1/changes.

type apiBoardJSON struct {
	Totals       apiBoardTotals  `json:"totals"`
	Changes24h   int             `json:"changes_24h"`
	Changes7d    int             `json:"changes_7d"`
	Week         apiBoardWeek    `json:"week"`
	Reopened     []apiProgramRef `json:"reopened"` // open again this week
	Paused       []apiProgramRef `json:"paused"`   // paused or closed this week
	FlipFlopping []apiFlipFlop   `json:"flip_flopping"`
	TopHunts     []apiHuntPick   `json:"top_hunts"` // highest HuntScores you can hunt now
}

// apiBoardTotals are what HuntDeck holds now, counted as the pages the
// board links to count them: ignored programs left out, and all but
// programs and private for open programs only (so paying is
// /api/v1/programs?bounty=1's total, wildcards /api/v1/targets?kind=wildcard's).
type apiBoardTotals struct {
	Programs  int `json:"programs"` // listed, paused and closed ones too
	Private   int `json:"private"`  // listed, paused and closed ones too
	Open      int `json:"open"`
	Paying    int `json:"paying"`
	InScope   int `json:"in_scope_entries"`
	Wildcards int `json:"wildcards"`
	Domains   int `json:"domains"`
}

// apiBoardWeek counts what's new in the last 7 days, ignored programs left
// out.
type apiBoardWeek struct {
	ScopeAdded     int `json:"scope_added"`
	ScopePrograms  int `json:"programs_added_scope"`
	WildcardsAdded int `json:"wildcards_added"`
	DomainsAdded   int `json:"domains_added"`
	NewPrograms    int `json:"new_programs"`
	NewInvites     int `json:"new_invites"`    // private ones among them
	BountyChanges  int `json:"bounty_changes"` // paying started or stopped, or the bounty table changed
}

type apiFlipFlop struct {
	Program       apiProgramRef `json:"program"`
	StatusChanges int           `json:"status_changes"` // this week
}

type apiHuntPick struct {
	Program      apiProgramRef     `json:"program"`
	OffersBounty bool              `json:"offers_bounty"`
	Hunt         *store.HuntDetail `json:"hunt_score"`
	InScope      int               `json:"in_scope_entries"`
	Wildcards    int               `json:"wildcards"`
	TopBounty    *float64          `json:"top_bounty"`
	AvgBounty    *float64          `json:"average_bounty"` // where the platform shares only that
	Currency     string            `json:"currency"`
}

func (s *server) apiBoard(w http.ResponseWriter, r *http.Request) {
	var d dashboardData
	if err := s.boardHead(r.Context(), &d); err != nil {
		s.apiServerError(w, r, err)
		return
	}
	t, wk := d.Totals, d.Week
	out := apiBoardJSON{
		Totals: apiBoardTotals{Programs: t.Programs, Private: t.Private, Open: t.Open, Paying: t.Bounty, InScope: t.InScope,
			Wildcards: t.Wildcards, Domains: t.Domains},
		Changes24h: d.Changes24h, Changes7d: d.Changes7d,
		Week: apiBoardWeek{ScopeAdded: wk.ScopeAdded, ScopePrograms: wk.ScopePrograms, WildcardsAdded: wk.Wildcards, DomainsAdded: wk.Domains,
			NewPrograms: wk.Programs, NewInvites: wk.Invites, BountyChanges: wk.Bounty},
		Reopened: refsJSON(d.Status.Reopened), Paused: refsJSON(d.Status.Paused), FlipFlopping: []apiFlipFlop{}, TopHunts: []apiHuntPick{},
	}
	for _, f := range d.Status.FlipFloppers {
		out.FlipFlopping = append(out.FlipFlopping, apiFlipFlop{refJSON(f.Program), f.Flips})
	}
	for _, h := range d.TopHunts {
		out.TopHunts = append(out.TopHunts, apiHuntPick{refJSON(h.ProgramRef), h.OffersBounty, h.Detail, h.InScope, h.Wildcards,
			h.TopBounty, h.AvgBounty, h.Currency})
	}
	writeJSON(w, http.StatusOK, out)
}

func refsJSON(refs []store.ProgramRef) []apiProgramRef {
	out := make([]apiProgramRef, 0, len(refs))
	for _, r := range refs {
		out = append(out, refJSON(r))
	}
	return out
}
