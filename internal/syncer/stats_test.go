package syncer

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/mdpsec/huntdeck/internal/platform"
	"github.com/mdpsec/huntdeck/internal/platform/fake"
	"github.com/mdpsec/huntdeck/internal/store"
)

func TestUserStatsSnapshotsOnlyOnChange(t *testing.T) {
	e := newEnv(t)
	p := prog("acme", false)
	p.UserStats = &platform.UserStats{Reports: 1}
	e.fake.SetPrograms(p)
	e.fake.SetScope("acme", entry("1", "acme.example", true))
	e.poll(ModeQuick, 0)
	e.poll(ModeQuick, 0)
	p.UserStats = &platform.UserStats{Reports: 2, ValidReports: 1, BountyEarned: 250}
	e.fake.SetPrograms(p)
	e.poll(ModeQuick, 0)

	hist, err := e.store.UserStatsHistory(context.Background(), "fake", "acme")
	if err != nil || len(hist) != 2 || hist[1].BountyEarned != 250 {
		t.Errorf("stats snapshots = %+v, %v; want 2, latest 250", hist, err)
	}
}

func TestProgramStatsRefreshAboutDailyOnFullPolls(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	e.clock = time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	e.seed()
	paid := 1000.0
	rewarded, total, week, validation := 12, 340, 5, 144.0
	e.fake.SetStats("acme", platform.ProgramStats{Currency: "USD", BountiesPaid: &paid,
		Rewards: []platform.RewardRow{{Scope: "*.acme.example", High: platform.RewardRange{Max: 2000}}}})
	e.fake.SetStats("globex", platform.ProgramStats{Currency: "EUR", RewardedReports: &rewarded, TotalReports: &total,
		ReportsLast7d: &week, ValidationHours: &validation})

	e.poll(ModeQuick, 0)
	if n := len(e.fake.StatsRequests()); n != 0 {
		t.Fatalf("quick poll fetched stats %d times", n)
	}
	run := e.poll(ModeFull, 0)
	if got := e.fake.StatsRequests(); len(got) != 1 || strings.Join(got[0], ",") != "acme,globex" {
		t.Fatalf("stats requests = %v, want one for acme,globex", got)
	}
	if run.StatsFetched != 2 || run.StatsFailed != 0 || run.Status != store.RunOK {
		t.Errorf("run = %+v, want 2 stats fetched", run)
	}
	acme, err := e.store.Program(ctx, "fake", "acme")
	if err != nil || acme.PublicStats == nil || *acme.PublicStats.BountiesPaid != 1000 ||
		len(acme.PublicStats.Rewards) != 1 || acme.PublicStats.Rewards[0].High.Max != 2000 {
		t.Fatalf("acme public stats = %+v, %v", acme.PublicStats, err)
	}
	globex, err := e.store.Program(ctx, "fake", "globex")
	if gs := globex.PublicStats; err != nil || gs == nil || *gs.RewardedReports != 12 || *gs.TotalReports != 340 ||
		*gs.ReportsLast7d != 5 || *gs.ValidationHours != 144 {
		t.Fatalf("globex public stats = %+v, %v", gs, err)
	}
	if e.poll(ModeFull, 0); len(e.fake.StatsRequests()) != 1 {
		t.Error("fresh stats were fetched again")
	}

	// 21h later, still the same UTC day: the day's snapshot is replaced.
	e.clock = e.clock.Add(21 * time.Hour)
	paid = 1500
	e.fake.SetStats("acme", platform.ProgramStats{Currency: "USD", BountiesPaid: &paid})
	e.poll(ModeFull, 0)
	// The next day adds a snapshot.
	e.clock = e.clock.Add(21 * time.Hour)
	paid = 2500
	e.fake.SetStats("acme", platform.ProgramStats{Currency: "USD", BountiesPaid: &paid})
	e.poll(ModeFull, 0)
	hist, err := e.store.ProgramStatsHistory(ctx, acme.ID)
	if err != nil || len(hist) != 2 || *hist[0].BountiesPaid != 1500 || *hist[1].BountiesPaid != 2500 {
		t.Errorf("history = %d snapshots, %v; want 2 (1500, 2500)", len(hist), err)
	}

	// A stats failure is reported, keeps what was fetched, and doesn't make
	// the scope poll partial.
	e.clock = e.clock.Add(21 * time.Hour)
	e.fake.FailStats(errors.New("endpoint changed"))
	run = e.poll(ModeFull, 0)
	if run.Status != store.RunOK || run.StatsFetched != 1 || run.StatsFailed != 1 || !strings.Contains(run.Error, "endpoint changed") {
		t.Errorf("run with failing stats = %+v", run)
	}
	e.fake.FailStats(nil)
	e.clock = e.clock.Add(time.Hour)
	e.poll(ModeFull, 0)
	if got := e.fake.StatsRequests(); strings.Join(got[len(got)-1], ",") != "globex" {
		t.Errorf("after a failure only the missed program should be due, got %v", got[len(got)-1])
	}
}

func TestLimitedFullPollFetchesStatsOnlyForItsPrograms(t *testing.T) {
	e := newEnv(t)
	e.seed()
	e.fake.SetStats("acme", platform.ProgramStats{})
	e.fake.SetStats("globex", platform.ProgramStats{})
	e.pollWith(Options{Mode: ModeFull, Limit: 1})
	if got := e.fake.StatsRequests(); len(got) != 1 || strings.Join(got[0], ",") != "acme" {
		t.Errorf("stats requests = %v, want acme only", got)
	}
}

func TestRefreshStatsFetchesNamedProgramsWithoutAPoll(t *testing.T) {
	e := newEnv(t)
	e.seed()
	e.poll(ModeQuick, 0)
	e.fake.SetStats("acme", platform.ProgramStats{Currency: "USD"})
	e.fake.SetStats("globex", platform.ProgramStats{Currency: "USD"})
	ctx := context.Background()
	runs, _ := e.store.RecentRuns(ctx, 10)

	fetched, failed, err := e.sync.RefreshStats(ctx, e.fake, []string{"globex"})
	if err != nil || fetched != 1 || failed != 0 {
		t.Fatalf("RefreshStats(globex) = %d, %d, %v", fetched, failed, err)
	}
	if got := e.fake.StatsRequests(); len(got) != 1 || strings.Join(got[0], ",") != "globex" {
		t.Errorf("stats requests = %v", got)
	}
	if after, _ := e.store.RecentRuns(ctx, 10); len(after) != len(runs) {
		t.Error("RefreshStats recorded a poll run")
	}
	if _, _, err := e.sync.RefreshStats(ctx, e.fake, []string{"nope"}); err == nil {
		t.Error("unknown handle accepted")
	}
	if fetched, _, err := e.sync.RefreshStats(ctx, e.fake, nil); err != nil || fetched != 2 {
		t.Errorf("RefreshStats(all) fetched %d, %v; want 2", fetched, err)
	}
}

func TestPolicyAndProfileAreStored(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	p := prog("acme", false)
	p.Policy = "## Rules\n\nBe nice."
	e.fake.SetPrograms(p)
	e.fake.SetScope("acme", entry("1", "acme.example", true))
	hours := 5.0
	e.fake.SetStats("acme", platform.ProgramStats{FirstResponseHours: &hours,
		Profile: platform.ProgramProfile{Introduction: "Hello", StandardsKnown: true}})
	e.poll(ModeFull, 0)

	d, err := e.store.Program(ctx, "fake", "acme")
	if err != nil || d.Policy != p.Policy || d.PublicStats == nil || d.PublicStats.Profile.Introduction != "Hello" ||
		*d.PublicStats.FirstResponseHours != 5 {
		t.Fatalf("program = %+v, %v", d, err)
	}
	// A newer profile replaces the old one; the policy follows the listing.
	p.Policy = "Be nicer."
	e.fake.SetPrograms(p)
	e.fake.SetStats("acme", platform.ProgramStats{Profile: platform.ProgramProfile{Introduction: "Hi again"}})
	e.clock = e.clock.Add(48 * time.Hour)
	e.poll(ModeFull, 0)
	d, _ = e.store.Program(ctx, "fake", "acme")
	if d.Policy != "Be nicer." || d.PublicStats.Profile.Introduction != "Hi again" || d.PublicStats.FirstResponseHours != nil {
		t.Errorf("after update: policy %q, profile %+v", d.Policy, d.PublicStats.Profile)
	}
}

func TestFactsAndCVSSAreStored(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	yes, no := true, false
	p := prog("acme", false)
	p.BountySplitting = &yes
	e.fake.SetPrograms(p)
	high := entry("1", "*.acme.example", true)
	high.CVSS = platform.CVSSRequirements{Confidentiality: "high", Integrity: "low", Availability: "not_defined"}
	e.fake.SetScope("acme", high, entry("2", "old.acme.example", false))
	e.fake.SetScopeDetail("acme", platform.ScopeDetail{Facts: &platform.Facts{SafeHarbor: "partial", Disclosure: &no}})
	e.fake.SetStats("acme", platform.ProgramStats{Facts: &platform.Facts{OffersSwag: &yes}})
	poll := func() store.Run {
		t.Helper()
		e.clock = e.clock.Add(time.Minute)
		run, err := e.sync.Poll(ctx, fake.Detailed{Platform: e.fake}, Options{Mode: ModeFull, Trigger: "manual"})
		if err != nil {
			t.Fatal(err)
		}
		return run
	}
	poll()

	program := func() *store.ProgramDetail {
		t.Helper()
		d, err := e.store.Program(ctx, "fake", "acme")
		if err != nil {
			t.Fatal(err)
		}
		return d
	}
	cvss := func(d *store.ProgramDetail) string {
		var out []string
		for _, en := range d.Entries {
			out = append(out, fmt.Sprintf("%s=%v", en.Identifier, en.CVSS))
		}
		return strings.Join(out, " ")
	}
	d := program()
	if d.BountySplitting == nil || !*d.BountySplitting || d.OffersSwag == nil || !*d.OffersSwag ||
		d.SafeHarborLevel != "partial" || d.Disclosure == nil || *d.Disclosure || d.Collaboration != nil {
		t.Errorf("facts = splitting %v, swag %v, safe harbor %q, disclosure %v, collaboration %v",
			d.BountySplitting, d.OffersSwag, d.SafeHarborLevel, d.Disclosure, d.Collaboration)
	}
	if got, want := cvss(d), "*.acme.example={high low not_defined} old.acme.example={  }"; got != want {
		t.Errorf("CVSS = %q, want %q", got, want)
	}

	// New CVSS requirements alone aren't a scope change, but are stored;
	// facts the next brief, stats and listing don't give are kept.
	high.CVSS.Integrity = "high"
	e.fake.SetScope("acme", high, entry("2", "old.acme.example", false))
	e.fake.SetScopeDetail("acme", platform.ScopeDetail{Facts: &platform.Facts{Collaboration: &yes}})
	e.fake.SetStats("acme", platform.ProgramStats{})
	e.fake.SetPrograms(prog("acme", false))
	e.clock = e.clock.Add(48 * time.Hour)
	run := poll()
	if c := e.changes(run.ID); len(c) != 0 {
		t.Errorf("changes = %v, want none", c)
	}
	d = program()
	if got, want := cvss(d), "*.acme.example={high high not_defined} old.acme.example={  }"; got != want {
		t.Errorf("CVSS after update = %q, want %q", got, want)
	}
	if d.BountySplitting == nil || d.OffersSwag == nil || d.SafeHarborLevel != "partial" || d.Disclosure == nil ||
		d.Collaboration == nil || !*d.Collaboration {
		t.Errorf("facts after update = splitting %v, swag %v, safe harbor %q, disclosure %v, collaboration %v",
			d.BountySplitting, d.OffersSwag, d.SafeHarborLevel, d.Disclosure, d.Collaboration)
	}
}

func TestRequirementsFromListingAndStats(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	acme, globex := prog("acme", false), prog("globex", false)
	acme.Requirements = &platform.Requirements{Application: true}
	e.fake.SetPrograms(acme, globex)
	e.fake.SetScope("acme", entry("1", "acme.example", true))
	e.fake.SetScope("globex", entry("2", "globex.example", true))
	three := 3.0
	e.fake.SetStats("globex", platform.ProgramStats{Requirements: &platform.Requirements{MinSignal: &three, IDVerified: true}})
	e.poll(ModeQuick, 0)

	req := func(h string) store.ProgramRequirements {
		d, err := e.store.Program(ctx, "fake", h)
		if err != nil {
			t.Fatal(err)
		}
		return d.Requirements
	}
	if r := req("acme"); !r.Known || !r.Application || r.None() {
		t.Errorf("acme from listing = %+v", r)
	}
	if r := req("globex"); r.Known {
		t.Errorf("globex before stats = %+v; want unknown", r)
	}
	e.poll(ModeFull, 0)
	if r := req("globex"); !r.Known || r.MinSignal == nil || *r.MinSignal != 3 || !r.IDVerified {
		t.Errorf("globex from stats = %+v", r)
	}
	// A listing without requirements leaves stats-derived ones alone.
	e.poll(ModeQuick, 0)
	if r := req("globex"); !r.Known || !r.IDVerified {
		t.Errorf("globex after quick poll = %+v", r)
	}

}

// A platform with a browser sync gets its stats from it: full polls leave
// them alone while it's on, and pick them up again when it's turned off.
func TestWebSync(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	e.clock = time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	e.seed()
	web := fake.Web{Platform: e.fake}
	public, logged := 100.0, 900.0
	e.fake.SetStats("acme", platform.ProgramStats{Currency: "USD", BountiesPaid: &public})
	e.fake.SetWebStats("acme", platform.ProgramStats{Currency: "USD", BountiesPaid: &logged})

	poll := func(mode string) store.Run {
		t.Helper()
		e.clock = e.clock.Add(time.Minute)
		run, err := e.sync.Poll(ctx, web, Options{Mode: mode, Trigger: "manual"})
		if err != nil {
			t.Fatalf("%s poll: %v", mode, err)
		}
		return run
	}
	if poll(ModeFull); len(e.fake.StatsRequests()) != 0 {
		t.Fatalf("a full poll fetched stats with the browser sync on: %v", e.fake.StatsRequests())
	}
	run := poll(ModeWeb)
	if got := e.fake.WebRequests(); len(got) != 1 || strings.Join(got[0], ",") != "acme,globex" {
		t.Fatalf("web requests = %v", got)
	}
	if run.Mode != ModeWeb || run.Status != store.RunOK || run.StatsFetched != 1 {
		t.Errorf("run = %+v", run)
	}
	acme, err := e.store.Program(ctx, "fake", "acme")
	if err != nil || acme.PublicStats == nil || *acme.PublicStats.BountiesPaid != 900 {
		t.Fatalf("acme stats after the browser sync = %+v, %v", acme.PublicStats, err)
	}

	if err := e.store.SaveWebSync(ctx, "fake", store.WebSyncSettings{Interval: 24 * time.Hour}); err != nil {
		t.Fatal(err)
	}
	e.clock = e.clock.Add(25 * time.Hour)
	if poll(ModeFull); len(e.fake.StatsRequests()) != 1 {
		t.Errorf("a full poll with the browser sync off didn't fetch stats: %v", e.fake.StatsRequests())
	}

	// Without a browser sync, a web run is refused.
	if _, err := e.sync.Poll(ctx, e.fake, Options{Mode: ModeWeb, Trigger: "manual"}); err == nil {
		t.Error("a web run on a platform without a browser sync ran")
	}
}
