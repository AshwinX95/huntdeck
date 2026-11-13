package syncer

import (
	"context"
	"fmt"
	"log/slog"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/mdpsec/huntdeck/internal/db"
	"github.com/mdpsec/huntdeck/internal/platform"
	"github.com/mdpsec/huntdeck/internal/platform/fake"
	"github.com/mdpsec/huntdeck/internal/scope"
	"github.com/mdpsec/huntdeck/internal/store"
)

type env struct {
	t     testing.TB
	store *store.Store
	sync  *Syncer
	fake  *fake.Platform
	clock time.Time
}

func newEnv(t testing.TB) *env {
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
	log := slog.New(slog.DiscardHandler)
	if _, err := d.Migrate(ctx, migs, t.TempDir(), log); err != nil {
		t.Fatal(err)
	}
	e := &env{t: t, fake: fake.New(), clock: time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)}
	e.store = store.New(d, func() time.Time { return e.clock })
	e.sync = New(e.store, log, 3)
	return e
}

// poll runs a poll and advances the clock so each run has distinct times.
func (e *env) poll(mode string, limit int) store.Run {
	e.t.Helper()
	e.clock = e.clock.Add(time.Minute)
	run, err := e.sync.Poll(context.Background(), e.fake, Options{Mode: mode, Trigger: "manual", Limit: limit})
	if err != nil {
		e.t.Fatalf("Poll: %v", err)
	}
	return run
}

func (e *env) changes(runID int64) []string {
	e.t.Helper()
	all, _, err := e.store.Changes(context.Background(), store.ChangeFilter{Limit: 1000})
	if err != nil {
		e.t.Fatal(err)
	}
	var out []string
	for _, c := range all {
		if c.PollRunID == runID {
			out = append(out, c.Program.Handle+" "+c.Kind+" "+c.Field+" "+c.OldValue+"→"+c.NewValue)
		}
	}
	slices.Sort(out)
	return out
}

func (e *env) targets(kind string) []string {
	e.t.Helper()
	var v []string
	err := e.store.EachTarget(context.Background(), store.TargetFilter{Kind: kind}, func(s string) error {
		v = append(v, s)
		return nil
	})
	if err != nil {
		e.t.Fatal(err)
	}
	return v
}

func deref(v *float64) float64 {
	if v == nil {
		return 0
	}
	return *v
}

func prog(handle string, private bool) platform.Program {
	return platform.Program{Handle: handle, Name: handle, URL: "https://platform.example/" + handle,
		Private: private, State: platform.StateOpen, OffersBounty: true}
}

func entry(id, ident string, in bool) platform.ScopeEntry {
	at := scope.AssetURL
	if ident[0] == '*' {
		at = scope.AssetWildcard
	}
	return platform.ScopeEntry{PlatformID: id, InScope: in, AssetType: at, PlatformAssetType: "URL", Identifier: ident, BountyEligible: in}
}

func (e *env) seed() {
	e.fake.SetPrograms(prog("acme", false), prog("globex", true))
	e.fake.SetScope("acme", entry("1", "*.acme.example", true), entry("2", "old.acme.example", false))
	e.fake.SetScope("globex", entry("3", "app.globex.example", true))
}

func (e *env) pollWith(o Options) store.Run {
	e.t.Helper()
	e.clock = e.clock.Add(time.Minute)
	o.Trigger = "manual"
	run, err := e.sync.Poll(context.Background(), e.fake, o)
	if err != nil {
		e.t.Fatalf("Poll: %v", err)
	}
	return run
}

// cancelOnScope cancels the poll as soon as scope fetching starts, like a
// shutdown in the middle of a poll.
type cancelOnScope struct {
	*fake.Platform
	cancel context.CancelFunc
}

func (c cancelOnScope) FetchScope(ctx context.Context, p platform.Program) ([]platform.ScopeEntry, error) {
	c.cancel()
	return nil, ctx.Err()
}

func (e *env) hits() string {
	return fmt.Sprintf("acme=%d globex=%d", e.fake.ScopeHits("acme"), e.fake.ScopeHits("globex"))
}

func disclosure(id, handle, severity, weakness string, at time.Time, bounty float64) platform.Disclosure {
	d := platform.Disclosure{ID: id, ProgramHandle: handle, Title: "Report " + id, Severity: severity, Weakness: weakness,
		DisclosedAt: at, URL: "https://platform.example/reports/" + id, Reporter: "hunter" + id}
	if bounty > 0 {
		d.Bounty = &bounty
	}
	return d
}

func ptr[T any](v T) *T { return &v }
