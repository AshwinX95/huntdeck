package web

import (
	"cmp"
	"slices"

	"github.com/mdpsec/huntdeck/internal/store"
)

// The 90-day figures added up over programs, one way for the Stats page's
// rows and /api/v1/stats's totals, so the same names mean the same
// numbers: the Programs list's figures (Bugcrowd's estimates included),
// reports received from every program that has a count, money from
// programs that pay bounties (a program that stopped keeps its last
// figures, which aren't its bounties now).

// ninetyDays are programs' 90-day figures added up.
type ninetyDays struct {
	Received          int
	ReceivedPrograms  int
	ReceivedEstimated bool       // some counts are estimates (store.ProgramListItem.Reports90dIsEstimate)
	Paid              []moneySum // by currency, largest first
	PaidPrograms      int
	PaidEstimated     bool // some amounts are estimates (store.ProgramListItem.Paid90dIsEstimate)
}

// programCurrency is the currency a program's amounts are in.
func programCurrency(it store.ProgramListItem) string {
	return cmp.Or(it.StatsCurrency, it.Currency, "USD")
}

// pays reports whether amount, one of a program's figures, counts towards
// a total of what programs pay.
func pays(it store.ProgramListItem, amount *float64) bool {
	return it.OffersBounty && amount != nil && *amount > 0
}

// sumNinetyDays adds up items' 90-day figures.
func sumNinetyDays(items []store.ProgramListItem) ninetyDays {
	var out ninetyDays
	paidBy := map[string]float64{}
	for _, it := range items {
		if it.Reports90d != nil {
			out.Received += *it.Reports90d
			out.ReceivedPrograms++
			out.ReceivedEstimated = out.ReceivedEstimated || it.Reports90dIsEstimate()
		}
		if pays(it, it.Paid90d) {
			paidBy[programCurrency(it)] += *it.Paid90d
			out.PaidPrograms++
			out.PaidEstimated = out.PaidEstimated || it.Paid90dIsEstimate()
		}
	}
	for c, v := range paidBy {
		out.Paid = append(out.Paid, moneySum{c, v})
	}
	slices.SortFunc(out.Paid, func(a, b moneySum) int {
		return cmp.Or(cmp.Compare(b.Amount, a.Amount), cmp.Compare(a.Currency, b.Currency))
	})
	return out
}

// statsTotalsJSON is /api/v1/stats's totals: items (every program the
// filter matches) by the currency their amounts are in, the most programs
// first, each with its 90-day figures added up as the Stats page adds them
// and the bounties programs that pay have paid all time.
func statsTotalsJSON(items []store.ProgramListItem) []apiStatsTotal {
	byCurrency := map[string][]store.ProgramListItem{}
	for _, it := range items {
		c := programCurrency(it)
		byCurrency[c] = append(byCurrency[c], it)
	}
	out := []apiStatsTotal{}
	for c, group := range byCurrency {
		n := sumNinetyDays(group)
		t := apiStatsTotal{Currency: c, Programs: len(group), ReportsReceived90d: n.Received, ReceivedPrograms: n.ReceivedPrograms,
			ReceivedEstimated: n.ReceivedEstimated, PaidPrograms: n.PaidPrograms, PaidEstimated: n.PaidEstimated}
		if len(n.Paid) > 0 { // the group's one currency
			t.BountiesPaid90d = round2(n.Paid[0].Amount)
		}
		for _, it := range group {
			if pays(it, it.PaidAll) {
				t.BountiesPaidTotal += *it.PaidAll
				t.PaidTotalEstimated = t.PaidTotalEstimated || it.PaidAllEstimated
			}
		}
		t.BountiesPaidTotal = round2(t.BountiesPaidTotal)
		out = append(out, t)
	}
	slices.SortFunc(out, func(a, b apiStatsTotal) int {
		return cmp.Or(cmp.Compare(b.Programs, a.Programs), cmp.Compare(a.Currency, b.Currency))
	})
	return out
}
