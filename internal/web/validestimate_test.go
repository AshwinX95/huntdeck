package web

import (
	"strings"
	"testing"

	"github.com/mdpsec/huntdeck/internal/platform"
	"github.com/mdpsec/huntdeck/internal/store"
)

func TestValidEstimateTitleAndShare(t *testing.T) {
	n := func(v int) *int { return &v }
	p := store.ProgramListItem{Platform: "yeswehack", Reports90d: n(302), AcceptedAll: n(564), TotalReports: n(1092)}
	if got := validEstimateTitle(p); !strings.Contains(got, "302 reports received in 90 days × 52% of its reports accepted all time (564 of 1,092)") {
		t.Errorf("title = %q", got)
	}
	if got := validEstimateTitle(store.ProgramListItem{Platform: "yeswehack"}); got != "" {
		t.Errorf("title without totals = %q", got)
	}
	for _, tc := range []struct {
		accepted, total *int
		want            string
	}{{n(564), n(1092), "52%"}, {n(163), n(15), ""}, {nil, n(10), ""}, {n(0), n(0), ""}} {
		s := store.ProgramStats{ProgramStats: platform.ProgramStats{AcceptedReports: tc.accepted, TotalReports: tc.total}}
		if got := s.AcceptedShare(); got != tc.want {
			t.Errorf("share of %v/%v = %q, want %q", tc.accepted, tc.total, got, tc.want)
		}
	}
}
