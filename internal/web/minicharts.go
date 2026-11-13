package web

import (
	"fmt"
	"math"
	"slices"
	"strings"

	"github.com/mdpsec/huntdeck/internal/store"
)

// Small line charts for the Programs list's trend cells (programtrends.go):
// inline SVG, so the strict CSP holds.

// miniLine is a number by day, as a line on its own scale with a tooltip
// per point.
type miniLine struct {
	Line   string // SVG path; "" when there are fewer than two days
	Area   string // the fill under Line, down to the chart's foot
	Gap    string // dotted links across dry spells (dryRuns), "" for none
	Points []sparkPoint
	Last   sparkPoint
}

// dryRuns marks the days of a count that are zero between days that
// aren't: a 90-day count of sparse reports empties when the last one
// ages out and fills again with the next. Drawn as a drop to the chart's
// foot that reads as a collapse, or as missing data; so the line breaks
// there instead, a dotted link joining its two sides, and the days keep
// their 0 in their tooltips. Zeros at either end are the line's own (a
// 0 now is the cell's number).
func dryRuns(days []store.DayValue) []bool {
	dry := make([]bool, len(days))
	first := slices.IndexFunc(days, func(d store.DayValue) bool { return d.Value != 0 })
	last := len(days) - 1
	for last >= 0 && days[last].Value == 0 {
		last--
	}
	for i := first + 1; first >= 0 && i < last; i++ {
		dry[i] = days[i].Value == 0
	}
	return dry
}

// buildLine charts daily values as a line of any size (its viewBox), on
// their own scale; title words each day's tooltip. Dry spells (dryRuns)
// are left out of the line, a dotted link across each.
func buildLine(days []store.DayValue, w, h float64, title func(store.DayValue) string) miniLine {
	var out miniLine
	if len(days) < 2 {
		return out
	}
	dry := dryRuns(days)
	first, last := days[0].At, days[len(days)-1].At
	// The scale is every day's, dry ones too: the count did reach 0, so a
	// stretch either side sits at its true height above it.
	lo, hi := days[0].Value, days[0].Value
	for _, d := range days {
		lo, hi = min(lo, d.Value), max(hi, d.Value)
	}
	lo, hi = chartRange(lo, hi)
	const pad = 2.0
	span := last.Sub(first).Seconds()
	for _, d := range days {
		x, y := w/2, h/2
		if span > 0 {
			x = pad + (w-2*pad)*d.At.Sub(first).Seconds()/span
		}
		if hi > lo {
			y = h - pad - (h-2*pad)*(d.Value-lo)/(hi-lo)
		}
		out.Points = append(out.Points, sparkPoint{X: round1(x), Y: round1(y), Title: title(d)})
	}
	// The line in stretches between dry spells, each with its fill; a dry
	// day sits on its dotted link, for its tooltip.
	var lines, areas, gaps []string
	for i := 0; i < len(days); {
		if dry[i] {
			j := i
			for dry[j] {
				j++
			}
			a, b := out.Points[i-1], out.Points[j]
			gaps = append(gaps, fmt.Sprintf("M%g %g L%g %g", a.X, a.Y, b.X, b.Y))
			for k := i; k < j; k++ {
				t := (out.Points[k].X - a.X) / (b.X - a.X)
				out.Points[k].Y = round1(a.Y + (b.Y-a.Y)*t)
			}
			i = j
			continue
		}
		j := i
		for j+1 < len(days) && !dry[j+1] {
			j++
		}
		seg := out.Points[i : j+1]
		path := linePath(len(seg), func(k int) (float64, float64) { return seg[k].X, seg[k].Y })
		if len(seg) == 1 { // a lone day: a dot, its round cap
			path += fmt.Sprintf(" L%g %g", seg[0].X, seg[0].Y)
		}
		lines = append(lines, path)
		areas = append(areas, fmt.Sprintf("%s L%g %g L%g %g Z", path, seg[len(seg)-1].X, h, seg[0].X, h))
		i = j + 1
	}
	out.Line, out.Area, out.Gap = strings.Join(lines, " "), strings.Join(areas, " "), strings.Join(gaps, " ")
	for i := range out.Points {
		left, right := 0.0, w
		if i > 0 {
			left = (out.Points[i-1].X + out.Points[i].X) / 2
		}
		if i < len(out.Points)-1 {
			right = (out.Points[i].X + out.Points[i+1].X) / 2
		}
		out.Points[i].HitX, out.Points[i].HitW = round1(left), round1(math.Max(right-left, 0.5))
	}
	out.Last = out.Points[len(out.Points)-1]
	return out
}

// linePath is an SVG path through n points (at(i) is the i-th), "M x y L
// x y …", leaving out a point between two at the same height: a number
// that stays put for days is one straight stretch, drawn the same with two
// points as with ninety. "" for no points.
func linePath(n int, at func(int) (x, y float64)) string {
	var b strings.Builder
	for i := range n {
		x, y := at(i)
		if i > 0 && i < n-1 {
			_, prev := at(i - 1)
			_, next := at(i + 1)
			if prev == y && next == y {
				continue
			}
		}
		if i == 0 {
			fmt.Fprintf(&b, "M%g %g", x, y)
		} else {
			fmt.Fprintf(&b, " L%g %g", x, y)
		}
	}
	return b.String()
}

// minSwing is the least a line chart's height stands for, as a share of
// its largest value.
const minSwing = 0.2

// chartRange is the range a line chart's height covers for values from lo
// to hi: theirs, widened around the middle to at least minSwing of the
// largest, so 17 then 18 reports draws as a gentle slope rather than a
// cliff. (Values that never go below zero still don't: their middle is
// at least half the largest.)
func chartRange(lo, hi float64) (float64, float64) {
	least := math.Max(math.Abs(lo), math.Abs(hi)) * minSwing
	if hi-lo >= least {
		return lo, hi
	}
	mid := (lo + hi) / 2
	return mid - least/2, mid + least/2
}
