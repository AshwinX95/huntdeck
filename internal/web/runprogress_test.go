package web

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"
)

// A running poll shows its step, count, time left and last activity, and
// says when it has done nothing for a while; the panel reloads itself.
func TestRunningPollProgress(t *testing.T) {
	var prog RunProgress
	e := newTestEnv(t, func(d *Deps) {
		d.Progress = func(int64) (RunProgress, bool) { return prog, true }
	})
	if _, err := e.store.StartRun(context.Background(), "fake", "full", "schedule", time.Hour); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	prog = RunProgress{Step: "Program stats", Done: 5, Total: 10, Requests: 1234, Started: now.Add(-20 * time.Minute),
		StepStarted: now.Add(-5 * time.Minute), LastActivity: now.Add(-2 * time.Second)}
	_, body := e.do(e.client(), http.MethodGet, "/settings", nil, nil)
	row := body[strings.Index(body, "data-live>"):]
	row = row[:strings.Index(row, "</tr>")]
	for _, want := range []string{"<b>Program stats 5 / 10</b>", "about 5m left in this step", "last activity 2s ago", "so far", "1,234"} {
		if !strings.Contains(row, want) {
			t.Errorf("running row lacks %q:\n%s", want, row)
		}
	}
	if strings.Contains(row, "partial list") || strings.Contains(row, "no activity") {
		t.Errorf("running row says partial list or no activity:\n%s", row)
	}
	if !strings.Contains(body, `id="history" data-live-refresh="10"`) {
		t.Error("the poll history doesn't reload itself while a poll runs")
	}

	prog.LastActivity = now.Add(-10 * time.Minute)
	_, body = e.do(e.client(), http.MethodGet, "/settings", nil, nil)
	if !strings.Contains(body, `<span class="chip stalled"`) || !strings.Contains(body, "no activity 10m") {
		t.Error("a poll idle for 10 minutes isn't marked")
	}
}
