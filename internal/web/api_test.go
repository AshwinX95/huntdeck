package web

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/mdpsec/huntdeck/internal/store"
)

func TestAPI(t *testing.T) {
	e := newTestEnv(t)
	addDomain(t, e)
	c := e.client()

	res, body := e.do(c, "GET", "/api/v1/targets?kind=wildcards", nil, nil)
	if res.StatusCode != http.StatusOK || body != "*.acme.example\n" || !strings.HasPrefix(res.Header.Get("Content-Type"), "text/plain") {
		t.Errorf("targets txt: %d %q", res.StatusCode, body)
	}
	for _, tt := range []struct {
		offset int
		want   string
	}{{0, "api.acme.example"}, {1, "app.evil.example"}, {2, ""}} {
		var page struct {
			Total, Limit, Offset int
			Targets              []struct{ Value string }
		}
		_, body := e.do(c, "GET", fmt.Sprintf("/api/v1/targets?kind=domains&format=json&limit=1&offset=%d", tt.offset), nil, nil)
		if err := json.Unmarshal([]byte(body), &page); err != nil {
			t.Fatalf("targets json: %v: %s", err, body)
		}
		got := ""
		if len(page.Targets) > 0 {
			got = page.Targets[0].Value
		}
		if page.Total != 2 || page.Limit != 1 || len(page.Targets) > 1 || got != tt.want {
			t.Errorf("targets json offset %d = %+v, want total 2 and %q", tt.offset, page, tt.want)
		}
	}
	if res, _ := e.do(c, "GET", "/api/v1/targets", nil, nil); res.StatusCode != http.StatusBadRequest {
		t.Errorf("targets without kind = %d", res.StatusCode)
	}
	if res, _ := e.do(c, "GET", "/api/v1/nope", nil, nil); res.StatusCode != http.StatusNotFound {
		t.Errorf("unknown endpoint = %d", res.StatusCode)
	}

	var progs struct {
		Total    int `json:"total"`
		Programs []struct {
			Handle string `json:"handle"`
		} `json:"programs"`
	}
	_, body = e.do(c, "GET", "/api/v1/programs?visibility=private", nil, nil)
	if err := json.Unmarshal([]byte(body), &progs); err != nil || progs.Total != 1 || progs.Programs[0].Handle != "evil" {
		t.Errorf("programs json: %v %s", err, body)
	}

	var prog struct {
		Scope []struct {
			Identifier string `json:"identifier"`
		} `json:"scope"`
		Stats struct {
			BountyEarned float64 `json:"bounty_earned"`
		} `json:"your_stats"`
		ProgramStats struct {
			Paid90d *float64 `json:"bounties_paid_90d"`
			Rewards []struct {
				Scope string `json:"scope"`
				Low   struct{ Min, Max float64 }
			} `json:"rewards"`
		} `json:"program_stats"`
	}
	_, body = e.do(c, "GET", "/api/v1/programs/fake/acme", nil, nil)
	if err := json.Unmarshal([]byte(body), &prog); err != nil || len(prog.Scope) != 4 || prog.Stats.BountyEarned != 1500 ||
		prog.ProgramStats.Paid90d == nil || *prog.ProgramStats.Paid90d != 41500 || len(prog.ProgramStats.Rewards) != 2 ||
		prog.ProgramStats.Rewards[1].Low.Min != 100 {
		t.Errorf("program json: %v %s", err, body)
	}
	if res, _ := e.do(c, "GET", "/api/v1/programs/fake/missing", nil, nil); res.StatusCode != http.StatusNotFound {
		t.Errorf("missing program API = %d", res.StatusCode)
	}

	var match struct {
		Matches []struct {
			InScope bool `json:"in_scope"`
		} `json:"matches"`
	}
	_, body = e.do(c, "GET", "/api/v1/match?host=x.acme.example", nil, nil)
	if err := json.Unmarshal([]byte(body), &match); err != nil || len(match.Matches) != 1 || !match.Matches[0].InScope {
		t.Errorf("match json: %v %s", err, body)
	}

	var changes struct {
		Total   int `json:"total"`
		Changes []struct {
			Description string `json:"description"`
		} `json:"changes"`
	}
	_, body = e.do(c, "GET", "/api/v1/changes?since=7d", nil, nil) // the URL, then addDomain's domain
	if err := json.Unmarshal([]byte(body), &changes); err != nil || changes.Total != 2 || !strings.Contains(changes.Changes[0].Description, "api.acme.example") {
		t.Errorf("changes json: %v %s", err, body)
	}
	if res, _ := e.do(c, "GET", "/api/v1/changes?since=yesterday", nil, nil); res.StatusCode != http.StatusBadRequest {
		t.Errorf("bad since = %d", res.StatusCode)
	}
	if res, body := e.do(c, "GET", "/api/v1/status", nil, nil); res.StatusCode != http.StatusOK || !strings.Contains(body, `"programs": 2`) {
		t.Errorf("status: %d %s", res.StatusCode, body)
	}

	if res, _ := e.do(c, "POST", "/api/v1/poll/fake?mode=quick", nil, nil); res.StatusCode != http.StatusAccepted {
		t.Errorf("API poll = %d", res.StatusCode)
	}
	e.poller.err = store.ErrRunInProgress
	if res, _ := e.do(c, "POST", "/api/v1/poll/fake", nil, nil); res.StatusCode != http.StatusConflict {
		t.Errorf("busy API poll = %d", res.StatusCode)
	}
	if got := strings.Join(e.poller.calls, ","); got != "fake/quick,fake/quick" {
		t.Errorf("poller calls = %s", got)
	}
}

func TestAPIStats(t *testing.T) {
	e := newTestEnv(t)
	c := e.client()
	var out struct {
		Sort   string `json:"sort"`
		Total  int    `json:"total"`
		Totals []struct {
			Currency string  `json:"currency"`
			Programs int     `json:"programs"`
			Paid90d  float64 `json:"bounties_paid_90d"`
		} `json:"totals"`
		Programs []struct {
			Handle        string   `json:"handle"`
			PaidPerReport *float64 `json:"bounties_paid_per_report_90d"`
			Stats         struct {
				Paid90d    *float64        `json:"bounties_paid_90d"`
				Efficiency *int            `json:"response_efficiency_percent"`
				Rewards    json.RawMessage `json:"rewards"`
			} `json:"program_stats"`
		} `json:"programs"`
	}
	res, body := e.do(c, "GET", "/api/v1/stats", nil, nil)
	if err := json.Unmarshal([]byte(body), &out); err != nil || res.StatusCode != http.StatusOK {
		t.Fatalf("stats json: %d %v %s", res.StatusCode, err, body)
	}
	if out.Sort != "paid90" || out.Total != 2 || len(out.Programs) != 2 || out.Programs[0].Handle != "acme" ||
		out.Programs[1].Stats.Paid90d != nil || out.Programs[0].PaidPerReport == nil || *out.Programs[0].Stats.Efficiency != 96 {
		t.Errorf("stats json = %s", body)
	}
	if out.Programs[0].Stats.Rewards != nil {
		t.Error("stats list includes bounty tables; they belong on the program endpoint")
	}
	if len(out.Totals) != 1 || out.Totals[0].Currency != "USD" || out.Totals[0].Programs != 2 || out.Totals[0].Paid90d != 41500 {
		t.Errorf("stats totals = %+v", out.Totals)
	}
	_, body = e.do(c, "GET", "/api/v1/stats?sort=resolved&visibility=private&sort=bogus", nil, nil)
	if err := json.Unmarshal([]byte(body), &out); err != nil || out.Total != 1 || out.Programs[0].Handle != "evil" || out.Sort != "resolved" {
		t.Errorf("filtered stats json = %s", body)
	}
	_, body = e.do(c, "GET", "/api/v1/stats?sort=bogus", nil, nil)
	if err := json.Unmarshal([]byte(body), &out); err != nil || out.Sort != "paid90" {
		t.Errorf("unknown sort should fall back to paid90: %s", body)
	}
}

func TestBulkMatch(t *testing.T) {
	e := newTestEnv(t)
	c := e.client()
	post := func(path, body string) (*http.Response, string) {
		t.Helper()
		req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, e.srv.URL+path, strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Content-Type", "text/plain")
		res, err := c.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = res.Body.Close() }()
		b, _ := io.ReadAll(res.Body)
		return res, string(b)
	}
	list := "# subfinder output\nhttps://shop.acme.example/x\nadmin.acme.example\nunrelated.example.org\n\n192.0.2.7\nshop.acme.example\napp.evil.example\n"

	res, body := post("/api/v1/match?format=txt", list)
	if res.StatusCode != http.StatusOK || body != "https://shop.acme.example/x\n192.0.2.7\nshop.acme.example\napp.evil.example\n" {
		t.Errorf("txt = %d %q", res.StatusCode, body)
	}

	_, body = post("/api/v1/match", list)
	var out struct {
		In, Out, None int
		Results       []struct {
			Input, Verdict string
			Matches        []struct {
				InScope bool   `json:"in_scope"`
				Via     string `json:"via"`
				Program struct {
					Handle string `json:"handle"`
				} `json:"program"`
			} `json:"matches"`
		} `json:"results"`
	}
	if err := json.Unmarshal([]byte(body), &out); err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, r := range out.Results {
		s := r.Verdict + " " + r.Input
		for _, m := range r.Matches {
			s += fmt.Sprintf(" %s:%v:%s", m.Program.Handle, m.InScope, m.Via)
		}
		got = append(got, s)
	}
	want := []string{
		"in https://shop.acme.example/x acme:true:*.acme.example",
		"out admin.acme.example acme:false:admin.acme.example",
		"none unrelated.example.org",
		"in 192.0.2.7 evil:true:192.0.2.0/24",
		"in shop.acme.example acme:true:*.acme.example",
		"in app.evil.example evil:true:app.evil.example",
	}
	if out.In != 4 || out.Out != 1 || out.None != 1 || strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("results %d/%d/%d:\n%s\nwant\n%s", out.In, out.Out, out.None, strings.Join(got, "\n"), strings.Join(want, "\n"))
	}

	// Over several lookup batches, the match in the last one is still found.
	if _, body = post("/api/v1/match?format=txt", bigList(1200)+"shop.acme.example\n"); body != "shop.acme.example\n" {
		t.Errorf("long list = %q", body)
	}
	if res, _ := post("/api/v1/match", bigList(matchManyMax+1)); res.StatusCode != http.StatusRequestEntityTooLarge {
		t.Errorf("oversized list = %d, want 413", res.StatusCode)
	}
	// A cross-site page can't make the browser post a list.
	req, _ := http.NewRequestWithContext(context.Background(), http.MethodPost, e.srv.URL+"/api/v1/match", strings.NewReader("a.example.com"))
	req.Header.Set("Sec-Fetch-Site", "cross-site")
	if res, err := c.Do(req); err != nil || res.StatusCode != http.StatusForbidden {
		t.Errorf("cross-site POST = %v, %v; want 403", res, err)
	} else {
		_ = res.Body.Close()
	}
}
