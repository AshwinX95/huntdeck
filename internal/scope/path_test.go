package scope

import (
	"strings"
	"testing"
)

// A path rule covers its path only: not the bare host, nor its other paths,
// and the verdict names the path rule.
func TestMatchPathRules(t *testing.T) {
	rules := []Rule{}
	add := func(id int64, in bool, kind, ident string) {
		for _, tg := range Targets(kind, ident) {
			rules = append(rules, Rule{ProgramID: id, InScope: in, Target: tg})
		}
	}
	add(1, true, AssetURL, "https://github.com/example-org/*")
	add(2, true, AssetURL, "https://marketplace.example.com/apps/1234")
	add(3, true, AssetWildcard, "*.example.net")
	add(3, false, AssetURL, "https://admin.example.net/internal/")
	add(4, true, AssetURL, "https://*.shop.example/api/")
	tests := []struct {
		input string
		want  map[int64]bool // program: in scope; missing: no verdict
		rule  string         // program 1's or 3's rule, when it matched
	}{
		{"https://github.com/example-org/repo", map[int64]bool{1: true}, "https://github.com/example-org/*"},
		{"github.com/Example-Org/repo/issues", map[int64]bool{1: true}, ""},
		{"https://github.com/other-org/repo", map[int64]bool{}, ""},
		{"github.com", map[int64]bool{}, ""},
		{"https://marketplace.example.com/apps/1234/details", map[int64]bool{2: true}, ""},
		{"https://marketplace.example.com/apps/12345", map[int64]bool{}, ""},
		{"https://admin.example.net/internal/users", map[int64]bool{3: false}, "https://admin.example.net/internal/"},
		{"https://admin.example.net/", map[int64]bool{3: true}, "*.example.net"},
		{"https://eu.shop.example/api/v1", map[int64]bool{4: true}, ""},
		{"https://eu.shop.example/", map[int64]bool{}, ""},
	}
	for _, tt := range tests {
		got := map[int64]bool{}
		var rule string
		for _, m := range Match(rules, tt.input) {
			got[m.ProgramID] = m.InScope
			if m.ProgramID == 1 || m.ProgramID == 3 {
				rule = m.Rule.Target.Value
			}
		}
		if len(got) != len(tt.want) {
			t.Errorf("%s: verdicts %v, want %v", tt.input, got, tt.want)
			continue
		}
		for id, in := range tt.want {
			if v, ok := got[id]; !ok || v != in {
				t.Errorf("%s: verdicts %v, want %v", tt.input, got, tt.want)
			}
		}
		if tt.rule != "" && rule != tt.rule {
			t.Errorf("%s: matched rule %q, want %q", tt.input, rule, tt.rule)
		}
	}
}

// Exports give a path rule as host and path, never the bare host.
func TestExportPathRules(t *testing.T) {
	in := Targets(AssetURL, "https://github.com/example-org/*")
	in = append(in, Targets(AssetURL, "https://docs.example.com/v*/api")...)
	b, err := BurpScope(in, nil)
	if err != nil {
		t.Fatal(err)
	}
	if s := string(b); !strings.Contains(s, `"file": "^/example-org/.*"`) || !strings.Contains(s, `"file": "^/v.*/api.*"`) ||
		strings.Count(s, `"host"`) != 2 {
		t.Errorf("burp scope:\n%s", s)
	}
	z, err := ZAPContext("x", in, nil)
	if err != nil {
		t.Fatal(err)
	}
	if s := string(z); !strings.Contains(s, `<incregexes>^https://github\.com(:[0-9]+)?/example-org/.*$</incregexes>`) ||
		strings.Contains(s, `github\.com(:[0-9]+)?(/.*)?$`) {
		t.Errorf("zap context:\n%s", s)
	}
}
