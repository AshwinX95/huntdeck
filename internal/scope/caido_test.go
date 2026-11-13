package scope

import (
	"encoding/json"
	"slices"
	"testing"
)

func TestCaidoLists(t *testing.T) {
	targets := func(entries ...[2]string) []Target {
		var out []Target
		for _, e := range entries {
			out = append(out, Targets(e[0], e[1])...)
		}
		return out
	}
	in := targets(
		[2]string{AssetWildcard, "*.Example.com"},
		[2]string{AssetDomain, "example.com"},
		[2]string{AssetURL, "https://api.example.com/v2/users?x=1"},
		[2]string{AssetURL, "https://api.example.com/v3"}, // the same host again
		[2]string{AssetWildcard, "*.bücher.example"},
		[2]string{AssetIP, "192.0.2.10"},
		[2]string{AssetCIDR, "198.51.100.0/24"},
		[2]string{AssetCIDR, "203.0.113.0/25"},
		[2]string{AssetIP, "2001:db8::1"},
		[2]string{AssetURL, "http://192.0.2.20:8080/admin"},
	)
	out := targets(
		[2]string{AssetDomain, "admin.example.com"},
		[2]string{AssetURL, "https://example.com/admin"}, // a path: not the whole host
		[2]string{AssetURL, "https://status.example.com/"},
		[2]string{AssetWildcard, "*.internal.example.com"},
	)
	allow, deny, skipped := CaidoLists(in, out)
	wantAllow := []string{"*.example.com", "example.com", "api.example.com", "*.xn--bcher-kva.example", "192.0.2.10", "198.51.100.*", "192.0.2.20"}
	wantDeny := []string{"admin.example.com", "status.example.com", "*.internal.example.com"}
	if !slices.Equal(allow, wantAllow) {
		t.Errorf("allow = %q, want %q", allow, wantAllow)
	}
	if !slices.Equal(deny, wantDeny) {
		t.Errorf("deny = %q, want %q", deny, wantDeny)
	}
	var left []string
	for _, s := range skipped {
		left = append(left, s.Value)
		if s.Why == "" {
			t.Errorf("%s skipped without a reason", s.Value)
		}
	}
	if !slices.Equal(left, []string{"203.0.113.0/25", "2001:db8::1", "https://example.com/admin"}) {
		t.Errorf("skipped = %q", left)
	}
}

func TestCaidoConfig(t *testing.T) {
	b, err := CaidoConfig("HuntDeck Acme", "Acme (HackerOne)", []string{"*.example.com"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Version string `json:"version"`
		Config  struct {
			Project struct {
				Name   string `json:"name"`
				Scopes []struct {
					Name      string   `json:"name"`
					Allowlist []string `json:"allowlist"`
					Denylist  []string `json:"denylist"`
				} `json:"scopes"`
			} `json:"project"`
		} `json:"config"`
	}
	if err := json.Unmarshal(b, &doc); err != nil {
		t.Fatal(err)
	}
	sc := doc.Config.Project.Scopes
	if doc.Version != CaidoVersion || doc.Config.Project.Name != "HuntDeck Acme" || len(sc) != 1 || sc[0].Name != "Acme (HackerOne)" ||
		!slices.Equal(sc[0].Allowlist, []string{"*.example.com"}) || sc[0].Denylist == nil {
		t.Errorf("config = %s", b)
	}
}
