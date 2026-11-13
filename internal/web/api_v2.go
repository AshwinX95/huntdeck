package web

import (
	"time"

	"github.com/mdpsec/huntdeck/internal/store"
)

// API v2: programs as v1 has them, but each number that can be estimated
// or that a sync refreshes (bounty range, scope counts, the Programs page's
// figures) is an object saying what it is: its value, whether it's an
// estimate, where it comes from and when it last changed. v1 keeps the
// flat shape, unchanged, for scripts written against it; both have the
// program's updated_at and last_synced_at.

// apiValueMeta is what a v2 value says about itself.
type apiValueMeta struct {
	// Estimated is true where the value is worked out by HuntDeck rather
	// than published as it is (see the Methodology page).
	Estimated bool `json:"estimated"`
	// Source is the platform that publishes it ("hackerone", …), "manual"
	// for programs added by hand, or "derived": counted or worked out by
	// HuntDeck from what the platform publishes.
	Source string `json:"source"`
	// UpdatedAt is when the value last changed, to the poll that saw it
	// change: a figure read again unchanged keeps its time. Null where it
	// can't be dated (unchanged since HuntDeck began keeping these times,
	// with no history to date it from).
	UpdatedAt *time.Time `json:"updated_at"`
}

// apiMetaNumber is an amount or a number of hours; null where the
// platform doesn't publish it.
type apiMetaNumber struct {
	Value *float64 `json:"value"`
	apiValueMeta
}

// apiMetaCount is a count; null where the platform doesn't publish it.
type apiMetaCount struct {
	Value *int `json:"value"`
	apiValueMeta
}

// apiMetaInt is a count HuntDeck always has (scope).
type apiMetaInt struct {
	Value int `json:"value"`
	apiValueMeta
}

// apiMetaBounty is the bounty range, as v1's bounty; null value where
// none is known.
type apiMetaBounty struct {
	Value *apiBounty `json:"value"`
	apiValueMeta
}

// apiProgramNumbersV2 is v1's numbers, each with what it is.
type apiProgramNumbersV2 struct {
	Currency           string        `json:"currency"`
	Paid90d            apiMetaNumber `json:"paid_90d"`
	PaidTotal          apiMetaNumber `json:"paid_total"`
	AvgBountyLow       apiMetaNumber `json:"average_bounty_low"`
	AvgBountyHigh      apiMetaNumber `json:"average_bounty_high"`
	RewardedReports    apiMetaCount  `json:"rewarded_reports"`
	Received90d        apiMetaCount  `json:"reports_received_90d"`
	CountedDays        *int          `json:"reports_received_counted_days"` // reports_received_90d and paid_90d were worked out from this many days of saved totals; under 90 is an estimate
	Resolved90d        apiMetaCount  `json:"resolved_90d"`
	Accepted90d        apiMetaCount  `json:"accepted_90d"`
	Reports7d          apiMetaCount  `json:"reports_7d"`
	ReportsThisMonth   apiMetaCount  `json:"reports_this_month"`
	TotalReports       apiMetaCount  `json:"total_reports"`
	AcceptedTotal      apiMetaCount  `json:"accepted_total"`
	Hunters            apiMetaCount  `json:"hunters"`
	Hunters90d         apiMetaCount  `json:"hunters_90d"` // researchers who earned leaderboard points in 90 days (Intigriti); 100 means 100 or more
	FirstResponseHours apiMetaNumber `json:"first_response_hours"`
	ValidationHours    apiMetaNumber `json:"validation_hours"`
}

// apiProgramMetaV2 are the v2 fields that take the place of v1's flat ones
// (the outer struct's field wins over the embedded one's, as encoding/json
// has it).
type apiProgramMetaV2 struct {
	InScope    apiMetaInt          `json:"in_scope_entries"`
	OutOfScope apiMetaInt          `json:"out_of_scope_entries"`
	Wildcards  apiMetaInt          `json:"wildcards"`
	Domains    apiMetaInt          `json:"domains"`
	Apps       apiMetaInt          `json:"apps"`
	APIs       apiMetaInt          `json:"apis"`
	Bounty     apiMetaBounty       `json:"bounty"`
	Numbers    apiProgramNumbersV2 `json:"numbers"`
}

// apiProgramItemV2 is a program as v2's lists have it.
type apiProgramItemV2 struct {
	apiProgramItem
	InScope    apiMetaInt          `json:"in_scope_entries"`
	OutOfScope apiMetaInt          `json:"out_of_scope_entries"`
	Wildcards  apiMetaInt          `json:"wildcards"`
	Domains    apiMetaInt          `json:"domains"`
	Apps       apiMetaInt          `json:"apps"`
	APIs       apiMetaInt          `json:"apis"`
	Bounty     apiMetaBounty       `json:"bounty"`
	Numbers    apiProgramNumbersV2 `json:"numbers"`
}

type apiProgramListV2JSON struct {
	Total    int                `json:"total"`
	Programs []apiProgramItemV2 `json:"programs"`
}

// apiProgramV2JSON is one program in full, as v2 has it.
type apiProgramV2JSON struct {
	apiProgramJSON
	InScope    apiMetaInt           `json:"in_scope_entries"`
	OutOfScope apiMetaInt           `json:"out_of_scope_entries"`
	Wildcards  apiMetaInt           `json:"wildcards"`
	Domains    apiMetaInt           `json:"domains"`
	Apps       apiMetaInt           `json:"apps"`
	APIs       apiMetaInt           `json:"apis"`
	Bounty     apiMetaBounty        `json:"bounty"`
	Numbers    *apiProgramNumbersV2 `json:"numbers"`
}

// programMetaV2 is p's v2 values, with times when each changed.
func programMetaV2(p store.ProgramListItem, t store.ProgramTimes) apiProgramMetaV2 {
	own := p.Platform == store.Independent
	meta := func(field string, estimated, derived bool) apiValueMeta {
		src := p.Platform
		switch {
		case own:
			src = "manual"
		case derived || estimated:
			src = "derived"
		}
		return apiValueMeta{Estimated: estimated, Source: src, UpdatedAt: t.Fields[field]}
	}
	num := func(v *float64, field string, estimated, derived bool) apiMetaNumber {
		return apiMetaNumber{v, meta(field, estimated, derived)}
	}
	count := func(v *int, field string, estimated, derived bool) apiMetaCount {
		return apiMetaCount{v, meta(field, estimated, derived)}
	}
	scope := func(v int, field string) apiMetaInt { return apiMetaInt{v, meta(field, false, false)} }
	n := numbersJSON(p)
	counted := p.CountedDays != nil // worked out from the totals HuntDeck saved
	return apiProgramMetaV2{
		InScope: scope(p.InScope, store.ValInScope), OutOfScope: scope(p.OutOfScope, store.ValOutOfScope),
		Wildcards: scope(p.Wildcards, store.ValWildcards), Domains: scope(p.Domains, store.ValDomains),
		Apps: scope(p.Apps, store.ValApps), APIs: scope(p.APIs, store.ValAPIs),
		// A range read from the stats is only the most the program has paid.
		Bounty: apiMetaBounty{bountyJSON(p), meta(store.ValBounty, p.BountyFrom == store.PayFromStats, false)},
		Numbers: apiProgramNumbersV2{
			Currency:           n.Currency,
			Paid90d:            num(n.Paid90d, store.ValPaid90d, n.Paid90dEstimated, counted),
			PaidTotal:          num(n.PaidTotal, store.ValPaidTotal, n.PaidTotalEstimated, false),
			AvgBountyLow:       num(n.AvgBountyLow, store.ValAvgBountyLow, false, false),
			AvgBountyHigh:      num(n.AvgBountyHigh, store.ValAvgBountyHigh, false, false),
			RewardedReports:    count(n.RewardedReports, store.ValRewarded, false, false),
			Received90d:        count(n.Received90d, store.ValReceived90d, n.Received90dEstimated, counted),
			CountedDays:        n.CountedDays,
			Resolved90d:        count(n.Resolved90d, store.ValResolved90d, n.Resolved90dEstimated, true),
			Accepted90d:        count(n.Accepted90d, store.ValAccepted90d, false, true),
			Reports7d:          count(n.Reports7d, store.ValReports7d, false, false),
			ReportsThisMonth:   count(n.ReportsThisMonth, store.ValReportsMonth, false, false),
			TotalReports:       count(n.TotalReports, store.ValTotalReports, false, false),
			AcceptedTotal:      count(n.AcceptedTotal, store.ValAcceptedTotal, false, false),
			Hunters:            count(n.Hunters, store.ValHunters, false, false),
			Hunters90d:         count(n.Hunters90d, store.ValHunters90d, false, false),
			FirstResponseHours: num(n.FirstResponseHours, store.ValFirstResponseHrs, false, false),
			ValidationHours:    num(n.ValidationHours, store.ValValidationHours, false, false),
		},
	}
}

func itemV2(it apiProgramItem, m apiProgramMetaV2) apiProgramItemV2 {
	return apiProgramItemV2{it, m.InScope, m.OutOfScope, m.Wildcards, m.Domains, m.Apps, m.APIs, m.Bounty, m.Numbers}
}

func programV2(p apiProgramJSON, m *apiProgramMetaV2) apiProgramV2JSON {
	out := apiProgramV2JSON{apiProgramJSON: p}
	if m != nil {
		out.InScope, out.OutOfScope, out.Wildcards, out.Domains, out.Apps, out.APIs = m.InScope, m.OutOfScope, m.Wildcards, m.Domains, m.Apps, m.APIs
		out.Bounty, out.Numbers = m.Bounty, &m.Numbers
	}
	return out
}
