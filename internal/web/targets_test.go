package web

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/mdpsec/huntdeck/internal/platform"
	"github.com/mdpsec/huntdeck/internal/scope"
	"github.com/mdpsec/huntdeck/internal/syncer"
)

func TestSearchRoutesHostsToMatch(t *testing.T) {
	e := newTestEnv(t)
	c := e.client()
	for _, tt := range []struct{ q, want string }{
		{"", "/programs"},
		{"acme", "/programs?q=acme&state=all"},
		{"Acme Corp", "/programs?q=Acme+Corp&state=all"},
		{"app.acme.example", "/match?q=app.acme.example"},
		{"https://shop.acme.example/x", "/match?q=https%3A%2F%2Fshop.acme.example%2Fx"},
		{"2001:db8::1", "/match?q=2001%3Adb8%3A%3A1"},
		{"acme.", "/match?q=acme."},         // no program's name: the scope check
		{"acme-apps", "/match?q=acme-apps"}, // finds scope mentioning it
	} {
		res, _ := e.do(c, "GET", "/search?"+url.Values{"q": {tt.q}}.Encode(), nil, nil)
		if res.StatusCode != http.StatusSeeOther || res.Header.Get("Location") != tt.want {
			t.Errorf("search %q: %d → %s, want %s", tt.q, res.StatusCode, res.Header.Get("Location"), tt.want)
		}
	}
}

func TestAddedColumnAndFilter(t *testing.T) {
	e := newTestEnv(t)
	c := e.client()
	_, body := e.do(c, http.MethodGet, "/programs/fake/acme?tab=scope&order=added", nil, nil)
	for _, want := range []string{`<th class="sc-added">Added</th>`, `<a class="link small" href="?tab=scope" title="Switch the order">newest first</a>`,
		`or when HuntDeck saw it appear)`, `<input type="hidden" name="order" value="added">`} {
		if !strings.Contains(body, want) {
			t.Errorf("program page lacks %q", want)
		}
	}
	_, body = e.do(c, http.MethodGet, "/targets?kind=urls&added=7d", nil, nil)
	if !strings.Contains(body, `<a class="fc on" href="/targets?kind=urls" title="Added to its program in the last 7 days`) || !strings.Contains(body, "api.acme.example") {
		t.Errorf("targets page with added=7d lacks the filter or the new URL")
	}
	if !strings.Contains(body, "added=7d") {
		t.Errorf("export link drops the added filter")
	}
	_, body = e.do(c, http.MethodGet, "/api/v1/targets?kind=urls&added=7d", nil, nil)
	if !strings.Contains(body, "api.acme.example") {
		t.Errorf("plain-text export with added=7d = %q", body)
	}
}

func TestRootDomains(t *testing.T) {
	e := newTestEnv(t)
	c := e.client()
	for _, path := range []string{"/api/v1/targets?kind=roots", "/api/v1/targets?kind=root&scope=out"} {
		// Out-of-scope roots aren't offered: roots always come from in-scope targets.
		if _, body := e.do(c, http.MethodGet, path, nil, nil); body != "acme.example\nevil.example\n" {
			t.Errorf("%s = %q", path, body)
		}
	}
	_, body := e.do(c, http.MethodGet, "/targets?kind=root", nil, nil)
	for _, want := range []string{"2 root domains", "a root domain itself is often not in scope", `<a class="fc on" href="/targets" title="Root domains of`, "acme.example"} {
		if !strings.Contains(body, want) {
			t.Errorf("targets page lacks %q", want)
		}
	}
	// One row per program and root, however many targets share it.
	_, body = e.do(c, http.MethodGet, "/api/v1/targets?kind=roots&format=json&platform=fake&q=acme", nil, nil)
	var roots struct {
		Total   int `json:"total"`
		Targets []struct {
			Kind, Value string
		} `json:"targets"`
	}
	if err := json.Unmarshal([]byte(body), &roots); err != nil || roots.Total != 1 || len(roots.Targets) != 1 ||
		roots.Targets[0].Kind != "root" || roots.Targets[0].Value != "acme.example" {
		t.Errorf("roots json = %v %s", err, body)
	}

	// Another program under the same root gets its own row.
	e.fake.SetScope("evil",
		platform.ScopeEntry{PlatformID: "3", InScope: true, AssetType: scope.AssetURL, Identifier: "app.evil.example"},
		platform.ScopeEntry{PlatformID: "5", InScope: true, AssetType: scope.AssetCIDR, Identifier: "192.0.2.0/24"},
		platform.ScopeEntry{PlatformID: "6", InScope: true, AssetType: scope.AssetDomain, Identifier: "shop.acme.example"},
		platform.ScopeEntry{PlatformID: "7", InScope: true, AssetType: scope.AssetWildcard, Identifier: "*.shop.acme.example"})
	if _, err := e.syncer.Poll(context.Background(), e.fake, syncer.Options{Mode: syncer.ModeFull, Trigger: "manual"}); err != nil {
		t.Fatal(err)
	}
	_, body = e.do(c, http.MethodGet, "/api/v1/targets?kind=roots&format=json&q=acme", nil, nil)
	var shared struct {
		Total   int `json:"total"`
		Targets []struct {
			Value   string `json:"value"`
			Program struct {
				Handle string `json:"handle"`
			} `json:"program"`
		} `json:"targets"`
	}
	var got []string
	if err := json.Unmarshal([]byte(body), &shared); err != nil {
		t.Fatal(err)
	}
	for _, r := range shared.Targets {
		got = append(got, r.Program.Handle+" "+r.Value)
	}
	if shared.Total != 2 || strings.Join(got, ",") != "acme acme.example,evil acme.example" {
		t.Errorf("shared root = %d %v", shared.Total, got)
	}
}

func TestScopeCheckShowsRemovedScope(t *testing.T) {
	e := newTestEnv(t)
	c := e.client()
	ctx := context.Background()
	// acme drops its API URL; evil goes away altogether.
	acme := platform.Program{Handle: "acme", Name: "Acme Corp", URL: "https://platform.example/acme", State: platform.StateOpen, OffersBounty: true}
	e.fake.SetPrograms(acme)
	e.fake.SetScope("acme",
		platform.ScopeEntry{PlatformID: "1", InScope: true, AssetType: scope.AssetWildcard, Identifier: "*.acme.example", BountyEligible: true},
		platform.ScopeEntry{PlatformID: "2", InScope: false, AssetType: scope.AssetURL, Identifier: "admin.acme.example"})
	if _, err := e.syncer.Poll(ctx, e.fake, syncer.Options{Mode: syncer.ModeFull, Trigger: "manual", ForceRemovals: true}); err != nil {
		t.Fatal(err)
	}

	_, body := e.do(c, http.MethodGet, "/match?q=https://api.acme.example/v2/users", nil, nil)
	for _, want := range []string{"*.acme.example", "Previously listed", "https://api.acme.example/v2"} {
		if !strings.Contains(body, want) {
			t.Errorf("scope check lacks %q", want)
		}
	}
	_, body = e.do(c, http.MethodGet, "/match?q=app.evil.example", nil, nil)
	if !strings.Contains(body, "lists <code>app.evil.example</code> any more") || !strings.Contains(body, "app.evil.example</td>") {
		t.Errorf("scope check of a removed program's host = %s", body)
	}
	if _, body = e.do(c, http.MethodGet, "/match?q=unrelated.example.org", nil, nil); strings.Contains(body, "Previously listed") || strings.Contains(body, "Under ") {
		t.Error("unrelated host shows removed scope or scope under it")
	}
	// The domain a wildcard is on isn't in scope itself, but the check
	// shows the wildcard, then what's listed under the domain, live only.
	_, body = e.do(c, http.MethodGet, "/match?q=ACME.example", nil, nil)
	under := body[strings.Index(body, "Under acme.example"):]
	wild, admin := strings.Index(under, `<td class="c-wide asset">*.acme.example</td>`), strings.Index(under, `<td class="c-wide asset">admin.acme.example</td>`)
	if !strings.Contains(under, "No program lists <code>acme.example</code> itself") ||
		!strings.Contains(under, "<code>*.acme.example</code> covers its subdomains, like <code>www.acme.example</code>, but not the bare domain.") ||
		strings.Contains(body, `class="empty-state"`) || wild < 0 || admin < wild || strings.Contains(under, "https://api.acme.example/v2</td>") ||
		!strings.Contains(under, `href="/targets?q=acme.example"`) || !strings.Contains(under, `<tr data-href="/programs/fake/acme">`) {
		t.Errorf("scope check of a wildcard's domain = %s", body)
	}
	// Text that isn't a listed host or under one finds the scope that
	// mentions it, as the Targets search does.
	_, body = e.do(c, http.MethodGet, "/match?q=ACME.ex", nil, nil)
	if i := strings.Index(body, "Mentioning acme.ex"); i < 0 || strings.Contains(body, `class="empty-state"`) ||
		!strings.Contains(body[i:], "No program lists <code>acme.ex</code> or anything under it") ||
		strings.Index(body[i:], `<td class="asset">*.acme.example</td>`) > strings.Index(body[i:], `<td class="asset">admin.acme.example</td>`) {
		t.Errorf("scope check of part of a host = %s", body)
	}
	if _, body = e.do(c, http.MethodGet, "/match?q=ac", nil, nil); strings.Contains(body, "Mentioning") || !strings.Contains(body, `class="empty-state"`) {
		t.Error("two letters look for scope mentioning them")
	}
	// A name search says how much scope mentions the words; finding no
	// program, it offers the scope check.
	if _, body = e.do(c, http.MethodGet, "/programs?q=acme&state=all", nil, nil); !strings.Contains(body,
		`<div class="note-bar">“acme” is also in 2 scope rules. <a class="link" href="/match?q=acme">Check scope →</a></div>`) {
		t.Errorf("a name search doesn't count the scope mentioning it: %s", body)
	}
	if _, body = e.do(c, http.MethodGet, "/programs?q=acme-apps&state=all", nil, nil); !strings.Contains(body, `<a class="link" href="/match?q=acme-apps">Check scope mentioning “acme-apps”</a>`) {
		t.Error("a name search finding nothing doesn't offer the scope check")
	}
	var near struct {
		Matches []struct{} `json:"matches"`
		Under   []struct {
			Via     string `json:"via"`
			InScope bool   `json:"in_scope"`
		} `json:"under"`
		UnderTotal int `json:"under_total"`
	}
	if _, body = e.do(c, http.MethodGet, "/api/v1/match?host=acme.example", nil, nil); json.Unmarshal([]byte(body), &near) != nil ||
		len(near.Matches) != 0 || near.UnderTotal != 2 || len(near.Under) != 2 || near.Under[0].Via != "*.acme.example" || !near.Under[0].InScope ||
		near.Under[1].Via != "admin.acme.example" || near.Under[1].InScope {
		t.Errorf("API scope check of a wildcard's domain = %s", body)
	}
	var mention struct {
		Under      []struct{} `json:"under"`
		Mentioning []struct {
			Via string `json:"via"`
		} `json:"mentioning"`
		MentioningTotal int `json:"mentioning_total"`
	}
	if _, body = e.do(c, http.MethodGet, "/api/v1/match?host=acme", nil, nil); json.Unmarshal([]byte(body), &mention) != nil ||
		len(mention.Under) != 0 || mention.MentioningTotal != 2 || len(mention.Mentioning) != 2 || mention.Mentioning[0].Via != "*.acme.example" {
		t.Errorf("API scope check of part of a host = %s", body)
	}

	var res struct {
		Matches []struct{} `json:"matches"`
		Past    []struct {
			InScope    bool      `json:"in_scope"`
			Identifier string    `json:"identifier"`
			RemovedAt  time.Time `json:"removed_at"`
		} `json:"past"`
	}
	_, body = e.do(c, http.MethodGet, "/api/v1/match?host=app.evil.example", nil, nil)
	if err := json.Unmarshal([]byte(body), &res); err != nil || len(res.Matches) != 0 || len(res.Past) != 1 ||
		!res.Past[0].InScope || res.Past[0].Identifier != "app.evil.example" || res.Past[0].RemovedAt.IsZero() {
		t.Errorf("match json = %v %s", err, body)
	}
}

func TestBestNewTargets(t *testing.T) {
	e := newTestEnv(t)
	c := e.client()
	ctx := context.Background()
	e.fake.SetScope("evil", platform.ScopeEntry{PlatformID: "3", InScope: true, AssetType: scope.AssetURL, Identifier: "app.evil.example"},
		platform.ScopeEntry{PlatformID: "5", InScope: true, AssetType: scope.AssetCIDR, Identifier: "192.0.2.0/24"},
		platform.ScopeEntry{PlatformID: "6", InScope: true, AssetType: scope.AssetWildcard, Identifier: "*.new.evil.example", BountyEligible: true})
	if _, err := e.syncer.Poll(ctx, e.fake, syncer.Options{Mode: syncer.ModeFull, Trigger: "manual"}); err != nil {
		t.Fatal(err)
	}

	_, body := e.do(c, http.MethodGet, "/targets?kind=wildcards&sort=best", nil, nil)
	for _, want := range []string{`<a class="qv on" href="/targets?kind=wildcards&amp;sort=best"`, `<td class="c-target asset" title="*.new.evil.example&#10;Wildcard added`,
		`<td class="c-score num" data-label="Score"><b class="score" title="Wildcard added`, "&lt;script&gt;", "sort=best"} {
		if !strings.Contains(body, want) {
			t.Errorf("best targets page lacks %q", want)
		}
	}
	if strings.Contains(body, "%!") {
		t.Error("best targets page has a formatting error")
	}
	if strings.Contains(body, "*.acme.example") {
		t.Error("a target of unknown age is among the best new targets")
	}
	_, body = e.do(c, http.MethodGet, "/targets?kind=wildcards&sort=best&scope=out", nil, nil)
	if !strings.Contains(body, "Only in-scope targets added in the last 90 days") {
		t.Error("out-of-scope best targets don't explain why they're empty")
	}
	_, body = e.do(c, http.MethodGet, "/targets?kind=wildcards", nil, nil)
	if !strings.Contains(body, `href="/targets?kind=wildcards&amp;sort=best"`) || !strings.Contains(body, `<a class="qv on" href="/targets?kind=wildcards" title="Most recently added`) ||
		!strings.Contains(body, `<em>1</em>`) || !strings.Contains(body, `<tr data-href="/programs/fake/`) {
		t.Error("the list lacks the best new targets link, the newest-first sort, or the rail's counts")
	}

	if _, body = e.do(c, http.MethodGet, "/api/v1/targets?kind=roots&sort=best", nil, nil); body != "acme.example\nevil.example\n" {
		t.Errorf("best roots txt = %q", body)
	}
	var page struct {
		Total   int `json:"total"`
		Targets []struct {
			Value   string   `json:"value"`
			Score   *float64 `json:"score"`
			Reasons []string `json:"reasons"`
		} `json:"targets"`
	}
	_, body = e.do(c, http.MethodGet, "/api/v1/targets?kind=wildcards&sort=best&format=json", nil, nil)
	if err := json.Unmarshal([]byte(body), &page); err != nil || page.Total != 1 || page.Targets[0].Value != "*.new.evil.example" ||
		page.Targets[0].Score == nil || *page.Targets[0].Score <= 0 || len(page.Targets[0].Reasons) < 3 {
		t.Errorf("best targets JSON = %+v, %v", page, err)
	}
}

// The Targets list's Bounty column gives each bounty-eligible target's
// range (its own bounty table row first), VDP for programs that don't pay,
// and sorts highest first, turning around on a second click.
func TestTargetsBountyColumn(t *testing.T) {
	e := newTestEnv(t)
	c := e.client()
	tests := []struct {
		path     string
		want     []string
		wantNone []string
	}{
		{"/targets?kind=wildcards&sort=bounty", []string{`<a class="sort on" href="/targets?kind=wildcards&amp;rev=1&amp;sort=bounty"`, `<span class="amt">$200 – $5k</span>`, `From the program's bounty table`}, nil},
		{"/targets?kind=wildcards&sort=bounty&rev=1", []string{`<a class="sort on rev" href="/targets?kind=wildcards&amp;sort=bounty"`}, nil},
		{"/targets?kind=domains", []string{`<span class="chip vdp" title="Vulnerability disclosure, no bounties">VDP</span>`, `<a class="sort" href="/targets?kind=domains&amp;sort=bounty"`}, []string{"Flags"}},
	}
	for _, tt := range tests {
		_, body := e.do(c, http.MethodGet, tt.path, nil, nil)
		for _, w := range tt.want {
			if !strings.Contains(body, w) {
				t.Errorf("%s lacks %q", tt.path, w)
			}
		}
		for _, w := range tt.wantNone {
			if strings.Contains(body, w) {
				t.Errorf("%s has %q", tt.path, w)
			}
		}
	}
}

// "All kinds" lists every kind of target at once, as the hunt board's
// "All N" link asks; best new targets say the period they're filtered to.
func TestTargetsAllKinds(t *testing.T) {
	e := newTestEnv(t)
	c := e.client()
	tests := []struct {
		path      string
		want, not []string
	}{
		{"/targets?kind=all", []string{"*.acme.example", "app.evil.example", "https://api.acme.example/v2", "192.0.2.0/24",
			`aria-current="true"><span class="box" aria-hidden="true"></span><span class="fl">All kinds</span>`}, nil},
		{"/targets", []string{"*.acme.example"}, []string{"app.evil.example"}}, // wildcards by default
		{"/targets?kind=all&sort=best&added=14d&bounty=1", []string{"best new: in scope, added in the last 14 days"}, []string{"Since "}},
		{"/targets?kind=all&sort=best", []string{"best new: in scope, added in the last 90 days"}, nil},
	}
	for _, tt := range tests {
		_, body := e.do(c, http.MethodGet, tt.path, nil, nil)
		for _, w := range tt.want {
			if !strings.Contains(body, w) {
				t.Errorf("%s lacks %s", tt.path, w)
			}
		}
		for _, n := range tt.not {
			if strings.Contains(body, n) {
				t.Errorf("%s has %s", tt.path, n)
			}
		}
	}
	if _, body := e.do(c, http.MethodGet, "/api/v1/targets?kind=all", nil, nil); !strings.Contains(body, "*.acme.example\n") || !strings.Contains(body, "app.evil.example\n") {
		t.Errorf("API kind=all = %q", body)
	}
}

// The export and copy links carry the page's search with its filters, and
// the plain-text export keeps to it, by value or by score.
func TestTargetsExportKeepsSearch(t *testing.T) {
	e := newTestEnv(t)
	c := e.client()
	_, body := e.do(c, http.MethodGet, "/targets?kind=all&q=ACME&platform=fake&sort=best", nil, nil)
	if !strings.Contains(body, `data-copy-url="/api/v1/targets?format=txt&amp;kind=all&amp;platform=fake&amp;q=ACME&amp;sort=best"`) {
		t.Error("copy link drops the search or a filter")
	}
	for _, path := range []string{"/api/v1/targets?kind=all&q=ACME", "/api/v1/targets?kind=all&q=ACME&sort=best"} {
		_, body := e.do(c, http.MethodGet, path, nil, nil)
		lines := strings.Fields(body)
		if len(lines) == 0 {
			t.Errorf("%s: nothing", path)
		}
		for _, l := range lines {
			if !strings.Contains(l, "acme") {
				t.Errorf("%s has %q", path, l)
			}
		}
	}
}
