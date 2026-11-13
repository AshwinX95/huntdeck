package syncer

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/mdpsec/huntdeck/internal/platform"
	"github.com/mdpsec/huntdeck/internal/platform/fake"
	"github.com/mdpsec/huntdeck/internal/scope"
	"github.com/mdpsec/huntdeck/internal/store"
)

func TestFirstImportIsSilentAndSecondPollChangesNothing(t *testing.T) {
	e := newEnv(t)
	e.seed()

	first := e.poll(ModeFull, 0)
	if first.Status != store.RunOK || first.ProgramsSeen != 2 || first.ScopesFetched != 2 || !first.ListingComplete {
		t.Fatalf("first run: %+v", first)
	}
	if got := e.changes(first.ID); len(got) != 0 {
		t.Errorf("first import should record no changes, got %v", got)
	}
	if got := e.targets(scope.KindWildcard); !slices.Equal(got, []string{"*.acme.example"}) {
		t.Errorf("wildcards = %v", got)
	}
	if got := e.targets(scope.KindDomain); !slices.Equal(got, []string{"app.globex.example"}) {
		t.Errorf("in-scope domains = %v (out-of-scope must not be exported)", got)
	}

	second := e.poll(ModeFull, 0)
	if second.Status != store.RunOK || second.Changes != 0 {
		t.Errorf("identical second poll: status=%s changes=%d %v", second.Status, second.Changes, e.changes(second.ID))
	}
}

func TestScopeAndProgramChangesAreRecorded(t *testing.T) {
	e := newEnv(t)
	e.seed()
	e.poll(ModeFull, 0)

	newbie := prog("newbie", true)
	globex := prog("globex", true)
	globex.State = platform.StatePaused
	e.fake.SetPrograms(prog("acme", false), globex, newbie)
	e.fake.SetScope("newbie", entry("9", "newbie.example", true))
	e.fake.SetScope("acme",
		entry("1", "*.acme.example", true),
		entry("4", "api.acme.example", true), // added; "2" removed
	)
	g := entry("3", "app.globex.example", true)
	g.MaxSeverity = "critical"
	e.fake.SetScope("globex", g)

	run := e.poll(ModeFull, 0)
	want := []string{
		"acme scope_added in_scope →api.acme.example",
		"acme scope_removed out_of_scope old.acme.example→",
		"globex scope_updated max_severity →critical",
		"globex state_changed state open→paused",
		"newbie program_added visibility →private",
	}
	if got := e.changes(run.ID); !slices.Equal(got, want) {
		t.Errorf("changes:\n got  %q\n want %q", got, want)
	}
	// newbie's scope is its baseline: no scope_added noise for a new program.
}

func TestProgramRemovalNeedsCompleteListing(t *testing.T) {
	e := newEnv(t)
	e.seed()
	e.poll(ModeFull, 0)

	e.fake.SetPrograms(prog("acme", false)) // globex gone…
	e.fake.FailListing(errors.New("page 2: HTTP 502"), true)
	run := e.poll(ModeFull, 0)
	if run.Status != store.RunPartial || run.ListingComplete {
		t.Errorf("partial listing run: %+v", run)
	}
	if got := e.changes(run.ID); len(got) != 0 {
		t.Errorf("incomplete listing must not remove programs, got %v", got)
	}

	e.fake.FailListing(nil, false)
	run = e.poll(ModeFull, 0)
	if got := e.changes(run.ID); !slices.Equal(got, []string{"globex program_removed  →"}) {
		t.Errorf("complete listing should remove globex, got %v", got)
	}
	if got := e.targets(scope.KindDomain); len(got) != 0 {
		t.Errorf("removed program's targets still exported: %v", got)
	}

	e.fake.SetPrograms(prog("acme", false), prog("globex", true))
	run = e.poll(ModeFull, 0)
	if got := e.changes(run.ID); !slices.Equal(got, []string{"globex program_added visibility removed→private"}) {
		t.Errorf("re-added program: %v", got)
	}
}

func TestListingFailureFailsRun(t *testing.T) {
	e := newEnv(t)
	e.fake.FailListing(errors.New("authentication failed"), false)
	run := e.poll(ModeFull, 0)
	if run.Status != store.RunFailed || run.Error != "authentication failed" {
		t.Errorf("run: %+v", run)
	}
}

func TestQuickModeFetchesOnlyNewPrograms(t *testing.T) {
	e := newEnv(t)
	e.seed()
	e.poll(ModeFull, 0)

	e.fake.SetPrograms(prog("acme", false), prog("globex", true), prog("newbie", true))
	e.fake.SetScope("newbie", entry("9", "*.newbie.example", true))
	run := e.poll(ModeQuick, 0)
	if run.ScopesFetched != 1 || e.fake.ScopeHits("acme") != 1 || e.fake.ScopeHits("newbie") != 1 {
		t.Errorf("quick poll should fetch only newbie: fetched=%d acme hits=%d", run.ScopesFetched, e.fake.ScopeHits("acme"))
	}
	if got := e.targets(scope.KindWildcard); !slices.Contains(got, "*.newbie.example") {
		t.Errorf("newbie scope missing: %v", got)
	}
}

func TestLimitNeverRemoves(t *testing.T) {
	e := newEnv(t)
	e.seed()
	e.poll(ModeFull, 0)

	run := e.poll(ModeFull, 1)
	if run.ListingComplete || run.ProgramsSeen != 1 {
		t.Errorf("limited run: %+v", run)
	}
	if got := e.changes(run.ID); len(got) != 0 {
		t.Errorf("limited poll must not remove programs, got %v", got)
	}
}

func TestLimitedRunsDontBreakTheBaseline(t *testing.T) {
	e := newEnv(t)
	e.seed()
	e.poll(ModeFull, 1) // only acme
	run := e.poll(ModeFull, 0)
	if got := e.changes(run.ID); len(got) != 0 {
		t.Errorf("first complete listing after a limited run should be silent, got %v", got)
	}
	e.fake.SetPrograms(prog("acme", false), prog("globex", true), prog("newbie", true))
	e.fake.SetScope("newbie", entry("9", "newbie.example", true))
	run = e.poll(ModeFull, 0)
	if got := e.changes(run.ID); len(got) != 1 || got[0] != "newbie program_added visibility →private" {
		t.Errorf("after the baseline, new programs should be recorded: %v", got)
	}
}

func TestLeasePreventsConcurrentPolls(t *testing.T) {
	e := newEnv(t)
	if _, err := e.store.StartRun(context.Background(), "fake", ModeFull, "manual", time.Hour); err != nil {
		t.Fatal(err)
	}
	_, err := e.sync.Poll(context.Background(), e.fake, Options{Mode: ModeQuick, Trigger: "manual"})
	if !IsRunInProgress(err) {
		t.Errorf("err = %v, want run in progress", err)
	}
	// A lease older than the stale window is reclaimed.
	e.clock = e.clock.Add(3 * time.Hour)
	e.seed()
	if _, err := e.sync.Poll(context.Background(), e.fake, Options{Mode: ModeFull, Trigger: "manual"}); err != nil {
		t.Errorf("stale lease not reclaimed: %v", err)
	}
}

func TestMassRemovalIsHeldBack(t *testing.T) {
	e := newEnv(t)
	var all []platform.Program
	for i := range 12 {
		h := fmt.Sprintf("prog%02d", i)
		all = append(all, prog(h, false))
		e.fake.SetScope(h, entry(h, h+".example.com", true))
	}
	e.fake.SetPrograms(all...)
	e.poll(ModeFull, 0)

	// A "complete" listing that drops 10 of 12 programs.
	e.fake.SetPrograms(all[:2]...)
	run := e.poll(ModeQuick, 0)
	if run.Status != store.RunPartial || run.ListingComplete || !strings.Contains(run.Error, "would remove 10 of 12") {
		t.Fatalf("run: %+v", run)
	}
	if got := e.changes(run.ID); len(got) != 0 {
		t.Fatalf("held-back run recorded changes: %v", got)
	}

	run = e.pollWith(Options{Mode: ModeQuick, ForceRemovals: true})
	if run.Status != store.RunOK || len(e.changes(run.ID)) != 10 {
		t.Fatalf("forced run: %+v changes=%v", run, e.changes(run.ID))
	}
}

func TestEmptyListingRemovesNothing(t *testing.T) {
	e := newEnv(t)
	e.seed()
	e.poll(ModeFull, 0)
	e.fake.SetPrograms()
	run := e.poll(ModeQuick, 0)
	if len(e.changes(run.ID)) != 0 || run.ListingComplete {
		t.Fatalf("an empty listing must not remove programs: %+v %v", run, e.changes(run.ID))
	}
}

func TestSmallRemovalsStillHappen(t *testing.T) {
	if massRemoval(1, 2, 1) || massRemoval(9, 12, 3) || !massRemoval(10, 12, 2) || !massRemoval(1, 1, 0) || massRemoval(0, 5, 0) {
		t.Fatal("massRemoval thresholds changed")
	}
}

func TestInterruptedPollIsRecordedAsInterrupted(t *testing.T) {
	e := newEnv(t)
	e.seed()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	run, err := e.sync.Poll(ctx, cancelOnScope{e.fake, cancel}, Options{Mode: ModeFull, Trigger: "schedule"})
	if err != nil {
		t.Fatalf("Poll: %v", err)
	}
	if run.Status != store.RunFailed || run.Error != store.RunInterrupted || run.DisplayStatus() != store.RunInterrupted {
		t.Errorf("run = %+v (shown as %q), want failed/interrupted", run, run.DisplayStatus())
	}
	stored, err := e.store.LastRun(context.Background(), e.fake.Name(), ModeFull)
	if err != nil || stored == nil || stored.DisplayStatus() != store.RunInterrupted || stored.FinishedAt == nil {
		t.Errorf("stored run = %+v, %v; want a finished, interrupted run", stored, err)
	}
	// The lease is released, so the next poll can run.
	if next := e.poll(ModeFull, 0); next.Status != store.RunOK {
		t.Errorf("next poll: %+v", next)
	}
}

func TestQuickPollsListNewestProgramsBetweenCompleteListings(t *testing.T) {
	e := newEnv(t)
	e.seed()
	poll := func(mode string) store.Run {
		t.Helper()
		e.clock = e.clock.Add(time.Minute)
		run, err := e.sync.Poll(context.Background(), fake.Recent{Platform: e.fake}, Options{Mode: mode, Trigger: "manual"})
		if err != nil {
			t.Fatalf("Poll: %v", err)
		}
		return run
	}
	// No complete listing yet: even a quick poll reads all of it.
	if run := poll(ModeQuick); run.ListingRecent || !run.ListingComplete {
		t.Fatalf("first quick poll: recent=%v complete=%v; want a complete listing", run.ListingRecent, run.ListingComplete)
	}

	// Within two hours, quick polls read the newest page: a new program is
	// added, and the ones it doesn't list stay.
	e.fake.SetRecent(prog("initech", false))
	e.fake.SetScope("initech", entry("4", "*.initech.example", true))
	run := poll(ModeQuick)
	if !run.ListingRecent || run.ListingComplete || e.fake.RecentRequests() != 1 {
		t.Fatalf("second quick poll: recent=%v complete=%v recent requests=%d", run.ListingRecent, run.ListingComplete, e.fake.RecentRequests())
	}
	if got := e.changes(run.ID); len(got) != 1 || !strings.HasPrefix(got[0], "initech program_added") {
		t.Errorf("changes = %v; want initech added and nothing removed", got)
	}
	if !strings.Contains(FormatRun(run), "newest programs only") {
		t.Errorf("summary = %q", FormatRun(run))
	}

	// Full polls always list everything; so do quick polls two hours on.
	if run := poll(ModeFull); run.ListingRecent || !run.ListingComplete {
		t.Errorf("full poll: recent=%v complete=%v", run.ListingRecent, run.ListingComplete)
	}
	e.clock = e.clock.Add(completeListingEvery)
	if run := poll(ModeQuick); run.ListingRecent || !run.ListingComplete {
		t.Errorf("quick poll after two hours: recent=%v complete=%v", run.ListingRecent, run.ListingComplete)
	}
}

func TestPollsKeepHuntScores(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	e.seed()
	start := e.clock
	e.poll(ModeFull, 0)
	picks, err := e.store.TopHunts(ctx, 10)
	if err != nil || len(picks) != 2 {
		t.Fatalf("after the first poll: %v picks, %v", len(picks), err)
	}

	// Two days on, acme adds a wildcard and an Android app: its score rises
	// and history has both days.
	e.clock = e.clock.Add(48 * time.Hour)
	app := platform.ScopeEntry{PlatformID: "5", InScope: true, AssetType: scope.AssetAndroid, PlatformAssetType: "GOOGLE_PLAY_APP_ID",
		Identifier: "com.example.acme", BountyEligible: true}
	e.fake.SetScope("acme", entry("1", "*.acme.example", true), entry("2", "old.acme.example", false),
		entry("4", "*.new.example", true), app)
	e.poll(ModeFull, 0)
	d, err := e.store.Program(ctx, "fake", "acme")
	if err != nil || d.Hunt == nil {
		t.Fatalf("acme = %+v, %v", d.Hunt, err)
	}
	hist, err := e.store.HuntHistory(ctx, d.ID, start.Add(-time.Hour))
	if err != nil || len(hist) != 2 || hist[1].Fresh <= hist[0].Fresh || hist[1].Score <= hist[0].Score {
		t.Fatalf("history = %+v, %v", hist, err)
	}
	rising, err := e.store.RisingHunts(ctx, start, 10)
	if err != nil || len(rising) != 1 || rising[0].Handle != "acme" || rising[0].Change != hist[1].Score-hist[0].Score {
		t.Errorf("rising = %+v, %v", rising, err)
	}

	// The new wildcard is a best new target; the first import's scope, of
	// unknown age, isn't.
	best, total, err := e.store.BestTargets(ctx, store.TargetFilter{Kind: scope.KindWildcard}, "", 10, 0)
	if err != nil || total != 1 || len(best) != 1 || best[0].Value != "*.new.example" || best[0].Score <= 0 ||
		best[0].Program.Handle != "acme" || len(best[0].Reasons) == 0 {
		t.Fatalf("best targets = %+v (total %d), %v", best, total, err)
	}
	// The app is scored too, listed under apps and with everything else,
	// but never as a web target.
	apps, total, err := e.store.BestTargets(ctx, store.TargetFilter{Kind: scope.KindApp}, "", 10, 0)
	if err != nil || total != 1 || apps[0].Kind != scope.AssetAndroid || apps[0].Value != "com.example.acme" || !apps[0].Bounty || apps[0].Score <= 0 {
		t.Fatalf("best apps = %+v (total %d), %v", apps, total, err)
	}
	if _, total, err := e.store.BestTargets(ctx, store.TargetFilter{}, "", 10, 0); err != nil || total != 2 {
		t.Errorf("best of every kind: total %d, %v; want 2", total, err)
	}
	var txt []string
	if err := e.store.EachBestTarget(ctx, store.TargetFilter{Kind: scope.KindRoot}, func(v string) error {
		txt = append(txt, v)
		return nil
	}); err != nil || !slices.Equal(txt, []string{"new.example"}) {
		t.Errorf("best roots = %v, %v", txt, err)
	}
	// A date that's only an upper bound ("there by then") isn't new scope.
	for _, en := range d.Entries {
		if en.Identifier == "*.new.example" || en.Identifier == "com.example.acme" {
			at := e.clock
			if err := e.store.Write(ctx, func(tx *store.Tx) error { return tx.SetEntryDates(ctx, en.ID, &at, true, nil) }); err != nil {
				t.Fatal(err)
			}
		}
	}
	e.clock = e.clock.Add(time.Minute)
	e.poll(ModeQuick, 0)
	if best, total, err := e.store.BestTargets(ctx, store.TargetFilter{Kind: scope.KindWildcard}, "", 10, 0); err != nil || total != 0 {
		t.Errorf("an entry with a bound date is still scored: %+v, %v", best, err)
	}
	if d, err := e.store.Program(ctx, "fake", "acme"); err != nil || d.Hunt == nil || d.Hunt.Parts[0].Key != "fresh" || d.Hunt.Parts[0].Score != 0 {
		t.Errorf("an entry with a bound date counts as fresh: %+v, %v", d.Hunt, err)
	}

	// A failed poll doesn't rescore.
	e.clock = e.clock.Add(24 * time.Hour)
	e.fake.FailListing(errors.New("boom"), false)
	if run := e.poll(ModeFull, 0); run.Status != store.RunFailed {
		t.Fatalf("run = %s", run.Status)
	}
	if hist, _ := e.store.HuntHistory(ctx, d.ID, start.Add(-time.Hour)); len(hist) != 2 {
		t.Errorf("a failed poll scored: %+v", hist)
	}

	// Acme, whose listing now gives a bounty range, adds a wildcard that pays,
	// and a day later one that doesn't; a day after that, Globex, paying at
	// most 50, adds one. Each order puts them differently.
	e.fake.FailListing(nil, false)
	acme := prog("acme", false)
	lo, hi := 100.0, 5000.0
	acme.BountyMin, acme.BountyMax = &lo, &hi
	e.fake.SetPrograms(acme, prog("globex", true))
	paid, unpaid := entry("6", "*.paid.example", true), entry("7", "*.unpaid.example", true)
	unpaid.BountyEligible = false
	e.fake.SetScope("acme", entry("1", "*.acme.example", true), entry("2", "old.acme.example", false), entry("4", "*.new.example", true), paid)
	e.clock = e.clock.Add(time.Hour)
	e.poll(ModeFull, 0)
	e.fake.SetScope("acme", entry("1", "*.acme.example", true), entry("2", "old.acme.example", false), entry("4", "*.new.example", true), paid, unpaid)
	e.clock = e.clock.Add(24 * time.Hour)
	e.poll(ModeFull, 0)
	globex := prog("globex", true)
	cheap := 50.0
	globex.BountyMax = &cheap
	e.fake.SetPrograms(acme, globex)
	e.fake.SetScope("globex", entry("3", "app.globex.example", true), entry("8", "*.cheap.example", true))
	e.clock = e.clock.Add(24 * time.Hour)
	e.poll(ModeFull, 0)
	for _, tt := range []struct {
		order   string
		reverse bool
		want    []string
	}{
		{store.TargetsByScore, false, []string{"*.cheap.example", "*.paid.example", "*.unpaid.example"}},
		{store.TargetsByScore, true, []string{"*.unpaid.example", "*.paid.example", "*.cheap.example"}},
		{store.TargetsByNewest, false, []string{"*.cheap.example", "*.unpaid.example", "*.paid.example"}},
		{store.TargetsByNewest, true, []string{"*.paid.example", "*.unpaid.example", "*.cheap.example"}},
		{store.TargetsByBounty, false, []string{"*.paid.example", "*.cheap.example", "*.unpaid.example"}},
		// Lowest bounty first, with the one that pays nothing still last.
		{store.TargetsByBounty, true, []string{"*.cheap.example", "*.paid.example", "*.unpaid.example"}},
	} {
		rows, _, err := e.store.BestTargets(ctx, store.TargetFilter{Kind: scope.KindWildcard, Order: tt.order, Reverse: tt.reverse}, "", 10, 0)
		var got []string
		for _, r := range rows {
			got = append(got, r.Value)
		}
		if err != nil || !slices.Equal(got, tt.want) {
			t.Errorf("best targets by %q (reversed %v) = %v, %v; want %v", tt.order, tt.reverse, got, err, tt.want)
		}
		for _, r := range rows {
			got := fmt.Sprint(deref(r.PayMin), " ", deref(r.PayMax), " ", r.PayFrom)
			if want := map[string]string{"*.paid.example": "100 5000 listing", "*.unpaid.example": "0 0 ", "*.cheap.example": "0 50 listing"}[r.Value]; got != want {
				t.Errorf("%s bounty range = %s, want %s", r.Value, got, want)
			}
		}
	}
}

// A listing that can't say whether a program pays records no bounty change
// and keeps the stored answer.
func TestBountyUnknownRecordsNoChange(t *testing.T) {
	e := newEnv(t)
	e.seed()
	acme := prog("acme", false) // pays
	e.fake.SetPrograms(acme)
	e.poll(ModeFull, 0)

	acme.OffersBounty, acme.BountyUnknown = false, true
	e.fake.SetPrograms(acme)
	run := e.poll(ModeFull, 0)
	for _, c := range e.changes(run.ID) {
		if strings.Contains(c, "bounty") {
			t.Errorf("unexpected change %q", c)
		}
	}
	items, _, err := e.store.Programs(context.Background(), store.ProgramFilter{Limit: 10})
	if err != nil || len(items) != 1 || !items[0].OffersBounty {
		t.Errorf("programs = %+v, %v; want acme still paying", items, err)
	}
}
