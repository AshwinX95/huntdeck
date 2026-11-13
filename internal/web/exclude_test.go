package web

import (
	"net/http"
	"slices"
	"strings"
	"testing"
)

func TestSearchTerms(t *testing.T) {
	for _, tt := range []struct {
		q, not, find string
		exclude      []string
	}{
		{"acme", "", "acme", nil},
		{"acme -staging -Dev", "", "acme", []string{"staging", "dev"}},
		{"-", "", "-", nil},
		{"", "test, sandbox  -qa", "", []string{"test", "sandbox", "qa"}},
		{"-x", "x", "", []string{"x"}},
	} {
		find, ex := searchTerms(tt.q, tt.not)
		if find != tt.find || !slices.Equal(ex, tt.exclude) {
			t.Errorf("searchTerms(%q, %q) = %q, %q", tt.q, tt.not, find, ex)
		}
	}
}

// Programs leaves out names with excluded words, from the search's -words
// or the field beside it, and its views keep them.
func TestProgramsExclude(t *testing.T) {
	e := newTestEnv(t)
	c := e.client()
	count := func(path string) (int, string) {
		_, body := e.do(c, http.MethodGet, path, nil, nil)
		return strings.Count(body, `<tr class="prow"`), body
	}
	all, _ := count("/programs")
	for _, path := range []string{"/programs?q=-acme", "/programs?not=ACME", "/programs?not=nothing,acme"} {
		n, body := count(path)
		if n != all-1 || strings.Contains(body, `data-href="/programs/fake/acme"`) {
			t.Errorf("%s: %d rows of %d, or still Acme", path, n, all)
		}
	}
	_, body := count("/programs?not=acme")
	for _, want := range []string{`name="not" value="acme"`, `<a class="af" href="/programs" title="Remove this filter">Without “acme”`,
		`href="/programs?bounty=1&amp;not=acme&amp;sort=huntscore"`} {
		if !strings.Contains(body, want) {
			t.Errorf("lacks %s", want)
		}
	}
	if n, _ := count("/programs?q=acme+-corp"); n != 0 {
		t.Errorf("acme without corp = %d rows", n)
	}
}

func TestMinBounty(t *testing.T) {
	for in, want := range map[string]float64{"5000": 5000, "2.5k": 2500, "2,500": 2500, "$750": 750, "": 0, "-5": 0, "abc": 0, "1e12": maxMinBounty, "NaN": 0} {
		if got := minBounty(in); got != want {
			t.Errorf("minBounty(%q) = %v, want %v", in, got, want)
		}
	}
}

// A minimum bounty of one's own sits beside the chips, as a pill, and
// views keep it: Quiet programs paying 2.5k or more.
func TestProgramsCustomMinBounty(t *testing.T) {
	e := newTestEnv(t)
	_, body := e.do(e.client(), http.MethodGet, "/programs?top=2.5k&bounty=1&sort=quiet", nil, nil)
	for _, want := range []string{`<form class="fform" method="get" action="/programs">`, `name="top" value="2500"`,
		`<input type="hidden" name="sort" value="quiet">`, `Top bounty 2.5k or more <span aria-hidden="true">×</span>`,
		// Quiet programs is on, and keeps the amount; others switch to theirs with it.
		`<a class="fc on" href="/programs?top=2.5k" title="Least competition first`,
		`href="/programs?bounty=1&amp;sort=huntscore&amp;top=2.5k"`} {
		if !strings.Contains(body, want) {
			t.Errorf("lacks %s", want)
		}
	}
	_, body = e.do(e.client(), http.MethodGet, "/programs?top=5k", nil, nil)
	if !strings.Contains(body, `<a class="fc on" href="/programs" title="Top bounty, in the program&#39;s own currency" aria-current="true"><span class="box" aria-hidden="true"></span><span class="fl">5k or more`) {
		t.Error("a typed amount matching a chip doesn't turn it on")
	}
}
