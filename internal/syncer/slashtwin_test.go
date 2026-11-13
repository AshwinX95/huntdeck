package syncer

import (
	"slices"
	"testing"
)

// For a platform without scope IDs, an asset listed again with only a
// trailing slash added or taken off is edited, not removed and added; one
// with an ID keeps the usual rules.
func TestTrailingSlashIsAnEdit(t *testing.T) {
	e := newEnv(t)
	e.fake.SetPrograms(prog("acme", false))
	e.fake.SetScope("acme", entry("", "https://app.acme.example", true), entry("", "https://shop.acme.example/", true),
		entry("", "https://old.acme.example", true))
	e.poll(ModeFull, 0)

	e.fake.SetScope("acme", entry("", "https://app.acme.example/", true), entry("", "https://shop.acme.example", true),
		entry("", "https://new.acme.example", true))
	run := e.poll(ModeFull, 0)
	want := []string{
		"acme scope_added in_scope →https://new.acme.example",
		"acme scope_removed in_scope https://old.acme.example→",
		"acme scope_updated identifier https://app.acme.example→https://app.acme.example/",
		"acme scope_updated identifier https://shop.acme.example/→https://shop.acme.example",
	}
	if got := e.changes(run.ID); !slices.Equal(got, want) {
		t.Errorf("changes:\n got  %q\n want %q", got, want)
	}
	if run := e.poll(ModeFull, 0); len(e.changes(run.ID)) != 0 {
		t.Errorf("an identical poll changed %q", e.changes(run.ID))
	}
}
