package web

import (
	"slices"
	"testing"
	"time"

	"github.com/mdpsec/huntdeck/internal/store"
)

// A program's feed names the asset an edit is about, and a poll's run of
// instruction changes is one row.
func TestFeedRows(t *testing.T) {
	ins := func(run int64, asset string) store.ChangeView {
		c := store.ChangeView{Asset: asset}
		c.Kind, c.Field, c.PollRunID, c.ProgramID = store.ChangeScopeUpdated, "instruction", run, 1
		return c
	}
	sev := ins(2, "api.example.com")
	sev.Field, sev.OldValue, sev.NewValue = "max_severity", "high", "critical"
	f := changeFeed{Changes: []store.ChangeView{
		ins(2, "a.example.com"), ins(2, "b.example.com"), ins(2, "c.example.com"), ins(2, "d.example.com"), sev,
		ins(2, "e.example.com"), ins(1, "f.example.com"),
	}}
	var got []string
	for _, r := range f.Rows() {
		got = append(got, r.Text())
	}
	want := []string{
		"Scope instructions changed for 4 assets: a.example.com, b.example.com, c.example.com and 1 more",
		"Scope max severity of api.example.com: high → critical",
		"Scope instructions changed: e.example.com",
		"Scope instructions changed: f.example.com",
	}
	if !slices.Equal(got, want) {
		t.Errorf("rows:\n got  %q\n want %q", got, want)
	}
	if title := f.Rows()[0].Title(); title != "a.example.com\nb.example.com\nc.example.com\nd.example.com" {
		t.Errorf("title = %q", title)
	}
}

// A day of more than busyDay rows is one row that opens to them.
func TestFeedFoldsBusyDays(t *testing.T) {
	at := func(day, i int) store.ChangeView {
		c := store.ChangeView{}
		c.ID, c.Kind, c.Field, c.NewValue = int64(day*100+i), store.ChangeScopeAdded, "in_scope", "a.example.com"
		c.OccurredAt = time.Date(2026, 11, day, 12, 0, 0, 0, time.UTC)
		if i%3 == 0 {
			c.Kind, c.OldValue, c.NewValue = store.ChangeScopeRemoved, "b.example.com", ""
		}
		return c
	}
	var changes []store.ChangeView
	for i := range 8 {
		changes = append(changes, at(6, i))
	}
	changes = append(changes, at(5, 1), at(5, 2))
	rows := changeFeed{Changes: changes}.Rows()
	if len(rows) != 3 || len(rows[0].Day) != 8 || rows[0].Changes() != 8 || rows[1].Day != nil {
		t.Fatalf("rows = %d, first folds %d", len(rows), len(rows[0].Day))
	}
	if got := rows[0].DaySummary(); got != "5 scope added, 3 removed" {
		t.Errorf("summary = %q", got)
	}
}
