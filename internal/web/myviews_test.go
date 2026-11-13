package web

import (
	"net/http"
	"strings"
	"testing"
)

// The Hunt board, Programs (and Invites) and Targets offer saved views,
// each keyed by its own page with the query keys it reads; views.js keeps
// them in the browser.
func TestMyViews(t *testing.T) {
	e := newTestEnv(t)
	c := e.client()
	for path, want := range map[string]string{
		"/":         `<div class="myviews" data-views="/" data-view-keys="kind,not,platform,bounty,added,sort,rev" data-view-cols="board-targets" hidden>`,
		"/programs": `<div class="myviews" data-views="/programs" data-view-keys="platform,visibility,`,
		"/invites":  `<div class="myviews" data-views="/invites" `,
		"/targets":  `<div class="myviews" data-views="/targets" data-view-keys="kind,q,scope,`,
	} {
		_, body := e.do(c, http.MethodGet, path, nil, nil)
		if !strings.Contains(body, want) || !strings.Contains(body, "No saved views yet. Set your filters and hit Save view.") ||
			!strings.Contains(body, "data-view-save") || !strings.Contains(body, `src="/static/views.js`) {
			t.Errorf("%s lacks its saved views", path)
		}
	}
	if _, body := e.do(c, http.MethodGet, "/programs", nil, nil); !strings.Contains(body, `data-view-cols="program"`) || !strings.Contains(body, ",sort,rev\"") {
		t.Error("Programs' views don't keep its columns or sort")
	}
}
