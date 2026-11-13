package web

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/mdpsec/huntdeck/internal/platform"
	"github.com/mdpsec/huntdeck/internal/store"
)

// A figure still being collected charts from its first saved total: a
// dotted placeholder until the totals give an early line, then that line,
// provisional.
func TestCollectingSpark(t *testing.T) {
	day := func(n int) time.Time { return time.Date(2026, 11, 8+n, 10, 0, 0, 0, time.UTC) }
	totals := func(n int) []store.DayValue {
		var v []store.DayValue
		for i := range n {
			v = append(v, store.DayValue{At: day(i), Value: float64(240 + 2*i)})
		}
		return v
	}
	old := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	m := trendMetric{value: func(n float64) string { return "≈" + formatCount(n, "") }, words: "reports received in 90 days", heading: "Reports received · 90 days"}
	tests := []struct {
		name       string
		days       int
		line       bool
		collecting string
		summaryHas string
	}{
		{"nothing saved", 0, false, "", ""},
		{"the first day", 1, false, "1 of 7 days saved (since Nov 8); the figure shows from Nov 15.", ""},
		{"two days", 2, false, "2 of 7 days saved", ""},
		{"four days: one early point", 4, false, "4 of 7 days saved", ""},
		// From the third day's rise on: 2 a day × 90 = 180.
		{"an early line", 5, true, "", "Provisional: Reports received in 90 days"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sp := collectingSpark(totals(tt.days), &old, m, "reports received", "intigriti")
			if (sp.Line != "") != tt.line || sp.Provisional != tt.line || !strings.Contains(sp.Collecting, tt.collecting) ||
				(tt.collecting == "") != (sp.Collecting == "") {
				t.Fatalf("line %q provisional %v collecting %q", sp.Line, sp.Provisional, sp.Collecting)
			}
			if !strings.Contains(sp.Summary, tt.summaryHas) || sp.Shown() != (tt.days > 0) {
				t.Errorf("summary %q, shown %v", sp.Summary, sp.Shown())
			}
			if tt.line && !strings.Contains(sp.Days, "≈180") {
				t.Errorf("points %q, want ≈180", sp.Days)
			}
		})
	}
}

// On the lists, a program with an all-time total but no 90-day figure yet
// charts the dotted placeholder in place of "Pending".
func TestCollectingOnTheList(t *testing.T) {
	e := newTestEnv(t)
	total := 50
	e.fake.SetStats("evil", platform.ProgramStats{Currency: "USD", TotalReports: &total})
	if _, _, err := e.syncer.RefreshStats(context.Background(), e.fake, []string{"evil"}); err != nil {
		t.Fatal(err)
	}
	_, body := e.do(e.client(), http.MethodGet, "/programs?sort=name", nil, nil)
	i := strings.Index(body, `<td class="c-comp num nowrap charted" data-label="Received · 90d">`)
	if i < 0 {
		t.Fatal("no charted reports-received cell")
	}
	cell := body[i:]
	cell = cell[:strings.Index(cell, "</td>")]
	for _, want := range []string{">Pending</span>", `<span class="tchart flat collecting" title="Reports received in 90 days: worked out from`, "1 of 7 days saved"} {
		if !strings.Contains(cell, want) {
			t.Errorf("the cell lacks %q: %s", want, cell)
		}
	}
}

func TestFirstDaySpark(t *testing.T) {
	day := func(n int, v float64) store.DayValue {
		return store.DayValue{At: time.Date(2026, 11, 9+n, 22, 0, 0, 0, time.UTC), Value: v}
	}
	m := trendMetric{value: func(n float64) string { return "≈" + formatCount(n, "") }, heading: "Reports received · 90 days"}
	tests := []struct {
		name string
		days []store.DayValue
		want string
	}{
		{"one day", []store.DayValue{day(0, 93)}, "Reports received · 90 days: ≈93, saved Nov 9. HuntDeck saves it daily: its line starts with the next day's (Nov 10)."},
		{"none", nil, ""},
		{"zero", []store.DayValue{day(0, 0)}, ""},
		{"two days: a line instead", []store.DayValue{day(0, 93), day(1, 95)}, ""},
	}
	for _, tt := range tests {
		sp := firstDaySpark(tt.days, m)
		if sp.Collecting != tt.want || sp.FirstDay != (tt.want != "") {
			t.Errorf("%s: %q (first day %v)", tt.name, sp.Collecting, sp.FirstDay)
		}
		if tt.want != "" && sp.CellClass() != " firstday" {
			t.Errorf("%s: class %q", tt.name, sp.CellClass())
		}
	}
}
