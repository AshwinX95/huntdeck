package web

import (
	"strings"
	"testing"
	"time"

	"github.com/mdpsec/huntdeck/internal/humanize"
	"github.com/mdpsec/huntdeck/internal/store"
)

func TestBuildMonthChart(t *testing.T) {
	months := make([]time.Time, 12)
	for i := range months {
		months[i] = time.Date(2025, time.December, 1, 0, 0, 0, 0, time.UTC).AddDate(0, i, 0)
	}
	vals := func(ns ...float64) []chartValue {
		out := make([]chartValue, len(ns))
		for i, n := range ns {
			out[i] = chartValue{V: n, Tip: months[i].Format("Jan"), Known: n >= 0}
		}
		return out
	}
	tests := []struct {
		name        string
		values      []chartValue
		open        int
		lowerBetter bool
		tone        string
		max, half   string
		last        string // the dot's label; "" for none
		line, rest  bool
	}{
		{"rising, this month open", vals(0, 3, 28, 68, 29, 27, 16, 28, 13, 33, 29, 5), 1, false, "good", "68", "34", "29", true, true},
		{"falling, three months open", vals(4, 34, 49, 16, 23, 15, 15, 15, 15, 6, 12, 0), 3, false, "bad", "49", "25", "15", true, true},
		{"falling is good for days to resolve", vals(4, 34, 49, 16, 23, 15, 15, 15, 15, 6, 12, 0), 3, true, "good", "49", "25", "15", true, true},
		{"flat", vals(5, 5, 5, 5, 5, 5, 5, 5, 5, 5, 5, 5), 1, false, "flat", "5", "3", "5", true, true},
		{"data starts late, down 5 to 2", vals(-1, -1, -1, -1, 0, 1, 3, 1, 0, 2, 0, 0), 1, false, "bad", "3", "", "0", true, true},
		{"too few months to judge", vals(-1, -1, -1, -1, -1, -1, -1, 1, 3, 1, 0, 0), 1, false, "flat", "3", "", "0", true, true},
		{"nothing at all", vals(0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0), 1, false, "flat", "0", "", "0", true, true},
		{"one known month", vals(-1, -1, -1, -1, -1, -1, -1, -1, -1, -1, -1, 2), 1, false, "flat", "2", "", "", false, false},
	}
	for _, tt := range tests {
		c := buildMonthChart(months, tt.values, chartOpts{Label: "x", Open: tt.open, LowerBetter: tt.lowerBetter})
		if c.Tone != tt.tone || c.MaxLabel != tt.max || c.HalfLabel != tt.half {
			t.Errorf("%s: tone %s max %q half %q, want %s %q %q", tt.name, c.Tone, c.MaxLabel, c.HalfLabel, tt.tone, tt.max, tt.half)
		}
		switch {
		case tt.last == "" && c.Last != nil:
			t.Errorf("%s: a dot at %+v", tt.name, c.Last)
		case tt.last != "" && (c.Last == nil || c.Last.Label != tt.last):
			t.Errorf("%s: dot %+v, want %q", tt.name, c.Last, tt.last)
		}
		if (c.Rest != "") != tt.rest || (c.Line != "") != tt.line {
			t.Errorf("%s: line %q rest %q", tt.name, c.Line, c.Rest)
		}
		known := 0
		for i, m := range c.Months {
			if m.Label != months[i].Format("Jan") {
				t.Errorf("%s: month %d labelled %q", tt.name, i, m.Label)
			}
			if m.Known {
				known++
			}
		}
		if got := len(strings.Split(c.Days, ";")); c.Days != "" && got != known {
			t.Errorf("%s: %d tooltip points for %d known months", tt.name, got, known)
		}
		if known > 0 && !strings.HasSuffix(c.Days, "(so far)") {
			t.Errorf("%s: the open month's tooltip doesn't say so: %q", tt.name, c.Days)
		}
	}
	// The line runs over the finished months only, centred in each month's
	// slot, from 0 at the bottom to the peak below the top's margin.
	c := buildMonthChart(months, vals(0, 3, 28, 68, 29, 27, 16, 28, 13, 33, 29, 5), chartOpts{Open: 1})
	if !strings.HasPrefix(c.Line, "M4.2 30 ") || !strings.Contains(c.Line, "L29.2 3") || strings.Contains(c.Line, "95.8") {
		t.Errorf("line = %q", c.Line)
	}
	if !strings.HasPrefix(c.Rest, "M87.5 ") || !strings.HasSuffix(c.Rest, "L95.8 28") {
		t.Errorf("rest = %q", c.Rest)
	}
	if c.Last.X != "87.5%" || c.Last.Edge {
		t.Errorf("dot at %+v", c.Last)
	}
	// Money charts label in the program's currency.
	m := buildMonthChart(months, money10750(vals(0, 150, 600, 3200, 1850, 6124, 6150, 6700, 5500, 3700, 10750, 3950)), chartOpts{Open: 1, Format: moneyAxis("USD")})
	if m.MaxLabel != "$11k" || m.Last.Label != "$11k" || !strings.Contains(m.Days, "$10,750") {
		t.Errorf("money labels: %q, %+v", m.MaxLabel, m.Last)
	}
}

// money10750 gives October's tooltip its full amount.
func money10750(v []chartValue) []chartValue {
	v[10].Tip = chartTip("Oct 2026", humanize.MoneyFull(10750, "USD")+" shown")
	return v
}

func TestBuildDayChart(t *testing.T) {
	start := time.Date(2026, 5, 20, 12, 0, 0, 0, time.UTC)
	series := func(n int, f func(i int) float64) []store.DayValue {
		out := make([]store.DayValue, n)
		for i := range out {
			out[i] = store.DayValue{At: start.AddDate(0, 0, i), Value: f(i)}
		}
		return out
	}
	say := func(v float64) string { return formatCount(v, "") + " received" }
	if c := buildDayChart(series(20, func(int) float64 { return 5 }), chartOpts{}, say); c != nil {
		t.Errorf("20 days charted: %+v", c)
	}
	rising := buildDayChart(series(170, func(i int) float64 { return float64(800 + i*5) }), chartOpts{Label: "x"}, say)
	if rising == nil || rising.Tone != "good" || rising.From != "May 20" || rising.To != "Nov 5" || rising.Months != nil {
		t.Fatalf("rising: %+v", rising)
	}
	// The last day is the peak at the right edge: its label stands in for
	// the gridline's.
	if !rising.Last.Edge || rising.MaxLabel != "" || rising.Last.Label != "1,645" {
		t.Errorf("rising end: %+v, max %q", rising.Last, rising.MaxLabel)
	}
	if n := len(strings.Split(rising.Days, ";")); n > 200 {
		t.Errorf("%d points, want at most 200", n)
	}
	falling := buildDayChart(series(60, func(i int) float64 { return float64(1000 - i*10) }), chartOpts{}, say)
	if falling.Tone != "bad" || falling.MaxLabel != "1,000" || falling.Last.Edge != true {
		t.Errorf("falling: tone %s max %q end %+v", falling.Tone, falling.MaxLabel, falling.Last)
	}
	if lower := buildDayChart(series(60, func(i int) float64 { return float64(1000 - i*10) }), chartOpts{LowerBetter: true}, say); lower.Tone != "good" {
		t.Errorf("lower is better, falling: %s", lower.Tone)
	}
}

func TestHistoryCharts(t *testing.T) {
	start := time.Date(2026, 5, 20, 12, 0, 0, 0, time.UTC)
	var hist []store.ProgramStats
	for i := range 170 {
		var s store.ProgramStats
		s.CapturedAt = start.AddDate(0, 0, i)
		s.Currency = "USD"
		n := 800 + i
		s.ReportsLast90d = &n
		if i >= 160 { // paid totals only for the last ten days
			v := 55000.0 + float64(i)
			s.BountiesPaidLast90d = &v
		}
		hist = append(hist, s)
	}
	got := historyCharts(hist, true)
	if len(got) != 1 || got[0].Title != "Reports received · 90 days" {
		t.Fatalf("charts: %+v", got)
	}
	if !strings.Contains(got[0].Chart.Days, "969 received in 90 days") {
		t.Errorf("tooltips: %.120s…", got[0].Chart.Days)
	}
	if historyCharts(nil, true) != nil {
		t.Error("charts without history")
	}
}

func TestEndLabelClearsGridlines(t *testing.T) {
	xs := []float64{0, 50, 100}
	v := func(ns ...float64) []chartValue {
		out := make([]chartValue, len(ns))
		for i, n := range ns {
			out[i] = chartValue{V: n, Known: true}
		}
		return out
	}
	tests := []struct {
		name      string
		values    []chartValue
		max, half string
	}{
		{"ends at the peak", v(10, 50, 100), "", "50"},
		{"ends just under the peak", v(10, 100, 98), "", "50"},
		{"ends on the middle line", v(10, 100, 51), "100", ""},
		{"ends low", v(10, 100, 5), "100", "50"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := plotLine(xs, tt.values, chartOpts{})
			if c.MaxLabel != tt.max || c.HalfLabel != tt.half {
				t.Errorf("labels %q, %q; want %q, %q", c.MaxLabel, c.HalfLabel, tt.max, tt.half)
			}
		})
	}
}

// A Floor chart scales from just under its lowest value, so a level that
// moves little against its size still shows its trend; a tiny change
// stays tiny.
func TestPlotLineFloor(t *testing.T) {
	xs := []float64{0, 50, 100}
	v := func(ns ...float64) []chartValue {
		out := make([]chartValue, len(ns))
		for i, n := range ns {
			out[i] = chartValue{V: n, Known: true}
		}
		return out
	}
	ys := func(c lineChart) []string {
		var out []string
		for _, d := range strings.Split(c.Days, ";") {
			out = append(out, strings.Split(d, "|")[1])
		}
		return out
	}
	tests := []struct {
		name   string
		values []chartValue
		floor  bool
		ys     []string
	}{
		{"from zero, a big level looks flat", v(1000, 1050, 1100), false, []string{"5.5", "4.2", "3"}},
		{"floored, the rise shows", v(1000, 1050, 1100), true, []string{"26.5", "14.7", "3"}},
		{"floored, a tiny change stays small", v(1000, 1001, 1001), true, []string{"15", "14.4", "14.4"}},
		{"floored, flat sits in the middle", v(500, 500, 500), true, []string{"14.7", "14.7", "14.7"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ys(plotLine(xs, tt.values, chartOpts{Floor: tt.floor}))
			if strings.Join(got, " ") != strings.Join(tt.ys, " ") {
				t.Errorf("ys %v; want %v", got, tt.ys)
			}
		})
	}
}

// A level's tone is where it is against where it was: a steady rise is
// good news even when each quarter is only a little up on the last.
func TestMonthChartLevelTone(t *testing.T) {
	months := make([]time.Time, 12)
	values := make([]chartValue, 12)
	for i := range months {
		months[i] = time.Date(2025, time.December, 1, 0, 0, 0, 0, time.UTC).AddDate(0, i, 0)
		values[i] = chartValue{V: float64(416 + 3*i), Known: true}
	}
	if c := buildMonthChart(months, values, chartOpts{}); c.Tone != "flat" {
		t.Errorf("as monthly counts, tone %q; want flat", c.Tone)
	}
	if c := buildMonthChart(months, values, chartOpts{Floor: true}); c.Tone != "good" {
		t.Errorf("as a level, tone %q; want good", c.Tone)
	}
}
