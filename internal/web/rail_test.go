package web

import (
	"net/http"
	"strings"
	"testing"
)

// Active counts the filters on: not views, nor a list's default.
func TestRailActive(t *testing.T) {
	v := railView{Groups: []railGroup{
		{Options: []railOption{{On: true, View: true}, {}}},
		{Options: []railOption{{On: true}, {On: true, Default: true}, {On: true}}},
	}}
	if got := v.Active(); got != 2 {
		t.Errorf("Active = %d, want 2", got)
	}
}

// The rail's Filters button says how many filters are on, and controls
// the groups it folds away.
func TestRailFoldButton(t *testing.T) {
	e := newTestEnv(t)
	tests := []struct {
		path, want string
	}{
		{"/programs", `<span class="hide">Hide filters</span><svg class="i chev">`},
		{"/programs?bounty=1&wild=1", `<span class="hide">Hide filters</span><b title="2 filters on">2</b>`},
		{"/programs?bounty=1", `<b title="1 filter on">1</b>`},
	}
	for _, tt := range tests {
		_, body := e.do(e.client(), http.MethodGet, tt.path, nil, nil)
		if !strings.Contains(body, `aria-controls="fgroups" aria-expanded="false" data-filters-toggle>`) ||
			!strings.Contains(body, `<div class="fgroups" id="fgroups">`) || !strings.Contains(body, tt.want) {
			i := strings.Index(body, "data-filters-toggle")
			t.Errorf("%s: button %s", tt.path, body[max(i, 0):min(len(body), max(i, 0)+400)])
		}
	}
}

// Clearing filters, or dropping one, keeps the search; only its own pill
// (or emptying the box) drops it. The views keep it too.
func TestClearingFiltersKeepsSearch(t *testing.T) {
	e := newTestEnv(t)
	tests := []struct {
		path      string
		want, not []string
	}{
		{"/programs?q=acme&platform=fake&bounty=1&sort=bounty", []string{
			`<a class="link small" href="/programs?q=acme&amp;sort=bounty">Clear filters</a>`,
			`<a class="af" href="/programs?platform=fake&amp;q=acme&amp;sort=bounty" title="Remove this filter">Pays bounties`,
			`<a class="af" href="/programs?bounty=1&amp;platform=fake&amp;sort=bounty" title="Remove this filter">“acme”`,
			`<a class="fc" href="/programs?bounty=1&amp;platform=fake&amp;q=acme&amp;sort=huntscore"`}, nil}, // views keep the platforms too
		// A view stays on beside a search, and turning it off keeps the search.
		{"/programs?bounty=1&sort=huntscore&q=acme", []string{`<a class="fc on" href="/programs?q=acme" title="Highest HuntScore among paying programs" aria-current="page">`}, nil},
		// A search alone has nothing to clear but itself.
		{"/programs?q=nomatch", []string{"No programs match this search.", `<a class="af" href="/programs" title="Remove this filter">“nomatch”`}, []string{"Clear filters"}},
		{"/targets?q=acme&kind=domain&platform=fake", []string{`<a class="link small" href="/targets?kind=domain&amp;q=acme">Clear filters</a>`}, nil},
		{"/disclosed?q=xss&platform=fake", []string{`<a class="link small" href="/disclosed?q=xss">Clear filters</a>`}, nil},
	}
	for _, tt := range tests {
		_, body := e.do(e.client(), http.MethodGet, tt.path, nil, nil)
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
}
