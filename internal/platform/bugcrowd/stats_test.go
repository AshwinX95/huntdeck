package bugcrowd

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"reflect"
	"testing"
	"time"

	"github.com/mdpsec/huntdeck/internal/platform"
)

func TestFetchProgramStats(t *testing.T) {
	f := newFake(t)
	now := time.Date(2026, 10, 28, 12, 0, 0, 0, time.UTC)
	c := f.client(t, Options{Now: func() time.Time { return now }})
	var batches int
	got := map[string]platform.ProgramStats{}
	err := c.FetchProgramStats(context.Background(), []string{"acme-bb", "globex-vdp", "initech-pt", "gone"},
		func(covered []string, stats map[string]platform.ProgramStats) error {
			batches++
			if len(covered) != 4 {
				t.Errorf("covered = %v", covered)
			}
			maps.Copy(got, stats)
			return nil
		})
	if err != nil {
		t.Fatal(err)
	}
	if batches != 1 || len(got) != 2 {
		t.Fatalf("batches %d, stats %v; want acme-bb and globex-vdp (initech-pt is empty, gone is a 404)", batches, got)
	}
	acme, globex := got["acme-bb"], got["globex-vdp"]
	if acme.Currency != "USD" || *acme.RewardedReports != 643 || *acme.AvgBountyLow != 1569.04 || *acme.AvgBountyHigh != 1569.04 ||
		*acme.ValidationHours != 144 || acme.ResolvedReports != nil {
		t.Errorf("acme-bb = %+v", acme)
	}
	if *globex.ResolvedReports != 109 || globex.RewardedReports != nil || globex.AvgBountyLow != nil || globex.ValidationHours != nil {
		t.Errorf("globex-vdp = %+v", globex)
	}
	// The brief's extras: hunters joined, scope rank, announcements (an
	// empty list is "read, none"; a missing one is nil).
	want := []platform.Announcement{{ID: "a2", Title: "Bonus week", Body: "Double bounties on **api.example.com**.",
		PublishedAt: time.Date(2026, 10, 20, 10, 0, 0, 0, time.UTC)}}
	if acme.Hunters == nil || *acme.Hunters != 544 || acme.ScopeRank == nil || *acme.ScopeRank != 4 || !reflect.DeepEqual(acme.Announcements, want) {
		t.Errorf("acme-bb extras: hunters %v rank %v announcements %+v", acme.Hunters, acme.ScopeRank, acme.Announcements)
	}
	if k, u := acme.KnownIssues, acme.KnownIssuesUnique; k == nil || *k != 12 || u == nil || *u != 9 || globex.KnownIssues != nil {
		t.Errorf("known issues: acme-bb %v/%v, globex-vdp %v; want 12/9 and none", k, u, globex.KnownIssues)
	}
	if globex.Hunters != nil || globex.ScopeRank != nil || globex.Announcements == nil || len(globex.Announcements) != 0 {
		t.Errorf("globex-vdp extras: hunters %v rank %v announcements %#v", globex.Hunters, globex.ScopeRank, globex.Announcements)
	}
	if acme.HackersThanked == nil || *acme.HackersThanked != 578 || globex.HackersThanked != nil {
		t.Errorf("hall of fame: acme-bb %v, globex-vdp %v", acme.HackersThanked, globex.HackersThanked)
	}

	// After a listing, the scope rank comes from it (acme-bb's is 2 there),
	// without a request.
	l, err := c.ListPrograms(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range l.Programs {
		if p.Handle == "acme-bb" && (p.TriageService != "Platform" || p.Industry != "Finance") {
			t.Errorf("acme-bb listed with triage %q, industry %q", p.TriageService, p.Industry)
		}
	}
	before := f.hit("/engagements/acme-bb/scope_ranks")
	got = map[string]platform.ProgramStats{}
	if err := c.FetchProgramStats(context.Background(), []string{"acme-bb"}, func(_ []string, s map[string]platform.ProgramStats) error {
		maps.Copy(got, s)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if r := got["acme-bb"].ScopeRank; r == nil || *r != 2 || f.hit("/engagements/acme-bb/scope_ranks") != before {
		t.Errorf("rank after a listing = %v, scope_ranks asked %d more times", r, f.hit("/engagements/acme-bb/scope_ranks")-before)
	}

	// Hunters joined and the hall of fame are reused for a week, then read
	// again.
	refetch := func(h string) platform.ProgramStats {
		t.Helper()
		var s platform.ProgramStats
		if err := c.FetchProgramStats(context.Background(), []string{h}, func(_ []string, m map[string]platform.ProgramStats) error {
			s = m[h]
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		return s
	}
	asked := func() string {
		return fmt.Sprintf("joined %d, hall of fame %d",
			f.hit("/engagements/acme-bb/recently_joined_users.json"), f.hit("/engagements/acme-bb/hall_of_fames.json"))
	}
	if got := asked(); got != "joined 1, hall of fame 1" {
		t.Fatalf("after two passes: %s; want each read once", got)
	}
	now = now.Add(6 * 24 * time.Hour)
	s := refetch("acme-bb")
	if got := asked(); got != "joined 1, hall of fame 1" || s.Hunters == nil || *s.Hunters != 544 ||
		s.HackersThanked == nil || *s.HackersThanked != 578 {
		t.Errorf("within a week: %s, hunters %v, thanked %v; want no requests and the last counts", got, s.Hunters, s.HackersThanked)
	}
	now = now.Add(2 * 24 * time.Hour)
	refetch("acme-bb")
	if got := asked(); got != "joined 2, hall of fame 2" {
		t.Errorf("after a week: %s; want acme-bb read again", got)
	}
}

func TestFetchProgramStatsStopsWhenBlocked(t *testing.T) {
	f := newFake(t)
	f.blockAll = true
	c := f.client(t, Options{})
	err := c.FetchProgramStats(context.Background(), []string{"acme-bb", "globex-vdp"},
		func([]string, map[string]platform.ProgramStats) error { t.Error("saved a batch"); return nil })
	if !errors.Is(err, ErrBlocked) || f.hit("/engagements/globex-vdp/statistics") != 0 {
		t.Errorf("err = %v; want ErrBlocked after one request", err)
	}
}

func TestStatsParsing(t *testing.T) {
	for _, tc := range []struct {
		in    string
		hours float64
		ok    bool
	}{{"6 days", 144, true}, {"1 day", 24, true}, {"12 hours", 12, true}, {"2 weeks", 336, true}, {"soon", 0, false}, {"3 fortnights", 0, false}} {
		h, err := hours(tc.in)
		if (err == nil) != tc.ok || h != tc.hours {
			t.Errorf("hours(%q) = %v, %v", tc.in, h, err)
		}
	}
	for _, tc := range []struct {
		in, cur string
		v       float64
	}{{"$569.04", "USD", 569.04}, {"€1,250", "EUR", 1250}, {"£50", "GBP", 50}, {"Points", "", 0}, {"$", "", 0}} {
		v, cur, err := money(tc.in)
		if cur != tc.cur || v != tc.v || (err == nil) != (tc.cur != "") {
			t.Errorf("money(%q) = %v %q %v", tc.in, v, cur, err)
		}
	}
}

// A bug bounty the listing shows without rewards pays if its stats publish
// an average payout; otherwise it doesn't.
func TestUnpricedBugBountyPaysByAveragePayout(t *testing.T) {
	f := newFake(t)
	c := f.client(t, Options{})
	raw := []byte(`{"name":"Acme","briefUrl":"/engagements/acme-bb","rewardSummary":null,"isDemo":false,"accessStatus":"open",
		"productEngagementType":{"label":"Bug Bounty","iconVariant":"bug-bounty"}}`)
	p, ok, err := c.parseEngagement(raw)
	if err != nil || !ok || !p.BountyUnknown || p.OffersBounty {
		t.Fatalf("parse = %+v, %v, %v; want bounty unknown", p, ok, err)
	}
	vdp, _, _ := c.parseEngagement([]byte(`{"name":"Globex","briefUrl":"/engagements/globex-vdp","rewardSummary":null,
		"productEngagementType":{"label":"Vulnerability Disclosure","iconVariant":"vdp"}}`))
	if vdp.BountyUnknown || vdp.OffersBounty {
		t.Errorf("a VDP without rewards = %+v; want known not to pay", vdp)
	}

	c.rankMu.Lock()
	c.unpriced, c.ranks, c.ranksAt = map[string]bool{"acme-bb": true, "initech-pt": true}, map[string]int{}, testNow
	c.rankMu.Unlock()
	got := map[string]platform.ProgramStats{}
	err = c.FetchProgramStats(context.Background(), []string{"acme-bb", "initech-pt", "globex-vdp"},
		func(_ []string, stats map[string]platform.ProgramStats) error { maps.Copy(got, stats); return nil })
	if err != nil {
		t.Fatal(err)
	}
	pays := func(h string) *bool {
		if f := got[h].Facts; f != nil {
			return f.OffersBounty
		}
		return nil
	}
	if b := pays("acme-bb"); b == nil || !*b {
		t.Errorf("acme-bb (average payout $1,569.04) pays = %v; want true", b)
	}
	if b := pays("initech-pt"); b == nil || *b {
		t.Errorf("initech-pt (no average payout) pays = %v; want false", b)
	}
	if b := pays("globex-vdp"); b != nil {
		t.Errorf("globex-vdp (listed with its answer) pays = %v; want left alone", *b)
	}
}
