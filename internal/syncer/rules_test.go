package syncer

import (
	"context"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/mdpsec/huntdeck/internal/platform"
	"github.com/mdpsec/huntdeck/internal/platform/fake"
	"github.com/mdpsec/huntdeck/internal/store"
)

func TestRuleChangesAreRecorded(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	e.clock = time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	p := prog("acme", false)
	p.Policy = "## Rules\n\nBe nice."
	e.fake.SetPrograms(p, prog("globex", true))
	e.fake.SetScope("acme", entry("1", "*.acme.example", true))
	e.fake.SetScope("globex", entry("3", "app.globex.example", true))
	table := []platform.RewardRow{{Scope: "*.acme.example", High: platform.RewardRange{Max: 2000}},
		{Scope: "api.acme.example", Critical: platform.RewardRange{Max: 5000}}}
	stats := platform.ProgramStats{Rewards: table, Profile: platform.ProgramProfile{Campaign: "Double bounties",
		ScopeExclusions: []platform.Exclusion{{Name: "DoS"}}}}
	e.fake.SetStats("acme", stats)
	e.fake.SetStats("globex", platform.ProgramStats{}) // its stats carry no bounty table

	kinds := func(runID int64) []string {
		t.Helper()
		all, _, err := e.store.Changes(ctx, store.ChangeFilter{Limit: 1000})
		if err != nil {
			t.Fatal(err)
		}
		var out []string
		for _, c := range all {
			if c.PollRunID == runID {
				out = append(out, c.Program.Handle+" "+c.Kind+" "+c.Field)
			}
		}
		slices.Sort(out)
		return out
	}
	tests := []struct {
		name  string
		setup func()
		mode  string
		want  []string
	}{
		{"first import is a baseline", func() {}, ModeFull, nil},
		{"policy edited", func() {
			p.Policy = "## Rules\n\nBe nicer."
			e.fake.SetPrograms(p, prog("globex", true))
		}, ModeQuick, []string{"acme policy_changed policy"}},
		{"whitespace only", func() {
			p.Policy = "## Rules  \r\n\r\nBe nicer.\n"
			e.fake.SetPrograms(p, prog("globex", true))
		}, ModeQuick, nil},
		{"listing without the policy", func() {
			p.Policy = ""
			e.fake.SetPrograms(p, prog("globex", true))
		}, ModeQuick, nil},
		{"same table in another order", func() {
			e.clock = e.clock.Add(21 * time.Hour)
			stats.Rewards = []platform.RewardRow{table[1], table[0]}
			e.fake.SetStats("acme", stats)
		}, ModeFull, nil},
		{"rewards, notes and exclusions change", func() {
			e.clock = e.clock.Add(21 * time.Hour)
			stats.Rewards = []platform.RewardRow{table[0], {Scope: "api.acme.example", Critical: platform.RewardRange{Max: 10000}}}
			stats.Profile = platform.ProgramProfile{ScopeExclusions: []platform.Exclusion{{Name: "DoS"}, {Name: "Clickjacking", Details: "on  static pages"}}}
			e.fake.SetStats("acme", stats)
		}, ModeFull, []string{"acme policy_changed campaign", "acme policy_changed exclusions", "acme rewards_changed rewards"}},
		{"a brief section read the first time is a baseline", func() {
			e.clock = e.clock.Add(21 * time.Hour)
			stats.Profile.OutScope = "shop.acme.example"
			e.fake.SetStats("acme", stats)
		}, ModeFull, nil},
		{"brief section edited", func() {
			e.clock = e.clock.Add(21 * time.Hour)
			stats.Profile.OutScope = "shop.acme.example and blog.acme.example"
			e.fake.SetStats("acme", stats)
		}, ModeFull, []string{"acme policy_changed brief_out_of_scope"}},
		{"stats without a bounty table say nothing about it", func() {
			e.clock = e.clock.Add(21 * time.Hour)
			stats.Rewards = nil
			e.fake.SetStats("acme", stats)
		}, ModeFull, nil},
	}
	for _, tt := range tests {
		tt.setup()
		run := e.poll(tt.mode, 0)
		if got := kinds(run.ID); !slices.Equal(got, tt.want) {
			t.Errorf("%s: changes %q, want %q", tt.name, got, tt.want)
		}
	}
	d, err := e.store.Program(ctx, "fake", "acme")
	if err != nil || d.Policy != "## Rules  \r\n\r\nBe nicer.\n" {
		t.Errorf("stored policy = %q, %v", d.Policy, err)
	}
	all, _, _ := e.store.Changes(ctx, store.ChangeFilter{Kinds: []string{store.ChangeRewardsChanged}})
	if len(all) != 1 || !strings.Contains(all[0].OldValue, `"max":5000`) || !strings.Contains(all[0].NewValue, `"max":10000`) {
		t.Errorf("rewards change = %+v", all)
	}

	// Stats refreshed outside a poll still record what changed, with no run.
	stats.Rewards = table
	e.fake.SetStats("acme", stats)
	if _, _, err := e.sync.RefreshStats(ctx, e.fake, []string{"acme"}); err != nil {
		t.Fatal(err)
	}
	if got := kinds(0); !slices.Equal(got, []string{"acme rewards_changed rewards"}) {
		t.Errorf("refresh changes = %q", got)
	}
}

// Platforms whose scope download carries the rules and bounty table have
// them compared on every download, even when the scope itself is unchanged.
func TestRulesFromScopeDownloads(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	e.seed()
	e.fake.SetStats("acme", platform.ProgramStats{}) // stats without a bounty table
	p := fake.Detailed{Platform: e.fake}
	table := []platform.RewardRow{{Scope: "Tier 1", Critical: platform.RewardRange{Min: 4000, Max: 10000}}}
	poll := func(d platform.ScopeDetail) []string {
		t.Helper()
		e.fake.SetScopeDetail("acme", d)
		e.clock = e.clock.Add(time.Minute)
		run, err := e.sync.Poll(ctx, p, Options{Mode: ModeFull, Trigger: "manual"})
		if err != nil {
			t.Fatal(err)
		}
		all, _, _ := e.store.Changes(ctx, store.ChangeFilter{Limit: 100})
		var out []string
		for _, c := range all {
			if c.PollRunID == run.ID {
				out = append(out, c.Kind+" "+c.Field)
			}
		}
		slices.Sort(out)
		return out
	}
	tests := []struct {
		name   string
		detail platform.ScopeDetail
		want   []string
	}{
		{"first download is a baseline", platform.ScopeDetail{Policy: "Be nice.", Rewards: table}, nil},
		{"rules edited, scope the same", platform.ScopeDetail{Policy: "Be nicer.", Rewards: table}, []string{"policy_changed policy"}},
		{"bounty raised", platform.ScopeDetail{Policy: "Be nicer.",
			Rewards: []platform.RewardRow{{Scope: "Tier 1", Critical: platform.RewardRange{Min: 5000, Max: 15000}}}},
			[]string{"rewards_changed rewards"}},
		{"a download without them says nothing", platform.ScopeDetail{}, nil},
	}
	for _, tt := range tests {
		if got := poll(tt.detail); !slices.Equal(got, tt.want) {
			t.Errorf("%s: changes %q, want %q", tt.name, got, tt.want)
		}
	}
	d, err := e.store.Program(ctx, "fake", "acme")
	if err != nil || d.Policy != "Be nicer." || d.PublicStats == nil || len(d.PublicStats.Rewards) != 1 ||
		d.PublicStats.Rewards[0].Critical.Max != 15000 {
		t.Errorf("program = policy %q, stats %+v, %v", d.Policy, d.PublicStats, err)
	}
}
