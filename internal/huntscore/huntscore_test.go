package huntscore

import (
	"math"
	"strings"
	"testing"
	"time"

	"github.com/mdpsec/huntdeck/internal/platform"
	"github.com/mdpsec/huntdeck/internal/scope"
	"github.com/mdpsec/huntdeck/internal/store"
)

var now = time.Date(2026, 10, 26, 12, 0, 0, 0, time.UTC)

func days(n float64) time.Time { return now.Add(-time.Duration(n * 24 * float64(time.Hour))) }

func ptr[T any](v T) *T { return &v }

func prog(id int64, name string) store.ScoreProgram {
	return store.ScoreProgram{ID: id, Platform: name, State: platform.StateOpen, OffersBounty: true,
		InScope: 10, Domains: 10}
}

func score(t *testing.T, d store.ScoreData) map[int64]store.HuntDetail {
	t.Helper()
	out := map[int64]store.HuntDetail{}
	for _, s := range Compute(d, now, Presets[0]) {
		out[s.ProgramID] = s.Detail
	}
	return out
}

func part(d store.HuntDetail, key string) store.HuntPart {
	for _, p := range d.Parts {
		if p.Key == key {
			return p
		}
	}
	return store.HuntPart{}
}

func TestFreshWeighsNewScope(t *testing.T) {
	wild := func(id int64, age float64) store.ScoreEntry {
		return store.ScoreEntry{ProgramID: id, Identifier: "*.example.com", AssetType: scope.AssetWildcard, Bounty: true, AddedAt: days(age)}
	}
	url := func(id int64, age float64, bounty bool) store.ScoreEntry {
		return store.ScoreEntry{ProgramID: id, Identifier: "https://a.example.com", AssetType: scope.AssetURL, Bounty: bounty, AddedAt: days(age)}
	}
	vdp := prog(9, "x")
	vdp.OffersBounty = false
	invited := prog(10, "x")
	invited.Private, invited.JoinedAt = true, ptr(days(2))
	d := store.ScoreData{
		Programs: []store.ScoreProgram{prog(1, "x"), prog(2, "x"), prog(3, "x"), prog(4, "x"), prog(5, "x"), prog(6, "x"), vdp, invited},
		Recent: []store.ScoreEntry{
			wild(1, 1),
			wild(2, 28),
			url(3, 1, true),
			url(4, 1, false),
			{ProgramID: 5, Identifier: "new.example.org", AssetType: scope.AssetDomain, Bounty: true, AddedAt: days(1), NewRoot: true},
			{ProgramID: 6, Identifier: "new.example.org", AssetType: scope.AssetDomain, Bounty: true, AddedAt: days(1)},
			url(9, 1, false),
		},
	}
	s := score(t, d)
	f := func(id int64) float64 { return part(s[id], Fresh).Score }
	checks := []struct {
		name string
		ok   bool
	}{
		{"a fresh wildcard beats a month-old one", f(1) > f(2)},
		{"a month-old asset counts about a quarter", math.Abs(f(2)-saturate(3*0.25, 8)) < 1},
		{"a wildcard beats a URL", f(1) > f(3)},
		{"a bounty-eligible URL beats one that isn't", f(3) > f(4)},
		{"a new root domain counts extra", f(5) > f(6)},
		{"assets without bounties still count a little", f(4) > 0},
		{"a new invite is fresh", f(10) > 30},
	}
	for _, c := range checks {
		if !c.ok {
			t.Errorf("%s (fresh: %v)", c.name, map[int64]float64{1: f(1), 2: f(2), 3: f(3), 4: f(4), 5: f(5), 6: f(6), 10: f(10)})
		}
	}
	if r := part(s[5], Fresh).Reasons; len(r) < 2 || r[0] != "1 asset added in the last 30 days: 1 new root domain" || !strings.HasPrefix(r[1], "Newest: new.example.org, 1 day ago") {
		t.Errorf("reasons = %q", r)
	}
	if r := part(s[10], Fresh).Reasons; len(r) != 1 || r[0] != "Invited 2 days ago" {
		t.Errorf("invite reasons = %q", r)
	}
	if s[9].Score >= s[4].Score {
		t.Errorf("a program without bounties (%v) should score below the same one with (%v)", s[9].Score, s[4].Score)
	}
}

func TestCompetitionRanksCrowdingWithinThePlatform(t *testing.T) {
	quiet, busy, bigBusy, private, unknown, otherPlatform := prog(1, "hackerone"), prog(2, "hackerone"), prog(3, "hackerone"), prog(4, "hackerone"), prog(5, "intigriti"), prog(6, "yeswehack")
	quiet.Reports90d, busy.Reports90d = ptr(10), ptr(300)
	bigBusy.Reports90d, bigBusy.Wildcards, bigBusy.InScope = ptr(300), 200, 210
	private.Reports90d, private.Private = ptr(300), true
	otherPlatform.Reports7d = ptr(100) // the busiest anywhere, but alone on its platform
	s := score(t, store.ScoreData{Programs: []store.ScoreProgram{quiet, busy, bigBusy, private, unknown, otherPlatform}})
	c := func(id int64) store.HuntPart { return part(s[id], Competition) }
	if !(c(1).Score > c(3).Score && c(3).Score > c(2).Score) {
		t.Errorf("want quiet > big scope with the same reports > small busy scope: %v %v %v", c(1).Score, c(3).Score, c(2).Score)
	}
	if c(4).Score != math.Min(100, c(2).Score+30) || !strings.Contains(strings.Join(c(4).Reasons, "|"), "Private: invite-only") {
		t.Errorf("private boost: %v vs %v, %q", c(4).Score, c(2).Score, c(4).Reasons)
	}
	if c(5).Known || c(5).Score != unknownScore || s[5].Confidence != "low" {
		t.Errorf("unknown volume = %+v, confidence %s", c(5), s[5].Confidence)
	}
	if c(6).Score != 50 {
		t.Errorf("alone on its platform ranks in the middle, got %v", c(6).Score)
	}
	if r := c(1).Reasons[0]; r != "10 reports in 90 days for 10 in-scope assets: quieter than 88% of HackerOne programs" {
		t.Errorf("reason = %q", r)
	}
	if r := c(2).Reasons[0]; r != "300 reports in 90 days for 10 in-scope assets: busier than 75% of HackerOne programs" {
		t.Errorf("busy reason = %q", r)
	}
}

// Bugcrowd programs that keep CrowdStream private rank by the rise in
// rewarded reports, next to the accepted reports of those that don't.
func TestCompetitionFromRewardedReports(t *testing.T) {
	listed, private, unknown := prog(1, "bugcrowd"), prog(2, "bugcrowd"), prog(3, "bugcrowd")
	listed.Accepted90d = ptr(40)
	private.Rewarded90d, private.RewardedDays = ptr(4.4), ptr(9)
	s := score(t, store.ScoreData{Programs: []store.ScoreProgram{listed, private, unknown}})
	c := func(id int64) store.HuntPart { return part(s[id], Competition) }
	if !c(2).Known || c(2).Score <= c(1).Score {
		t.Errorf("rewarded volume: known %v, score %v vs listed %v", c(2).Known, c(2).Score, c(1).Score)
	}
	if r := c(2).Reasons[0]; !strings.HasPrefix(r, "≈4 rewarded reports in 90 days (from 9 days of saved counts) for 10 in-scope assets") {
		t.Errorf("reason = %q", r)
	}
	if c(3).Known {
		t.Error("no volume at all is still unknown")
	}
	private.RewardedDays = ptr(90)
	if v, what, _ := volume(private); v != 4.4 || what != "4 rewarded reports in 90 days" {
		t.Errorf("counted: %v %q", v, what)
	}
}

func TestPayoutAndResponse(t *testing.T) {
	rich, poor, vdp, silent := prog(1, "bugcrowd"), prog(2, "bugcrowd"), prog(3, "bugcrowd"), prog(4, "intigriti")
	rich.Rewards = []platform.RewardRow{{Scope: "Tier 1", Critical: platform.RewardRange{Min: 5000, Max: 15000}}}
	poor.Rewards = []platform.RewardRow{{Scope: "Tier 1", Critical: platform.RewardRange{Max: 1000}}}
	rich.ValidationHours, poor.ValidationHours = ptr(24.0), ptr(240.0)
	vdp.OffersBounty = false
	s := score(t, store.ScoreData{Programs: []store.ScoreProgram{rich, poor, vdp, silent}})
	if p := part(s[1], Payout); p.Score != 75 || p.Reasons[0] != "Bounties up to $15k (top 25% on Bugcrowd)" {
		t.Errorf("rich payout = %+v", p)
	}
	paidMore := prog(5, "yeswehack")
	paidMore.Rewards = []platform.RewardRow{{Scope: "Asset value: high", Critical: platform.RewardRange{Max: 2500}}}
	paidMore.TopBounty = ptr(5000.0)
	if p := part(score(t, store.ScoreData{Programs: []store.ScoreProgram{paidMore}})[5], Payout); !strings.HasPrefix(p.Reasons[0], "Top bounty $5k (") {
		t.Errorf("a top bounty past the table: %q", p.Reasons)
	}
	if p := part(s[3], Payout); p.Score != 0 || !p.Known {
		t.Errorf("VDP payout = %+v", p)
	}
	if p := part(s[4], Payout); p.Known || p.Score != unknownScore {
		t.Errorf("unpublished payout = %+v", p)
	}
	if r1, r2 := part(s[1], Response), part(s[2], Response); r1.Score <= r2.Score || r1.Reasons[0] != "Most reports validated within 1d" {
		t.Errorf("response: %+v vs %+v", r1, r2)
	}
}

func TestGatesAndPresets(t *testing.T) {
	paused, closed, ignored := prog(1, "x"), prog(2, "x"), prog(4, "x")
	paused.State, closed.State = platform.StatePaused, platform.StateClosed
	ignored.Ignored, ignored.State = true, platform.StatePaused
	s := score(t, store.ScoreData{Programs: []store.ScoreProgram{paused, closed, prog(3, "x"), ignored}})
	if s[1].Gate != "paused" || s[2].Gate != "closed" || s[3].Gate != "" || s[4].Gate != "ignored" {
		t.Errorf("gates = %q %q %q %q", s[1].Gate, s[2].Gate, s[3].Gate, s[4].Gate)
	}
	for _, p := range Presets {
		var sum float64
		for _, k := range []string{Fresh, Competition, Payout, Response} {
			sum += p.Weights[k]
		}
		if math.Abs(sum-1) > 1e-9 {
			t.Errorf("preset %s weights sum to %v", p.Key, sum)
		}
	}
	if FindPreset("nope").Key != "balanced" || FindPreset("quiet").Label != "Low competition" {
		t.Error("FindPreset")
	}
	// The weighting decides between a fresh crowded program and a stale quiet one.
	freshBusy, staleQuiet := prog(1, "hackerone"), prog(2, "hackerone")
	freshBusy.Reports90d, staleQuiet.Reports90d = ptr(500), ptr(1)
	d := store.ScoreData{Programs: []store.ScoreProgram{freshBusy, staleQuiet},
		Recent: []store.ScoreEntry{{ProgramID: 1, Identifier: "*.example.com", AssetType: scope.AssetWildcard, Bounty: true, AddedAt: days(1)}}}
	winner := func(preset string) int64 {
		sc := Compute(d, now, FindPreset(preset))
		if sc[0].Detail.Score > sc[1].Detail.Score {
			return sc[0].ProgramID
		}
		return sc[1].ProgramID
	}
	if winner("fresh") != 1 || winner("quiet") != 2 {
		t.Errorf("new surface picks %d, low competition picks %d", winner("fresh"), winner("quiet"))
	}
}

func TestTargetScores(t *testing.T) {
	vdp, paused := prog(2, "x"), prog(3, "x")
	vdp.OffersBounty = false
	paused.State = platform.StatePaused
	target := func(kind, value string, shared int) store.ScoreTarget {
		return store.ScoreTarget{Kind: kind, Value: value, Apex: "example.com", Shared: shared}
	}
	entry := func(prog int64, age float64, bounty, newRoot bool, targets ...store.ScoreTarget) store.ScoreEntry {
		return store.ScoreEntry{ProgramID: prog, AssetType: scope.AssetWildcard, Bounty: bounty, AddedAt: days(age), NewRoot: newRoot, Targets: targets}
	}
	d := store.ScoreData{
		Programs: []store.ScoreProgram{prog(1, "x"), vdp, paused},
		Recent: []store.ScoreEntry{
			entry(1, 1, true, false, target(scope.KindWildcard, "*.new.example.com", 0)),
			entry(1, 30, true, false, target(scope.KindWildcard, "*.old.example.com", 0)),
			entry(1, 1, true, false, target(scope.KindURL, "https://app.example.com", 0)),
			entry(1, 1, true, false, target(scope.KindWildcard, "*.shared.example.com", 2)),
			entry(1, 1, true, true, target(scope.KindWildcard, "*.root.example.org", 0)),
			entry(1, 1, false, false, target(scope.KindWildcard, "*.nobounty.example.com", 0)),
			entry(2, 1, false, false, target(scope.KindWildcard, "*.vdp.example.com", 0)),
			entry(3, 1, true, false, target(scope.KindWildcard, "*.paused.example.com", 0)),
		},
	}
	got := map[string]store.TargetScore{}
	for _, ts := range ComputeTargets(d, Compute(d, now, Presets[0]), now) {
		got[ts.Value] = ts
	}
	s := func(v string) float64 { return got[v].Score }
	checks := []struct {
		name string
		ok   bool
	}{
		{"a new target beats a month-old one", s("*.new.example.com") > 2*s("*.old.example.com")},
		{"a wildcard beats a URL", s("*.new.example.com") > s("https://app.example.com")},
		{"a target no other program lists beats a shared one", s("*.new.example.com") > s("*.shared.example.com")},
		{"a new root domain counts extra", s("*.root.example.org") > s("*.new.example.com")},
		{"a bounty-eligible target beats one that isn't", s("*.new.example.com") > s("*.nobounty.example.com")},
		{"a program without bounties scores lower", s("*.nobounty.example.com") > s("*.vdp.example.com")},
	}
	for _, c := range checks {
		if !c.ok {
			t.Errorf("%s: %v", c.name, got)
		}
	}
	if _, ok := got["*.paused.example.com"]; ok {
		t.Error("a paused program's targets are scored")
	}
	if r := got["*.shared.example.com"].Reasons; len(r) != 4 || r[0] != "Wildcard added 1 day ago" || r[1] != "Bounty-eligible" ||
		r[2] != "Also listed by 2 other programs" || !strings.HasPrefix(r[3], "Program HuntScore ") {
		t.Errorf("reasons = %q", r)
	}
	if ts := got["*.vdp.example.com"]; ts.Bounty || ts.ProgramID != 2 || ts.Apex != "example.com" {
		t.Errorf("vdp target = %+v", ts)
	}
}
