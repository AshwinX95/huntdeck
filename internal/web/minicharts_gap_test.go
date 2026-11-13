package web

import (
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/mdpsec/huntdeck/internal/store"
)

func dayValues(vals ...float64) []store.DayValue {
	start := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	out := make([]store.DayValue, len(vals))
	for i, v := range vals {
		out[i] = store.DayValue{At: start.AddDate(0, 0, i), Value: v}
	}
	return out
}

func TestDryRuns(t *testing.T) {
	for _, tt := range []struct {
		vals []float64
		want []bool
	}{
		{[]float64{7, 7, 0, 0, 10}, []bool{false, false, true, true, false}},
		{[]float64{0, 0, 1, 0, 1, 0}, []bool{false, false, false, true, false, false}}, // zeros at the ends are the line's
		{[]float64{3, 2, 1}, []bool{false, false, false}},
		{[]float64{0, 0, 0}, []bool{false, false, false}},
	} {
		if got := dryRuns(dayValues(tt.vals...)); !slices.Equal(got, tt.want) {
			t.Errorf("dryRuns(%v) = %v, want %v", tt.vals, got, tt.want)
		}
	}
}

// A count that empties between reports breaks its line, with a dotted
// link at the two sides' heights instead of a drop to the foot (where 0
// is, on the scale); the dry days keep their 0 for tooltips.
func TestSparkBreaksAcrossDrySpells(t *testing.T) {
	days := dayValues(8, 8, 7, 0, 0, 0, 10, 10)
	sp := buildSpark(days, trendW, trendH, trendMetric{value: func(v float64) string { return formatCount(v, "") }, words: "resolved in 90 days"})
	if strings.Count(sp.Line, "M") != 2 || strings.Count(sp.Area, "Z") != 2 || strings.Count(sp.Gap, "M") != 1 {
		t.Fatalf("line %q, area %q, gap %q", sp.Line, sp.Area, sp.Gap)
	}
	if strings.Contains(sp.Line, " 28") || strings.Contains(sp.Gap, " 28") { // the foot: h - pad
		t.Errorf("line or gap drops to the foot: %q / %q", sp.Line, sp.Gap)
	}
	if !strings.Contains(sp.Days, "· 0") {
		t.Errorf("dry days lost their 0: %q", sp.Days)
	}
	// A lone day between dry spells still shows, as a dot.
	sp = buildSpark(dayValues(1, 0, 1, 0, 1), trendW, trendH, trendMetric{value: func(v float64) string { return formatCount(v, "") }})
	if strings.Count(sp.Line, "M") != 3 || strings.Count(sp.Gap, "M") != 2 {
		t.Errorf("lone days: line %q, gap %q", sp.Line, sp.Gap)
	}
	// Without dry spells, one line as before.
	sp = buildSpark(dayValues(3, 4, 0), trendW, trendH, trendMetric{value: func(v float64) string { return formatCount(v, "") }})
	if strings.Count(sp.Line, "M") != 1 || sp.Gap != "" {
		t.Errorf("no dry spell: line %q, gap %q", sp.Line, sp.Gap)
	}
}
