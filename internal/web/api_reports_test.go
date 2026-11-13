package web

import (
	"testing"
	"time"

	"github.com/mdpsec/huntdeck/internal/store"
)

// Bounties a program hides, counted at its average bounty, are flagged
// estimated in the API, as the program page marks them ≈; amounts the
// program shows aren't.
func TestResolutionsJSONFlagsHiddenBounties(t *testing.T) {
	avg := 750.0
	oct, nov := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC), time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	months := []store.ResolutionMonth{{Month: oct, Resolved: 3, Bounties: 1500, Rewarded: 2}, {Month: nov, Resolved: 1}}
	tests := []struct {
		name          string
		sum           store.ResolutionSummary
		est90, estOct bool
	}{
		{"hidden amounts", store.ResolutionSummary{Total: 4, Bounties90d: 1500, Rewarded90d: 2, BountyAverage: &avg, Months: months}, true, true},
		{"hidden amounts, none rewarded lately", store.ResolutionSummary{Total: 4, BountyAverage: &avg,
			Months: []store.ResolutionMonth{{Month: oct, Resolved: 3}, {Month: nov}}}, false, false},
		{"shown amounts", store.ResolutionSummary{Total: 4, Bounties90d: 1500, Rewarded90d: 2, Months: months}, false, false},
	}
	for _, tt := range tests {
		out := resolutionsJSON(tt.sum)
		if out.ShownBounties90dEstimated != tt.est90 || out.Months[0].BountiesEstimated != tt.estOct || out.Months[1].BountiesEstimated {
			t.Errorf("%s: 90 days estimated %v, October %v, November %v; want %v, %v, false", tt.name,
				out.ShownBounties90dEstimated, out.Months[0].BountiesEstimated, out.Months[1].BountiesEstimated, tt.est90, tt.estOct)
		}
		if (out.BountyAverage != nil) != (tt.sum.BountyAverage != nil) {
			t.Errorf("%s: average %v", tt.name, out.BountyAverage)
		}
	}
}
