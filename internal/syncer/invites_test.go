package syncer

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/mdpsec/huntdeck/internal/platform"
	"github.com/mdpsec/huntdeck/internal/platform/fake"
	"github.com/mdpsec/huntdeck/internal/store"
)

// Polls keep the pending invitations: listed ones are pending with their
// date, one that leaves the list is accepted then, and a list that can't
// be read changes nothing.
func TestPendingInvites(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	e.seed() // acme public, globex a private invite
	p := fake.Inviting{Platform: e.fake}
	poll := func(mode string) {
		t.Helper()
		e.clock = e.clock.Add(time.Minute)
		if _, err := e.sync.Poll(ctx, p, Options{Mode: mode, Trigger: "manual"}); err != nil {
			t.Fatal(err)
		}
	}
	invite := func(handle string) *store.ProgramDetail {
		t.Helper()
		d, err := e.store.Program(ctx, "fake", handle)
		if err != nil {
			t.Fatal(err)
		}
		return d
	}

	sent := time.Date(2026, 9, 22, 8, 0, 0, 0, time.UTC)
	e.fake.SetInvites(nil, platform.Invite{Handle: "globex", InvitedAt: sent}, platform.Invite{Handle: "unknown"})
	poll(ModeFull)
	g := invite("globex")
	if !g.InvitePending || g.InvitedAt == nil || !g.InvitedAt.Equal(sent) || g.JoinedAt != nil {
		t.Fatalf("after the first poll: pending %v, invited %v, joined %v", g.InvitePending, g.InvitedAt, g.JoinedAt)
	}
	if a := invite("acme"); a.InvitePending {
		t.Error("acme is pending without an invitation")
	}

	// The list can't be read: globex stays pending.
	e.fake.SetInvites(errors.New("needs a login"))
	poll(ModeQuick)
	if g := invite("globex"); !g.InvitePending {
		t.Error("a failed read took globex off the pending list")
	}

	// Accepted: it leaves the list.
	e.fake.SetInvites(nil)
	poll(ModeQuick)
	accepted := e.clock
	g = invite("globex")
	if g.InvitePending || g.JoinedAt == nil || !g.JoinedAt.Equal(accepted) || g.InvitedAt == nil || !g.InvitedAt.Equal(sent) {
		t.Errorf("after accepting: pending %v, joined %v, invited %v", g.InvitePending, g.JoinedAt, g.InvitedAt)
	}
	// Listings that don't say when it was joined keep that date.
	poll(ModeFull)
	if g := invite("globex"); g.JoinedAt == nil || !g.JoinedAt.Equal(accepted) {
		t.Errorf("a later listing changed when globex was joined: %v", g.JoinedAt)
	}
}
