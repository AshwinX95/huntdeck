package web

import (
	"github.com/mdpsec/huntdeck/internal/humanize"
	"github.com/mdpsec/huntdeck/internal/store"
)

// historyChart is one of the platform's own numbers over every day
// HuntDeck has saved it, for the program page's Stats tab.
type historyChart struct {
	Title, Hint string
	Chart       *lineChart
}

// historyMetric is a number in the daily stats snapshots.
type historyMetric struct {
	title, hint string
	get         func(store.ProgramStats) *float64
	say         func(v float64, currency string) string
	money       bool
}

// historyMetrics are the snapshot numbers worth charting over time; the
// rest change too slowly or have too little history.
var historyMetrics = []historyMetric{
	{"Reports received · 90 days", "Every report the program received in the 90 days before each day, duplicates and N/A included: the competition",
		func(s store.ProgramStats) *float64 { return intf(s.ReportsLast90d) },
		func(v float64, _ string) string { return formatCount(v, "") + " received in 90 days" }, false},
	{"Paid · 90 days", "Bounties paid in the 90 days before each day",
		func(s store.ProgramStats) *float64 { return s.BountiesPaidLast90d },
		func(v float64, c string) string { return humanize.MoneyFull(v, c) + " paid in 90 days" }, true},
	{"Total paid", "Bounties paid since the program started",
		func(s store.ProgramStats) *float64 { return s.BountiesPaid },
		func(v float64, c string) string { return humanize.MoneyFull(v, c) + " paid all time" }, true},
}

// historyCharts charts each metric with at least dayChartMin of history;
// the money ones only for a program that pays.
func historyCharts(hist []store.ProgramStats, paying bool) []historyChart {
	if len(hist) == 0 {
		return nil
	}
	currency := hist[len(hist)-1].Currency
	var out []historyChart
	for _, m := range historyMetrics {
		if m.money && !paying {
			continue // money charts only for programs that pay
		}
		var days []store.DayValue
		for _, s := range hist {
			if v := m.get(s); v != nil {
				days = append(days, store.DayValue{At: s.CapturedAt, Value: *v})
			}
		}
		o := chartOpts{Label: m.title}
		if m.money {
			o.Format = moneyAxis(currency)
		}
		say := func(v float64) string { return m.say(v, currency) }
		if c := buildDayChart(days, o, say); c != nil {
			out = append(out, historyChart{Title: m.title, Hint: m.hint, Chart: c})
		}
	}
	return out
}

// intf is an optional count as a float.
func intf(n *int) *float64 {
	if n == nil {
		return nil
	}
	f := float64(*n)
	return &f
}

// topBeyondTable says whether a program's top bounty is more than its
// bounty table pays: worth showing on its own. One the table covers only
// repeats it.
func topBeyondTable(s *store.ProgramStats) bool {
	top := s.TopBountyHigh
	if top == nil {
		top = s.TopBountyLow
	}
	if top == nil {
		return false
	}
	tableMax := 0.0
	for _, r := range s.SeverityRanges() {
		tableMax = max(tableMax, r.Max)
	}
	return *top > tableMax
}
