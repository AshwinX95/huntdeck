package intigriti

import (
	"context"
	"reflect"
	"testing"
	"time"

	"github.com/mdpsec/huntdeck/internal/platform"
)

func TestSumBoard(t *testing.T) {
	n := func(v int) *int { return &v }
	full := make([]leaderJSON, platform.LeaderboardCap)
	for i := range full {
		full[i] = leaderJSON{Reputation: n(5)}
	}
	tests := []struct {
		name    string
		entries []leaderJSON
		points  *int
		hunters int
		capped  bool
	}{
		{"points", []leaderJSON{{n(40)}, {n(20)}, {n(4)}, {n(0)}}, n(64), 3, false},
		{"empty", nil, n(0), 0, false},
		// The program hides the points: still a count of researchers.
		{"hidden", []leaderJSON{{nil}, {nil}}, nil, 2, false},
		{"cut short at the cap", full, n(5 * platform.LeaderboardCap), platform.LeaderboardCap, true},
	}
	for _, tt := range tests {
		b := sumBoard(tt.entries)
		if (b.points == nil) != (tt.points == nil) || (b.points != nil && *b.points != *tt.points) || b.hunters != tt.hunters || b.capped != tt.capped {
			t.Errorf("%s: %+v (points %v)", tt.name, b, b.points)
		}
	}
	// Points count for the share only from two whole boards.
	whole, cut := sumBoard([]leaderJSON{{n(5)}}), sumBoard(full)
	for _, tt := range []struct {
		recent, all board
		want        bool
	}{
		{whole, whole, true}, {whole, cut, false}, {cut, whole, false}, {sumBoard([]leaderJSON{{nil}}), whole, false},
		{whole, sumBoard(nil), false},
	} {
		if got := shareable(tt.recent, tt.all); got != tt.want {
			t.Errorf("shareable(%+v, %+v) = %v", tt.recent, tt.all, got)
		}
	}
}

// Past months' leaderboards are read a month a request, in sums; a
// program the account can't open is skipped without a save.
func TestLeaderboardMonths(t *testing.T) {
	f := &fakeWeb{}
	c := webClient(t, f, func(c *Client) error { return f.giveSSO(c) })
	sep, oct := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC), time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	got := map[string][]platform.LeaderboardMonth{}
	err := c.LeaderboardMonths(context.Background(), []platform.LeaderboardWant{
		{Handle: globexID, Months: []time.Time{oct}}, {Handle: acmeID, Months: []time.Time{sep, oct}},
	}, func(h string, months []platform.LeaderboardMonth) error {
		got[h] = months
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	zero, points := 0, 46
	want := map[string][]platform.LeaderboardMonth{acmeID: {{Month: sep, Points: &zero}, {Month: oct, Points: &points, Hunters: 3}}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %+v, want %+v", got, want)
	}
	if n := f.hits["GET "+webAPI+"/program/"+acmeID+"/leaderboard/month"]; n != 2 {
		t.Errorf("acme's months asked %d times, want 2", n)
	}
}
