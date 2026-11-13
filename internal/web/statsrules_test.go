package web

import (
	"reflect"
	"testing"
	"time"

	"github.com/mdpsec/huntdeck/internal/store"
)

// statsMonths is a year of months from f, for the Stats page's tests.
func statsMonths(f func(i int) store.PlatformMonth) []store.PlatformMonth {
	out := make([]store.PlatformMonth, overviewMonths)
	for i := range out {
		out[i] = f(i)
	}
	return out
}

// The All platforms row marks a month's valid reports ≈ where a platform it
// adds up estimated some, as that platform's own row does, and the API
// with it.
func TestStatsAllRowValidEstimated(t *testing.T) {
	start := time.Date(2025, 12, 1, 0, 0, 0, 0, time.UTC)
	since := start
	overviews := []store.PlatformOverview{
		{Platform: "hackerone", ValidKind: "resolved", Months: statsMonths(func(int) store.PlatformMonth { return store.PlatformMonth{Valid: 20} })},
		{Platform: "intigriti", ValidKind: "accepted", ValidSince: &since, Months: statsMonths(func(i int) store.PlatformMonth {
			return store.PlatformMonth{Valid: 5, Estimated: i < 10} // estimated until last month
		})},
	}
	d := buildStats(overviews, []string{"hackerone", "intigriti"}, nil, nil, start)
	api := platformRowJSON(d.All)
	for i, m := range d.All.overview.Months {
		want := i < 10
		if m.Estimated != want || api.Months[i].ValidEstimated != want {
			t.Errorf("month %d: all row estimated %v, API %v; want %v", i, m.Estimated, api.Months[i].ValidEstimated, want)
		}
	}
	if d.All.ValidLast != 25 || d.All.ValidEst {
		t.Errorf("last month: %d valid, estimated %v; want 25, counted", d.All.ValidLast, d.All.ValidEst)
	}
}

// The Stats rows, the Programs API and the Programs list mark the same
// 90-day figures ≈: estimated by how they're worked out, or counted from
// under 90 days of saved totals; 90 days counted is exact.
func TestStatsNinetyDayEstimatesFollowTheProgram(t *testing.T) {
	start := time.Date(2025, 12, 1, 0, 0, 0, 0, time.UTC)
	n := func(v int) *int { return &v }
	item := func(platform string, counted *int, published bool) store.ProgramListItem {
		paid, recv := 500.0, 12
		it := store.ProgramListItem{ProgramRef: store.ProgramRef{Platform: platform}, OffersBounty: true, StatsCurrency: "EUR",
			Paid90d: &paid, Reports90d: &recv, CountedDays: counted, Paid90dEstimated: published}
		if platform == "bugcrowd" {
			it.Reports90d = nil
		}
		return it
	}
	tests := []struct {
		name           string
		item           store.ProgramListItem
		paid, received bool
	}{
		{"counted from 90 days", item("intigriti", n(90), false), false, false},
		{"counted from 30 days", item("intigriti", n(30), false), true, true},
		{"published", item("intigriti", nil, false), false, false},
		{"accepted × average", item("bugcrowd", nil, true), true, false},
		{"rewarded rise × average", item("bugcrowd", n(90), false), true, false},
	}
	for _, tt := range tests {
		o := store.PlatformOverview{Platform: tt.item.Platform, Months: make([]store.PlatformMonth, overviewMonths)}
		row := buildStats([]store.PlatformOverview{o}, nil, []store.ProgramListItem{tt.item}, nil, start).Rows[0]
		api := numbersJSON(tt.item)
		if row.PaidEstimated != tt.paid || api.Paid90dEstimated != tt.paid || tt.item.Paid90dIsEstimate() != tt.paid {
			t.Errorf("%s: paid ≈ on Stats %v, API %v; want %v", tt.name, row.PaidEstimated, api.Paid90dEstimated, tt.paid)
		}
		if row.ReceivedEstimated != tt.received || api.Received90dEstimated != tt.received {
			t.Errorf("%s: received ≈ on Stats %v, API %v; want %v", tt.name, row.ReceivedEstimated, api.Received90dEstimated, tt.received)
		}
	}
}

// /api/v1/stats's totals are the Stats page's figures under the same
// names: the Programs list's estimates included, money from programs that
// pay, every program the filter matches counted.
func TestStatsTotalsMatchTheStatsPage(t *testing.T) {
	f := func(v float64) *float64 { return &v }
	n := func(v int) *int { return &v }
	items := []store.ProgramListItem{
		{ProgramRef: store.ProgramRef{Platform: "hackerone"}, OffersBounty: true, StatsCurrency: "USD", Paid90d: f(1000), Reports90d: n(40), PaidAll: f(9000)},
		// Stopped paying: its last figures aren't bounties now.
		{ProgramRef: store.ProgramRef{Platform: "hackerone"}, StatsCurrency: "USD", Paid90d: f(500), Reports90d: n(10), PaidAll: f(4000)},
		// Bugcrowd's estimates, which the Programs list shows.
		{ProgramRef: store.ProgramRef{Platform: "bugcrowd"}, OffersBounty: true, StatsCurrency: "USD", Paid90d: f(300), Paid90dEstimated: true,
			PaidAll: f(2000), PaidAllEstimated: true},
		{ProgramRef: store.ProgramRef{Platform: "intigriti"}, OffersBounty: true, StatsCurrency: "EUR", Paid90d: f(200), Reports90d: n(5), CountedDays: n(30)},
		{ProgramRef: store.ProgramRef{Platform: "intigriti"}, OffersBounty: true, Currency: "EUR"}, // no stats yet
	}
	o := store.PlatformOverview{Months: make([]store.PlatformMonth, overviewMonths)}
	row := buildStats([]store.PlatformOverview{o}, nil, items, nil, time.Date(2025, 12, 1, 0, 0, 0, 0, time.UTC)).All
	totals := statsTotalsJSON(items)
	want := []apiStatsTotal{
		{Currency: "USD", Programs: 3, BountiesPaid90d: 1300, PaidPrograms: 2, PaidEstimated: true, ReportsReceived90d: 50, ReceivedPrograms: 2,
			BountiesPaidTotal: 11000, PaidTotalEstimated: true},
		{Currency: "EUR", Programs: 2, BountiesPaid90d: 200, PaidPrograms: 1, PaidEstimated: true, ReportsReceived90d: 5, ReceivedPrograms: 1,
			ReceivedEstimated: true},
	}
	if !reflect.DeepEqual(totals, want) {
		t.Errorf("totals = %+v\nwant %+v", totals, want)
	}
	paid := map[string]float64{}
	for _, m := range row.Paid {
		paid[m.Currency] = m.Amount
	}
	if paid["USD"] != totals[0].BountiesPaid90d || paid["EUR"] != totals[1].BountiesPaid90d ||
		*row.ReceivedNow != totals[0].ReportsReceived90d+totals[1].ReportsReceived90d || row.PaidPrograms != totals[0].PaidPrograms+totals[1].PaidPrograms {
		t.Errorf("Stats page: paid %v, received %d over %d paying programs; the API's totals differ", paid, *row.ReceivedNow, row.PaidPrograms)
	}
}

// Sorting a platform's programs by paid or bounties never compares euros
// with dollars: the lead currency's programs come first, each currency
// sorted within itself, and those without the number last.
func TestSortStatsRowsByCurrency(t *testing.T) {
	row := func(name, cur string, amount float64) platformStats {
		r := platformStats{Item: &store.ProgramListItem{ProgramRef: store.ProgramRef{Name: name}}}
		if amount > 0 {
			r.Paid = []moneySum{{cur, amount}}
			r.Bounties, r.BountiesLast, r.BountiesCurrency = &lineChart{}, amount, cur
		}
		return r
	}
	tests := []struct {
		key, lead string
		rev       bool
		want      string
	}{
		{"paid", "EUR", false, "BAdc-"},
		{"paid", "EUR", true, "ABcd-"},
		{"paid", "USD", false, "dcBA-"},
		{"bounties", "EUR", false, "BAdc-"},
	}
	for _, tt := range tests {
		// Upper case pays in euros, lower case in dollars; "-" nothing.
		rows := []platformStats{row("A", "EUR", 100), row("c", "USD", 50), row("-", "", 0), row("B", "EUR", 300), row("d", "USD", 900)}
		sortStatsRows(rows, tt.key, tt.rev, tt.lead)
		got := ""
		for _, r := range rows {
			got += r.Item.Name
		}
		if got != tt.want {
			t.Errorf("by %s, lead %s, rev %v: %s; want %s", tt.key, tt.lead, tt.rev, got, tt.want)
		}
	}
}
