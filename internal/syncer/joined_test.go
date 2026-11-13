package syncer

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/mdpsec/huntdeck/internal/platform"
	"github.com/mdpsec/huntdeck/internal/store"
)

// A private program the user gets access to is fresh attack surface for
// them however old its scope is: its targets are scored as of then, and
// the board can keep a few of each program's.
func TestJoinedProgramsScopeIsNewToYou(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	e.seed() // globex is private from the first import: not new to anyone
	e.poll(ModeFull, 0)

	old := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	e.fake.SetPrograms(prog("acme", false), prog("globex", true), prog("initech", true))
	e.fake.SetScope("initech", entry("i1", "a.initech.example", true), entry("i2", "b.initech.example", true),
		entry("i3", "c.initech.example", true), entry("i4", "d.initech.example", true), entry("i5", "e.initech.example", true),
		entry("i6", "out.initech.example", false))
	e.fake.SetHistory("initech", platform.ScopeVersion{At: old, Entries: []platform.ScopeEntry{
		entry("i1", "a.initech.example", true), entry("i2", "b.initech.example", true), entry("i3", "c.initech.example", true),
		entry("i4", "d.initech.example", true), entry("i5", "e.initech.example", true)}})
	e.poll(ModeFull, 0)

	since := e.clock.Add(-14 * 24 * time.Hour)
	all, _, err := e.store.BestTargets(ctx, store.TargetFilter{AddedSince: since, Order: store.TargetsByNewest}, "", 50, 0)
	if err != nil {
		t.Fatal(err)
	}
	joined := 0
	for _, r := range all {
		switch r.Program.Handle {
		case "globex":
			t.Errorf("globex was there from the first import, yet %s shows as fresh", r.Value)
		case "initech":
			joined++
			if !r.Joined || r.AddedAt == nil || r.AddedAt.Before(since) {
				t.Errorf("initech %s: joined %v, added %v; want new to you, dated now", r.Value, r.Joined, r.AddedAt)
			}
		}
	}
	if joined != 5 {
		t.Errorf("%d of initech's targets listed, want its 5 in scope", joined)
	}

	capped, _, err := e.store.BestTargets(ctx, store.TargetFilter{AddedSince: since, Order: store.TargetsByNewest, JoinedPerProgram: 3}, "", 50, 0)
	if err != nil {
		t.Fatal(err)
	}
	var got []int
	for _, r := range capped {
		if r.Program.Handle == "initech" {
			got = append(got, r.MoreJoined)
		}
	}
	if len(got) != 3 || got[0] != 0 || got[1] != 0 || got[2] != 2 {
		t.Errorf("capped initech rows' MoreJoined = %v, want [0 0 2]", got)
	}
}

// Signing in with another account lists its private programs: they were
// the user's already, so they aren't new programs or new to the user; a
// private program after that is. A partial listing doesn't settle the
// account, and the first account seen settles without a baseline.
func TestNewAccountsProgramsArentNew(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	e.seed()
	e.fake.SetAccount("alice")
	e.poll(ModeFull, 0)

	added := func() []string {
		t.Helper()
		all, _, err := e.store.Changes(ctx, store.ChangeFilter{Limit: 1000})
		if err != nil {
			t.Fatal(err)
		}
		var out []string
		for _, c := range all {
			if c.Kind == store.ChangeProgramAdded {
				out = append(out, c.Program.Handle)
			}
		}
		slices.Sort(out)
		return out
	}

	// Bob's listing, partial at first: his private programs aren't new,
	// and neither are they on the complete listing after it.
	e.fake.SetAccount("bob")
	e.fake.SetPrograms(prog("acme", false), prog("globex", true), prog("initech", true), prog("launch", false))
	e.fake.SetScope("initech", entry("i1", "a.initech.example", true))
	e.fake.SetScope("launch", entry("l1", "a.launch.example", true))
	e.fake.FailListing(errors.New("page 2: boom"), true)
	e.poll(ModeFull, 0)
	e.fake.FailListing(nil, false)
	e.fake.SetPrograms(prog("acme", false), prog("globex", true), prog("initech", true), prog("launch", false), prog("umbrella", true))
	e.fake.SetScope("umbrella", entry("u1", "a.umbrella.example", true))
	e.poll(ModeFull, 0)
	if got := strings.Join(added(), ","); got != "launch" {
		t.Errorf("programs added = %s, want only the public launch (bob's private ones were his already)", got)
	}

	// Then an invitation arrives: that one is new.
	e.fake.SetPrograms(prog("acme", false), prog("globex", true), prog("initech", true), prog("launch", false), prog("umbrella", true), prog("hooli", true))
	e.fake.SetScope("hooli", entry("h1", "a.hooli.example", true))
	e.poll(ModeFull, 0)
	if got := strings.Join(added(), ","); got != "hooli,launch" {
		t.Errorf("programs added = %s, want hooli too", got)
	}
	rows, _, err := e.store.BestTargets(ctx, store.TargetFilter{AddedSince: e.clock.Add(-14 * 24 * time.Hour)}, "", 50, 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range rows {
		if r.Joined && r.Program.Handle != "hooli" {
			t.Errorf("%s of %s is marked new to you; only hooli's scope is", r.Value, r.Program.Handle)
		}
	}
}
