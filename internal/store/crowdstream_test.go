package store

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/mdpsec/huntdeck/internal/platform"
)

// A feed that has never listed a report is kept private, not empty: it
// isn't marked complete, so its counts read as not shared.
func TestCrowdStreamShared(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 11, 5, 12, 0, 0, 0, time.UTC)
	s := newTestStore(t, func() time.Time { return now })
	since := now.AddDate(0, -6, 0)
	var id int64
	shared := func() bool {
		t.Helper()
		var ok bool
		if err := s.Write(ctx, func(tx *Tx) error {
			var err error
			ok, err = tx.CrowdStreamShared(ctx, id)
			return err
		}); err != nil {
			t.Fatal(err)
		}
		return ok
	}
	if err := s.Write(ctx, func(tx *Tx) error {
		var err error
		if id, err = tx.UpsertProgram(ctx, "fake", platform.Program{Handle: "quiet", Name: "Quiet", URL: "https://example.com/quiet", State: "open"}); err != nil {
			return err
		}
		return tx.MarkAcceptedSynced(ctx, id, &since)
	}); err != nil {
		t.Fatal(err)
	}
	if shared() {
		t.Error("an empty feed counts as shared")
	}
	if sum, err := s.ProgramAccepted(ctx, id, now, 12); err != nil || sum.Since != nil || sum.SyncedAt == nil {
		t.Errorf("empty feed: since %v synced %v, %v; want no since, synced", sum.Since, sum.SyncedAt, err)
	}

	// Once it lists a report, it is shared, complete from that read's window.
	if err := s.Write(ctx, func(tx *Tx) error {
		if _, err := tx.SaveAccepted(ctx, id, platform.AcceptedReport{ID: "r1", AcceptedAt: now.AddDate(0, 0, -3)}); err != nil {
			return err
		}
		return tx.MarkAcceptedSynced(ctx, id, &since)
	}); err != nil {
		t.Fatal(err)
	}
	if !shared() {
		t.Error("a feed listing a report isn't shared")
	}
	if sum, err := s.ProgramAccepted(ctx, id, now, 12); err != nil || sum.Since == nil || !sum.Since.Equal(since) {
		t.Errorf("since %v, %v; want %v", sum.Since, err, since)
	}
}

// A program with saved reports lists them, however they came: from its
// own feed's window, or from the all-programs feed (from its earliest
// report's day) when its own feed gives no window or wasn't read yet. One
// that never listed a report stays kept private.
func TestAcceptedSinceRules(t *testing.T) {
	now := time.Date(2026, 11, 5, 12, 0, 0, 0, time.UTC)
	window := now.AddDate(0, -6, 0)
	later := window.AddDate(0, 0, 7)
	day := func(d int) *time.Time { t := time.Date(2026, 11, d, 0, 0, 0, 0, time.UTC); return &t }
	// A step saves a report accepted on Nov feed from the all-programs feed
	// and marks listed programs (feed > 0), or reads the program's own
	// feed with window ("own"), a later window ("later") or none ("none").
	type step struct {
		feed int
		own  string
	}
	tests := []struct {
		name  string
		steps []step
		want  *time.Time
	}{
		{"never listed", []step{{own: "own"}}, nil},
		{"never listed, own feed unreadable", []step{{own: "none"}}, nil},
		{"feed only", []step{{feed: 1}, {feed: 3}}, day(1)},
		{"feed, then own feed's window", []step{{feed: 3}, {own: "own"}}, &window},
		{"feed, own feed unreadable", []step{{feed: 3}, {own: "none"}}, day(3)},
		{"own feed's window kept over a later one", []step{{feed: 3}, {own: "own"}, {own: "later"}}, &window},
		{"later feed reports don't move it", []step{{feed: 3}, {own: "own"}, {feed: 4}}, &window},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			s := newTestStore(t, func() time.Time { return now })
			var id int64
			if err := s.Write(ctx, func(tx *Tx) error {
				var err error
				id, err = tx.UpsertProgram(ctx, "fake", platform.Program{Handle: "acme", Name: "Acme", URL: "https://example.com/acme", State: "open"})
				return err
			}); err != nil {
				t.Fatal(err)
			}
			for i, st := range tt.steps {
				err := s.Write(ctx, func(tx *Tx) error {
					switch st.own {
					case "own":
						return tx.MarkAcceptedSynced(ctx, id, &window)
					case "later":
						return tx.MarkAcceptedSynced(ctx, id, &later)
					case "none":
						return tx.MarkAcceptedSynced(ctx, id, nil)
					}
					if _, err := tx.SaveAccepted(ctx, id, platform.AcceptedReport{ID: fmt.Sprint("r", i), AcceptedAt: *day(st.feed)}); err != nil {
						return err
					}
					return tx.MarkAcceptedListed(ctx, "fake")
				})
				if err != nil {
					t.Fatal(err)
				}
			}
			sum, err := s.ProgramAccepted(ctx, id, now, 12)
			if err != nil {
				t.Fatal(err)
			}
			switch {
			case tt.want == nil && sum.Since != nil:
				t.Errorf("since %v; want none (kept private)", sum.Since)
			case tt.want != nil && (sum.Since == nil || !sum.Since.Equal(*tt.want)):
				t.Errorf("since %v; want %v", sum.Since, *tt.want)
			}
		})
	}
}
