package bugcrowd

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/mdpsec/huntdeck/internal/platform"
	"github.com/mdpsec/huntdeck/internal/scope"
)

func TestFetchScopeFromChangelog(t *testing.T) {
	f := newFake(t)
	c := f.client(t, Options{})
	es, err := c.FetchScope(context.Background(), program(f, "acme-bb", "/engagements/acme-bb"))
	if err != nil {
		t.Fatal(err)
	}
	type want struct {
		id, ident, asset string
		in, bounty       bool
		instruction      string
	}
	wants := []want{
		{"gt-1", "https://www.acme.example.com", scope.AssetURL, true, true, "www.acme.example.com"},
		{"gt-2", "*.acme.example.com", scope.AssetWildcard, true, true, "All subdomains, except those below."},
		{"gt-3", "https://api.acme.example.com/v2/*", scope.AssetAPI, true, true, "Acme API"},
		{"gt-4", "https://play.google.com/store/apps/details?id=com.example.acme", scope.AssetAndroid, true, true, "Acme Android App"},
		{"gt-5", "192.0.2.0/24", scope.AssetCIDR, true, true, "Acme office range"},
		{"g-tier2:t-www", "https://www.acme.example.com", scope.AssetURL, true, false, "www.acme.example.com"},
		{"gt-7", "blog.acme.example.com", scope.AssetDomain, false, false, "Third-party hosted."},
	}
	if len(es) != len(wants) {
		t.Fatalf("got %d entries, want %d: %+v", len(es), len(wants), es)
	}
	for i, w := range wants {
		e := es[i]
		if e.PlatformID != w.id || e.Identifier != w.ident || e.AssetType != w.asset || e.InScope != w.in ||
			e.BountyEligible != w.bounty || e.Instruction != w.instruction {
			t.Errorf("entry %d:\n got %+v\nwant %+v", i, e, w)
		}
	}

}

func TestScopeChangedAt(t *testing.T) {
	f := newFake(t)
	c := f.client(t, Options{})
	for _, tt := range []struct {
		handle, brief string
		want          time.Time
	}{
		{"acme-bb", "/engagements/acme-bb", time.Date(2026, 10, 11, 11, 23, 1, 81_000_000, time.UTC)},
		{"globex-vdp", "/engagements/globex-vdp", time.Time{}}, // no changelog: unknown
		{"legacy", "/legacy", time.Time{}},
	} {
		got, err := c.ScopeChangedAt(context.Background(), program(f, tt.handle, tt.brief))
		if err != nil || !got.Equal(tt.want) {
			t.Errorf("%s: ScopeChangedAt = %v, %v; want %v", tt.handle, got, err, tt.want)
		}
	}
	if n := f.hit("/engagements/acme-bb/changelog/0d5e0c1a-1111-4aaa-8bbb-000000000002.json"); n != 0 {
		t.Errorf("version document fetched %d times; a check needs only the changelog", n)
	}
}

func TestFetchScopeFallsBackToBriefPage(t *testing.T) {
	f := newFake(t)
	c := f.client(t, Options{})
	es, err := c.FetchScope(context.Background(), program(f, "globex-vdp", "/engagements/globex-vdp"))
	if err != nil {
		t.Fatal(err)
	}
	if len(es) != 1 || es[0].Identifier != "*.globex.example.net" || es[0].AssetType != scope.AssetWildcard || es[0].BountyEligible {
		t.Fatalf("entries: %+v", es)
	}
}

func TestLoggedOutDocumentIsRejectedWhenLoggedIn(t *testing.T) {
	f := newFake(t)
	c := f.client(t, Options{Session: goodSession})
	_, err := c.FetchScope(context.Background(), program(f, "globex-vdp", "/engagements/globex-vdp"))
	if !errors.Is(err, ErrSessionExpired) {
		t.Fatalf("err = %v (logged-out documents hide target names)", err)
	}
}

func TestComplianceGateIsUnavailable(t *testing.T) {
	f := newFake(t)
	c := f.client(t, Options{})
	_, err := c.FetchScope(context.Background(), program(f, "initech-pt", "/engagements/initech-pt"))
	if !errors.Is(err, platform.ErrScopeUnavailable) {
		t.Fatalf("err = %v", err)
	}
}

func TestMovedEngagementIsUnavailable(t *testing.T) {
	f := newFake(t)
	c := f.client(t, Options{})
	_, err := c.FetchScope(context.Background(), program(f, "umbrella-vdp", "/engagements/umbrella-vdp"))
	if !errors.Is(err, platform.ErrScopeUnavailable) || !strings.Contains(err.Error(), "moved to Bugcrowd Gov (US)") {
		t.Fatalf("err = %v", err)
	}
	if strings.Contains(err.Error(), "csrf") || strings.Contains(err.Error(), "fake-token") {
		t.Fatalf("error leaks page props: %v", err)
	}
}

func TestFetchLegacyScope(t *testing.T) {
	f := newFake(t)
	c := f.client(t, Options{})
	es, err := c.FetchScope(context.Background(), program(f, "hooli", "/hooli"))
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, e := range es {
		got = append(got, e.PlatformID+"|"+e.Identifier+"|"+e.AssetType+"|"+map[bool]string{true: "in", false: "out"}[e.InScope])
	}
	want := "g0:101|app.hooli.example.org|domain|in,g0:102|*.hooli.example.org|wildcard|in,g1:201|status.hooli.example.org|domain|out"
	if strings.Join(got, ",") != want {
		t.Fatalf("got  %s\nwant %s", strings.Join(got, ","), want)
	}
}

func TestAssetType(t *testing.T) {
	tests := []struct{ cat, ident, want string }{
		{"website", "*.example.com", scope.AssetWildcard},
		{"website", "https://*.example.com/*", scope.AssetWildcard},
		{"website", "example.com", scope.AssetDomain},
		{"website", "example.com/*", scope.AssetDomain},
		{"website", "https://example.com", scope.AssetURL},
		{"website", "example.com/admin", scope.AssetURL},
		{"website", "Main website (see description)", scope.AssetOther},
		{"api", "api.example.com", scope.AssetAPI},
		{"ip_address", "192.0.2.1", scope.AssetIP},
		{"ip_address", "192.0.2.0/24", scope.AssetCIDR},
		{"network", "vpn.example.com", scope.AssetDomain},
		{"network", "VPN gateways", scope.AssetOther},
		{"android", "https://play.google.com/x", scope.AssetAndroid},
		{"ios", "https://apps.apple.com/x", scope.AssetIOS},
		{"iot", "Camera", scope.AssetHardware},
		{"other", "Steam game", scope.AssetOther},
		{"something-new", "x", scope.AssetOther},
		// Plain targets filed as "other" get their real type.
		{"other", "*.example.com", scope.AssetWildcard},
		{"other", `\*.example.com`, scope.AssetWildcard},
		{"other", " *.Example.com ", scope.AssetWildcard},
		{"other", "app.example.com", scope.AssetDomain},
		{"other", "https://app.example.com/", scope.AssetDomain},
		{"other", "https://app.example.com/*", scope.AssetDomain},
		{"other", "https://app.example.com/login", scope.AssetURL},
		{"other", "198.51.100.7", scope.AssetIP},
		{"other", "198.51.100.0/24", scope.AssetCIDR},
		// Anything more than the one target stays "other".
		{"other", "Main site: example.com", scope.AssetOther},
		{"other", "a.example.com, b.example.com", scope.AssetOther},
		{"other", "example.internal", scope.AssetOther},
		{"other", "Node.js", scope.AssetOther},
	}
	for _, tt := range tests {
		if got := assetType(tt.cat, tt.ident); got != tt.want {
			t.Errorf("assetType(%q, %q) = %q, want %q", tt.cat, tt.ident, got, tt.want)
		}
	}
}

func TestScopeHistory(t *testing.T) {
	f := newFake(t)
	c := f.client(t, Options{})
	got := map[string][]platform.ScopeVersion{}
	err := c.ScopeHistory(context.Background(), []platform.Program{
		program(f, "acme-bb", "/engagements/acme-bb"),
		program(f, "globex-vdp", "/engagements/globex-vdp"), // no changelog: skipped
		program(f, "legacy", "/legacy"),                     // no versions: skipped
	}, func(handle string, v []platform.ScopeVersion) error {
		got[handle] = v
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || len(got["acme-bb"]) != 2 {
		t.Fatalf("history = %v", got)
	}
	v0, v1 := got["acme-bb"][0], got["acme-bb"][1]
	if !v0.At.Equal(time.Date(2026, 9, 30, 13, 32, 57, 630_000_000, time.UTC)) || !v1.At.Equal(time.Date(2026, 10, 11, 11, 23, 1, 81_000_000, time.UTC)) {
		t.Errorf("version dates %v, %v; want oldest first", v0.At, v1.At)
	}
	has := func(v platform.ScopeVersion, ident string) bool {
		return slices.ContainsFunc(v.Entries, func(e platform.ScopeEntry) bool { return e.Identifier == ident })
	}
	if !has(v0, "https://legacy.acme.example.com") || has(v1, "https://legacy.acme.example.com") || len(v1.Entries) != 7 {
		t.Errorf("entries: v0 %d, v1 %d", len(v0.Entries), len(v1.Entries))
	}
}
