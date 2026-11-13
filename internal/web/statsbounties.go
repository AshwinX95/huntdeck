package web

import (
	"cmp"
	"maps"
	"slices"

	"github.com/mdpsec/huntdeck/internal/store"
)

// The Stats page's bounties per month: shown on Hacktivity and CrowdStream
// (HackerOne, Bugcrowd), paid as Intigriti's published totals rise; in the
// currency most is paid in, with the rest beside it.

// bountiesIn is m's bounties in currency.
func bountiesIn(m store.PlatformMonth, currency string) float64 {
	if currency == "USD" {
		return m.Bounties
	}
	return m.BountiesOther[currency]
}

// bountyCurrencies are the currencies months have bounties in, the one
// with the most first.
func bountyCurrencies(months []store.PlatformMonth) []string {
	sums := map[string]float64{}
	for _, m := range months {
		if m.Bounties > 0 {
			sums["USD"] += m.Bounties
		}
		for c, v := range m.BountiesOther {
			if v > 0 {
				sums[c] += v
			}
		}
	}
	out := slices.Collect(maps.Keys(sums))
	slices.SortFunc(out, func(a, b string) int { return cmp.Or(cmp.Compare(sums[b], sums[a]), cmp.Compare(a, b)) })
	return out
}

// bountiesTitle says what the Bounties column counts on a platform, for
// its tooltip; on every platform for "".
func bountiesTitle(platform string) string {
	switch platform {
	case "hackerone":
		return "Bounties shown on HackerOne Hacktivity for the month's resolved reports; many aren't shown, and programs that hide amounts count ≈ at their average bounty"
	case "bugcrowd":
		return "Bounties shown on Bugcrowd CrowdStream for the month's accepted reports; many aren't shown"
	case "intigriti":
		return "Bounties paid, from the rises in Intigriti programs' published totals; ≈ before the first save, from the programs' monthly leaderboards"
	}
	return "Bounties shown on reports (HackerOne Hacktivity, Bugcrowd CrowdStream; many aren't shown) or paid (Intigriti, from its published totals); ≈ where estimated"
}

// bountyWord says what a platform's bounties per month are.
func bountyWord(platform string) string {
	if platform == "intigriti" {
		return " paid"
	}
	return " shown"
}

// bountyEstimateFrom says how a platform's bounties per month are
// estimated (store.hiddenBountyAverages, store.estimateFromLeaderboards).
func bountyEstimateFrom(platform string) string {
	switch platform {
	case "hackerone":
		return "partly at programs' average bounties (they hide amounts)"
	case "intigriti":
		return "partly estimated from the programs' monthly leaderboards × paid per accepted report"
	}
	return "partly estimated (see Methodology)"
}
