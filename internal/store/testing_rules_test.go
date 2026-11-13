package store

import (
	"context"
	"testing"
	"time"

	"github.com/mdpsec/huntdeck/internal/platform"
)

// EEA residency counts as a requirement (for "no requirements" and against
// a profile); two-factor auth doesn't, as anyone can turn it on. Testing
// rules come back with the program once a platform has given them.
func TestEEAAndTestingRules(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 10, 28, 12, 0, 0, 0, time.UTC)
	s := newTestStore(t, func() time.Time { return now })
	ids := map[string]int64{}
	err := s.Write(ctx, func(tx *Tx) error {
		for _, p := range []struct {
			handle string
			req    platform.Requirements
		}{{"eea", platform.Requirements{EEA: true}}, {"twofa", platform.Requirements{TwoFactor: true}}} {
			id, err := tx.UpsertProgram(ctx, "fake", platform.Program{Handle: p.handle, Name: p.handle, URL: "https://example.com/" + p.handle, State: "open"})
			if err != nil {
				return err
			}
			ids[p.handle] = id
			if err := tx.SetRequirements(ctx, id, p.req); err != nil {
				return err
			}
		}
		return tx.SetTesting(ctx, ids["twofa"], platform.TestingRules{RateLimit: 5, Header: "X-Bug-Bounty: researcher1", PlatformEmail: true})
	})
	if err != nil {
		t.Fatal(err)
	}

	// Two-factor auth doesn't count as a requirement: anyone can turn it on.
	fc, err := s.ProgramFacets(ctx, ProgramFilter{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if fc.NoRequirements != 1 {
		t.Errorf("no requirements = %d, want 1 (twofa)", fc.NoRequirements)
	}

	d, err := s.Program(ctx, "fake", "twofa")
	if err != nil {
		t.Fatal(err)
	}
	want := platform.TestingRules{RateLimit: 5, Header: "X-Bug-Bounty: researcher1", PlatformEmail: true}
	if d.Testing == nil || *d.Testing != want || !d.Requirements.TwoFactor || !d.Requirements.None() {
		t.Errorf("twofa: testing %+v, requirements %+v", d.Testing, d.Requirements)
	}
	if d, err := s.Program(ctx, "fake", "eea"); err != nil || d.Testing != nil || !d.Requirements.EEA || d.Requirements.None() {
		t.Errorf("eea: testing %+v, requirements %+v, %v", d.Testing, d.Requirements, err)
	}
}
