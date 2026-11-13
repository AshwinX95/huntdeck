package store

import (
	"context"
	"reflect"
	"testing"
	"time"

	"github.com/mdpsec/huntdeck/internal/platform"
)

// Open programs and assets in scope at each month's end are worked back
// from today through status changes, removals, launches and when each
// asset was added or removed.
func TestOverviewOpenAndAssetsByMonth(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 11, 5, 12, 0, 0, 0, time.UTC)
	s := newTestStore(t, func() time.Time { return now })
	ids := map[string]int64{}
	err := s.Write(ctx, func(tx *Tx) error {
		for _, h := range []string{"steady", "paused", "reopened", "launched", "gone", "added"} {
			id, err := tx.UpsertProgram(ctx, "fake", platform.Program{Handle: h, Name: h, URL: "https://example.com/" + h, State: "open"})
			if err != nil {
				return err
			}
			ids[h] = id
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := s.db.W.ExecContext(ctx, q, args...); err != nil {
			t.Fatal(err)
		}
	}
	// paused: open until mid-September. reopened: paused until mid-October.
	exec(`UPDATE programs SET state = 'paused' WHERE id = ?`, ids["paused"])
	exec(`INSERT INTO changes (occurred_at, program_id, kind, old_value, new_value) VALUES ('2026-09-15T00:00:00.000Z', ?, 'state_changed', 'open', 'paused')`, ids["paused"])
	exec(`INSERT INTO changes (occurred_at, program_id, kind, old_value, new_value) VALUES ('2026-10-15T00:00:00.000Z', ?, 'state_changed', 'paused', 'open')`, ids["reopened"])
	// launched in October; gone removed in October; added first seen in October.
	exec(`UPDATE programs SET started_at = '2026-10-10T00:00:00.000Z' WHERE id = ?`, ids["launched"])
	exec(`UPDATE programs SET removed_at = '2026-10-20T00:00:00.000Z' WHERE id = ?`, ids["gone"])
	exec(`INSERT INTO changes (occurred_at, program_id, kind) VALUES ('2026-10-03T00:00:00.000Z', ?, 'program_added')`, ids["added"])
	// All pay now but steady, which stopped in October; reopened started
	// paying in August.
	exec(`UPDATE programs SET offers_bounty = 1 WHERE id != ?`, ids["steady"])
	exec(`INSERT INTO changes (occurred_at, program_id, kind, field, old_value, new_value) VALUES ('2026-10-20T00:00:00.000Z', ?, 'bounty_changed', 'offers_bounty', 'true', 'false')`, ids["steady"])
	exec(`INSERT INTO changes (occurred_at, program_id, kind, field, old_value, new_value) VALUES ('2026-08-10T00:00:00.000Z', ?, 'bounty_changed', 'offers_bounty', 'false', 'true')`, ids["reopened"])
	// steady's assets: one there all along (bound), one added in October,
	// one removed in October, one out of scope.
	for i, e := range []struct {
		in      int
		added   any
		bound   int
		removed any
	}{{1, "2026-08-01T00:00:00.000Z", 1, nil}, {1, "2026-10-02T00:00:00.000Z", 0, nil}, {1, "2026-05-01T00:00:00.000Z", 0, "2026-10-25T00:00:00.000Z"}, {0, nil, 0, nil}} {
		exec(`INSERT INTO scope_entries (program_id, entry_key, in_scope, asset_type, platform_asset_type, identifier, bounty_eligible,
			content_hash, first_seen_at, updated_at, added_at, added_bound, removed_at) VALUES (?, ?, ?, 'url', 'URL', 'example.com', 1, 'h', '2026-01-01T00:00:00.000Z', '2026-01-01T00:00:00.000Z', ?, ?, ?)`,
			ids["steady"], i, e.in, e.added, e.bound, e.removed)
	}
	exec(`UPDATE scope_entries SET asset_type = 'wildcard' WHERE entry_key = '1'`) // the October one
	// Wildcards are the targets entries expand to, once per program and
	// value, whatever the entry's type: the one there all along lists
	// *.example.org, the October one that and *.example.net, the one
	// removed in October three; the out-of-scope one's doesn't count.
	for key, values := range map[string][]string{"0": {"*.example.org"}, "1": {"*.example.net", "*.example.org"},
		"2": {"*.example.com", "*.example.info", "*.example.org"}, "3": {"*.example.edu"}} {
		for _, v := range values {
			exec(`INSERT INTO scope_targets (scope_entry_id, program_id, kind, value, host, apex)
				SELECT id, program_id, 'wildcard', ?, '', '' FROM scope_entries WHERE entry_key = ?`, v, key)
		}
	}
	// Two reports disclosed in October; gone's leaves with it.
	for i, p := range []string{"steady", "steady", "gone"} {
		exec(`INSERT INTO disclosed_reports (program_id, report_id, title, disclosed_at, url, first_seen_at, updated_at)
			VALUES (?, ?, 'x', '2026-10-09T00:00:00.000Z', 'https://example.com/r', ?, ?)`, ids[p], i, now.Format(TimeFormat), now.Format(TimeFormat))
	}

	start := time.Date(2025, 12, 1, 0, 0, 0, 0, time.UTC)
	got, err := s.PlatformOverviews(ctx, now, start, 12, "", "")
	if err != nil {
		t.Fatal(err)
	}
	// Every program here is public: public alone is the same, private
	// alone has nothing.
	if pub, err := s.PlatformOverviews(ctx, now, start, 12, "", "public"); err != nil || !reflect.DeepEqual(pub, got) {
		t.Errorf("public only differs: %v", err)
	}
	if priv, err := s.PlatformOverviews(ctx, now, start, 12, "", "private"); err != nil || len(priv) != 0 {
		t.Errorf("private only: %d platforms, %v", len(priv), err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d platforms", len(got))
	}
	m := got[0].Months
	tests := []struct {
		month                int // 0 = December 2025
		open, paying, assets int
	}{
		{7, 3, 3, 2},  // July: steady, paused, gone, all paying; not yet reopened, launched or added. Bound + removed-later asset.
		{8, 3, 3, 2},  // August
		{9, 2, 2, 2},  // September: paused has paused; steady and gone both pay
		{10, 4, 3, 2}, // October: steady (no longer paying), reopened, launched, added; gone removed. The October asset in, the removed one out.
		{11, 4, 3, 2}, // now
	}
	for _, tt := range tests {
		if got := m[tt.month]; got.Open != tt.open || got.Paying != tt.paying || got.Assets != tt.assets {
			t.Errorf("month %d: open %d, paying %d, assets %d; want %d, %d, %d", tt.month, got.Open, got.Paying, got.Assets, tt.open, tt.paying, tt.assets)
		}
	}
	for i, mo := range m {
		wild, scoped, disclosed := 1, 0, 0 // *.example.org, all along
		switch {
		case i >= 10: // *.example.net added in October; the removed entry's gone
			wild = 2
		case i >= 4: // the removed entry's *.example.com and *.example.info, added as April ended
			wild = 3
		}
		switch i {
		case 5: // May: the asset removed later
			scoped = 1
		case 10:
			scoped, disclosed = 1, 2
		}
		if mo.Wildcards != wild || mo.ScopePrograms != scoped || mo.Disclosed != disclosed {
			t.Errorf("month %d: %d wildcards, %d programs with new scope, %d disclosed; want %d, %d, %d",
				i, mo.Wildcards, mo.ScopePrograms, mo.Disclosed, wild, scoped, disclosed)
		}
	}
	if m[11].Open != got[0].Open || m[11].Paying != got[0].Paying {
		t.Errorf("now: %d open, %d paying; the headline says %d, %d", m[11].Open, m[11].Paying, got[0].Open, got[0].Paying)
	}
}

// Where a platform publishes no paid total, each day's is its payable
// accepted reports in the 90 days to that day times its average payout,
// from the first day its reports cover the whole 90 days.
func TestEstimatedPaidDays(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 11, 5, 12, 0, 0, 0, time.UTC)
	s := newTestStore(t, func() time.Time { return now })
	var id, other, free int64
	err := s.Write(ctx, func(tx *Tx) error {
		var err error
		if id, err = tx.UpsertProgram(ctx, "fake", platform.Program{Handle: "acme", Name: "acme", URL: "https://example.com/acme", State: "open"}); err != nil {
			return err
		}
		if free, err = tx.UpsertProgram(ctx, "fake", platform.Program{Handle: "free", Name: "free", URL: "https://example.com/free", State: "open"}); err != nil {
			return err
		}
		other, err = tx.UpsertProgram(ctx, "fake", platform.Program{Handle: "euro", Name: "euro", URL: "https://example.com/euro", State: "open"})
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := s.db.W.ExecContext(ctx, q, args...); err != nil {
			t.Fatal(err)
		}
	}
	const at = "2026-11-05T00:00:00.000Z"
	// EUR programs are left out, as on the line, and so are programs that
	// don't pay bounties now, as Paid · 90 days leaves them out.
	for _, p := range []struct {
		id       int64
		currency string
		pays     bool
	}{{id, "USD", true}, {other, "EUR", true}, {free, "USD", false}} {
		exec(`UPDATE programs SET accepted_since = '2026-07-01T00:00:00.000Z', stats_at = ?, offers_bounty = ? WHERE id = ?`, at, p.pays, p.id)
		exec(`INSERT INTO program_public_stats (program_id, captured_at, currency, avg_bounty_high) VALUES (?, ?, ?, 100)`, p.id, at, p.currency)
	}
	for i, r := range []struct {
		day      string
		priority any
	}{{"2026-07-15", 2}, {"2026-08-20", nil}, {"2026-09-30", 5}, {"2026-11-01", 3}} {
		for _, p := range []int64{id, free} {
			exec(`INSERT INTO accepted_reports (program_id, report_id, accepted_at, priority, first_seen_at, updated_at) VALUES (?, ?, ?, ?, ?, ?)`,
				p, i, r.day+"T00:00:00.000Z", r.priority, at, at)
		}
	}

	got, err := s.PlatformDays(ctx, DayPaid90d, time.Date(2026, 9, 27, 0, 0, 0, 0, time.UTC), "", "")
	if err != nil {
		t.Fatal(err)
	}
	byDay := map[string]float64{}
	for _, d := range got["fake"] {
		if d.ProgramID != id {
			t.Fatalf("program %d has days; only USD programs that pay should", d.ProgramID)
		}
		byDay[d.Day.Format(time.DateOnly)] = d.Value
	}
	tests := []struct {
		day  string
		want float64
		ok   bool
	}{
		{"2026-09-27", 0, false},  // 90 days back is before July 1
		{"2026-09-28", 200, true}, // Jul 1 – Sep 28: Jul 15 and Aug 20
		{"2026-10-12", 200, true}, // to Oct 12's end: Jul 15 and Aug 20; Sep 30 is priority 5
		{"2026-10-13", 100, true}, // Jul 15 has gone
		{"2026-11-05", 200, true}, // today, to now: Aug 20 and Nov 1
	}
	for _, tt := range tests {
		v, ok := byDay[tt.day]
		if ok != tt.ok || v != tt.want {
			t.Errorf("%s: %v (%v); want %v (%v)", tt.day, v, ok, tt.want, tt.ok)
		}
	}
}

// YesWeHack gives no launch date, so a program's first version stands in;
// where a platform publishes only a running total of accepted reports
// (Intigriti), its rises are the accepted reports, from the first day saved.
func TestOverviewLaunchesAndAcceptedTotals(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 11, 5, 12, 0, 0, 0, time.UTC)
	s := newTestStore(t, func() time.Time { return now })
	ids := map[string]int64{}
	err := s.Write(ctx, func(tx *Tx) error {
		for _, pp := range [][2]string{{"yeswehack", "new"}, {"yeswehack", "old"}, {"intigriti", "acme"}} {
			id, err := tx.UpsertProgram(ctx, pp[0], platform.Program{Handle: pp[1], Name: pp[1], URL: "https://example.com/" + pp[1], State: "open"})
			if err != nil {
				return err
			}
			ids[pp[1]] = id
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := s.db.W.ExecContext(ctx, q, args...); err != nil {
			t.Fatal(err)
		}
	}
	entry := func(id int64, key, added string, bound int) {
		exec(`INSERT INTO scope_entries (program_id, entry_key, in_scope, asset_type, platform_asset_type, identifier, bounty_eligible,
			content_hash, first_seen_at, updated_at, added_at, added_bound) VALUES (?, ?, 1, 'url', 'URL', 'example.com', 1, 'h', ?, ?, ?, ?)`,
			id, key, added, added, added, bound)
	}
	entry(ids["new"], "a", "2026-09-10T00:00:00.000Z", 1) // first version in September
	entry(ids["new"], "b", "2026-10-01T00:00:00.000Z", 0) // added later: not the launch
	entry(ids["old"], "a", "2019-03-01T00:00:00.000Z", 1)
	for _, snap := range []struct {
		at string
		n  int
	}{{"2026-10-28T10:00:00.000Z", 90}, {"2026-10-30T10:00:00.000Z", 94}, {"2026-11-02T10:00:00.000Z", 93}, {"2026-11-03T10:00:00.000Z", 99}} {
		exec(`INSERT INTO program_public_stats (program_id, captured_at, accepted_reports) VALUES (?, ?, ?)`, ids["acme"], snap.at, snap.n)
	}

	start := time.Date(2025, 12, 1, 0, 0, 0, 0, time.UTC)
	got, err := s.PlatformOverviews(ctx, now, start, 12, "", "")
	if err != nil {
		t.Fatal(err)
	}
	by := map[string]PlatformOverview{}
	for _, o := range got {
		by[o.Platform] = o
	}
	ywh := by["yeswehack"]
	if !ywh.Launches || ywh.Months[9].Launched != 1 || ywh.Months[10].Launched != 0 {
		t.Errorf("yeswehack launches %v, September %d, October %d; want true, 1, 0", ywh.Launches, ywh.Months[9].Launched, ywh.Months[10].Launched)
	}
	if ywh.Months[8].Open != 1 || ywh.Months[9].Open != 2 {
		t.Errorf("yeswehack open in August %d, September %d; want 1, 2 (new launched in September)", ywh.Months[8].Open, ywh.Months[9].Open)
	}
	igt := by["intigriti"]
	if igt.ValidKind != "accepted" || igt.ValidSince == nil || !igt.ValidSince.Equal(time.Date(2026, 10, 28, 10, 0, 0, 0, time.UTC)) {
		t.Errorf("intigriti valid %q since %v; want accepted since Oct 28", igt.ValidKind, igt.ValidSince)
	}
	// October: 90 → 94. November: 94 → 93 (a fall counts as none), 93 → 99.
	if igt.Months[10].Valid != 4 || igt.Months[11].Valid != 6 {
		t.Errorf("intigriti accepted October %d, November %d; want 4, 6", igt.Months[10].Valid, igt.Months[11].Valid)
	}
}

// A platform counting valid reports from saved totals since Nov 5 and
// saving reports received this month has October and the start of November
// estimated from reports received times the program's valid share.
func TestOverviewEstimatedValid(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 11, 7, 12, 0, 0, 0, time.UTC)
	s := newTestStore(t, func() time.Time { return now })
	var id int64
	err := s.Write(ctx, func(tx *Tx) error {
		var err error
		id, err = tx.UpsertProgram(ctx, "yeswehack", platform.Program{Handle: "acme", Name: "acme", URL: "https://example.com/acme", State: "open"})
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, snap := range []struct {
		at           string
		month, total int
		accepted     any
	}{
		{"2026-10-20T06:00:00.000Z", 30, 380, nil}, // too early in October to stand for it
		{"2026-10-31T06:00:00.000Z", 40, 390, nil}, // October: 40 × ¼
		{"2026-11-03T06:00:00.000Z", 6, 396, nil},
		{"2026-11-05T21:00:00.000Z", 8, 398, 100}, // counting starts: Nov 1–5 is 8 × ¼
		{"2026-11-06T06:00:00.000Z", 9, 399, 101},
		{"2026-11-07T06:00:00.000Z", 12, 400, 103}, // 3 counted; the share is 103/400
	} {
		if _, err := s.db.W.ExecContext(ctx, `INSERT INTO program_public_stats (program_id, captured_at, reports_month, total_reports, accepted_reports)
			VALUES (?, ?, ?, ?, ?)`, id, snap.at, snap.month, snap.total, snap.accepted); err != nil {
			t.Fatal(err)
		}
	}
	start := time.Date(2025, 12, 1, 0, 0, 0, 0, time.UTC)
	got, err := s.PlatformOverviews(ctx, now, start, 12, "yeswehack", "")
	if err != nil || len(got) != 1 {
		t.Fatalf("overviews %v, %v", got, err)
	}
	o := got[0]
	// October 40 × .2575 = 10.3; November 8 × .2575 = 2.06 plus 3 counted.
	if o.Months[10].Valid != 10 || !o.Months[10].Estimated || o.Months[11].Valid != 5 || !o.Months[11].Estimated || o.Months[9].Estimated {
		t.Errorf("September %+v, October %+v, November %+v; want ≈10 and ≈5", o.Months[9], o.Months[10], o.Months[11])
	}
	if o.ValidSince == nil || !o.ValidSince.Equal(time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)) {
		t.Errorf("valid since %v; want Oct 1", o.ValidSince)
	}
}
