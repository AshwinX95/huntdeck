package web

import (
	"strings"
	"testing"
	"time"

	"github.com/mdpsec/huntdeck/internal/store"
)

func TestMiniLine(t *testing.T) {
	day := func(n int, v float64) store.DayValue {
		return store.DayValue{At: time.Date(2026, 10, 1+n, 9, 0, 0, 0, time.UTC), Value: v}
	}
	mini := func(days []store.DayValue) miniLine {
		return buildLine(days, 96, 22, func(d store.DayValue) string { return chartTip(d.At.UTC().Format("Jan 2"), formatCount(d.Value, "")) })
	}
	if l := mini([]store.DayValue{day(0, 100)}); l.Line != "" {
		t.Errorf("one day drew %q", l.Line)
	}
	l := mini([]store.DayValue{day(0, 100), day(1, 200), day(3, 150)})
	if !strings.HasPrefix(l.Line, "M2 20 L") || len(l.Points) != 3 || l.Last != l.Points[2] {
		t.Errorf("line = %+v", l)
	}
	if l.Points[1].Y != 2 || l.Points[1].Title != "Oct 2 · 200" {
		t.Errorf("peak point = %+v", l.Points[1])
	}
	// A small move stays off the top and bottom (chartRange).
	l = mini([]store.DayValue{day(0, 100), day(1, 120), day(3, 110)})
	if l.Points[0].Y >= 20 || l.Points[1].Y <= 2 {
		t.Errorf("small move fills the chart: %+v", l.Points)
	}
}
