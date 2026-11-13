package web

import (
	"context"
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/mdpsec/huntdeck/internal/platform"
	"github.com/mdpsec/huntdeck/internal/platform/independent"
	"github.com/mdpsec/huntdeck/internal/scope"
)

// Program changes split status changes by where the program went, and
// the list sorts by date, program name or platform.
func TestChangesProgramKindsAndSort(t *testing.T) {
	e := newTestEnv(t)
	c := e.client()
	// Two more programs with changes: the first save is a baseline, the
	// second adds scope.
	own := func(extra bool) []independent.Program {
		var out []independent.Program
		for _, n := range []string{"Zeta Labs", "Alpha Labs"} {
			h := strings.ToLower(strings.Fields(n)[0])
			sc := []platform.ScopeEntry{{InScope: true, AssetType: scope.AssetWildcard, Identifier: "*." + h + ".example"}}
			if extra {
				sc = append(sc, platform.ScopeEntry{InScope: true, AssetType: scope.AssetDomain, Identifier: "api." + h + ".example"})
			}
			out = append(out, independent.Program{Program: platform.Program{Handle: h, Name: n, URL: "https://" + h + ".example/", State: platform.StateOpen}, Scope: sc})
		}
		return out
	}
	for _, extra := range []bool{false, true} {
		if err := e.syncer.SaveOwn(context.Background(), own(extra)); err != nil {
			t.Fatal(err)
		}
	}
	_, body := e.do(c, http.MethodGet, "/changes?since=all&group=programs", nil, nil)
	for _, want := range []string{`<div class="ft">Program changes</div>`, `href="/changes?change=paused&amp;group=programs&amp;since=all"`,
		`href="/changes?change=opened&amp;group=programs&amp;since=all"`, `<span class="sorts">Sort by `,
		`<a class="sortopt on" href="/changes?group=programs&amp;rev=1&amp;since=all" title="Newest first; again for oldest first" aria-current="true">Date <small>newest first</small></a>`,
		`<a class="sortopt" href="/changes?group=programs&amp;since=all&amp;sort=name"`} {
		if !strings.Contains(body, want) {
			t.Errorf("lacks %s", want)
		}
	}
	// Choosing a kind drops the sub-filter.
	_, body = e.do(c, http.MethodGet, "/changes?since=all&group=programs&change=paused", nil, nil)
	if !strings.Contains(body, `href="/changes?group=scope&amp;since=all"`) {
		t.Error("a kind keeps the program-change filter")
	}
	names := func(path string) []string {
		_, body := e.do(c, http.MethodGet, path, nil, nil)
		var out []string
		for _, part := range strings.Split(body, `<a class="name" href="`)[1:] {
			name := part[strings.Index(part, `">`)+2 : strings.Index(part, "</a>")]
			out = append(out, strings.ToLower(name))
		}
		return out
	}
	byName := names("/changes?since=all&sort=name")
	if len(byName) < 3 || !slices.IsSorted(byName) || byName[0] != "acme corp" {
		t.Errorf("by name = %q", byName)
	}
	back := names("/changes?since=all&sort=name&rev=1")
	if len(back) < 3 || back[0] != "zeta labs" || back[len(back)-1] != "acme corp" {
		t.Errorf("by name, Z to A = %q", back)
	}
}

func TestChangesByPlatform(t *testing.T) {
	e := newTestEnv(t)
	if err := e.syncer.SaveOwn(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
	_, body := e.do(e.client(), http.MethodGet, "/changes?since=all&sort=platform", nil, nil)
	if !strings.Contains(body, `aria-current="true">Platform <small>A–Z</small>`) {
		t.Error("platform order isn't marked")
	}
}
