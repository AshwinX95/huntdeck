package store

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/mdpsec/huntdeck/internal/platform"
)

// A flip-flopping program's state changes (FlipFlops or more in a week)
// collapse into their week's newest, which says how many it stands for;
// a program that paused once and came back keeps both, and only its
// reopening counts as back open.
func TestCollapseFlips(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 11, 5, 12, 0, 0, 0, time.UTC)
	s := newTestStore(t, func() time.Time { return now })
	ids := map[string]int64{}
	err := s.Write(ctx, func(tx *Tx) error {
		for _, h := range []string{"flipper", "steady"} {
			id, err := tx.UpsertProgram(ctx, "fake", platform.Program{Handle: h, Name: h, URL: "https://example.com/" + h, State: "open"})
			if err != nil {
				return err
			}
			ids[h] = id
		}
		// flipper: paused and reopened three times in the last two days.
		for i, at := range []time.Duration{48, 40, 30, 20, 10, 2} {
			from, to := "open", "paused"
			if i%2 == 1 {
				from, to = "paused", "open"
			}
			if err := tx.SetState(ctx, ids["flipper"], 0, from, to, now.Add(-at*time.Hour), false); err != nil {
				return err
			}
		}
		// steady: paused for a month, back open yesterday.
		if err := tx.SetState(ctx, ids["steady"], 0, "open", "paused", now.Add(-30*24*time.Hour), false); err != nil {
			return err
		}
		return tx.SetState(ctx, ids["steady"], 0, "paused", "open", now.Add(-24*time.Hour), false)
	})
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name      string
		f         ChangeFilter
		total     int
		wantFlips []int
	}{
		{"collapsed", ChangeFilter{Newest: true, CollapseFlips: true}, 3, []int{6, 0, 0}},
		{"as recorded", ChangeFilter{Newest: true}, 8, []int{0, 0, 0, 0, 0, 0, 0, 0}},
		{"one program", ChangeFilter{Newest: true, CollapseFlips: true, ProgramID: ids["flipper"]}, 1, []int{6}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			list, total, err := s.Changes(ctx, tt.f)
			if err != nil {
				t.Fatal(err)
			}
			var flips []int
			for _, c := range list {
				flips = append(flips, c.Flips)
			}
			if total != tt.total || fmt.Sprint(flips) != fmt.Sprint(tt.wantFlips) {
				t.Errorf("total %d, flips %v; want %d, %v", total, flips, tt.total, tt.wantFlips)
			}
		})
	}
	counts, err := s.ChangeCounts(ctx, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, c := range counts {
		n += c.N
	}
	if n != 3 {
		t.Errorf("ChangeCounts count %d changes, want 3 (flips collapsed)", n)
	}
	progs, _, err := s.Programs(ctx, ProgramFilter{State: "all", ResumedSince: now.Add(-30 * 24 * time.Hour)})
	if err != nil {
		t.Fatal(err)
	}
	if len(progs) != 1 || progs[0].Handle != "steady" {
		t.Errorf("back open: %+v, want just steady", progs)
	}
}

// The flip-flop SQL written out in flipsUpTo and resumedAt matches
// FlipFlops and FlipWindow.
func TestFlipSQL(t *testing.T) {
	days := fmt.Sprintf("'-%d days'", int(FlipWindow.Hours()/24))
	if !strings.Contains(flipsUpTo, days) || !strings.Contains(flipsAround, days) || !strings.Contains(flipsAround, strings.Replace(days, "-", "+", 1)) ||
		!strings.Contains(resumedAt, fmt.Sprintf("< %d)", FlipFlops)) {
		t.Errorf("flipsUpTo or resumedAt don't match FlipWindow %v and FlipFlops %d", FlipWindow, FlipFlops)
	}
}

// LongPauses finds a flip-flopping program's pause once it has lasted, and
// not before, nor once it's notified, nor a pause that wasn't held back.
func TestLongPauses(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 11, 5, 12, 0, 0, 0, time.UTC)
	s := newTestStore(t, func() time.Time { return now })
	run, err := s.StartRun(ctx, "fake", "full", "manual", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	ids := map[string]int64{}
	err = s.Write(ctx, func(tx *Tx) error {
		for _, h := range []string{"flipper", "once"} {
			id, err := tx.UpsertProgram(ctx, "fake", platform.Program{Handle: h, Name: h, URL: "https://example.com/" + h, State: "open"})
			if err != nil {
				return err
			}
			ids[h] = id
		}
		for i, at := range []time.Duration{70, 60, 50, 40, 30} { // flipper: paused 30 hours ago, after four changes
			from, to := "open", "paused"
			if i%2 == 1 {
				from, to = "paused", "open"
			}
			if err := tx.SetState(ctx, ids["flipper"], run, from, to, now.Add(-at*time.Hour), false); err != nil {
				return err
			}
		}
		return tx.SetState(ctx, ids["once"], run, "open", "paused", now.Add(-30*time.Hour), false)
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.QueueRun(ctx, run, nil, nil); err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name   string
		lasted time.Time
		want   int
	}{
		{"not a day yet", now.Add(-36 * time.Hour), 0},
		{"a day", now.Add(-24 * time.Hour), 1},
	}
	for _, tt := range tests {
		got, err := s.LongPauses(ctx, tt.lasted, now.Add(-FlipWindow))
		if err != nil {
			t.Fatal(err)
		}
		if len(got) != tt.want || (len(got) == 1 && (got[0].Program.Handle != "flipper" || !got[0].LongPause || got[0].Flips != 5)) {
			t.Errorf("%s: %+v", tt.name, got)
		}
	}
	got, _ := s.LongPauses(ctx, now.Add(-24*time.Hour), now.Add(-FlipWindow))
	if err := s.QueueLongPauses(ctx, []int64{got[0].ID}, nil, nil); err != nil {
		t.Fatal(err)
	}
	if again, _ := s.LongPauses(ctx, now.Add(-24*time.Hour), now.Add(-FlipWindow)); len(again) != 0 {
		t.Errorf("notified twice: %+v", again)
	}
}

// State changes split by where the program went, in the list and its
// counts.
func TestStateChangesByDirection(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 11, 5, 12, 0, 0, 0, time.UTC)
	s := newTestStore(t, func() time.Time { return now })
	err := s.Write(ctx, func(tx *Tx) error {
		id, err := tx.UpsertProgram(ctx, "fake", platform.Program{Handle: "p", Name: "P", URL: "https://example.com/p", State: "open"})
		if err != nil {
			return err
		}
		for i, step := range [][2]string{{"open", "paused"}, {"paused", "closed"}, {"closed", "open"}} {
			if err := tx.SetState(ctx, id, 0, step[0], step[1], now.Add(-time.Duration(30-10*i)*24*time.Hour), false); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	for to, want := range map[string]int{"paused,closed": 2, "open": 1} {
		_, n, err := s.Changes(ctx, ChangeFilter{Kinds: []string{ChangeStateChanged}, NewValues: strings.Split(to, ",")})
		if err != nil || n != want {
			t.Errorf("to %s: %d, %v; want %d", to, n, err, want)
		}
	}
	counts, err := s.ChangeCounts(ctx, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]int{}
	for _, c := range counts {
		if c.Kind == ChangeStateChanged {
			got[c.To] += c.N
		}
	}
	if fmt.Sprint(got) != "map[closed:1 open:1 paused:1]" {
		t.Errorf("counts by new state = %v", got)
	}
}
