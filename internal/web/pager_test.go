package web

import (
	"net/http"
	"strings"
	"testing"
)

// A page past the end of a list says so and links to the last page,
// rather than claiming nothing matches.
func TestPastTheEnd(t *testing.T) {
	e := newTestEnv(t)
	for _, path := range []string{"/programs?q=acme&page=9", "/targets?kind=all&page=9", "/changes?since=30d&page=9", "/disclosed?page=9", "/stats/fake?page=9"} {
		res, body := e.do(e.client(), http.MethodGet, path, nil, nil)
		if res.StatusCode != http.StatusOK || !strings.Contains(body, "No more results: this list ends on page 1.") ||
			!strings.Contains(body, "page=1") || strings.Contains(body, "match this search") || strings.Contains(body, "match these filters") {
			t.Errorf("%s = %d without the past-the-end note", path, res.StatusCode)
		}
	}
	if _, body := e.do(e.client(), http.MethodGet, "/programs?q=nothing-like-this", nil, nil); !strings.Contains(body, "No programs match this search.") {
		t.Error("an empty search no longer says nothing matches")
	}
}
