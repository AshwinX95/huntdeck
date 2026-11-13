package web

import (
	"net/http"
	"strings"
	"testing"
)

// Each program has a Notes tab (its notes are the browser's, notes.js),
// and Programs keeps to those with notes with ?noted=, beside the
// watchlist, as views keep it too.
func TestNotes(t *testing.T) {
	e := newTestEnv(t)
	c := e.client()
	_, body := e.do(c, http.MethodGet, "/programs/fake/acme?tab=notes", nil, nil)
	for _, want := range []string{`data-notes-tab="fake/acme"`, `<section class="panel pnotes" data-notes="fake/acme" data-notes-name="Acme Corp">`,
		"<b>Only on this device.</b>", `<textarea id="notes-text"`, "data-notes-export", "data-notes-import", `src="/static/notes.js`} {
		if !strings.Contains(body, want) {
			t.Errorf("notes tab lacks %s", want)
		}
	}
	_, body = e.do(c, http.MethodGet, "/programs", nil, nil)
	if !strings.Contains(body, `<div class="ft">In this browser</div>`) || !strings.Contains(body, `data-notes-link hidden>`) {
		t.Error("rail lacks the hidden Has notes choice")
	}
	_, body = e.do(c, http.MethodGet, "/programs?noted=fake/acme", nil, nil)
	if n := strings.Count(body, `<tr class="prow"`); n != 1 || !strings.Contains(body, `data-notes-link="on"`) ||
		!strings.Contains(body, `<a class="af" href="/programs" title="Remove this filter">Has notes`) ||
		!strings.Contains(body, `href="/programs?bounty=1&amp;noted=fake%2Facme&amp;sort=huntscore"`) {
		t.Errorf("noted programs: %d rows, or no choice, pill or view keeping it", n)
	}
	// With the watchlist too, both apply.
	if _, body = e.do(c, http.MethodGet, "/programs?noted=fake/acme&watch=fake/evil", nil, nil); strings.Contains(body, `<tr class="prow"`) {
		t.Error("watchlist and notes together list a program in only one")
	}
}
