package syncer

import (
	"context"
	"path/filepath"
	"slices"
	"testing"

	"github.com/mdpsec/huntdeck/internal/db"
	"github.com/mdpsec/huntdeck/internal/scope"
	"github.com/mdpsec/huntdeck/internal/store"
)

// A seeded install's polls carry on from the seed: its scope is the
// baseline, so changes since the seed are recorded, while the user's own
// private invites (never in seeds) arrive quietly with the first listing.
func TestPollsCarryOnFromASeed(t *testing.T) {
	src := newEnv(t)
	src.seed() // acme is public, globex a private invite
	src.poll(ModeFull, 0)
	src.poll(ModeFull, 0)

	e := seeded(t, src)
	if got := e.targets(scope.KindDomain); len(got) != 0 {
		t.Errorf("the private program's scope came with the seed: %v", got)
	}
	if got := e.targets(scope.KindWildcard); !slices.Equal(got, []string{"*.acme.example"}) {
		t.Errorf("seeded wildcards = %v", got)
	}

	// This user is invited to globex too, and acme added scope since.
	e.seed()
	e.fake.SetScope("acme", entry("1", "*.acme.example", true), entry("2", "old.acme.example", false),
		entry("4", "api.acme.example", true))
	run := e.poll(ModeFull, 0)
	if run.Status != store.RunOK || !run.ListingComplete {
		t.Fatalf("first poll after seeding: %+v", run)
	}
	if got, want := e.changes(run.ID), []string{"acme scope_added in_scope →api.acme.example"}; !slices.Equal(got, want) {
		t.Errorf("first poll after seeding:\n got  %q\n want %q", got, want)
	}
	if got := e.targets(scope.KindDomain); !slices.Equal(got, []string{"api.acme.example", "app.globex.example"}) {
		t.Errorf("domains after the first poll = %v", got)
	}

	again := e.poll(ModeFull, 0)
	if again.Status != store.RunOK || again.Changes != 0 {
		t.Errorf("identical poll after that: status=%s changes=%v", again.Status, e.changes(again.ID))
	}
}

// A program that was private and is public now stays out of seeds however
// it went public, so its scope from its private days isn't handed out.
func TestOncePrivateProgramsStayOutOfSeeds(t *testing.T) {
	cases := []struct {
		name string
		run  func(e *env) store.Run // returns the run globex went public in
	}{
		{"flipped", func(e *env) store.Run {
			e.seed()
			e.poll(ModeFull, 0)
			e.fake.SetPrograms(prog("acme", false), prog("globex", false))
			return e.poll(ModeFull, 0)
		}},
		{"removed while private, back public", func(e *env) store.Run {
			e.seed()
			e.poll(ModeFull, 0)
			e.fake.SetPrograms(prog("acme", false))
			e.poll(ModeFull, 0)
			e.fake.SetPrograms(prog("acme", false), prog("globex", false))
			return e.poll(ModeFull, 0)
		}},
		{"public by the first complete listing", func(e *env) store.Run {
			e.seed()
			e.fake.SetPrograms(prog("globex", true), prog("acme", false))
			e.poll(ModeFull, 1) // a --limit poll stores globex, private
			e.fake.SetPrograms(prog("globex", false), prog("acme", false))
			return e.poll(ModeFull, 0) // still the first import
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			src := newEnv(t)
			run := c.run(src)
			if got := src.changes(run.ID); !slices.Contains(got, "globex visibility_changed visibility private→public") {
				t.Errorf("going public wasn't recorded: %q", got)
			}
			if got := seeded(t, src).targets(scope.KindDomain); len(got) != 0 {
				t.Errorf("the once-private program's scope came with the seed: %v", got)
			}
		})
	}
}

// seeded is a new install started from a seed of src.
func seeded(t *testing.T, src *env) *env {
	t.Helper()
	ctx := context.Background()
	migs, err := db.Migrations()
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "seed.db.gz")
	if _, err := src.store.ExportSeed(ctx, path, migs, store.SeedOptions{}); err != nil {
		t.Fatal(err)
	}
	e := newEnv(t)
	e.clock = src.clock
	if _, err := e.store.ImportSeed(ctx, path, t.TempDir(), migs); err != nil {
		t.Fatal(err)
	}
	return e
}
