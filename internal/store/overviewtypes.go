package store

import "time"

// PlatformMonth is one platform's numbers for a calendar month.
type PlatformMonth struct {
	ScopeAdded int // in-scope assets added, dates known exactly
	Launched   int // programs launched
	// Valid counts reports resolved (Hacktivity) or accepted (CrowdStream)
	// in the month, and Bounties the bounties shown for them, in USD
	// programs (BountiesOther has the rest).
	Valid    int
	Bounties float64
	// ResolveDays are the days from submission to resolution of the
	// month's resolved reports whose dates are known exactly.
	ResolveDays []float64
	// Open counts programs open at the month's end (now, for this month),
	// Paying those of them paying bounties then, Assets the in-scope
	// assets (scope entries) of today's open programs then, and Wildcards
	// their in-scope wildcard targets then, once per program as the
	// Targets page and hunt board count them (wildcardsByMonth); by
	// platform only.
	Open, Paying, Assets, Wildcards int
	// ScopePrograms counts programs that added in-scope assets in the
	// month, and Disclosed the reports disclosed in it; by platform only.
	ScopePrograms, Disclosed int
	// Estimated says Valid is partly estimated (see estimateValid).
	Estimated bool
	// BountiesEstimated says Bounties counts some reports at their
	// program's average bounty, as the program hides amounts
	// (hiddenbounties.go), or some months from leaderboards
	// (boardestimates.go).
	BountiesEstimated bool
	// BountiesOther are the month's bounties of programs paying in other
	// currencies, by currency (Intigriti's euros).
	BountiesOther map[string]float64
}

// AddBounty adds v, in currency, to the month's bounties.
func (m *PlatformMonth) AddBounty(currency string, v float64) {
	if currency == "" || currency == "USD" {
		m.Bounties += v
		return
	}
	if m.BountiesOther == nil {
		m.BountiesOther = map[string]float64{}
	}
	m.BountiesOther[currency] += v
}

// PlatformOverview is one platform on the Stats page.
type PlatformOverview struct {
	Platform string
	// Open counts open programs, Paying those that pay bounties, Assets
	// their in-scope entries.
	Open, Paying, Assets int
	// Added30 and Removed30 count in-scope entries added and removed in the
	// last 30 days.
	Added30, Removed30 int
	Months             []PlatformMonth // one a month from the overview's start
	// ValidKind is "resolved" (Hacktivity) or "accepted" (CrowdStream),
	// "" without either; ValidSince is where those counts are complete
	// from.
	ValidKind  string
	ValidSince *time.Time
	Launches   bool // the platform says when programs launched
}
