package syncer

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/mdpsec/huntdeck/internal/platform/custom"
	"github.com/mdpsec/huntdeck/internal/store"
)

// A custom source polls like a platform: its scope is stored and its
// changes recorded, a second identical poll changes nothing, and only a
// complete document removes programs.
func TestCustomSource(t *testing.T) {
	e := newEnv(t)
	path := filepath.Join(t.TempDir(), "source.json")
	write := func(doc string) {
		t.Helper()
		if err := os.WriteFile(path, []byte(doc), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	src, err := custom.New(custom.Options{Name: "inspectiv", Location: path})
	if err != nil {
		t.Fatal(err)
	}
	poll := func() store.Run {
		t.Helper()
		e.clock = e.clock.Add(time.Minute)
		run, err := e.sync.Poll(context.Background(), src, Options{Mode: ModeFull, Trigger: "manual"})
		if err != nil {
			t.Fatal(err)
		}
		return run
	}
	const a = `{"handle": "a", "name": "A", "url": "https://platform.example.com/a", "scope": [{"identifier": "*.a.example.com"}]}`
	const b = `{"handle": "b", "name": "B", "url": "https://platform.example.com/b", "scope": []}`
	write(`{"version": 1, "programs": [` + a + `, ` + b + `]}`)
	poll()
	if run := poll(); len(e.changes(run.ID)) != 0 {
		t.Errorf("an identical poll changed %q", e.changes(run.ID))
	}
	write(`{"version": 1, "complete": false, "programs": [` + a[:len(a)-2] + `, {"identifier": "api.a.example.com"}]}]}`)
	run := poll()
	if got := e.changes(run.ID); len(got) != 1 || got[0] != "a scope_added in_scope →api.a.example.com" {
		t.Errorf("incomplete document: changes %q (b must stay)", got)
	}
	write(`{"version": 1, "programs": [` + a + `]}`)
	run = poll()
	got := e.changes(run.ID)
	if len(got) != 2 || got[0] != "a scope_removed in_scope api.a.example.com→" || got[1] != "b program_removed  →" {
		t.Errorf("complete document: changes %q", got)
	}
}
