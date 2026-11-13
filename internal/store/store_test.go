package store

import (
	"context"
	"log/slog"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/mdpsec/huntdeck/internal/db"
	"github.com/mdpsec/huntdeck/internal/platform"
	"github.com/mdpsec/huntdeck/internal/scope"
)

func newTestStore(t *testing.T, now func() time.Time) *Store {
	t.Helper()
	ctx := context.Background()
	d, err := db.Open(ctx, filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = d.Close() })
	migs, err := db.Migrations()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := d.Migrate(ctx, migs, t.TempDir(), slog.New(slog.DiscardHandler)); err != nil {
		t.Fatal(err)
	}
	return New(d, now)
}

// Targets carry their entry's program, which root-domain lists seek on.
func TestTargetsRecordTheirProgram(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t, nil)
	entry := platform.ScopeEntry{InScope: true, AssetType: "wildcard", PlatformAssetType: "WILDCARD", Identifier: "*.example.com"}
	target := scope.Target{Kind: "wildcard", Value: "*.example.com", Host: "example.com", Apex: "example.com"}
	want := map[int64]int64{} // entry → program
	err := s.Write(ctx, func(tx *Tx) error {
		for _, h := range []string{"acme", "globex"} {
			pid, err := tx.UpsertProgram(ctx, "fake", platform.Program{Handle: h, Name: h, URL: "https://example.com/" + h, State: "open"})
			if err != nil {
				return err
			}
			eid, err := tx.InsertEntry(ctx, pid, "k", "h", entry, false)
			if err != nil {
				return err
			}
			if err := tx.ReplaceTargets(ctx, eid, []scope.Target{target, target}); err != nil {
				return err
			}
			want[eid] = pid
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	rows, err := s.db.R.QueryContext(ctx, `SELECT scope_entry_id, program_id FROM scope_targets`)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()
	n := 0
	for rows.Next() {
		var eid, pid int64
		if err := rows.Scan(&eid, &pid); err != nil {
			t.Fatal(err)
		}
		if n++; pid != want[eid] {
			t.Errorf("target of entry %d has program %d, want %d", eid, pid, want[eid])
		}
	}
	if err := rows.Err(); err != nil || n != 4 {
		t.Errorf("read %d targets: %v", n, err)
	}
}

// Retarget brings targets a parser change affects up to date, once per
// parser version, and leaves the rest (and history-only entries) alone.
func TestRetarget(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t, nil)
	stale := platform.ScopeEntry{InScope: true, AssetType: "wildcard", Identifier: "*. example.com"}
	current := platform.ScopeEntry{InScope: true, AssetType: "domain", Identifier: "app.example.org"}
	history := platform.ScopeEntry{InScope: true, AssetType: "wildcard", Identifier: "*.example.net"}
	ids := map[string]int64{}
	err := s.Write(ctx, func(tx *Tx) error {
		pid, err := tx.UpsertProgram(ctx, "fake", platform.Program{Handle: "acme", Name: "Acme", URL: "https://example.com/acme", State: "open"})
		if err != nil {
			return err
		}
		for key, e := range map[string]platform.ScopeEntry{"stale": stale, "current": current, "history": history} {
			id, err := tx.InsertEntry(ctx, pid, key, key, e, false)
			if err != nil {
				return err
			}
			ids[key] = id
		}
		if err := tx.ReplaceTargets(ctx, ids["stale"], []scope.Target{{Kind: "domain", Value: "example.com", Host: "example.com", Apex: "example.com"}}); err != nil {
			return err
		}
		if err := tx.ReplaceTargets(ctx, ids["current"], scope.Targets(current.AssetType, current.Identifier)); err != nil {
			return err
		}
		return tx.MarkEntryRemoved(ctx, ids["history"])
	})
	if err != nil {
		t.Fatal(err)
	}
	targets := func(id int64) []string {
		rows, err := s.db.R.QueryContext(ctx, `SELECT kind || ' ' || value FROM scope_targets WHERE scope_entry_id = ? ORDER BY id`, id)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = rows.Close() }()
		var out []string
		for rows.Next() {
			var v string
			if err := rows.Scan(&v); err != nil {
				t.Fatal(err)
			}
			out = append(out, v)
		}
		return out
	}

	for i, want := range []int{1, 0} {
		n, err := s.Retarget(ctx, scope.Version, scope.Targets)
		if err != nil || n != want {
			t.Errorf("Retarget #%d = %d, %v; want %d", i+1, n, err, want)
		}
	}
	if got := targets(ids["stale"]); !slices.Equal(got, []string{"wildcard *.example.com"}) {
		t.Errorf("stale entry's targets = %q", got)
	}
	if got := targets(ids["current"]); !slices.Equal(got, []string{"domain app.example.org"}) {
		t.Errorf("current entry's targets = %q", got)
	}
	if got := targets(ids["history"]); got != nil {
		t.Errorf("history entry gained targets %q", got)
	}
	// The program's cached counts follow its targets: the hunt board and
	// Targets page count from them.
	var wild, dom int
	if err := s.db.R.QueryRowContext(ctx, `SELECT n_wildcards, n_domains FROM programs WHERE handle = 'acme'`).Scan(&wild, &dom); err != nil {
		t.Fatal(err)
	}
	if wild != 1 || dom != 1 {
		t.Errorf("counts after Retarget: %d wildcards, %d domains; want 1 and 1", wild, dom)
	}
	if n, err := s.Retarget(ctx, scope.Version+1, func(string, string) []scope.Target { return nil }); err != nil || n != 2 {
		t.Errorf("Retarget with a new parser = %d, %v; want 2", n, err)
	}
}

// Cached counts survive writes that leave counted data as it was (every poll
// makes some) and are recomputed after any that change it.
func TestCachedCountKeptUntilCountedDataChanges(t *testing.T) {
	ctx := context.Background()
	clock := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	s := newTestStore(t, func() time.Time { return clock })

	prog := platform.Program{Handle: "acme", Name: "Acme", URL: "https://example.com/acme", State: "open"}
	entry := platform.ScopeEntry{InScope: true, AssetType: "wildcard", PlatformAssetType: "WILDCARD", Identifier: "*.example.com"}
	target := scope.Target{Kind: "wildcard", Value: "*.example.com", Host: "example.com", Apex: "example.com"}
	var progID, entryID int64

	tests := []struct {
		name    string
		write   func(tx *Tx) error
		recount bool
	}{
		{"program added", func(tx *Tx) (err error) {
			progID, err = tx.UpsertProgram(ctx, "fake", prog)
			return err
		}, true},
		{"program seen again unchanged", func(tx *Tx) error {
			_, err := tx.UpsertProgram(ctx, "fake", prog)
			return err
		}, false},
		{"sync bookkeeping", func(tx *Tx) error {
			if err := tx.MarkScopeChecked(ctx, progID); err != nil {
				return err
			}
			return tx.RefreshScopeCounts(ctx, progID)
		}, false},
		{"entry added", func(tx *Tx) (err error) {
			entryID, err = tx.InsertEntry(ctx, progID, "k1", "h1", entry, false)
			return err
		}, true},
		{"entry text updated", func(tx *Tx) error {
			e := entry
			e.Instruction = "be nice"
			return tx.UpdateEntry(ctx, entryID, "h2", e, false)
		}, false},
		{"targets added", func(tx *Tx) error { return tx.ReplaceTargets(ctx, entryID, []scope.Target{target}) }, true},
		{"program ignored", func(tx *Tx) error {
			_, err := tx.tx.ExecContext(ctx, `UPDATE programs SET ignored = 1 WHERE id = ?`, progID)
			return err
		}, true},
		{"program ignored again", func(tx *Tx) error {
			_, err := tx.tx.ExecContext(ctx, `UPDATE programs SET ignored = 1 WHERE id = ?`, progID)
			return err
		}, false},
		{"program tagged", func(tx *Tx) error {
			_, err := tx.tx.ExecContext(ctx, `INSERT INTO program_tags (program_id, tag) VALUES (?, 'focus')`, progID)
			return err
		}, true},
		{"tag removed", func(tx *Tx) error {
			_, err := tx.tx.ExecContext(ctx, `DELETE FROM program_tags WHERE program_id = ?`, progID)
			return err
		}, true},
		{"entry made out of scope", func(tx *Tx) error {
			e := entry
			e.InScope = false
			return tx.UpdateEntry(ctx, entryID, "h3", e, false)
		}, true},
		{"program paused", func(tx *Tx) error {
			p := prog
			p.State = "paused"
			_, err := tx.UpsertProgram(ctx, "fake", p)
			return err
		}, true},
		{"entry removed", func(tx *Tx) error { return tx.MarkEntryRemoved(ctx, entryID) }, true},
		{"program removed", func(tx *Tx) error { return tx.MarkProgramRemoved(ctx, progID) }, true},
	}
	calls := 0
	count := func() {
		t.Helper()
		if _, err := s.cachedCount(ctx, "k", func() (int, error) { calls++; return calls, nil }); err != nil {
			t.Fatal(err)
		}
	}
	count()
	for _, tt := range tests {
		clock = clock.Add(time.Minute)
		if err := s.Write(ctx, tt.write); err != nil {
			t.Fatalf("%s: %v", tt.name, err)
		}
		before := calls
		count()
		count()
		if got := calls - before; got != map[bool]int{false: 0, true: 1}[tt.recount] {
			t.Errorf("%s: counted %d times, want recount %v", tt.name, got, tt.recount)
		}
	}
}

func TestNormalizeTags(t *testing.T) {
	long := strings.Repeat("a", MaxTagLength+1)
	for _, tt := range []struct {
		in      string
		want    []string
		wantErr bool
	}{
		{"", nil, false},
		{"Focus, api  focus,web3.old", []string{"api", "focus", "web3.old"}, false},
		{"a\tb\nc", []string{"a", "b", "c"}, false},
		{"<script>", nil, true},
		{"naïve", nil, true},
		{long, nil, true},
		{"t1 t2 t3 t4 t5 t6 t7 t8 t9 t10 t11 t12 t13 t14 t15 t16 t17 t18 t19 t20 t21", nil, true},
	} {
		got, err := NormalizeTags(tt.in)
		if (err != nil) != tt.wantErr || !slices.Equal(got, tt.want) {
			t.Errorf("NormalizeTags(%q) = %q, %v", tt.in, got, err)
		}
	}
}

func TestTargetBounty(t *testing.T) {
	rng := func(lo, hi float64) platform.RewardRange { return platform.RewardRange{Min: lo, Max: hi} }
	f := func(v float64) *float64 { return &v }
	table := []platform.RewardRow{
		{Scope: "Tier 1", Low: rng(150, 500), Medium: rng(0, 2000), High: rng(0, 5000), Critical: rng(10000, 15000)},
		{Scope: "*.api.example.com", Low: rng(0, 100), Medium: rng(0, 400), Critical: rng(0, 3000)},
	}
	tests := []struct {
		name, value      string
		table            []platform.RewardRow
		listMin, listMax *float64
		stats            *float64
		wantLo, wantHi   float64 // 0 for none
		wantFrom         string
	}{
		{"the whole table", "*.example.com", table, f(1), f(2), f(9), 100, 15000, PayFromTable},
		{"the asset's own row", "*.API.example.com", table, nil, nil, nil, 100, 3000, PayFromTable},
		{"the whole program: every row", "", []platform.RewardRow{{Low: platform.RewardRange{Min: 10, Max: 50}},
			{Critical: platform.RewardRange{Min: 900, Max: 9000}}}, nil, nil, nil, 10, 9000, PayFromTable},
		{"no table: the listing's range", "*.example.com", nil, f(50), f(5000), f(9000), 50, 5000, PayFromListing},
		{"a listing with only a top", "*.example.com", nil, nil, f(5000), nil, 0, 5000, PayFromListing},
		{"a table that pays nothing", "*.example.com", []platform.RewardRow{{Scope: "x"}}, f(50), f(500), nil, 50, 500, PayFromListing},
		{"only what it has paid", "*.example.com", nil, nil, nil, f(8000), 0, 8000, PayFromStats},
		{"a listing with only a low (added by hand)", "*.example.com", nil, f(500), nil, nil, 500, 0, PayFromListing},
		{"what it has paid beats only a low", "*.example.com", nil, f(500), nil, f(8000), 0, 8000, PayFromStats},
		{"nothing known", "*.example.com", nil, nil, nil, nil, 0, 0, ""},
	}
	for _, tt := range tests {
		lo, hi, from := targetBounty(tt.table, tt.value, tt.listMin, tt.listMax, tt.stats)
		v := func(p *float64) float64 {
			if p == nil {
				return 0
			}
			return *p
		}
		if v(lo) != tt.wantLo || v(hi) != tt.wantHi || from != tt.wantFrom {
			t.Errorf("%s: got %v – %v (%q), want %v – %v (%q)", tt.name, v(lo), v(hi), from, tt.wantLo, tt.wantHi, tt.wantFrom)
		}
	}
}

func TestReverseOrder(t *testing.T) {
	tests := []struct{ in, want string }{
		{"lower(p.name), p.id", "lower(p.name) DESC, p.id DESC"},
		{"p.first_seen_at DESC, p.id DESC", "p.first_seen_at, p.id"},
		{"s.top_bounty_high IS NULL, s.top_bounty_high DESC, s.top_bounty_low DESC", "s.top_bounty_high IS NULL, s.top_bounty_high, s.top_bounty_low"},
		{"s.last_resolved_at IS NULL, s.last_resolved_at ASC", "s.last_resolved_at IS NULL, s.last_resolved_at DESC"},
		{"COALESCE(a, b) DESC, MAX(ts.score) desc, ts.value", "COALESCE(a, b), MAX(ts.score), ts.value DESC"},
		{"x IS NULL, c <> (SELECT 1 LIMIT 1)" + keepOrder + ", c" + keepOrder + ", x DESC", "x IS NULL, c <> (SELECT 1 LIMIT 1)" + keepOrder + ", c" + keepOrder + ", x"},
	}
	for _, tt := range tests {
		if got := reverseOrder(tt.in); got != tt.want {
			t.Errorf("reverseOrder(%q) = %q, want %q", tt.in, got, tt.want)
		}
		if got := reverseOrder(reverseOrder(tt.in)); reverseOrder(got) != reverseOrder(tt.in) {
			t.Errorf("reversing %q twice doesn't come back: %q", tt.in, got)
		}
	}
}

// A start date from a program's brief is kept; facts without one keep the
// stored date.
func TestSetFactsStartedAt(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t, nil)
	started := time.Date(2026, 8, 11, 18, 0, 0, 0, time.UTC)
	var id int64
	err := s.Write(ctx, func(tx *Tx) error {
		var err error
		if id, err = tx.UpsertProgram(ctx, "bugcrowd", platform.Program{Handle: "acme", Name: "Acme", URL: "https://example.com/acme", State: "open"}); err != nil {
			return err
		}
		if err := tx.SetFacts(ctx, id, platform.Facts{StartedAt: &started}); err != nil {
			return err
		}
		yes := true
		if err := tx.SetFacts(ctx, id, platform.Facts{Collaboration: &yes}); err != nil {
			return err
		}
		// A listing without a start date keeps it too, and so does one
		// with another (HackerOne's started_accepting_at, months before
		// the public launch the stats give).
		if _, err = tx.UpsertProgram(ctx, "bugcrowd", platform.Program{Handle: "acme", Name: "Acme", URL: "https://example.com/acme", State: "open"}); err != nil {
			return err
		}
		earlier := started.AddDate(0, -3, 0)
		_, err = tx.UpsertProgram(ctx, "bugcrowd", platform.Program{Handle: "acme", Name: "Acme", URL: "https://example.com/acme", State: "open", StartedAt: &earlier})
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	var got string
	if err := s.db.R.QueryRowContext(ctx, `SELECT started_at FROM programs WHERE id = ?`, id).Scan(&got); err != nil {
		t.Fatal(err)
	}
	if got != started.Format(TimeFormat) {
		t.Errorf("started_at = %q, want %q", got, started.Format(TimeFormat))
	}
}

// A listing that can't say whether a program pays keeps the stored answer,
// which the stats settle.
func TestBountyUnknownKeepsStoredAnswer(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t, nil)
	prog := platform.Program{Handle: "acme", Name: "Acme", URL: "https://example.com/acme", State: "open", BountyUnknown: true}
	pays := func() bool {
		t.Helper()
		var b bool
		if err := s.db.R.QueryRowContext(ctx, `SELECT offers_bounty FROM programs WHERE handle = 'acme'`).Scan(&b); err != nil {
			t.Fatal(err)
		}
		return b
	}
	write := func(f func(tx *Tx) error) {
		t.Helper()
		if err := s.Write(ctx, f); err != nil {
			t.Fatal(err)
		}
	}
	var id int64
	write(func(tx *Tx) (err error) { id, err = tx.UpsertProgram(ctx, "bugcrowd", prog); return err })
	if pays() {
		t.Fatal("a new program of unknown pay is stored as paying")
	}
	yes := true
	write(func(tx *Tx) error { return tx.SetFacts(ctx, id, platform.Facts{OffersBounty: &yes}) })
	write(func(tx *Tx) error { _, err := tx.UpsertProgram(ctx, "bugcrowd", prog); return err })
	if !pays() {
		t.Error("the next listing undid the stats' answer")
	}
	prog.BountyUnknown = false // the listing says it doesn't pay
	write(func(tx *Tx) error { _, err := tx.UpsertProgram(ctx, "bugcrowd", prog); return err })
	if pays() {
		t.Error("a listing that says it doesn't pay didn't count")
	}
}
