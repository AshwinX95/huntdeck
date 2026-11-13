package web

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/mdpsec/huntdeck/internal/platform"
	"github.com/mdpsec/huntdeck/internal/scope"
	"github.com/mdpsec/huntdeck/internal/syncer"
)

// The Changes page narrows scope changes to added, removed or edited
// assets, and lists changes to paying programs or VDPs, as Programs does,
// each combining with the others, the period and the platform.
func TestChangesScopeAndBountyFilters(t *testing.T) {
	e := newTestEnv(t) // acme (paying) has added https://api.acme.example/v2
	c := e.client()
	// evil, a VDP, adds an asset and drops another.
	e.fake.SetScope("evil",
		platform.ScopeEntry{PlatformID: "3", InScope: true, AssetType: scope.AssetURL, Identifier: "app.evil.example",
			Instruction: `<img src=x onerror=alert(1)>`},
		platform.ScopeEntry{PlatformID: "8", InScope: true, AssetType: scope.AssetDomain, Identifier: "shop.evil.example"})
	if _, err := e.syncer.Poll(context.Background(), e.fake, syncer.Options{Mode: syncer.ModeFull, Trigger: "manual"}); err != nil {
		t.Fatal(err)
	}

	const acmeAdd, evilAdd, evilGone = "https://api.acme.example/v2", "shop.evil.example", "192.0.2.0/24"
	tests := []struct {
		path      string
		want, not []string
	}{
		{"/changes?group=scope&since=24h", []string{acmeAdd, evilAdd, evilGone, "3 changes</span>",
			`<a class="fc" href="/changes?group=scope&amp;scope=added&amp;since=24h" title="Assets new to a program&#39;s scope"><span class="box" aria-hidden="true"></span><span class="fl">Assets added</span><em>2</em></a>`,
			`<span class="fl">Assets removed</span><em>1</em>`, `<span class="fl">Assets edited</span><em>0</em>`,
			`<span class="fl">Pays bounties</span><em>1</em>`, `<span class="fl">VDP only</span><em>2</em>`}, nil},
		{"/changes?group=scope&scope=added&since=24h&platform=fake", []string{acmeAdd, evilAdd, "2 changes</span>",
			// Choosing another kind drops the scope filter.
			`href="/changes?group=programs&amp;platform=fake&amp;since=24h"`,
			`<span class="fl">Pays bounties</span><em>1</em>`, `<span class="fl">VDP only</span><em>1</em>`},
			[]string{evilGone}},
		{"/changes?group=scope&scope=removed&since=24h", []string{evilGone, "1 change</span>"}, []string{acmeAdd, evilAdd}},
		{"/changes?group=scope&scope=added&bounty=1&since=24h", []string{acmeAdd, "1 change</span>"}, []string{evilAdd, evilGone}},
		{"/changes?group=scope&scope=added&bounty=vdp&since=24h", []string{evilAdd, "1 change</span>",
			`<span class="fl">Assets added</span><em>1</em>`, `<span class="fl">Assets removed</span><em>1</em>`}, []string{acmeAdd, evilGone}},
		{"/changes?bounty=vdp&since=all", []string{evilAdd, evilGone}, []string{acmeAdd}},
		// The scope filter is for scope changes only.
		{"/changes?scope=added&since=all", []string{acmeAdd, evilGone}, []string{`<span class="fl">Assets added</span>`}},
	}
	for _, tt := range tests {
		res, body := e.do(c, http.MethodGet, tt.path, nil, nil)
		if res.StatusCode != http.StatusOK {
			t.Errorf("GET %s = %d", tt.path, res.StatusCode)
			continue
		}
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

	_, body := e.do(c, http.MethodGet, "/api/v1/changes?since=24h&kind=scope_added&bounty=vdp", nil, nil)
	var out apiChangeListJSON
	if err := json.Unmarshal([]byte(body), &out); err != nil || out.Total != 1 || !strings.Contains(out.Changes[0].Description+out.Changes[0].NewValue, evilAdd) {
		t.Errorf("API changes bounty=vdp = %v %s", err, body)
	}
}
