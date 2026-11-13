package store

import "testing"

// One rule for whether a 90-day figure is an estimate: one by how it's
// worked out, or counted from fewer than 90 days of saved totals.
func TestNinetyDayEstimates(t *testing.T) {
	n := func(v int) *int { return &v }
	paid, reports := 1000.0, 10
	tests := []struct {
		name           string
		item           ProgramListItem
		paid, received bool
	}{
		{"published (HackerOne)", ProgramListItem{}, false, false},
		{"accepted × average (Bugcrowd)", ProgramListItem{Paid90dEstimated: true}, true, false},
		{"rewarded rise × average, 90 days (Bugcrowd)", ProgramListItem{ProgramRef: ProgramRef{Platform: "bugcrowd"}, CountedDays: n(90)}, true, false},
		{"counted from 30 days (Intigriti)", ProgramListItem{ProgramRef: ProgramRef{Platform: "intigriti"}, CountedDays: n(30)}, true, true},
		{"counted from 90 days (Intigriti)", ProgramListItem{ProgramRef: ProgramRef{Platform: "intigriti"}, CountedDays: n(90)}, false, false},
	}
	for _, tt := range tests {
		it := tt.item
		it.Paid90d, it.Reports90d = &paid, &reports
		if got := it.Paid90dIsEstimate(); got != tt.paid {
			t.Errorf("%s: paid estimate %v, want %v", tt.name, got, tt.paid)
		}
		if got := it.Reports90dIsEstimate(); got != tt.received {
			t.Errorf("%s: received estimate %v, want %v", tt.name, got, tt.received)
		}
	}
}
