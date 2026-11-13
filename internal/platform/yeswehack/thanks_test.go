package yeswehack

import (
	"context"
	"encoding/json"
	"maps"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync"
	"testing"

	"github.com/mdpsec/huntdeck/internal/httpclient"
)

// thanksServer serves acme-bug-bounty's Thanks list, 12 hunters a page, and
// counts the pages read.
type thanksServer struct {
	mu    sync.Mutex
	list  []thanksItem
	pages []int
}

type thanksItem struct {
	Slug     string `json:"slug"`
	Accepted int    `json:"accepted_reports"`
}

func (s *thanksServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/programs/acme-bug-bounty/thanks" {
		http.NotFound(w, r)
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	page, _ := strconv.Atoi(r.URL.Query().Get("page"))
	s.pages = append(s.pages, page)
	n := (len(s.list) + 11) / 12
	items := s.list[min((page-1)*12, len(s.list)):min(page*12, len(s.list))]
	var out struct {
		Pagination map[string]int `json:"pagination"`
		Items      []thanksItem   `json:"items"`
	}
	out.Pagination = map[string]int{"page": page, "nb_pages": n, "results_per_page": 12, "nb_results": len(s.list)}
	out.Items = items
	_ = json.NewEncoder(w).Encode(out)
}

func TestFetchThanks(t *testing.T) {
	ts := &thanksServer{}
	for i := range 30 {
		ts.list = append(ts.list, thanksItem{"h" + strconv.Itoa(i), i%4 + 1})
	}
	srv := httptest.NewServer(ts)
	t.Cleanup(srv.Close)
	c, err := New(Options{BaseURL: srv.URL, HTTP: httpclient.New(httpclient.Options{RatePerSecond: 1000, Burst: 100, MaxAttempts: 1})})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()

	full, err := c.FetchThanks(ctx, "acme-bug-bounty", nil)
	if err != nil {
		t.Fatal(err)
	}
	if !full.Shown || !full.Complete || full.Listed != 30 || len(full.Hunters) != 30 || len(ts.pages) != 3 {
		t.Fatalf("full read = %+v, pages %v", full, ts.pages)
	}

	// A hunter's count rose, moving them to the top: the second page is
	// all known, so the read stops there.
	ts.pages = nil
	ts.list = append([]thanksItem{{"h20", 9}}, append(ts.list[:20:20], ts.list[21:]...)...)
	got, err := c.FetchThanks(ctx, "acme-bug-bounty", maps.Clone(full.Hunters))
	if err != nil {
		t.Fatal(err)
	}
	if got.Complete || got.Hunters["h20"] != 9 || len(ts.pages) != 2 {
		t.Errorf("update = %+v, pages %v", got, ts.pages)
	}

	// Nothing changed: one page.
	ts.pages = nil
	known := maps.Clone(full.Hunters)
	known["h20"] = 9
	if _, err := c.FetchThanks(ctx, "acme-bug-bounty", known); err != nil || len(ts.pages) != 1 {
		t.Errorf("unchanged read: err %v, pages %v", err, ts.pages)
	}

	// A program hiding its list.
	hidden, err := c.FetchThanks(ctx, "hidden", nil)
	if err != nil || hidden.Shown {
		t.Errorf("hidden = %+v, %v", hidden, err)
	}
}

func TestIsDojo(t *testing.T) {
	for _, tc := range []struct {
		raw  string
		want bool
	}{
		{`{"slug":"dojo","business_unit":{"slug":"dojo-by-yes-we-hack"}}`, true},
		{`{"slug":"dojo-challenge-47","business_unit":{"slug":"dojo-by-yes-we-hack"}}`, true},
		{`{"slug":"acme-bug-bounty","business_unit":{"slug":"acme"}}`, false},
		{`{"slug":"acme-bug-bounty"}`, false},
	} {
		var pj programJSON
		if err := json.Unmarshal([]byte(tc.raw), &pj); err != nil {
			t.Fatal(err)
		}
		if got := isDojo(pj); got != tc.want {
			t.Errorf("isDojo(%s) = %v", tc.raw, got)
		}
	}
}
