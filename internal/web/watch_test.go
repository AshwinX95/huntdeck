package web

import (
	"encoding/xml"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/mdpsec/huntdeck/internal/store"
)

func TestWatchParam(t *testing.T) {
	for _, tt := range []struct {
		in   string
		want []store.ProgramKey
	}{
		{"", nil},
		{"h1/acme", []store.ProgramKey{{Platform: "hackerone", Handle: "acme"}}},
		{"fake/acme, fake/acme,bugcrowd/x", []store.ProgramKey{{Platform: "fake", Handle: "acme"}, {Platform: "bugcrowd", Handle: "x"}}},
		{"acme,/acme,fake/,fake/a/b,fake/a?b", nil},
	} {
		if got := watchParam(tt.in); !slices.Equal(got, tt.want) {
			t.Errorf("watchParam(%q) = %v, want %v", tt.in, got, tt.want)
		}
	}
	var long []string
	for i := range watchMax + 5 {
		long = append(long, "fake/p"+strconv.Itoa(i))
	}
	if n := len(watchParam(strings.Join(long, ","))); n != watchMax {
		t.Errorf("long watchlist kept %d, want %d", n, watchMax)
	}
}

// ?watch= keeps Programs, Changes and the changes feed to the programs a
// browser watches; the rail's watchlist choice waits hidden for watch.js.
func TestWatchlist(t *testing.T) {
	e := newTestEnv(t)
	c := e.client()
	_, body := e.do(c, http.MethodGet, "/programs", nil, nil)
	if !strings.Contains(body, `<div class="facet" data-watch-group hidden>`) || strings.Count(body, `<tr class="prow"`) < 2 {
		t.Errorf("programs: no hidden watchlist group, or too few programs")
	}
	_, body = e.do(c, http.MethodGet, "/programs?watch=fake/acme", nil, nil)
	if n := strings.Count(body, `<tr class="prow"`); n != 1 || !strings.Contains(body, `data-href="/programs/fake/acme"`) ||
		!strings.Contains(body, `<div class="facet" data-watch-group>`) || !strings.Contains(body, `data-watch-link="on"`) ||
		!strings.Contains(body, `<a class="af" href="/programs" title="Remove this filter">Your watchlist`) {
		t.Errorf("watched programs: %d rows, or no watchlist choice and pill", n)
	}
	// A view keeps the watchlist.
	if !strings.Contains(body, `href="/programs?bounty=1&amp;sort=huntscore&amp;watch=fake%2Facme"`) {
		t.Error("views drop the watchlist")
	}
	_, body = e.do(c, http.MethodGet, "/changes?since=all&watch=fake/acme", nil, nil)
	if !strings.Contains(body, `data-watch-link="on"`) || strings.Count(body, "<tr data-at=") == 0 || strings.Contains(body, `href="/programs/fake/evil"`) {
		t.Error("watched changes: none, or other programs'")
	}
	if !strings.Contains(body, `href="/changes.atom?watch=fake%2Facme"`) {
		t.Error("changes feed drops the watchlist")
	}
	res, body := e.do(c, http.MethodGet, "/changes.atom?watch=fake/acme", nil, nil)
	var f atomFeed
	if err := xml.Unmarshal([]byte(body), &f); err != nil || res.StatusCode != http.StatusOK || len(f.Entries) == 0 {
		t.Fatalf("watched feed = %d, %v", res.StatusCode, err)
	}
	for _, en := range f.Entries {
		if !strings.HasPrefix(en.Title, "Acme Corp: ") {
			t.Errorf("watched feed has %q", en.Title)
		}
	}
	_, body = e.do(c, http.MethodGet, "/programs/fake/acme", nil, nil)
	if !strings.Contains(body, `data-watch="fake/acme" aria-pressed="false"`) {
		t.Error("program page has no Watch button")
	}
}
