package store

import (
	"context"
	"testing"

	"github.com/mdpsec/huntdeck/internal/platform"
	"github.com/mdpsec/huntdeck/internal/scope"
)

// The hunt board's totals are the pages they link to: open programs,
// paying ones and in-scope wildcards and domains as the Programs list and
// Targets page count them by default (open, not ignored), and private
// invites as the Programs list counts them with every status.
func TestSummaryMatchesTheListedPages(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t, nil)
	programs := []struct {
		handle, state     string
		bounty, priv, ign bool
		removed           bool
		scope             []string // in-scope identifiers, wildcards and domains
	}{
		{"open", "open", true, false, false, false, []string{"*.example.com", "*.example.net", "app.example.com"}},
		{"free", "open", false, true, false, false, []string{"*.example.org"}},
		{"paused", "paused", true, true, false, false, []string{"*.example.edu", "www.example.edu"}},
		{"ignored", "open", true, false, true, false, []string{"*.example.info"}},
		{"gone", "open", true, false, false, true, []string{"*.example.io"}},
	}
	err := s.Write(ctx, func(tx *Tx) error {
		for _, p := range programs {
			id, err := tx.UpsertProgram(ctx, "fake", platform.Program{Handle: p.handle, Name: p.handle, URL: "https://example.com/" + p.handle,
				State: p.state, OffersBounty: p.bounty, Private: p.priv})
			if err != nil {
				return err
			}
			for i, ident := range p.scope {
				typ := "domain"
				if ident[0] == '*' {
					typ = "wildcard"
				}
				eid, err := tx.InsertEntry(ctx, id, ident, ident, platform.ScopeEntry{InScope: true, AssetType: typ, Identifier: ident, BountyEligible: true}, i == 0)
				if err != nil {
					return err
				}
				if err := tx.ReplaceTargets(ctx, eid, scope.Targets(typ, ident)); err != nil {
					return err
				}
			}
			if err := tx.RefreshScopeCounts(ctx, id); err != nil {
				return err
			}
			if _, err := tx.tx.ExecContext(ctx, `UPDATE programs SET ignored = ?, removed_at = CASE WHEN ? THEN ? END WHERE id = ?`,
				p.ign, p.removed, "2026-10-01T00:00:00.000Z", id); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	sums, err := s.Summary(ctx)
	if err != nil || len(sums) != 1 {
		t.Fatalf("summary: %v, %v", sums, err)
	}
	sum := sums[0]
	listed := func(f ProgramFilter) int {
		t.Helper()
		_, n, err := s.Programs(ctx, f)
		if err != nil {
			t.Fatal(err)
		}
		return n
	}
	targets := func(kind string) int {
		t.Helper()
		_, n, err := s.TargetRows(ctx, TargetFilter{Kind: kind}, "", 1, 0)
		if err != nil {
			t.Fatal(err)
		}
		return n
	}
	tests := []struct {
		name      string
		got, want int
		exact     int
	}{
		{"open (/programs)", sum.Open, listed(ProgramFilter{State: "open"}), 2},
		{"paying (/programs?bounty=1)", sum.Bounty, listed(ProgramFilter{State: "open", BountyOnly: true}), 1},
		{"private (/programs?visibility=private&state=all)", sum.Private, listed(ProgramFilter{State: "all", Visibility: "private"}), 2},
		{"wildcards (/targets?kind=wildcard)", sum.Targets[scope.KindWildcard], targets(scope.KindWildcard), 3},
		{"domains (/targets?kind=domain)", sum.Targets[scope.KindDomain], targets(scope.KindDomain), 1},
	}
	for _, tt := range tests {
		if tt.got != tt.want || tt.got != tt.exact {
			t.Errorf("%s: board %d, page %d; want %d", tt.name, tt.got, tt.want, tt.exact)
		}
	}
}
