package bugcrowd

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/mdpsec/huntdeck/internal/platform"
)

func TestFetchAcceptedForOneProgram(t *testing.T) {
	f := newFake(t)
	c := f.client(t, Options{})
	var pages []platform.AcceptedPage
	err := c.FetchAccepted(context.Background(), &platform.Program{Handle: "acme-bb"}, 10, func(p platform.AcceptedPage) (bool, error) {
		pages = append(pages, p)
		return true, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(pages) != 2 {
		t.Fatalf("got %d pages, want 2", len(pages))
	}
	if want := testNow.AddDate(0, -6, 0); !pages[0].Since.Equal(want) {
		t.Errorf("since = %v, want %v", pages[0].Since, want)
	}
	var got []platform.AcceptedReport
	for _, p := range pages {
		got = append(got, p.Reports...)
	}
	// The third item has no usable acceptance day; the last one was
	// accepted before the window (an old disclosure).
	if len(got) != 3 {
		t.Fatalf("got %d reports, want 3: %+v", len(got), got)
	}
	open, fixed, old := got[0], got[1], got[2]
	if open.ID != "5b0d0000-0000-4000-8000-000000000001" || open.ProgramHandle != "acme-bb" || open.Priority != 3 ||
		!open.AcceptedAt.Equal(time.Date(2026, 10, 23, 0, 0, 0, 0, time.UTC)) || open.ResolvedAt != nil || open.Bounty != nil ||
		open.SubmittedAt == nil || !open.SubmittedAt.Equal(time.Date(2026, 10, 20, 8, 30, 27, 0, time.UTC)) || open.Reporter != "hunter-one" {
		t.Errorf("open report = %+v", open)
	}
	if fixed.Priority != 1 || fixed.Bounty == nil || *fixed.Bounty != 1600 || fixed.ResolvedAt == nil ||
		!fixed.ResolvedAt.Equal(time.Date(2026, 10, 24, 17, 46, 51, 826e6, time.UTC)) {
		t.Errorf("resolved report = %+v", fixed)
	}
	if !old.AcceptedAt.Equal(time.Date(2026, 4, 27, 0, 0, 0, 0, time.UTC)) || old.ResolvedAt == nil {
		t.Errorf("report from the window's first days = %+v", old)
	}
}

func TestFetchAcceptedFeedStopsAtTheWindow(t *testing.T) {
	f := newFake(t)
	c := f.client(t, Options{})
	var got []platform.AcceptedReport
	err := c.FetchAccepted(context.Background(), nil, 10, func(p platform.AcceptedPage) (bool, error) {
		if want := testNow.AddDate(0, 0, -7); !p.Since.Equal(want) {
			t.Errorf("since = %v, want %v", p.Since, want)
		}
		got = append(got, p.Reports...)
		return true, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].ProgramHandle != "acme-bb" || got[0].Priority != 2 {
		t.Errorf("got %+v; want only the acme-bb report", got)
	}
	if n := f.hit("/crowdstream.json"); n != 1 {
		t.Errorf("fetched %d pages; want 1 (the second report is before the window)", n)
	}
}

func TestFetchAcceptedStopsWhenSaveSaysSo(t *testing.T) {
	f := newFake(t)
	c := f.client(t, Options{})
	calls := 0
	err := c.FetchAccepted(context.Background(), &platform.Program{Handle: "acme-bb"}, 10, func(platform.AcceptedPage) (bool, error) {
		calls++
		return false, nil
	})
	if err != nil || calls != 1 || f.hit("/engagements/acme-bb/crowdstream.json") != 1 {
		t.Errorf("err %v, calls %d, requests %d; want one page", err, calls, f.hit("/engagements/acme-bb/crowdstream.json"))
	}
}

func TestFetchAcceptedErrors(t *testing.T) {
	f := newFake(t)
	f.blockAll = true
	c := f.client(t, Options{})
	save := func(platform.AcceptedPage) (bool, error) { t.Error("saved a page"); return true, nil }
	if err := c.FetchAccepted(context.Background(), nil, 10, save); !errors.Is(err, ErrBlocked) {
		t.Errorf("blocked: err = %v; want ErrBlocked", err)
	}
	if err := c.FetchAccepted(context.Background(), &platform.Program{Handle: "../x"}, 10, save); err == nil {
		t.Error("bad handle: no error")
	}
	f.blockAll = false
	if err := c.FetchAccepted(context.Background(), &platform.Program{Handle: "globex-vdp"}, 10, save); err != nil {
		t.Errorf("program without a feed: err = %v; want it skipped", err)
	}
}

func TestWindow(t *testing.T) {
	now := time.Date(2026, 10, 26, 12, 0, 0, 0, time.UTC)
	tests := []struct {
		label string
		want  time.Time
		ok    bool
	}{
		{"6 months", time.Date(2026, 4, 26, 12, 0, 0, 0, time.UTC), true},
		{"7 days", time.Date(2026, 10, 19, 12, 0, 0, 0, time.UTC), true},
		{"1 week", time.Date(2026, 10, 19, 12, 0, 0, 0, time.UTC), true},
		{"1 year", time.Date(2025, 10, 26, 12, 0, 0, 0, time.UTC), true},
		{"forever", time.Time{}, false},
		{"", time.Time{}, false},
		{"0 days", time.Time{}, false},
	}
	for _, tt := range tests {
		got, ok := window(tt.label, now)
		if ok != tt.ok || !got.Equal(tt.want) {
			t.Errorf("window(%q) = %v, %v; want %v, %v", tt.label, got, ok, tt.want, tt.ok)
		}
	}
}
