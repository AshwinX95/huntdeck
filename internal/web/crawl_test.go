package web

import (
	"fmt"
	"net/http"
	"strings"
	"testing"
)

// Crawlers are told to stay out, the static files can't be listed, and a
// read-only copy takes shorter scope-check lists.
func TestCrawlAndListing(t *testing.T) {
	e := newTestEnv(t)
	c := e.client()
	res, body := e.do(c, http.MethodGet, "/robots.txt", nil, nil)
	if res.StatusCode != http.StatusOK || body != "User-agent: *\nDisallow: /\n" {
		t.Errorf("robots.txt: %d %q", res.StatusCode, body)
	}
	if res, _ := e.do(c, http.MethodGet, "/", nil, nil); res.Header.Get("X-Robots-Tag") != "noindex, nofollow" {
		t.Errorf("X-Robots-Tag %q", res.Header.Get("X-Robots-Tag"))
	}
	for _, path := range []string{"/static/", "/static/css/"} {
		if res, _ := e.do(c, http.MethodGet, path, nil, nil); res.StatusCode != http.StatusNotFound {
			t.Errorf("%s = %d, want 404", path, res.StatusCode)
		}
	}
	if res, _ := e.do(c, http.MethodGet, "/static/app.js", nil, nil); res.StatusCode != http.StatusOK {
		t.Errorf("/static/app.js = %d", res.StatusCode)
	}

	var list strings.Builder
	for i := range matchManyDemoMax + 1 {
		fmt.Fprintf(&list, "h%d.example.com\n", i)
	}
	ro := newTestEnv(t, func(d *Deps) { d.ReadOnly = true })
	req, _ := http.NewRequest(http.MethodPost, ro.srv.URL+"/api/v1/match", strings.NewReader(list.String()))
	req.Header.Set("Content-Type", "text/plain")
	if res, err := ro.client().Do(req); err != nil || res.StatusCode != http.StatusRequestEntityTooLarge {
		t.Errorf("a read-only copy took %d lines: %v %v", matchManyDemoMax+1, res.StatusCode, err)
	}
}
