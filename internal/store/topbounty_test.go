package store

import (
	"context"
	"strconv"
	"testing"
	"time"
)

// A program's top bounty, for every summary, is the largest its bounty
// table, listing or stats give: a listing's alone counts.
func TestScoreDataTopBounty(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t, nil)
	exec(t, s, `INSERT INTO programs (id, platform, handle, name, url, private, state, offers_bounty, bounty_min, bounty_max, raw, first_seen_at, last_seen_at)
		VALUES (1, 'intigriti', 'listed', 'L', 'https://example.com/l', 0, 'open', 1, 100, 5000, '{}', '2026-01-01T00:00:00.000Z', '2026-01-01T00:00:00.000Z'),
			(2, 'intigriti', 'none', 'N', 'https://example.com/n', 0, 'open', 1, NULL, NULL, '{}', '2026-01-01T00:00:00.000Z', '2026-01-01T00:00:00.000Z')`)
	d, err := s.ScoreData(ctx, time.Now().Add(-90*24*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range d.Programs {
		switch {
		case p.ID == 1 && (p.TopBounty == nil || *p.TopBounty != 5000):
			t.Errorf("listed program's top = %v, want 5000", p.TopBounty)
		case p.ID == 2 && p.TopBounty != nil:
			t.Errorf("program without one has top %v", *p.TopBounty)
		}
	}
}

// A time of 0 is no measurement.
func TestMeasured(t *testing.T) {
	f := func(v float64) *float64 { return &v }
	for _, tt := range []struct {
		in   *float64
		want string
	}{{nil, "nil"}, {f(0), "nil"}, {f(-1), "nil"}, {f(0.5), "0.5"}, {f(186), "186"}} {
		got := "nil"
		if v := measured(tt.in); v != nil {
			got = strconv.FormatFloat(*v, 'f', -1, 64)
		}
		if got != tt.want {
			t.Errorf("measured(%v) = %s, want %s", tt.in, got, tt.want)
		}
	}
}
