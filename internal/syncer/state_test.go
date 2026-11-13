package syncer

import (
	"context"
	"testing"
	"time"

	"github.com/mdpsec/huntdeck/internal/platform"
	"github.com/mdpsec/huntdeck/internal/platform/fake"
	"github.com/mdpsec/huntdeck/internal/store"
)

// Where the listing can't tell paused from open (Bugcrowd), the brief's
// state is recorded, dated when that brief version was published, and the
// listing's "open" doesn't undo it; a resume in a later brief does.
func TestStateFromBrief(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	acme := prog("acme", false)
	acme.PauseUnknown = true
	e.fake.SetPrograms(acme)
	e.fake.SetScope("acme", entry("1", "*.acme.example", true))
	p := fake.Detailed{Platform: e.fake}
	poll := func(d platform.ScopeDetail) (string, []store.ChangeView) {
		t.Helper()
		e.fake.SetScopeDetail("acme", d)
		e.clock = e.clock.Add(time.Minute)
		run, err := e.sync.Poll(ctx, p, Options{Mode: ModeFull, Trigger: "manual", RefetchScope: true})
		if err != nil {
			t.Fatal(err)
		}
		row, err := e.store.Program(ctx, "fake", "acme")
		if err != nil {
			t.Fatal(err)
		}
		all, _, _ := e.store.Changes(ctx, store.ChangeFilter{Limit: 100})
		var mine []store.ChangeView
		for _, c := range all {
			if c.PollRunID == run.ID && c.Kind == store.ChangeStateChanged {
				mine = append(mine, c)
			}
		}
		return row.State, mine
	}
	poll(platform.ScopeDetail{State: platform.StateOpen}) // the first poll imports
	pausedAt := e.clock.Add(-30 * 24 * time.Hour)
	state, changes := poll(platform.ScopeDetail{State: platform.StatePaused, StateSince: &pausedAt})
	if state != platform.StatePaused || len(changes) != 1 || !changes[0].OccurredAt.Equal(pausedAt) {
		t.Fatalf("paused: state %q, changes %+v; want paused, dated %v", state, changes, pausedAt)
	}
	// A brief that says nothing leaves the pause; the listing still says open.
	if state, changes := poll(platform.ScopeDetail{}); state != platform.StatePaused || len(changes) != 0 {
		t.Errorf("listing only: state %q, changes %+v; want paused, none", state, changes)
	}
	resumed := e.clock
	state, changes = poll(platform.ScopeDetail{State: platform.StateOpen, StateSince: &resumed})
	if state != platform.StateOpen || len(changes) != 1 || changes[0].NewValue != platform.StateOpen {
		t.Errorf("resumed: state %q, changes %+v", state, changes)
	}
}
