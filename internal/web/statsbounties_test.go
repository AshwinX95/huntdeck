package web

import (
	"slices"
	"testing"

	"github.com/mdpsec/huntdeck/internal/store"
)

// Bounties per month chart in the currency most is paid in; the cell adds
// the others beside it.
func TestBountyCurrencies(t *testing.T) {
	months := []store.PlatformMonth{
		{Bounties: 300, BountiesOther: map[string]float64{"EUR": 500}},
		{Bounties: 400, BountiesOther: map[string]float64{"EUR": 900, "GBP": 0}},
	}
	tests := []struct {
		months []store.PlatformMonth
		want   []string
	}{
		{months, []string{"EUR", "USD"}},
		{months[:1], []string{"EUR", "USD"}},
		{[]store.PlatformMonth{{Bounties: 1}}, []string{"USD"}},
		{[]store.PlatformMonth{{}}, nil},
	}
	for _, tt := range tests {
		if got := bountyCurrencies(tt.months); !slices.Equal(got, tt.want) {
			t.Errorf("bountyCurrencies(%v) = %v, want %v", tt.months, got, tt.want)
		}
	}
	c := platformStats{Bounties: &lineChart{}, BountiesLast: 900, BountiesCurrency: "EUR", BountiesEst: true,
		BountiesAlso: []moneySum{{"USD", 400}}}.BountiesCell()
	if c.N != "≈€900" || c.Also != "+ $400" {
		t.Errorf("cell %q, also %q", c.N, c.Also)
	}
}
