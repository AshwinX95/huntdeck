package syncer

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/mdpsec/huntdeck/internal/platform"
	"github.com/mdpsec/huntdeck/internal/scope"
	"github.com/mdpsec/huntdeck/internal/store"
)

func TestFailedScopeFetchKeepsPreviousScope(t *testing.T) {
	e := newEnv(t)
	e.seed()
	e.poll(ModeFull, 0)

	e.fake.FailScope("globex", errors.New("HTTP 503"))
	run := e.poll(ModeFull, 0)
	if run.Status != store.RunPartial || run.ScopesFailed != 1 || run.ScopesFetched != 1 {
		t.Errorf("run: %+v", run)
	}
	if got := e.changes(run.ID); len(got) != 0 {
		t.Errorf("failed fetch must not remove scope, got %v", got)
	}
	if got := e.targets(scope.KindDomain); !slices.Equal(got, []string{"app.globex.example"}) {
		t.Errorf("globex scope lost after failed fetch: %v", got)
	}
}

func TestUnavailableScopeIsNotAFailure(t *testing.T) {
	e := newEnv(t)
	e.seed()
	e.poll(ModeFull, 0)

	e.fake.FailScope("globex", fmt.Errorf("bugcrowd: program globex: %w", platform.ErrScopeUnavailable))
	run := e.poll(ModeFull, 0)
	if run.Status != store.RunOK || run.ScopesFailed != 0 || run.ScopesFetched != 1 {
		t.Errorf("run: %+v", run)
	}
	if got := e.targets(scope.KindDomain); !slices.Equal(got, []string{"app.globex.example"}) {
		t.Errorf("globex scope lost: %v", got)
	}
}

func TestEntriesWithoutPlatformIDs(t *testing.T) {
	e := newEnv(t)
	e.fake.SetPrograms(prog("acme", false))
	e.fake.SetScope("acme", entry("", "a.acme.example", true), entry("", "a.acme.example", true))
	e.poll(ModeFull, 0)

	e.fake.SetScope("acme", entry("", "b.acme.example", true))
	run := e.poll(ModeFull, 0)
	want := []string{"acme scope_added in_scope →b.acme.example", "acme scope_removed in_scope a.acme.example→"}
	if got := e.changes(run.ID); !slices.Equal(got, want) {
		t.Errorf("changes = %q, want %q", got, want)
	}
}

func TestEmptyScopeKeepsPreviousScope(t *testing.T) {
	e := newEnv(t)
	e.seed()
	e.poll(ModeFull, 0)

	e.fake.SetScope("acme")
	run := e.poll(ModeFull, 0)
	if run.Status != store.RunPartial || run.ScopesFailed != 1 || len(e.changes(run.ID)) != 0 {
		t.Fatalf("run: %+v changes=%v", run, e.changes(run.ID))
	}
	if got := e.targets(scope.KindWildcard); !slices.Equal(got, []string{"*.acme.example"}) {
		t.Fatalf("acme scope lost: %v", got)
	}

	run = e.pollWith(Options{Mode: ModeFull, ForceRemovals: true})
	if got := e.changes(run.ID); len(got) != 2 {
		t.Fatalf("forced run should remove both entries: %v", got)
	}
}

func TestQuickPollSkipsUnavailableScopeUntilFullPoll(t *testing.T) {
	e := newEnv(t)
	e.seed()
	e.fake.SetPrograms(prog("acme", false), prog("globex", true), prog("initech", false))
	unavailable := fmt.Errorf("intigriti: program initech: %w", platform.ErrScopeUnavailable)
	e.fake.FailScope("initech", unavailable)

	steps := []struct {
		mode      string
		available bool
		wantHits  int
	}{
		{ModeFull, false, 1},  // first import tries it
		{ModeQuick, false, 1}, // quick polls leave refused scopes alone
		{ModeQuick, false, 1},
		{ModeFull, false, 2}, // full polls re-check
		{ModeQuick, true, 2}, // still waiting for the next full poll
		{ModeFull, true, 3},  // available now: fetched
		{ModeQuick, true, 3}, // synced, so quick polls skip it as usual
	}
	for i, st := range steps {
		if st.available {
			e.fake.FailScope("initech", nil)
			e.fake.SetScope("initech", entry("9", "*.initech.example", true))
		}
		run := e.poll(st.mode, 0)
		if run.Status != store.RunOK || run.ScopesFailed != 0 {
			t.Errorf("step %d: run %+v", i, run)
		}
		if got := e.fake.ScopeHits("initech"); got != st.wantHits {
			t.Errorf("step %d (%s): initech scope fetched %d times, want %d", i, st.mode, got, st.wantHits)
		}
	}
	if got := e.targets(scope.KindWildcard); !slices.Equal(got, []string{"*.acme.example", "*.initech.example"}) {
		t.Errorf("wildcards = %v", got)
	}
}

func TestScopeCountsFollowScopeChanges(t *testing.T) {
	e := newEnv(t)
	e.seed()
	e.poll(ModeFull, 0)

	counts := func() map[string][4]int {
		t.Helper()
		progs, _, err := e.store.Programs(context.Background(), store.ProgramFilter{State: "all", Limit: 10})
		if err != nil {
			t.Fatal(err)
		}
		out := map[string][4]int{}
		for _, p := range progs {
			out[p.Handle] = [4]int{p.InScope, p.OutOfScope, p.Wildcards, p.Domains}
		}
		return out
	}
	want := map[string][4]int{"acme": {1, 1, 1, 0}, "globex": {1, 0, 0, 1}}
	if got := counts(); !maps.Equal(got, want) {
		t.Errorf("after import: %v, want %v", got, want)
	}

	e.fake.SetScope("acme", entry("1", "*.acme.example", true), entry("4", "*.acme.example.net", true), entry("5", "api.acme.example", true))
	e.poll(ModeFull, 0)
	want["acme"] = [4]int{3, 0, 2, 1}
	if got := counts(); !maps.Equal(got, want) {
		t.Errorf("after scope change: %v, want %v", got, want)
	}
	progs, _, err := e.store.Programs(context.Background(), store.ProgramFilter{Sort: "updated", Limit: 1})
	if err != nil {
		t.Fatal(err)
	}
	if len(progs) != 1 || progs[0].Handle != "acme" || progs[0].ScopeChangedAt == nil {
		t.Errorf("most recently changed program = %+v, want acme with a change time", progs)
	}
}

func TestTargetTotalsFollowScopeChanges(t *testing.T) {
	e := newEnv(t)
	e.seed()
	e.poll(ModeFull, 0)

	total := func() int {
		t.Helper()
		_, n, err := e.store.TargetRows(context.Background(), store.TargetFilter{Kind: scope.KindDomain}, "", 10, 0)
		if err != nil {
			t.Fatal(err)
		}
		return n
	}
	if got := total(); got != 1 {
		t.Fatalf("domains = %d, want 1", got)
	}
	if got := total(); got != 1 { // served from the cache
		t.Fatalf("cached domains = %d, want 1", got)
	}
	e.fake.SetScope("globex", entry("3", "app.globex.example", true), entry("6", "api.globex.example", true))
	e.poll(ModeFull, 0)
	if got := total(); got != 2 {
		t.Errorf("after adding a domain: %d, want 2", got)
	}
	e.fake.SetScope("globex", entry("6", "api.globex.example", true))
	e.poll(ModeFull, 0)
	if got := total(); got != 1 {
		t.Errorf("after removing a domain: %d, want 1", got)
	}
}

func TestTargetRowsListATargetOncePerProgram(t *testing.T) {
	e := newEnv(t)
	e.fake.SetPrograms(prog("acme", false))
	// The same host as a URL (no bounty) and as a domain (bounty).
	noBounty := entry("1", "https://api.acme.example/v1", true)
	noBounty.BountyEligible = false
	e.fake.SetScope("acme", noBounty, entry("2", "api.acme.example", true), entry("3", "www.acme.example", true))
	e.poll(ModeFull, 0)

	for _, tt := range []struct {
		filter store.TargetFilter
		want   []string
	}{
		{store.TargetFilter{Kind: scope.KindDomain}, []string{"api.acme.example bounty", "www.acme.example bounty"}},
		{store.TargetFilter{Kind: scope.KindDomain, BountyOnly: true}, []string{"api.acme.example bounty", "www.acme.example bounty"}},
	} {
		rows, total, err := e.store.TargetRows(context.Background(), tt.filter, "", 10, 0)
		if err != nil {
			t.Fatal(err)
		}
		var got []string
		for _, r := range rows {
			got = append(got, r.Value+map[bool]string{true: " bounty", false: ""}[r.Bounty])
		}
		if total != len(tt.want) || !slices.Equal(got, tt.want) {
			t.Errorf("%+v: total %d, rows %v; want %v", tt.filter, total, got, tt.want)
		}
	}
}

func TestListedChangeTimesSkipUnchangedScopes(t *testing.T) {
	e := newEnv(t)
	e.seed()
	old := e.clock.Add(-30 * 24 * time.Hour)
	acme, globex := prog("acme", false), prog("globex", true)
	acme.ScopeChangedAt, globex.ScopeChangedAt = &old, &old
	e.fake.SetPrograms(acme, globex)
	e.poll(ModeFull, 0)

	run := e.poll(ModeFull, 0)
	if run.ScopesFetched != 0 || run.ScopesUnchanged != 2 || e.hits() != "acme=1 globex=1" {
		t.Fatalf("unchanged full poll: fetched=%d unchanged=%d %s", run.ScopesFetched, run.ScopesUnchanged, e.hits())
	}
	if n := e.fake.Checks("acme"); n != 0 {
		t.Errorf("checker asked %d times though the listing said", n)
	}

	// The listing reports a change: even a quick poll fetches it.
	now := e.clock
	acme.ScopeChangedAt = &now
	e.fake.SetPrograms(acme, globex)
	e.fake.SetScope("acme", entry("1", "*.acme.example", true), entry("5", "new.acme.example", true))
	run = e.poll(ModeQuick, 0)
	if run.ScopesFetched != 1 || run.ScopesUnchanged != 1 || e.hits() != "acme=2 globex=1" {
		t.Fatalf("quick poll after change: fetched=%d unchanged=%d %s", run.ScopesFetched, run.ScopesUnchanged, e.hits())
	}
	if got := strings.Join(e.changes(run.ID), "\n"); !strings.Contains(got, "scope_added in_scope →new.acme.example") {
		t.Errorf("changes = %v", got)
	}
	// The change was within changeSlack of that download, so the next poll
	// fetches once more in case the download predated it; then it settles.
	e.clock = e.clock.Add(20 * time.Minute)
	e.poll(ModeQuick, 0)
	if run = e.poll(ModeQuick, 0); run.ScopesFetched != 0 || e.hits() != "acme=3 globex=1" {
		t.Errorf("after the slack re-fetch: fetched=%d %s, want acme=3", run.ScopesFetched, e.hits())
	}

	// After scopeRecheck, full polls download anyway.
	e.clock = e.clock.Add(scopeRecheck)
	if run = e.poll(ModeFull, 0); run.ScopesFetched != 2 || run.ScopesUnchanged != 0 {
		t.Errorf("full poll after a day: fetched=%d unchanged=%d", run.ScopesFetched, run.ScopesUnchanged)
	}
	d, err := e.store.Program(context.Background(), "fake", "globex")
	if err != nil || d.ScopeCheckedAt == nil || !d.ScopeCheckedAt.Equal(e.clock) {
		t.Errorf("scope_checked_at = %v, want %v (%v)", d.ScopeCheckedAt, e.clock, err)
	}
}

func TestChangeFeedCoverage(t *testing.T) {
	e := newEnv(t)
	e.seed()
	start := e.clock.Add(-48 * time.Hour)
	e.fake.SetChangesSince(&start)
	e.poll(ModeFull, 0)

	// The feed covers everything since before the downloads and names no
	// program: both unchanged.
	if run := e.poll(ModeFull, 0); run.ScopesFetched != 0 || run.ScopesUnchanged != 2 {
		t.Fatalf("covered: fetched=%d unchanged=%d", run.ScopesFetched, run.ScopesUnchanged)
	}
	// The feed now starts after the downloads: unknown, so full polls fetch.
	later := e.clock.Add(time.Hour)
	e.fake.SetChangesSince(&later)
	if run := e.poll(ModeQuick, 0); run.ScopesFetched != 0 || run.ScopesUnchanged != 0 {
		t.Errorf("quick poll without coverage: fetched=%d unchanged=%d", run.ScopesFetched, run.ScopesUnchanged)
	}
	if run := e.poll(ModeFull, 0); run.ScopesFetched != 2 {
		t.Errorf("full poll without coverage: fetched=%d", run.ScopesFetched)
	}
}

func TestCheckerSkipsUnchangedScopes(t *testing.T) {
	e := newEnv(t)
	e.seed()
	e.fake.SetScopeChangedAt("acme", e.clock.Add(-30*24*time.Hour))
	e.poll(ModeFull, 0)

	run := e.poll(ModeFull, 0)
	if run.ScopesFetched != 1 || run.ScopesUnchanged != 1 || e.hits() != "acme=1 globex=2" {
		t.Fatalf("fetched=%d unchanged=%d %s (globex's checker says nothing)", run.ScopesFetched, run.ScopesUnchanged, e.hits())
	}
	e.poll(ModeQuick, 0)
	if n := e.fake.Checks("acme"); n != 1 {
		t.Errorf("acme checked %d times; quick polls must not check", n)
	}

	e.fake.SetScopeChangedAt("acme", e.clock)
	if run = e.poll(ModeFull, 0); e.hits() != "acme=2 globex=3" {
		t.Errorf("after a change: %s", e.hits())
	}
	// Past scopeRecheck the download happens without asking.
	e.fake.SetScopeChangedAt("acme", time.Time{}.Add(time.Hour))
	e.clock = e.clock.Add(scopeRecheck)
	checks := e.fake.Checks("acme")
	e.poll(ModeFull, 0)
	if e.fake.Checks("acme") != checks || e.fake.ScopeHits("acme") != 3 {
		t.Errorf("stale scope: checks %d→%d, hits %d", checks, e.fake.Checks("acme"), e.fake.ScopeHits("acme"))
	}
}

func TestAddedDates(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	created := time.Date(2025, 3, 10, 12, 0, 0, 0, time.UTC)
	dated := entry("1", "*.acme.example", true)
	dated.CreatedAt = &created
	e.fake.SetPrograms(prog("acme", false))
	e.fake.SetScope("acme", dated, entry("2", "old.acme.example", true))
	e.poll(ModeFull, 0)

	added := func() map[string]string {
		d, err := e.store.Program(ctx, "fake", "acme")
		if err != nil {
			t.Fatal(err)
		}
		out := map[string]string{}
		for _, en := range d.Entries {
			v := "unknown"
			if en.AddedAt != nil {
				v = en.AddedAt.Format(time.RFC3339)
				if en.AddedByPlatform {
					v += " (platform)"
				}
			}
			if en.RemovedAt != nil {
				v += " removed " + en.RemovedAt.Format(time.RFC3339)
			}
			out[en.Identifier] = v
		}
		return out
	}
	// Baseline entries without a platform date have no known age.
	if got := added(); got["*.acme.example"] != "2025-03-10T12:00:00Z (platform)" || got["old.acme.example"] != "unknown" {
		t.Fatalf("after baseline: %v", got)
	}

	// Later additions are dated when HuntDeck sees them; removals too.
	e.fake.SetScope("acme", dated, entry("3", "new.acme.example", true))
	e.poll(ModeFull, 0)
	seen := e.clock.Format(time.RFC3339)
	got := added()
	if got["new.acme.example"] != seen || got["old.acme.example"] != "unknown removed "+seen {
		t.Fatalf("after changes: %v", got)
	}

	// A removed entry that comes back counts as added again.
	e.fake.SetScope("acme", dated, entry("2", "old.acme.example", true), entry("3", "new.acme.example", true))
	e.poll(ModeFull, 0)
	if got := added(); got["old.acme.example"] != e.clock.Format(time.RFC3339) {
		t.Errorf("revived: %v", got)
	}

	// Targets can be filtered by when they were added.
	var vals []string
	err := e.store.EachTarget(ctx, store.TargetFilter{Kind: scope.KindDomain, AddedSince: e.clock.Add(-time.Hour)}, func(v string) error {
		vals = append(vals, v)
		return nil
	})
	if err != nil || strings.Join(vals, ",") != "new.acme.example,old.acme.example" {
		t.Errorf("domains added in the last hour = %v, %v", vals, err)
	}
	rows, n, err := e.store.TargetRows(ctx, store.TargetFilter{Kind: scope.KindWildcard, AddedSince: created.Add(-time.Hour)}, "", 10, 0)
	if err != nil || n != 1 || rows[0].AddedAt == nil || !rows[0].AddedAt.Equal(created) {
		t.Errorf("wildcards added since 2025: %+v n=%d err=%v", rows, n, err)
	}
}

func TestArchivedScopeBecomesHistory(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	e.seed()
	e.poll(ModeFull, 0)

	// acme's "2" is live in HuntDeck; "8" was removed before HuntDeck saw it.
	created, gone := time.Date(2024, 1, 2, 0, 0, 0, 0, time.UTC), time.Date(2026, 5, 19, 0, 0, 0, 0, time.UTC)
	old := entry("8", "legacy.acme.example", true)
	old.CreatedAt = &created
	stillLive := entry("2", "old.acme.example", false)
	e.fake.SetStats("acme", platform.ProgramStats{Archived: []platform.ArchivedScope{
		{Entry: old, ArchivedAt: gone}, {Entry: stillLive, ArchivedAt: gone}}})
	run := e.poll(ModeFull, 0)
	if run.Changes != 0 {
		t.Errorf("history recorded %d changes, want none: %v", run.Changes, e.changes(run.ID))
	}
	d, err := e.store.Program(ctx, "fake", "acme")
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, en := range d.Entries {
		got = append(got, fmt.Sprintf("%s removed=%v", en.Identifier, en.RemovedAt != nil && en.RemovedAt.Equal(gone)))
	}
	if want := "*.acme.example removed=false,old.acme.example removed=false,legacy.acme.example removed=true"; strings.Join(got, ",") != want {
		t.Errorf("entries = %v\nwant %s", got, want)
	}
	// Scope counts and exports ignore history.
	if vals := e.targets(scope.KindDomain); slices.Contains(vals, "legacy.acme.example") {
		t.Errorf("archived entry exported: %v", vals)
	}

	// An entry HuntDeck saw removed gets the platform's exact time.
	e.fake.SetScope("acme", entry("1", "*.acme.example", true))
	e.poll(ModeFull, 0)
	e.fake.SetStats("acme", platform.ProgramStats{Archived: []platform.ArchivedScope{{Entry: stillLive, ArchivedAt: gone}}})
	e.clock = e.clock.Add(25 * time.Hour) // stats are due again
	e.poll(ModeFull, 0)
	d, _ = e.store.Program(ctx, "fake", "acme")
	for _, en := range d.Entries {
		if en.Identifier == "old.acme.example" && (en.RemovedAt == nil || !en.RemovedAt.Equal(gone)) {
			t.Errorf("old.acme.example removed at %v, want %v", en.RemovedAt, gone)
		}
	}
}

func TestScopeHistoryDatesEntries(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	e.seed()
	at := func(y int, m time.Month) time.Time { return time.Date(y, m, 1, 0, 0, 0, 0, time.UTC) }
	wild, old := entry("1", "*.acme.example", true), entry("2", "old.acme.example", false)
	gone := entry("x9", "gone.acme.example", true)
	wildNewID := entry("other-id", "*.ACME.example", true) // IDs can change between versions
	e.fake.SetHistory("acme",
		platform.ScopeVersion{At: at(2024, 1), Entries: []platform.ScopeEntry{wildNewID, gone}},
		platform.ScopeVersion{At: at(2025, 6), Entries: []platform.ScopeEntry{wild, old}},
	)
	e.poll(ModeFull, 0)
	if got := strings.Join(e.fake.HistoryRequests(), ","); got != "acme,globex" {
		t.Errorf("history asked for %s", got)
	}
	d, err := e.store.Program(ctx, "fake", "acme")
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, en := range d.Entries {
		s := en.Identifier + " added " + en.AddedAt.Format("2006-01")
		if en.AddedBound {
			s += " (or before)"
		}
		if en.RemovedAt != nil {
			s += " removed " + en.RemovedAt.Format("2006-01")
		}
		got = append(got, s)
	}
	want := "*.acme.example added 2024-01 (or before),old.acme.example added 2025-06,gone.acme.example added 2024-01 (or before) removed 2025-06"
	if strings.Join(got, ",") != want {
		t.Errorf("entries:\n%s\nwant\n%s", strings.Join(got, ","), want)
	}

	// With no change recorded, Scope changed is the latest exact date the
	// history gives: gone's removal and old's addition, 2025-06.
	progs, _, err := e.store.Programs(ctx, store.ProgramFilter{State: "all", Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range progs {
		if p.Handle == "acme" && (p.ScopeChangedAt == nil || !p.ScopeChangedAt.Equal(at(2025, 6))) {
			t.Errorf("acme scope changed at %v, want 2025-06 from its history", p.ScopeChangedAt)
		}
	}

	// Read once per program: globex had no history, so it's asked again;
	// acme isn't.
	e.poll(ModeFull, 0)
	if got := strings.Join(e.fake.HistoryRequests(), ","); got != "acme,globex,globex" {
		t.Errorf("second poll asked for %s", got)
	}
	// Bound dates don't count as recently added.
	var vals []string
	_ = e.store.EachTarget(ctx, store.TargetFilter{Kind: scope.KindWildcard, AddedSince: at(2023, 1)}, func(v string) error {
		vals = append(vals, v)
		return nil
	})
	if len(vals) != 0 {
		t.Errorf("wildcards added since 2023 = %v; an on-or-before date must not match", vals)
	}
}

func TestStateHistoryAddsPastPausesOnce(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	e.seed()
	start := e.clock
	e.fake.SetStateHistory("globex",
		platform.StateChange{At: start.Add(-30 * 24 * time.Hour), From: platform.StateOpen, To: platform.StatePaused},
		platform.StateChange{At: start.Add(-20 * 24 * time.Hour), From: platform.StatePaused, To: platform.StateOpen},
		platform.StateChange{At: start.Add(time.Hour), From: platform.StateOpen, To: platform.StatePaused}, // after HuntDeck saw it: polls cover that
	)
	e.poll(ModeQuick, 0)
	if got := e.fake.StateHistoryRequests(); len(got) != 0 {
		t.Errorf("quick poll read state history for %v", got)
	}
	e.poll(ModeFull, 0)
	e.poll(ModeFull, 0)
	if got := strings.Join(e.fake.StateHistoryRequests(), ","); got != "acme,globex" {
		t.Errorf("state history asked for %s; want each program once", got)
	}
	all, _, err := e.store.Changes(ctx, store.ChangeFilter{Kinds: []string{store.ChangeStateChanged}, Limit: 100})
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, c := range all {
		got = append(got, fmt.Sprintf("%s %s→%s %s run=%d", c.Program.Handle, c.OldValue, c.NewValue, c.OccurredAt.Format(time.DateOnly), c.PollRunID))
	}
	want := "globex open→paused 2026-09-01 run=0,globex paused→open 2026-09-11 run=0"
	if strings.Join(got, ",") != want {
		t.Errorf("state changes:\n%s\nwant\n%s", strings.Join(got, ","), want)
	}
}

// RefetchScope downloads every scope in a full poll, even unchanged ones;
// a quick poll ignores it, and a second identical poll still records no
// changes.
func TestRefetchScope(t *testing.T) {
	e := newEnv(t)
	e.seed()
	old := e.clock.Add(-30 * 24 * time.Hour)
	acme, globex := prog("acme", false), prog("globex", true)
	acme.ScopeChangedAt, globex.ScopeChangedAt = &old, &old
	e.fake.SetPrograms(acme, globex)
	e.poll(ModeFull, 0)

	if run := e.pollWith(Options{Mode: ModeQuick, RefetchScope: true}); run.ScopesFetched != 0 || e.hits() != "acme=1 globex=1" {
		t.Errorf("quick poll with refetch: fetched=%d %s; want none", run.ScopesFetched, e.hits())
	}
	run := e.pollWith(Options{Mode: ModeFull, RefetchScope: true})
	if run.ScopesFetched != 2 || run.ScopesUnchanged != 0 || e.hits() != "acme=2 globex=2" {
		t.Fatalf("full poll with refetch: fetched=%d unchanged=%d %s", run.ScopesFetched, run.ScopesUnchanged, e.hits())
	}
	if got := e.changes(run.ID); len(got) != 0 {
		t.Errorf("refetching unchanged scopes recorded changes: %v", got)
	}
}
