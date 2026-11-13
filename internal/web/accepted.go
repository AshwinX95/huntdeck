package web

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/mdpsec/huntdeck/internal/humanize"
	"github.com/mdpsec/huntdeck/internal/store"
)

// The accepted-reports panel (Bugcrowd CrowdStream) charts a program's
// accepted, resolved and submitted reports per month. Months before the
// platform's window, when HuntDeck first read the feed, have no data.

type acceptedView struct {
	store.AcceptedSummary
	Accepted, Resolved, Submitted lineChart
	Bounties                      *lineChart // shown bounties per month; nil without any
	// Change in accepted reports, last 90 days vs the 90 before; "" when
	// the 90 before aren't fully known.
	CountDelta, CountDir string
}

func buildAcceptedView(sum store.AcceptedSummary, currency string) acceptedView {
	v := acceptedView{AcceptedSummary: sum}
	if sum.PrevKnown {
		v.CountDelta, v.CountDir = signed(float64(sum.Last90d-sum.Prev90d), func(f float64) string { return strconv.Itoa(int(f)) })
	}
	months := make([]time.Time, len(sum.Months))
	for i, m := range sum.Months {
		months[i] = m.Month
	}
	byMonth := func(value func(store.AcceptedMonth) (float64, string)) []chartValue {
		out := make([]chartValue, len(sum.Months))
		for i, m := range sum.Months {
			if known, _ := monthKnown(m.Month, sum.Since); !known {
				out[i] = chartValue{Tip: chartTip(m.Month.Format("Jan 2006"), "no data")}
				continue
			}
			n, tip := value(m)
			out[i] = chartValue{n, tip, true}
		}
		return out
	}
	at := func(m store.AcceptedMonth) string { return m.Month.Format("Jan 2006") }
	v.Accepted = buildMonthChart(months, byMonth(func(m store.AcceptedMonth) (float64, string) {
		value := strconv.Itoa(m.Accepted) + " accepted"
		if p := priorities(m.ByPriority); p != "" {
			value += " (" + p + ")"
		}
		return float64(m.Accepted), chartTip(at(m), value)
	}), chartOpts{Label: "Reports accepted per month", Open: 1})
	v.Resolved = buildMonthChart(months, byMonth(func(m store.AcceptedMonth) (float64, string) {
		return float64(m.Resolved), chartTip(at(m), strconv.Itoa(m.Resolved)+" resolved")
	}), chartOpts{Label: "Accepted reports resolved per month", Open: 1})
	v.Submitted = buildMonthChart(months, byMonth(func(m store.AcceptedMonth) (float64, string) {
		return float64(m.Submitted), chartTip(at(m), strconv.Itoa(m.Submitted)+" submitted")
	}), chartOpts{Label: "Accepted reports by the month they were submitted", Open: submittedOpen})
	paid := false
	for _, m := range sum.Months {
		paid = paid || m.Bounties > 0
	}
	if paid {
		c := buildMonthChart(months, byMonth(func(m store.AcceptedMonth) (float64, string) {
			return m.Bounties, chartTip(at(m), humanize.MoneyFull(m.Bounties, currency)+" shown")
		}), chartOpts{Label: "Bounties per month, shown on CrowdStream, by the month accepted", Open: 1, Format: moneyAxis(currency)})
		v.Bounties = &c
	}
	return v
}

// monthKnown says whether a month has data given where the data starts:
// not before since's month; from is since's date when the month is the one
// it starts in. Without since nothing is known.
func monthKnown(month time.Time, since *time.Time) (known bool, from string) {
	if since == nil {
		return false, ""
	}
	start := time.Date(since.Year(), since.Month(), 1, 0, 0, 0, 0, time.UTC)
	switch {
	case month.Before(start):
		return false, ""
	case month.Equal(start) && since.After(start):
		return true, since.Format("Jan 2")
	}
	return true, ""
}

// priorities words a priority split: "P1 1, P3 4, no priority 2".
func priorities(by [6]int) string {
	var parts []string
	for p := 1; p <= 5; p++ {
		if by[p] > 0 {
			parts = append(parts, fmt.Sprintf("P%d %d", p, by[p]))
		}
	}
	if by[0] > 0 {
		parts = append(parts, fmt.Sprintf("no priority %d", by[0]))
	}
	return strings.Join(parts, ", ")
}
