package scope

import (
	"encoding/json"
	"encoding/xml"
	"regexp"
	"strings"
	"testing"
)

var exportIn = []Target{
	{Kind: KindWildcard, Value: "*.example.com"},
	{Kind: KindWildcard, Value: "*-api-*.example.net"},
	{Kind: KindDomain, Value: "example.org"},
	{Kind: KindURL, Value: "https://app.example.org:8443/api/v2"},
	{Kind: KindURL, Value: "http://legacy.example.org/"},
	{Kind: KindIP, Value: "192.0.2.10"},
	{Kind: KindCIDR, Value: "198.51.100.0/24"},
	{Kind: KindDomain, Value: "example.org"}, // listed twice
}
var exportOut = []Target{{Kind: KindDomain, Value: "admin.example.com"}}

func TestBurpScope(t *testing.T) {
	b, err := BurpScope(exportIn, exportOut)
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Target struct {
			Scope struct {
				AdvancedMode bool       `json:"advanced_mode"`
				Include      []BurpRule `json:"include"`
				Exclude      []BurpRule `json:"exclude"`
			} `json:"scope"`
		} `json:"target"`
	}
	if err := json.Unmarshal(b, &doc); err != nil {
		t.Fatal(err)
	}
	sc := doc.Target.Scope
	if !sc.AdvancedMode || len(sc.Include) != 7 || len(sc.Exclude) != 1 {
		t.Fatalf("scope = %+v", sc)
	}
	// included reports whether a whole-host rule (no port or path) covers host.
	included := func(host string) bool {
		for _, r := range sc.Include {
			if r.File == "" && r.Port == "" && strings.HasPrefix(r.Host, "^") && regexp.MustCompile(r.Host).MatchString(host) {
				return true
			}
		}
		return false
	}
	if r := sc.Include[3]; r.Protocol != "https" || r.Port != "^8443$" || r.File != `^/api/v2.*` || r.Host != `^app\.example\.org$` {
		t.Errorf("URL rule = %+v", r)
	}
	if r := sc.Include[4]; r.Protocol != "http" || r.File != "" {
		t.Errorf("URL rule without a path = %+v", r)
	}
	if sc.Include[6].Host != "198.51.100.0/24" {
		t.Errorf("CIDR rule = %+v", sc.Include[6])
	}
	for host, want := range map[string]bool{"a.example.com": true, "a.b.example.com": true, "example.com": false, "badexample.com": false,
		"x-api-1.example.net": true, "example.org": true, "a.example.org": false, "192.0.2.10": true, "192.0.2.100": false} {
		if got := included(host); got != want {
			t.Errorf("Burp include of %s = %v, want %v", host, got, want)
		}
	}
	if !regexp.MustCompile(sc.Exclude[0].Host).MatchString("admin.example.com") {
		t.Errorf("exclude = %+v", sc.Exclude)
	}
}

func TestZAPContext(t *testing.T) {
	b, err := ZAPContext(`Acme & "Co" <x>`, exportIn, exportOut)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(string(b), `<?xml version="1.0" encoding="UTF-8" standalone="no"?>`) {
		t.Errorf("no XML declaration: %.80s", b)
	}
	var doc struct {
		Context struct {
			Name    string   `xml:"name"`
			InScope bool     `xml:"inscope"`
			Inc     []string `xml:"incregexes"`
			Exc     []string `xml:"excregexes"`
			Tech    []string `xml:"tech>include"`
			Parser  string   `xml:"urlparser>class"`
		} `xml:"context"`
	}
	if err := xml.Unmarshal(b, &doc); err != nil {
		t.Fatal(err)
	}
	c := doc.Context
	if c.Name != `Acme & "Co" <x>` || !c.InScope || len(c.Inc) != 6 || len(c.Exc) != 1 || len(c.Tech) != 5 || c.Parser == "" {
		t.Fatalf("context = %+v", c)
	}
	if !strings.Contains(string(b), "<!-- IP ranges in scope (ZAP regexes can't express them): 198.51.100.0/24 -->") {
		t.Error("CIDR isn't noted")
	}
	matches := func(u string, res []string) bool {
		for _, r := range res {
			if regexp.MustCompile(r).MatchString(u) {
				return true
			}
		}
		return false
	}
	for u, want := range map[string]bool{
		"https://a.example.com/login": true, "http://a.b.example.com:8080": true, "https://example.com/": false,
		"https://evil.com/?x=a.example.com": false, "https://x-api-y.example.net/": true,
		"https://example.org": true, "https://app.example.org:8443/api/v2/users": true, "https://app.example.org/api/v2": false,
		"https://app.example.org:8443/other": false, "http://legacy.example.org/x": true, "https://legacy.example.org/x": false,
		"http://192.0.2.10/": true,
	} {
		if got := matches(u, c.Inc); got != want {
			t.Errorf("ZAP include of %s = %v, want %v", u, got, want)
		}
	}
	for _, r := range append(c.Inc, c.Exc...) {
		if strings.Contains(r, ",") {
			t.Errorf("regex %q has a comma, which ZAP can't reload", r)
		}
	}
	if !matches("https://admin.example.com/", c.Exc) {
		t.Errorf("exclude = %q", c.Exc)
	}
}
