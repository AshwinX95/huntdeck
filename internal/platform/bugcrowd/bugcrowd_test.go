package bugcrowd

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/mdpsec/huntdeck/internal/httpclient"
	"github.com/mdpsec/huntdeck/internal/platform"
)

func TestListProgramsPublic(t *testing.T) {
	f := newFake(t)
	c := f.client(t, Options{})
	l, err := c.ListPrograms(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !l.Complete || len(l.Programs) != 4 {
		t.Fatalf("complete=%v programs=%d, want 4 (demo skipped)", l.Complete, len(l.Programs))
	}
	if f.hit("/auth/session") != 0 {
		t.Error("public mode must not check a session")
	}
	byHandle := map[string]platform.Program{}
	for _, p := range l.Programs {
		byHandle[p.Handle] = p
	}
	tests := []struct {
		handle   string
		private  bool
		bounty   bool
		currency string
		rng      string
		url      string
	}{
		{"acme-bb", false, true, "USD", "100..15000", f.base.URL + "/engagements/acme-bb"},
		{"globex-vdp", false, false, "", "-..-", f.base.URL + "/engagements/globex-vdp"},
		{"initech-pt", true, true, "EUR", "-..2000", f.base.URL + "/engagements/initech-pt"},
		{"hooli", false, false, "", "-..-", f.base.URL + "/hooli"},
	}
	for _, tt := range tests {
		p, ok := byHandle[tt.handle]
		if !ok {
			t.Errorf("%s missing", tt.handle)
			continue
		}
		if p.Private != tt.private || p.OffersBounty != tt.bounty || p.Currency != tt.currency || bountyRange(p) != tt.rng || p.URL != tt.url || p.State != platform.StateOpen {
			t.Errorf("%s: %+v", tt.handle, p)
		}
		if len(p.Raw) == 0 {
			t.Errorf("%s: raw JSON not kept", tt.handle)
		}
	}
}

func TestListRecent(t *testing.T) {
	f := newFake(t)
	c := f.client(t, Options{})
	l, err := c.ListRecent(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if l.Complete || len(l.Programs) == 0 || f.hit("/engagements.json") != 1 || f.hit("/auth/session") != 0 {
		t.Errorf("complete=%v programs=%d listing requests=%d session checks=%d; want one page, never complete",
			l.Complete, len(l.Programs), f.hit("/engagements.json"), f.hit("/auth/session"))
	}

	// Logged in, it checks the session once (logging in if needed).
	f = newFake(t)
	c = f.client(t, Options{Session: goodSession})
	if _, err := c.ListRecent(context.Background()); err != nil {
		t.Fatal(err)
	}
	if n := f.hit("/auth/session"); n != 1 {
		t.Errorf("session checks = %d, want 1", n)
	}
}

func TestSessionEndingDuringListingIsIncomplete(t *testing.T) {
	f := newFake(t)
	f.expireAfterChecks = 1
	c := f.client(t, Options{Session: goodSession})
	l, err := c.ListPrograms(context.Background())
	if !errors.Is(err, ErrSessionExpired) || l.Complete || len(l.Programs) != 4 {
		t.Fatalf("complete=%v programs=%d err=%v", l.Complete, len(l.Programs), err)
	}
}

func TestListingShorterThanTotalIsIncomplete(t *testing.T) {
	f := newFake(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("page") == "1" {
			f.write(w, "application/json", "engagements_page1.json")
			return
		}
		_, _ = w.Write([]byte(`{"engagements":[],"paginationMeta":{"limit":2,"totalCount":5}}`))
	}))
	t.Cleanup(srv.Close)
	c, err := New(Options{BaseURL: srv.URL, HTTP: httpclient.Options{RatePerSecond: 1000}})
	if err != nil {
		t.Fatal(err)
	}
	l, err := c.ListPrograms(context.Background())
	if err == nil || l.Complete || len(l.Programs) != 2 {
		t.Fatalf("complete=%v programs=%d err=%v", l.Complete, len(l.Programs), err)
	}
}

func TestOddListingsAreIncomplete(t *testing.T) {
	tests := []struct{ name, page1 string }{
		{"no total", `{"engagements":[{"name":"A","briefUrl":"/engagements/a"}],"paginationMeta":{}}`},
		{"bad item", `{"engagements":[{"name":"A","briefUrl":"/engagements/a"},{"name":"B","briefUrl":"https://evil.example.com/b"},{"name":"C","briefUrl":"/engagements/c"}],"paginationMeta":{"totalCount":3}}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_, _ = w.Write([]byte(tt.page1))
			}))
			t.Cleanup(srv.Close)
			c, err := New(Options{BaseURL: srv.URL, HTTP: httpclient.Options{RatePerSecond: 1000}})
			if err != nil {
				t.Fatal(err)
			}
			l, err := c.ListPrograms(context.Background())
			if err == nil || l.Complete {
				t.Fatalf("complete=%v err=%v", l.Complete, err)
			}
			if tt.name == "bad item" && len(l.Programs) != 2 {
				t.Errorf("good items should still be returned, got %d", len(l.Programs))
			}
		})
	}
}

func TestBlockedByWAF(t *testing.T) {
	f := newFake(t)
	f.blockAll = true
	c := f.client(t, Options{})
	if _, err := c.ListPrograms(context.Background()); !errors.Is(err, ErrBlocked) {
		t.Fatalf("err = %v", err)
	}
}

func TestHandleFromBrief(t *testing.T) {
	tests := []struct {
		in, want string
		ok       bool
	}{
		{"/engagements/acme-bb", "acme-bb", true},
		{"/acme", "acme", true},
		{"/engagements/", "", false},
		{"/engagements/a/b", "", false},
		{"//evil.example.com/x", "", false},
		{"https://bugcrowd.com/engagements/x", "", false},
		{"/a/b", "", false},
		{"/engagements/x?y=1", "", false},
		{"", "", false},
	}
	for _, tt := range tests {
		got, ok := handleFromBrief(tt.in)
		if got != tt.want || ok != tt.ok {
			t.Errorf("handleFromBrief(%q) = %q, %v", tt.in, got, ok)
		}
	}
}

func TestCurrency(t *testing.T) {
	tests := []struct {
		in, cur string
		money   bool
	}{{"$15,000", "USD", true}, {"€2,000", "EUR", true}, {"£500", "GBP", true}, {"Points", "", false}, {"", "", false}, {"CHF 900", "", true}}
	for _, tt := range tests {
		cur, money := currency(tt.in)
		if cur != tt.cur || money != tt.money {
			t.Errorf("currency(%q) = %q, %v", tt.in, cur, money)
		}
	}
}
