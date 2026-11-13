package web

import (
	"cmp"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/mdpsec/huntdeck/internal/store"
)

// lineChart is a number over time at full width, drawn like the trend
// sparklines (16-trends.css): a line with a fading fill, green when it
// went up and red when it went down (or the other way round for numbers
// where lower is better, such as days to resolve). Month charts have a
// point a month; the last Open of them aren't finished (this month; for
// reports counted by when they were submitted, the last few, still filling
// in as reports get resolved) and are dashed, so a partial month doesn't
// read as a drop. Day charts have a point a day from From to To.
//
// The line is drawn in a viewBox of chartW × chartH stretched to the
// chart's box; labels and the end dot go in an overlay positioned in
// percentages, so they keep their shape (inline styles are off under the
// page's CSP).
type lineChart struct {
	Months   []monthLabel // a month chart's months, oldest first
	From, To string       // a day chart's first and last day: "May 20"
	// Line and Area are the finished points; Rest and RestArea the
	// unfinished ones, joined to the last finished point. "" when there
	// aren't two points to join.
	Line, Area, Rest, RestArea string
	Tone                       string // "good", "bad" or "flat"
	// MaxLabel is the top gridline's value; HalfLabel the middle one's, ""
	// for none.
	MaxLabel, HalfLabel string
	Last                *chartEnd // the last finished point
	Days                string    // "x|y|tip;…" for the instant tooltip (app.js)
	Label               string    // what it charts, for screen readers
	Open                int       // how many points at the end aren't finished
}

// monthLabel is a month under a month chart.
type monthLabel struct {
	Label string // "Oct"
	Known bool   // false before the data starts
}

// chartEnd marks the last finished point, in percent of the chart's box.
// At the right edge its label ends at the point instead of centring on
// it, so it stays inside the box.
type chartEnd struct {
	X, Y  string // "62.5%"
	Label string
	Edge  bool
}

// chartValue is one point's number and its tooltip; Known is false for a
// month without data.
type chartValue struct {
	V     float64
	Tip   string
	Known bool
}

// The chart's viewBox: the trend charts' (app.js marks the hovered day in
// these units), stretched to the chart's box. chartTop leaves room above
// the peak for its label.
const (
	chartW, chartH = 100.0, 30.0
	chartTop       = 3.0
	// labelClear is how far (in viewBox units) the end dot must be from a
	// gridline for both labels to show at the right edge.
	labelClear = 5.0
)

// chartOpts says how a line chart reads.
type chartOpts struct {
	Label       string
	Open        int                  // unfinished points at the end
	Format      func(float64) string // gridline and dot labels; whole numbers if nil
	LowerBetter bool                 // a fall is good news
	MinSpan     time.Duration        // a day chart's shortest history; dayChartMin if 0
	// Floor scales from just under the lowest value instead of 0: for a
	// level that moves little against its size, such as open programs.
	Floor bool
}

// floorSpan is the least a Floor chart's scale spans, as a share of its
// peak: changes smaller than this stay small.
const floorSpan = .04

// plotLine lays out values at xs (0–chartW) on a scale from 0 (or, with
// o.Floor, from just under their lowest) to their peak.
func plotLine(xs []float64, values []chartValue, o chartOpts) lineChart {
	c := lineChart{Tone: "flat", Label: o.Label, Open: o.Open}
	format := o.Format
	if format == nil {
		format = func(v float64) string { return formatCount(v, "") }
	}
	peak := 0.0
	for _, v := range values {
		if v.Known {
			peak = math.Max(peak, v.V)
		}
	}
	lo, hi := 0.0, peak
	if o.Floor {
		low := peak
		for _, v := range values {
			if v.Known {
				low = math.Min(low, v.V)
			}
		}
		// A span widened to floorSpan grows both ways, so a flat line sits
		// in the middle; then a little room under the lowest.
		span := math.Max(peak-low, peak*floorSpan)
		extra := (span - (peak - low)) / 2
		hi, lo = peak+extra, math.Max(0, low-extra-span*.15)
	}
	c.MaxLabel = format(hi)
	if hi-lo >= 4 {
		c.HalfLabel = format((hi + lo) / 2)
	}
	type pt struct {
		i    int
		x, y float64
	}
	var finished, rest []pt
	var days []string
	done := len(values) - o.Open
	for i, v := range values {
		if !v.Known {
			continue
		}
		y := chartH
		if hi > lo {
			y = round1(chartH - (chartH-chartTop)*(v.V-lo)/(hi-lo))
		}
		p := pt{i, round1(xs[i]), y}
		tip := v.Tip
		if i >= done {
			tip += " (so far)"
			rest = append(rest, p)
		} else {
			finished = append(finished, p)
		}
		days = append(days, fmt.Sprintf("%g|%g|%s", p.x, p.y, tip))
	}
	c.Days = strings.Join(days, ";")
	if len(finished) > 0 {
		rest = append([]pt{finished[len(finished)-1]}, rest...)
		p := finished[len(finished)-1]
		c.Last = &chartEnd{X: pct(p.x / chartW), Y: pct(p.y / chartH), Label: format(values[p.i].V), Edge: p.x > chartW*.92}
		if c.Last.Edge { // the dot's label would sit on a gridline's
			if p.y-chartTop < labelClear {
				c.MaxLabel = ""
			}
			if math.Abs(p.y-(chartH+chartTop)/2) < labelClear {
				c.HalfLabel = ""
			}
		}
	}
	line := func(ps []pt) (string, string) {
		if len(ps) < 2 {
			return "", ""
		}
		l := linePath(len(ps), func(k int) (float64, float64) { return ps[k].x, ps[k].y })
		return l, fmt.Sprintf("%s L%g %g L%g %g Z", l, ps[len(ps)-1].x, chartH, ps[0].x, chartH)
	}
	c.Line, c.Area = line(finished)
	c.Rest, c.RestArea = line(rest)
	return c
}

// buildMonthChart charts values, one per month from months[0]. Its tone
// compares the last three finished months with the three before; a Floor
// chart's (a level's), its last month with its first.
func buildMonthChart(months []time.Time, values []chartValue, o chartOpts) lineChart {
	xs := make([]float64, len(months))
	for i := range months {
		xs[i] = (float64(i) + .5) / float64(len(months)) * chartW
	}
	c := plotLine(xs, values, o)
	for i, m := range months {
		c.Months = append(c.Months, monthLabel{Label: m.Format("Jan"), Known: values[i].Known})
	}
	var known []float64
	for _, v := range values[:max(len(values)-o.Open, 0)] {
		if v.Known {
			known = append(known, v.V)
		}
	}
	switch k := len(known); {
	case o.Floor && k >= 2: // a level: where it is against where it was
		c.Tone = toneOf(known[0], known[k-1], o.LowerBetter)
	case k >= 6:
		c.Tone = toneOf(sumf(known[k-6:k-3]), sumf(known[k-3:]), o.LowerBetter)
	}
	return c
}

// dayChartMin is the shortest history a day chart is drawn for: anything
// less, the headline's 90-day sparkline already shows.
const dayChartMin = 30 * 24 * time.Hour

// buildDayChart charts a daily series over its whole history, if it's at
// least o.MinSpan (dayChartMin) long; nil otherwise. Its tone compares the last day
// with the one 90 days before (or the first), as the sparklines do.
func buildDayChart(days []store.DayValue, o chartOpts, say func(float64) string) *lineChart {
	if len(days) < 2 || days[len(days)-1].At.Sub(days[0].At) < cmp.Or(o.MinSpan, dayChartMin) {
		return nil
	}
	days = thin(days, 200)
	first, last := days[0].At, days[len(days)-1].At
	span := last.Sub(first).Hours()
	xs := make([]float64, len(days))
	values := make([]chartValue, len(days))
	for i, d := range days {
		xs[i] = chartW * d.At.Sub(first).Hours() / span
		values[i] = chartValue{V: d.Value, Tip: chartTip(d.At.UTC().Format("Jan 2"), say(d.Value)), Known: true}
	}
	c := plotLine(xs, values, o)
	c.From, c.To = first.UTC().Format("Jan 2"), last.UTC().Format("Jan 2")
	base := days[0]
	for _, d := range days {
		if d.At.After(last.Add(-window90).Add(12 * time.Hour)) {
			break
		}
		base = d
	}
	c.Tone = toneOf(base.Value, days[len(days)-1].Value, o.LowerBetter)
	return &c
}

// toneOf says whether going from before to now is good news: a change of
// 5% or more either way, up being good unless lowerBetter.
func toneOf(before, now float64, lowerBetter bool) string {
	var change float64
	switch {
	case before == 0 && now == 0:
		return "flat"
	case before == 0:
		change = 100
	default:
		change = (now - before) / before * 100
	}
	if lowerBetter {
		change = -change
	}
	switch {
	case change >= 5:
		return "good"
	case change <= -5:
		return "bad"
	}
	return "flat"
}

// sumf adds vs.
func sumf(vs []float64) float64 {
	t := 0.0
	for _, v := range vs {
		t += v
	}
	return t
}

// pct is a fraction as a percentage for SVG: "62.5%".
func pct(f float64) string { return strconv.FormatFloat(round1(f*100), 'f', -1, 64) + "%" }

// Ends are the chart's first and last month or day, for under a
// sparkline: "Dec", "Nov" or "May 20", "Nov 5".
func (c lineChart) Ends() []string {
	if n := len(c.Months); n > 0 {
		return []string{c.Months[0].Label, c.Months[n-1].Label}
	}
	return []string{c.From, c.To}
}
