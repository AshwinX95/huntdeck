package hackerone

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/mdpsec/huntdeck/internal/platform"
)

// hacktivityItem renders the fixture item with the given ID, program and
// disclosure time.
func hacktivityItem(t *testing.T, id int, handle string, disclosed time.Time, url string) string {
	t.Helper()
	b, err := os.ReadFile("testdata/hacktivity_item.json")
	if err != nil {
		t.Fatal(err)
	}
	if url == "" {
		url = "https://hackerone.com/reports/" + strconv.Itoa(id)
	}
	return strings.NewReplacer("{{ID}}", strconv.Itoa(id), "{{HANDLE}}", handle, "{{URL}}", url,
		"{{DISCLOSED}}", disclosed.Format(time.RFC3339)).Replace(string(b))
}

// serveHacktivity serves pages of items: page n gets pages[n-1] items.
func serveHacktivity(t *testing.T, f *fakeAPI, pages []int, handleOf func(id int) string, queries *[]string) {
	base := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	f.override["/hackers/hacktivity"] = func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		*queries = append(*queries, q.Get("queryString")+" sort="+q.Get("sort")+" size="+q.Get("page[size]")+" page="+q.Get("page[number]"))
		n, _ := strconv.Atoi(q.Get("page[number]"))
		var items []string
		if n >= 1 && n <= len(pages) {
			for i := range pages[n-1] {
				id := (n-1)*hacktivityPage + i + 1
				items = append(items, hacktivityItem(t, id, handleOf(id), base.Add(-time.Duration(id)*time.Hour), ""))
			}
		}
		_, _ = fmt.Fprintf(w, `{"data":[%s],"links":{}}`, strings.Join(items, ","))
	}
}

func TestFetchDisclosuresPagesUntilShortPage(t *testing.T) {
	f := newFakeAPI(t)
	var queries []string
	serveHacktivity(t, f, []int{hacktivityPage, 3}, func(int) string { return "acme" }, &queries)
	c := f.client(t, "user", "token")

	var got []platform.Disclosure
	err := c.FetchDisclosures(context.Background(), nil, 10, func(page []platform.Disclosure) (bool, error) {
		got = append(got, page...)
		return true, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != hacktivityPage+3 || len(queries) != 2 {
		t.Fatalf("got %d disclosures in %d requests, want %d in 2", len(got), len(queries), hacktivityPage+3)
	}
	if want := "disclosed:true sort=-disclosed_at size=50 page=1"; queries[0] != want {
		t.Errorf("query = %q, want %q", queries[0], want)
	}

	d := got[0]
	want := platform.Disclosure{ID: "1", ProgramHandle: "acme", Title: "app.acme.example.com: Stored XSS in profile name",
		Severity: "high", Weakness: "Cross-site Scripting (XSS) - Stored", Substate: "resolved",
		URL: "https://hackerone.com/reports/1", Reporter: "hunter-example", Votes: 36,
		Summary: "An attacker could store script in a profile name."}
	if d.ID != want.ID || d.ProgramHandle != want.ProgramHandle || d.Title != want.Title || d.Severity != want.Severity ||
		d.Weakness != want.Weakness || d.Substate != want.Substate || d.URL != want.URL || d.Reporter != want.Reporter ||
		d.Votes != want.Votes || d.Summary != want.Summary {
		t.Errorf("disclosure:\n got %+v\nwant %+v", d, want)
	}
	if d.Bounty == nil || *d.Bounty != 1337 || len(d.CVEIDs) != 1 || d.SubmittedAt == nil ||
		!d.DisclosedAt.Equal(time.Date(2026, 9, 30, 23, 0, 0, 0, time.UTC)) {
		t.Errorf("bounty/cves/dates: %v %v %v %v", d.Bounty, d.CVEIDs, d.SubmittedAt, d.DisclosedAt)
	}
	if raw := string(d.Raw); strings.Contains(raw, "write-up") || !strings.Contains(raw, `"hacktivity_summary"`) {
		t.Errorf("raw keeps the write-up or lost metadata: %s", raw)
	}
}

func TestFetchDisclosuresForOneProgram(t *testing.T) {
	f := newFakeAPI(t)
	var queries []string
	// The name filter also matches another program; only acme's are kept.
	serveHacktivity(t, f, []int{4}, func(id int) string { return map[bool]string{true: "acme", false: "acme-other"}[id%2 == 1] }, &queries)
	c := f.client(t, "user", "token")

	var got []string
	p := &platform.Program{Handle: "acme", Name: `Acme "Corp"`}
	err := c.FetchDisclosures(context.Background(), p, 10, func(page []platform.Disclosure) (bool, error) {
		for _, d := range page {
			got = append(got, d.ID+"/"+d.ProgramHandle)
		}
		return true, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(got, ",") != "1/acme,3/acme" {
		t.Errorf("disclosures = %v", got)
	}
	if want := `team:"Acme \"Corp\"" AND disclosed:true sort=-disclosed_at size=50 page=1`; len(queries) != 1 || queries[0] != want {
		t.Errorf("queries = %q, want [%q]", queries, want)
	}
}

func TestFetchDisclosuresStopsWhenAsked(t *testing.T) {
	f := newFakeAPI(t)
	var queries []string
	serveHacktivity(t, f, []int{hacktivityPage, hacktivityPage, hacktivityPage}, func(int) string { return "acme" }, &queries)
	c := f.client(t, "user", "token")
	stop := errors.New("stop")
	calls := 0
	err := c.FetchDisclosures(context.Background(), nil, 10, func([]platform.Disclosure) (bool, error) {
		calls++
		return calls < 2, nil
	})
	if err != nil || len(queries) != 2 {
		t.Errorf("save said stop after page 2: err=%v requests=%d", err, len(queries))
	}
	queries = nil
	if err := c.FetchDisclosures(context.Background(), nil, 10, func([]platform.Disclosure) (bool, error) { return true, stop }); !errors.Is(err, stop) || len(queries) != 1 {
		t.Errorf("save error: err=%v requests=%d", err, len(queries))
	}
	queries = nil
	if err := c.FetchDisclosures(context.Background(), nil, 2, func([]platform.Disclosure) (bool, error) { return true, nil }); err != nil || len(queries) != 2 {
		t.Errorf("maxPages: err=%v requests=%d", err, len(queries))
	}
}

func TestFetchDisclosuresSkipsOddItemsAndFailsOnAuth(t *testing.T) {
	f := newFakeAPI(t)
	at := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	f.override["/hackers/hacktivity"] = func(w http.ResponseWriter, _ *http.Request) {
		_, _ = fmt.Fprintf(w, `{"data":[%s,%s,{"id":"9","attributes":{"title":"undisclosed"}}]}`,
			hacktivityItem(t, 1, "acme", at, ""), hacktivityItem(t, 2, "acme", at, "https://evil.example.net/reports/2"))
	}
	c := f.client(t, "user", "token")
	var got []string
	err := c.FetchDisclosures(context.Background(), nil, 1, func(page []platform.Disclosure) (bool, error) {
		for _, d := range page {
			got = append(got, d.ID)
		}
		return true, nil
	})
	if err != nil || strings.Join(got, ",") != "1" {
		t.Errorf("got %v, %v; want only the well-formed hackerone.com report", got, err)
	}

	bad := f.client(t, "user", "wrong")
	if err := bad.FetchDisclosures(context.Background(), nil, 1, func([]platform.Disclosure) (bool, error) { return true, nil }); !errors.Is(err, ErrAuth) {
		t.Errorf("bad token: err = %v", err)
	}
}

func TestFetchResolutions(t *testing.T) {
	f := newFakeAPI(t)
	page, err := os.ReadFile("testdata/hacktivity_resolved.json")
	if err != nil {
		t.Fatal(err)
	}
	var queries []string
	f.override["/hackers/hacktivity"] = func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		queries = append(queries, q.Get("queryString")+" sort="+q.Get("sort"))
		_, _ = w.Write(page)
	}
	c := f.client(t, "user", "token")

	for _, tc := range []struct {
		name      string
		program   *platform.Program
		wantQuery string
		wantIDs   string
	}{
		{"all programs", nil, "disclosed:false sort=-latest_disclosable_activity_at", "4058489,4058001"},
		{"one program, checked by handle", &platform.Program{Handle: "acme", Name: `Acme "Labs"`},
			`team:"Acme \"Labs\"" AND disclosed:false sort=-latest_disclosable_activity_at`, "4058489"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			queries = nil
			var got []platform.Resolution
			err := c.FetchResolutions(context.Background(), tc.program, 5, func(p []platform.Resolution) (bool, error) {
				got = append(got, p...)
				return true, nil
			})
			if err != nil {
				t.Fatal(err)
			}
			var ids []string
			for _, r := range got {
				ids = append(ids, r.ID)
			}
			// A short page ends paging; unknown actions and disclosed items are skipped.
			if len(queries) != 1 || queries[0] != tc.wantQuery || strings.Join(ids, ",") != tc.wantIDs {
				t.Errorf("queries %q, ids %v", queries, ids)
			}
		})
	}

	var got []platform.Resolution
	_ = c.FetchResolutions(context.Background(), nil, 1, func(p []platform.Resolution) (bool, error) { got = p; return false, nil })
	r := got[0]
	if r.ProgramHandle != "acme" || r.Action != platform.ActionBounty || r.Bounty == nil || *r.Bounty != 750 || r.Reporter != "hunter-example" ||
		!r.ActivityAt.Equal(time.Date(2026, 10, 22, 18, 1, 59, 289_000_000, time.UTC)) || r.SubmittedAt == nil ||
		!r.SubmittedAt.Equal(time.Date(2026, 10, 8, 7, 24, 31, 533_000_000, time.UTC)) {
		t.Errorf("resolution = %+v", r)
	}
	if got[1].Action != platform.ActionResolved || got[1].Bounty != nil {
		t.Errorf("second resolution = %+v", got[1])
	}
}

// A private program's Hacktivity holds only the first 10 items of each
// page past the first 50: paging carries on 10 at a time to the end.
func TestHacktivityPastThePrivateCap(t *testing.T) {
	f := newFakeAPI(t)
	const total = 73
	base := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	var queries []string
	f.override["/hackers/hacktivity"] = func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		size, _ := strconv.Atoi(q.Get("page[size]"))
		n, _ := strconv.Atoi(q.Get("page[number]"))
		queries = append(queries, fmt.Sprintf("%d/%d", size, n))
		from, to := (n-1)*size, min(n*size, total)
		if from >= hacktivityPage {
			to = min(to, from+cappedPage)
		}
		var items []string
		for id := from + 1; id <= to; id++ {
			items = append(items, hacktivityItem(t, id, "acme", base.Add(-time.Duration(id)*time.Hour), ""))
		}
		_, _ = fmt.Fprintf(w, `{"data":[%s],"links":{}}`, strings.Join(items, ","))
	}
	c := f.client(t, "user", "token")
	seen := map[string]bool{}
	err := c.FetchDisclosures(context.Background(), &platform.Program{Handle: "acme", Name: "Acme"}, 100, func(p []platform.Disclosure) (bool, error) {
		for _, d := range p {
			seen[d.ID] = true
		}
		return true, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	// 50, then a capped 10 (51-60), then 61-70 and 71-73 ten at a time.
	if len(seen) != total || strings.Join(queries, ",") != "50/1,50/2,10/7,10/8" {
		t.Errorf("%d of %d items in requests %v", len(seen), total, queries)
	}
}
