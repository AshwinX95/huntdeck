package web

import (
	"context"
	"net/http"
	"testing"

	"github.com/mdpsec/huntdeck/internal/platform"
	"github.com/mdpsec/huntdeck/internal/scope"
	"github.com/mdpsec/huntdeck/internal/syncer"
)

// addDomain adds api.acme.example to acme's scope as a domain: the test
// env's https://api.acme.example/v2 is a path rule, which names none.
func addDomain(t *testing.T, e *testEnv) {
	t.Helper()
	e.fake.SetScope("acme",
		platform.ScopeEntry{PlatformID: "1", InScope: true, AssetType: scope.AssetWildcard, Identifier: "*.acme.example", BountyEligible: true, MaxSeverity: "critical"},
		platform.ScopeEntry{PlatformID: "2", InScope: false, AssetType: scope.AssetURL, Identifier: "admin.acme.example"},
		platform.ScopeEntry{PlatformID: "4", InScope: true, AssetType: scope.AssetURL, Identifier: "https://api.acme.example/v2", BountyEligible: true},
		platform.ScopeEntry{PlatformID: "9", InScope: true, AssetType: scope.AssetDomain, Identifier: "api.acme.example", BountyEligible: true})
	if _, err := e.syncer.Poll(context.Background(), e.fake, syncer.Options{Mode: syncer.ModeFull, Trigger: "manual"}); err != nil {
		t.Fatal(err)
	}
}

// TestTargetsTextPages checks the plain-text targets export's limit and
// offset, and that a read-only copy gives a page at a time with a Link to
// the next one.
func TestTargetsTextPages(t *testing.T) {
	full := newTestEnv(t)
	ro := newTestEnv(t, func(d *Deps) { d.ReadOnly = true })
	addDomain(t, full)
	addDomain(t, ro)
	tests := []struct {
		name      string
		env       *testEnv
		query     string
		want      string
		wantLimit string // X-Targets-Limit
		wantNext  string // Link
	}{
		{"full: all", full, "", "api.acme.example\napp.evil.example\n", "", ""},
		{"full: limit", full, "&limit=1", "api.acme.example\n", "", ""},
		{"full: offset", full, "&offset=1", "app.evil.example\n", "", ""},
		{"full: past the end", full, "&offset=5", "", "", ""},
		{"read-only: all fit", ro, "", "api.acme.example\napp.evil.example\n", "5000", ""},
		{"read-only: limit", ro, "&limit=1", "api.acme.example\n", "1",
			`</api/v1/targets?kind=domains&limit=1&offset=1>; rel="next"`},
		{"read-only: last page", ro, "&limit=1&offset=1", "app.evil.example\n", "1", ""},
		{"read-only: limit capped", ro, "&limit=99999", "api.acme.example\napp.evil.example\n", "5000", ""},
		{"read-only: best", ro, "&sort=best", "api.acme.example\n", "5000", ""}, // only recent targets score
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			res, body := tt.env.do(tt.env.client(), http.MethodGet, "/api/v1/targets?kind=domains"+tt.query, nil, nil)
			if res.StatusCode != http.StatusOK {
				t.Fatalf("status %d: %s", res.StatusCode, body)
			}
			if body != tt.want {
				t.Errorf("body %q, want %q", body, tt.want)
			}
			if got := res.Header.Get("X-Targets-Limit"); got != tt.wantLimit {
				t.Errorf("X-Targets-Limit %q, want %q", got, tt.wantLimit)
			}
			if got := res.Header.Get("Link"); got != tt.wantNext {
				t.Errorf("Link %q, want %q", got, tt.wantNext)
			}
		})
	}
}
