package syncer

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/mdpsec/huntdeck/internal/platform"
	"github.com/mdpsec/huntdeck/internal/platform/fake"
	"github.com/mdpsec/huntdeck/internal/store"
)

// Accepting an invitation accepts it on the platform and records the
// program as joined; refreshing it then polls just that program (scope
// downloaded again, stats), without counting as the platform's full poll.
func TestAcceptInviteThenRefreshOneProgram(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	e.seed() // acme public, globex a private invite
	p := fake.Inviting{Platform: e.fake}
	e.fake.SetInvites(nil, platform.Invite{Handle: "globex", InvitedAt: time.Date(2026, 9, 22, 8, 0, 0, 0, time.UTC)})
	e.clock = e.clock.Add(time.Minute)
	full, err := e.sync.Poll(ctx, p, Options{Mode: ModeFull, Trigger: "manual"})
	if err != nil {
		t.Fatal(err)
	}
	acme, globex := e.fake.ScopeHits("acme"), e.fake.ScopeHits("globex")
	stats := len(e.fake.StatsRequests())

	if !CanAcceptInvites(p) || CanAcceptInvites(e.fake) {
		t.Error("only a platform that lists and accepts invitations can accept them")
	}
	if err := e.sync.AcceptInvite(ctx, e.fake, "globex"); !errors.Is(err, ErrCannotAccept) {
		t.Errorf("accepting without an InviteAccepter: %v", err)
	}
	if err := e.sync.AcceptInvite(ctx, p, "acme"); !errors.Is(err, ErrNoInvite) {
		t.Errorf("accepting a program without an invitation: %v", err)
	}
	if err := e.sync.AcceptInvite(ctx, p, "nope"); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("accepting an unknown program: %v", err)
	}

	e.clock = e.clock.Add(time.Minute)
	if err := e.sync.AcceptInvite(ctx, p, "globex"); err != nil {
		t.Fatal(err)
	}
	if got := e.fake.AcceptedInvites(); !slices.Equal(got, []string{"globex"}) {
		t.Errorf("accepted on the platform: %v", got)
	}
	g, err := e.store.Program(ctx, "fake", "globex")
	if err != nil {
		t.Fatal(err)
	}
	if g.InvitePending || g.JoinedAt == nil || !g.JoinedAt.Equal(e.clock) {
		t.Errorf("after accepting: pending %v, joined %v (want %v)", g.InvitePending, g.JoinedAt, e.clock)
	}
	if err := e.sync.AcceptInvite(ctx, p, "globex"); !errors.Is(err, ErrNoInvite) {
		t.Errorf("accepting twice: %v", err)
	}

	// Accepting shows more of it: a new asset.
	e.fake.SetScope("globex", entry("3", "app.globex.example", true), entry("4", "api.globex.example", true))
	e.fake.SetScope("acme", entry("1", "*.acme.example", true)) // not refreshed: stays as it was
	e.clock = e.clock.Add(time.Minute)
	run, err := e.sync.RefreshPrograms(ctx, p, []string{"globex"})
	if err != nil {
		t.Fatal(err)
	}
	if run.Trigger != store.TriggerProgram || run.Status != store.RunOK || run.ListingComplete {
		t.Errorf("refresh run: trigger %q status %q complete %v", run.Trigger, run.Status, run.ListingComplete)
	}
	if a, g := e.fake.ScopeHits("acme")-acme, e.fake.ScopeHits("globex")-globex; a != 0 || g != 1 {
		t.Errorf("scope downloads: acme %d, globex %d; want 0 and 1", a, g)
	}
	reqs := e.fake.StatsRequests()[stats:]
	if len(reqs) != 1 || !slices.Equal(reqs[0], []string{"globex"}) {
		t.Errorf("stats requests: %v, want just globex", reqs)
	}
	// Only globex's new asset is recorded: acme's dropped one waits for
	// acme's own next poll, and nothing is marked removed.
	ch := e.changes(run.ID)
	if len(ch) != 1 || !strings.HasPrefix(ch[0], "globex ") || !strings.Contains(ch[0], "api.globex.example") {
		t.Errorf("refresh changes = %q", ch)
	}
	if last, err := e.store.LastRun(ctx, "fake", ModeFull); err != nil || last == nil || last.ID != full.ID {
		t.Errorf("last full poll = %+v, %v; want the scheduled one (%d), not the refresh", last, err, full.ID)
	}
}

// A program listed after the last full poll (a private program just
// joined) has its report histories fetched when it's refreshed, rather
// than at the next full poll: just its own, not the platform-wide feeds.
func TestRefreshFetchesNewProgramsReportHistory(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	e.seed()
	e.pollWith(Options{Mode: ModeQuick}) // lists both; histories wait for a full poll
	resolutions, disclosures := len(e.fake.ResolutionRequests()), len(e.fake.DisclosureRequests())
	at := e.clock.Add(-10 * 24 * time.Hour)
	e.fake.SetResolutions(platform.Resolution{ID: "21", ProgramHandle: "globex", ActivityAt: at, Action: platform.ActionResolved})

	for i, want := range []int{1, 0} { // the second refresh finds it fetched already
		e.clock = e.clock.Add(time.Minute)
		run, err := e.sync.RefreshPrograms(ctx, e.fake, []string{"globex"})
		if err != nil {
			t.Fatal(err)
		}
		if run.ResolutionsNew != want {
			t.Errorf("refresh %d stored %d resolutions, want %d", i+1, run.ResolutionsNew, want)
		}
	}
	if got := e.fake.ResolutionRequests()[resolutions:]; !slices.Equal(got, []string{"globex"}) {
		t.Errorf("resolution requests %v, want globex's history once, no feed", got)
	}
	if got := e.fake.DisclosureRequests()[disclosures:]; !slices.Equal(got, []string{"globex"}) {
		t.Errorf("disclosure requests %v, want globex's history once, no feed", got)
	}
	g, err := e.store.Program(ctx, "fake", "globex")
	if err != nil {
		t.Fatal(err)
	}
	if sum, err := e.store.ProgramResolutions(ctx, g.ID, e.clock, 12); err != nil || sum.Last90d != 1 || sum.HistoryFetch == nil {
		t.Errorf("globex's resolutions %+v, %v; want the one, history fetched", sum, err)
	}
}
