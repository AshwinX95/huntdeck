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

func TestDisclosedReportsAreKeptPerProgram(t *testing.T) {
	e := newEnv(t)
	e.seed()
	start := e.clock
	day := func(n int) time.Time { return start.Add(time.Duration(-n) * 24 * time.Hour) }
	e.fake.SetDisclosures(
		disclosure("1", "acme", "high", "XSS", day(1), 500),
		disclosure("2", "acme", "high", "XSS", day(40), 0),
		disclosure("3", "acme", "critical", "SSRF", day(90), 5000),
		disclosure("4", "acme", "low", "XSS", day(200), 0),
		disclosure("5", "acme", "", "", day(400), 0),
		disclosure("6", "globex", "medium", "IDOR", day(3), 250),
		disclosure("7", "stranger", "high", "XSS", day(2), 0), // not a known program
	)
	run := e.poll(ModeFull, 0)
	if run.DisclosuresNew != 6 || run.Status != store.RunOK {
		t.Fatalf("first poll: new=%d status=%s err=%s", run.DisclosuresNew, run.Status, run.Error)
	}
	asks := e.fake.DisclosureRequests()
	if got := strings.Join(asks, ","); got != "*,*,acme,acme,acme,globex" {
		t.Errorf("requests = %s; want two feed pages then each program's history", got)
	}

	ctx := context.Background()
	acme, err := e.store.Program(ctx, "fake", "acme")
	if err != nil {
		t.Fatal(err)
	}
	list, sum, err := e.store.ProgramDisclosures(ctx, acme.ID, 3)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 3 || list[0].ReportID != "1" || list[0].URL != "https://platform.example/reports/1" || list[0].Reporter != "hunter1" {
		t.Errorf("newest three: %+v", list)
	}
	got := fmt.Sprintf("total=%d sev=%v weak=%v paid=%d bounties=%.0f latest=%v", sum.Total, sum.BySeverity, sum.Weaknesses,
		sum.Paid, sum.Bounties, sum.Latest.Equal(day(1)))
	want := "total=5 sev=[{critical 1} {high 2} {low 1} { 1}] weak=[{XSS 3} {SSRF 1}] paid=2 bounties=5500 latest=true"
	if got != want {
		t.Errorf("summary:\n got %s\nwant %s", got, want)
	}
	progs, _, err := e.store.Programs(ctx, store.ProgramFilter{Sort: "disclosed"})
	if err != nil || len(progs) != 2 || progs[0].Handle != "acme" || progs[0].Disclosed != 5 || progs[1].Disclosed != 1 {
		t.Errorf("programs by disclosed: %+v %v", progs, err)
	}

	// Later polls read only the feed, back to where the last one stopped.
	e.fake.SetDisclosures(append([]platform.Disclosure{disclosure("8", "globex", "high", "IDOR", e.clock, 0)},
		disclosure("1", "acme", "high", "XSS", day(1), 500), disclosure("6", "globex", "medium", "IDOR", day(3), 250))...)
	before := len(e.fake.DisclosureRequests())
	run = e.poll(ModeQuick, 0)
	if run.DisclosuresNew != 1 {
		t.Errorf("quick poll: new=%d, want 1", run.DisclosuresNew)
	}
	if got := e.fake.DisclosureRequests()[before:]; strings.Join(got, ",") != "*,*" {
		t.Errorf("quick poll requests = %v; want the feed only", got)
	}
	g, _ := e.store.Program(ctx, "fake", "globex")
	if _, sum, _ := e.store.ProgramDisclosures(ctx, g.ID, 10); sum.Total != 2 {
		t.Errorf("globex has %d disclosures, want 2", sum.Total)
	}
}

func TestDisclosureFailureDoesNotFailThePoll(t *testing.T) {
	e := newEnv(t)
	e.seed()
	e.fake.FailDisclosures(errors.New("hacktivity down"))
	run := e.poll(ModeFull, 0)
	if run.Status != store.RunOK || !strings.Contains(run.Error, "hacktivity down") {
		t.Errorf("status=%s error=%q; want ok with the error reported", run.Status, run.Error)
	}
	// Nothing was marked as having its history, so the next full poll retries.
	e.fake.FailDisclosures(nil)
	e.fake.SetDisclosures(disclosure("1", "acme", "low", "XSS", e.clock, 0))
	if run = e.poll(ModeFull, 0); run.DisclosuresNew != 1 {
		t.Errorf("retry: new=%d, want 1", run.DisclosuresNew)
	}
}

func TestResolutionsKeepEarliestDateAndBackfillOnce(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	e.seed()
	day := func(n int) time.Time { return e.clock.Add(time.Duration(n) * 24 * time.Hour) }
	sub := func(n int) *time.Time { v := day(n); return &v }
	e.fake.SetResolutions(
		platform.Resolution{ID: "11", ProgramHandle: "acme", SubmittedAt: sub(-30), ActivityAt: day(-10), Action: platform.ActionResolved},
		platform.Resolution{ID: "12", ProgramHandle: "acme", SubmittedAt: sub(-200), ActivityAt: day(-100), Action: platform.ActionResolved},
		platform.Resolution{ID: "13", ProgramHandle: "globex", SubmittedAt: sub(-9), ActivityAt: day(-5), Action: platform.ActionBounty, Bounty: ptr(500.0)},
		platform.Resolution{ID: "14", ProgramHandle: "unknown", ActivityAt: day(-1), Action: platform.ActionResolved},
	)
	run := e.poll(ModeFull, 0)
	if run.ResolutionsNew != 3 {
		t.Errorf("first full poll stored %d resolutions, want 3 (the unknown program's skipped)", run.ResolutionsNew)
	}
	asks := strings.Join(e.fake.ResolutionRequests(), ",")
	if asks != "*,*,acme,acme,globex" {
		t.Errorf("requests = %s; want two feed pages, then each program's history (acme fills a page)", asks)
	}

	// Report 11 later gets a bounty: its resolution date stays, the bounty is added.
	e.clock = day(1)
	e.fake.SetResolutions(platform.Resolution{ID: "11", ProgramHandle: "acme", SubmittedAt: sub(-31), ActivityAt: day(-1), Action: platform.ActionBounty, Bounty: ptr(1000.0)})
	run = e.poll(ModeFull, 0)
	if run.ResolutionsNew != 0 {
		t.Errorf("second poll stored %d new", run.ResolutionsNew)
	}
	if got := strings.Join(e.fake.ResolutionRequests(), ","); got != asks+",*" {
		t.Errorf("requests after the second poll = %s; history is fetched once", got)
	}
	acme, err := e.store.Program(ctx, "fake", "acme")
	if err != nil {
		t.Fatal(err)
	}
	sum, err := e.store.ProgramResolutions(ctx, acme.ID, e.clock, 12)
	if err != nil {
		t.Fatal(err)
	}
	// 11: submitted day -30, resolved day -10 → 20 days, in the last 90; 12 falls in the 90 before.
	if sum.Total != 2 || sum.Last90d != 1 || sum.Prev90d != 1 || sum.Median90d == nil || *sum.Median90d != 20 ||
		sum.MedianPrev == nil || *sum.MedianPrev != 100 || sum.Bounties90d != 1000 || sum.WithBounty != 1 || sum.HistoryFetch == nil {
		t.Errorf("summary = %+v", sum)
	}
	var months, submitted int
	for _, m := range sum.Months {
		months += m.Resolved
		submitted += m.Submitted
	}
	if len(sum.Months) != 12 || months != 2 || submitted != 2 {
		t.Errorf("months = %+v", sum.Months)
	}
}

func TestAcceptedReportsBackfillThenWeeklyRefresh(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	e.seed()
	start := e.clock
	day := func(n int) time.Time { return start.Add(time.Duration(n) * 24 * time.Hour) }
	at := func(n int) *time.Time { v := day(n); return &v }
	since := day(-182)
	poll := func(mode string) store.Run {
		t.Helper()
		e.clock = e.clock.Add(time.Minute)
		run, err := e.sync.Poll(ctx, fake.Accepting{Platform: e.fake}, Options{Mode: mode, Trigger: "manual"})
		if err != nil {
			t.Fatalf("Poll: %v", err)
		}
		return run
	}
	reports := []platform.AcceptedReport{
		{ID: "a1", ProgramHandle: "acme", SubmittedAt: at(-6), AcceptedAt: day(-2), Priority: 3},
		{ID: "a2", ProgramHandle: "acme", SubmittedAt: at(-45), AcceptedAt: day(-40), ResolvedAt: at(-5), Priority: 1, Bounty: ptr(900.0)},
		{ID: "a3", ProgramHandle: "globex", AcceptedAt: day(-100), Priority: 4},
		{ID: "a4", ProgramHandle: "unknown", AcceptedAt: day(-1)},
	}
	e.fake.SetAccepted(since, reports...)
	run := poll(ModeFull)
	if run.AcceptedNew != 3 {
		t.Errorf("first full poll stored %d accepted reports, want 3 (the unknown program's skipped)", run.AcceptedNew)
	}
	asks := strings.Join(e.fake.AcceptedRequests(), ",")
	if asks != "*,*,*,acme,acme,globex" {
		t.Errorf("requests = %s; want the feed, then each program's own", asks)
	}
	acme, err := e.store.Program(ctx, "fake", "acme")
	if err != nil {
		t.Fatal(err)
	}
	globex, err := e.store.Program(ctx, "fake", "globex")
	if err != nil {
		t.Fatal(err)
	}
	openOf := func(id int64) int {
		t.Helper()
		sum, err := e.store.ProgramAccepted(ctx, id, e.clock, 12)
		if err != nil {
			t.Fatal(err)
		}
		return sum.Open
	}

	// The next day a1 (on the feed's first page) and a3 (further down) are
	// resolved. The first page has nothing new, so only a1's resolution is
	// seen; the programs were read less than a week ago.
	e.clock = day(1)
	reports[0].ResolvedAt, reports[2].ResolvedAt = at(1), at(1)
	e.fake.SetAccepted(since, reports...)
	if run = poll(ModeFull); run.AcceptedNew != 0 {
		t.Errorf("second poll stored %d new", run.AcceptedNew)
	}
	if got := strings.Join(e.fake.AcceptedRequests(), ","); got != asks+",*" {
		t.Errorf("requests after the second poll = %s; want one feed page", got)
	}
	if a, g := openOf(acme.ID), openOf(globex.ID); a != 0 || g != 1 {
		t.Errorf("open reports: acme %d, globex %d; want 0 and 1", a, g)
	}
	sum, err := e.store.ProgramAccepted(ctx, acme.ID, e.clock, 12)
	if err != nil {
		t.Fatal(err)
	}
	if sum.Total != 2 || sum.Since == nil || !sum.Since.Equal(since) || sum.Last90d != 2 ||
		sum.Resolved90d != 2 || sum.Bounties90d != 900 || !sum.PrevKnown {
		t.Errorf("summary = %+v", sum)
	}
	// a1: 7 days from submission to resolution; a2: 40.
	if sum.MedianResolve90d == nil || *sum.MedianResolve90d != 23.5 {
		t.Errorf("median days to resolve = %v, want 23.5", sum.MedianResolve90d)
	}
	var accepted, prio1 int
	for _, m := range sum.Months {
		accepted += m.Accepted
		prio1 += m.ByPriority[1]
	}
	if accepted != 2 || prio1 != 1 {
		t.Errorf("months: %d accepted, %d P1; want 2 and 1", accepted, prio1)
	}

	// A week later quick polls still don't re-read programs; a full poll does.
	e.clock = day(9)
	poll(ModeQuick)
	if got := strings.Join(e.fake.AcceptedRequests(), ","); got != asks+",*,*" {
		t.Errorf("requests after a quick poll = %s", got)
	}
	poll(ModeFull)
	if g := openOf(globex.ID); g != 0 {
		t.Errorf("after the weekly re-read globex has %d open reports, want 0", g)
	}
}

// Reports the all-programs feed lists mark their programs as listing them
// (from the earliest one's day) before their own feeds are read, so a
// program's counts and its platform's agree; a program it never listed
// stays kept private.
func TestAcceptedFeedMarksProgramsListing(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	e.seed()
	day := func(n int) time.Time {
		return e.clock.Add(time.Duration(n) * 24 * time.Hour).UTC().Truncate(24 * time.Hour)
	}
	e.fake.SetAccepted(day(-182),
		platform.AcceptedReport{ID: "a1", ProgramHandle: "acme", AcceptedAt: day(-2)},
		platform.AcceptedReport{ID: "a2", ProgramHandle: "acme", AcceptedAt: day(-5)})
	e.clock = e.clock.Add(time.Minute)
	if _, err := e.sync.Poll(ctx, fake.Accepting{Platform: e.fake}, Options{Mode: ModeQuick, Trigger: "manual"}); err != nil {
		t.Fatalf("Poll: %v", err)
	}
	for _, tt := range []struct {
		handle string
		want   *time.Time
	}{{"acme", ptr(day(-5))}, {"globex", nil}} {
		p, err := e.store.Program(ctx, "fake", tt.handle)
		if err != nil {
			t.Fatal(err)
		}
		sum, err := e.store.ProgramAccepted(ctx, p.ID, e.clock, 12)
		if err != nil {
			t.Fatal(err)
		}
		if (tt.want == nil) != (sum.Since == nil) || (tt.want != nil && !sum.Since.Equal(*tt.want)) {
			t.Errorf("%s: listing since %v; want %v", tt.handle, sum.Since, tt.want)
		}
	}
}
