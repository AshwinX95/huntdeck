package notify

import (
	"testing"
	"time"

	"github.com/mdpsec/huntdeck/internal/scope"
	"github.com/mdpsec/huntdeck/internal/store"
)

// A preview counts a message per poll with a match (after each poll) and
// per daily digest with any, holds back a flip-flopping program's pauses
// until one has lasted a day, and shows the newest event of each kind.
func TestPreviewChannel(t *testing.T) {
	since := now.Add(-7 * 24 * time.Hour)
	at := func(c store.NotifyChange, run int64, ago time.Duration) store.NotifyChange {
		c.RunID, c.OccurredAt = run, now.Add(-ago)
		return c
	}
	wild := &store.NotifyEntry{Identifier: "*.acme.example", AssetType: scope.AssetWildcard, InScope: true, Bounty: true}
	host := &store.NotifyEntry{Identifier: "api.acme.example", AssetType: scope.AssetDomain, InScope: true, Bounty: true}
	pause := change(store.ChangeStateChanged, "state", "open", "paused", nil)
	pause.Program.ID, pause.Flips = 3, 4
	launch := change(store.ChangeProgramAdded, "visibility", "", "public", nil)
	launch.Program.ID = 2
	changes := []store.NotifyChange{
		at(change(store.ChangeScopeAdded, "in_scope", "", wild.Identifier, wild), 1, 100*time.Hour), // one poll: one message
		at(change(store.ChangeScopeAdded, "in_scope", "", host.Identifier, host), 1, 100*time.Hour),
		at(launch, 2, 50*time.Hour),
		at(pause, 3, 30*time.Hour), // a flip-flopper's pause that lasted: sent a day in
		at(change(store.ChangePolicyChanged, store.FieldPolicy, "a", "b", nil), 4, 2*time.Hour),
	}
	utc := time.UTC

	all := Rules{}
	for _, k := range EventKinds {
		all.Events = append(all.Events, k.Key)
	}
	tests := []struct {
		name             string
		rules            Rules
		instant          bool
		digestAt         string
		messages, events int
		shown            []string
	}{
		{"everything, after each poll", all, true, "", 4, 4, []string{EventPolicyChanged, EventPaused, EventLaunch, EventScopeAdded}},
		{"the usual", DefaultRules(), true, "", 2, 2, []string{EventLaunch, EventScopeAdded}},
		{"wildcards only", Rules{Events: []string{EventScopeAdded}, MajorScopeOnly: true}, true, "", 1, 1, []string{EventScopeAdded}},
		// Digests at 09:00 UTC, now being midnight: the ones after the scope
		// (100h ago) and the launch (50h); the pause (sent 6h ago) and the
		// rules edit (2h) wait for today's.
		{"digest only", all, false, "09:00", 2, 4, nil},
		{"both", all, true, "09:00", 6, 4, nil},
		{"neither", all, false, "", 0, 4, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := PreviewChannel(changes, tt.rules, tt.instant, tt.digestAt, utc, since, now, 5)
			if p.Messages != tt.messages || p.Events != tt.events {
				t.Errorf("%d messages, %d events; want %d, %d", p.Messages, p.Events, tt.messages, tt.events)
			}
			if tt.shown == nil {
				return
			}
			var got []string
			for _, e := range p.Shown {
				got = append(got, e.Kind)
			}
			if len(got) != len(tt.shown) {
				t.Fatalf("shown %v, want %v", got, tt.shown)
			}
			for i := range got {
				if got[i] != tt.shown[i] {
					t.Fatalf("shown %v, want %v", got, tt.shown)
				}
			}
		})
	}
}

// A pause that ended within the day isn't sent; one still going after a
// day is, with why it waited.
func TestPreviewShortPause(t *testing.T) {
	since := now.Add(-7 * 24 * time.Hour)
	pause := change(store.ChangeStateChanged, "state", "open", "paused", nil)
	pause.RunID, pause.OccurredAt, pause.Flips = 1, now.Add(-40*time.Hour), 3
	reopen := change(store.ChangeStateChanged, "state", "paused", "open", nil)
	reopen.RunID, reopen.OccurredAt, reopen.Flips, reopen.PrevStateAt = 2, now.Add(-30*time.Hour), 4, &pause.OccurredAt
	rules := Rules{Events: []string{EventPaused, EventReopened}}
	if p := PreviewChannel([]store.NotifyChange{pause, reopen}, rules, true, "", time.UTC, since, now, 3); p.Events != 0 {
		t.Errorf("a ten-hour pause and its reopening: %d events, want none", p.Events)
	}
	p := PreviewChannel([]store.NotifyChange{pause}, rules, true, "", time.UTC, since, now, 3)
	if p.Events != 1 || p.Shown[0].Detail == "" || !p.Shown[0].At.Equal(pause.OccurredAt.Add(LongPause)) {
		t.Errorf("a lasting pause: %+v", p)
	}
}
