package independent

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/mdpsec/huntdeck/internal/platform"
	"github.com/mdpsec/huntdeck/internal/scope"
)

func TestParseScope(t *testing.T) {
	in := "*.example.com\n  api.example.net \n\nhttps://app.example.com/v2\n192.0.2.7\n198.51.100.0/24\n*.example.com\nMobile app\n"
	got, err := ParseScope(in, "status.example.com\n", true)
	if err != nil {
		t.Fatal(err)
	}
	want := []struct {
		in       bool
		kind, id string
	}{
		{true, scope.AssetWildcard, "*.example.com"}, {true, scope.AssetDomain, "api.example.net"},
		{true, scope.AssetURL, "https://app.example.com/v2"}, {true, scope.AssetIP, "192.0.2.7"},
		{true, scope.AssetCIDR, "198.51.100.0/24"}, {true, scope.AssetOther, "Mobile app"},
		{false, scope.AssetDomain, "status.example.com"},
	}
	if len(got) != len(want) {
		t.Fatalf("got %d entries, want %d: %+v", len(got), len(want), got)
	}
	for i, w := range want {
		g := got[i]
		if g.InScope != w.in || g.AssetType != w.kind || g.Identifier != w.id || g.BountyEligible != w.in {
			t.Errorf("entry %d = %+v, want %+v", i, g, w)
		}
	}
	if vdp, _ := ParseScope("a.example.com", "", false); vdp[0].BountyEligible {
		t.Error("a VDP's scope is bounty-eligible")
	}
	if _, err := ParseScope(strings.Repeat("a", MaxLineLength+1), "", true); err == nil {
		t.Error("an overlong line was accepted")
	}
	if _, err := ParseScope(lines(MaxScopeLines+1), "", true); err == nil {
		t.Error("too many lines were accepted")
	}
	inText, outText := ScopeText(got)
	if again, _ := ParseScope(inText, outText, true); len(again) != len(got) {
		t.Errorf("ScopeText doesn't round-trip: %d entries, want %d", len(again), len(got))
	}
}

func lines(n int) string {
	var b strings.Builder
	for i := range n {
		fmt.Fprintf(&b, "h%d.example.com\n", i)
	}
	return b.String()
}

func TestSlug(t *testing.T) {
	taken := map[string]bool{"meta-bug-bounty": true, "meta-bug-bounty-2": true}
	tests := []struct{ name, want string }{
		{"Meta Bug Bounty", "meta-bug-bounty-3"},
		{"  Acme's   Bounty! ", "acme-s-bounty"},
		{"新しい", "program"},
		{"New", "new-2"},
		{strings.Repeat("a", 80), strings.Repeat("a", 60)},
	}
	for _, tt := range tests {
		if got := Slug(tt.name, func(h string) bool { return taken[h] }); got != tt.want {
			t.Errorf("Slug(%q) = %q, want %q", tt.name, got, tt.want)
		}
	}
}

func TestValidURL(t *testing.T) {
	for u, want := range map[string]bool{
		"https://example.com/security": true, "http://example.com": true, "javascript:alert(1)": false,
		"example.com": false, "https://": false, "https://exa mple.com": false, "ftp://example.com": false,
	} {
		if got := ValidURL(u); got != want {
			t.Errorf("ValidURL(%q) = %v, want %v", u, got, want)
		}
	}
}

// The Source is a complete listing of what it was given.
func TestSource(t *testing.T) {
	ctx := context.Background()
	s := New([]Program{{Program: platform.Program{Handle: "a"}, Scope: []platform.ScopeEntry{{Identifier: "a.example.com"}}}})
	l, err := s.ListPrograms(ctx)
	if err != nil || !l.Complete || len(l.Programs) != 1 {
		t.Fatalf("listing = %+v, %v", l, err)
	}
	if e, err := s.FetchScope(ctx, l.Programs[0]); err != nil || len(e) != 1 {
		t.Errorf("scope = %v, %v", e, err)
	}
	if _, err := s.FetchScope(ctx, platform.Program{Handle: "b"}); err == nil {
		t.Error("scope of a program not listed")
	}
}
