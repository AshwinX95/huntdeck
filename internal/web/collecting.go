package web

import (
	"fmt"
	"time"

	"github.com/mdpsec/huntdeck/internal/store"
)

// A 90-day figure worked out from a platform's all-time totals (Intigriti's
// reports received and bounties paid) needs collectingDays of them saved,
// a day at a time (syncer.count90d), so a program HuntDeck has just met,
// such as a new invitation, has none for its first week. Its chart shows
// from the first day instead: an early line drawn from the totals there
// are (backfillCounted, dashed: provisional), or until there are enough of
// those a dotted placeholder that ends at today's total.

// collectingDays is how many days of saved totals a counted figure waits
// for (syncer's minScaledDays).
const collectingDays = 7

// collectingSpark is the chart for a figure still being collected from
// totals, the program's saved all-time totals (oldest first). what names
// the figure ("reports received"); m words its values as estimates (≈).
func collectingSpark(totals []store.DayValue, started *time.Time, m trendMetric, what, platform string) spark {
	if len(totals) == 0 {
		return spark{}
	}
	first := totals[0].At.UTC()
	days := savedDays(totals)
	ready := first.AddDate(0, 0, collectingDays).Format("Jan 2")
	about := fmt.Sprintf("%s in 90 days: worked out from %s's all-time totals, which HuntDeck saves daily. %d of %d days saved (since %s); the figure shows from %s.",
		upperFirst(what), platformLabel(platform), days, collectingDays, first.Format("Jan 2"), ready)
	m.heading += ", provisional"
	if sp := buildSpark(backfillCounted(totals, nil, started), trendW, trendH, m); sp.Line != "" {
		sp.Provisional = true
		sp.Summary = sp.Summary + ". Provisional: " + about
		return sp
	}
	return spark{Collecting: about + " Until then the line is dotted."}
}

// savedDays counts the days of totals.
func savedDays(totals []store.DayValue) int {
	n, last := 0, ""
	for _, t := range totals {
		if d := t.At.UTC().Format(time.DateOnly); d != last {
			n, last = n+1, d
		}
	}
	return n
}

// firstDaySpark is the dotted placeholder for a figure saved on a single
// day so far, nonzero: its line starts with the next day's. Empty otherwise.
func firstDaySpark(days []store.DayValue, m trendMetric) spark {
	if savedDays(days) != 1 || days[len(days)-1].Value == 0 {
		return spark{}
	}
	last := days[len(days)-1]
	at := last.At.UTC()
	return spark{FirstDay: true, Heading: m.heading, points: days[len(days)-1:],
		Collecting: fmt.Sprintf("%s: %s, saved %s. HuntDeck saves it daily: its line starts with the next day's (%s).",
			m.heading, m.value(last.Value), at.Format("Jan 2"), at.AddDate(0, 0, 1).Format("Jan 2"))}
}

// Shown reports whether the chart draws anything: a line, or the
// placeholder of a figure being collected.
func (s spark) Shown() bool { return s.Line != "" || s.Collecting != "" }

// CellClass is a list cell's class for its chart: " charted" where the chart
// stands in for the number, " firstday" where the placeholder sits beside it.
func (s spark) CellClass() string {
	switch {
	case s.FirstDay:
		return " firstday"
	case s.Shown():
		return " charted"
	}
	return ""
}

// collectingPaid reports whether p's bounties paid in 90 days is still being
// collected: Intigriti publishes only the all-time total (as the m-paid
// cell says).
func collectingPaid(p store.ProgramListItem) bool {
	return p.Paid90d == nil && p.PaidAll != nil && !p.PaidAllEstimated && p.Platform == "intigriti"
}
