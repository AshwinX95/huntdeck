package web

import (
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/mdpsec/huntdeck/internal/store"
)

// Each platform's numbers say what they are: published as they are, the
// platform's; worked out by HuntDeck, derived, and estimated where they're
// estimates; typed in, manual.
func TestProgramMetaV2PerPlatform(t *testing.T) {
	at := time.Date(2026, 11, 1, 9, 0, 0, 0, time.UTC)
	times := store.ProgramTimes{Fields: map[string]*time.Time{store.ValPaid90d: &at}}
	type want struct {
		estimated bool
		source    string
	}
	tests := []struct {
		name  string
		item  store.ProgramListItem
		field func(apiProgramMetaV2) apiValueMeta
		want  want
	}{
		{"hackerone publishes paid in 90 days",
			store.ProgramListItem{ProgramRef: store.ProgramRef{Platform: "hackerone"}, Paid90d: ptrTo(1000.0)},
			func(m apiProgramMetaV2) apiValueMeta { return m.Numbers.Paid90d.apiValueMeta }, want{false, "hackerone"}},
		{"bugcrowd's paid in 90 days is an estimate",
			store.ProgramListItem{ProgramRef: store.ProgramRef{Platform: "bugcrowd"}, Paid90d: ptrTo(1000.0), Paid90dEstimated: true},
			func(m apiProgramMetaV2) apiValueMeta { return m.Numbers.Paid90d.apiValueMeta }, want{true, "derived"}},
		{"bugcrowd's total paid is an estimate",
			store.ProgramListItem{ProgramRef: store.ProgramRef{Platform: "bugcrowd"}, PaidAll: ptrTo(9000.0), PaidAllEstimated: true},
			func(m apiProgramMetaV2) apiValueMeta { return m.Numbers.PaidTotal.apiValueMeta }, want{true, "derived"}},
		{"bugcrowd publishes no received count",
			store.ProgramListItem{ProgramRef: store.ProgramRef{Platform: "bugcrowd"}},
			func(m apiProgramMetaV2) apiValueMeta { return m.Numbers.Received90d.apiValueMeta }, want{false, "bugcrowd"}},
		{"bugcrowd's accepted count is counted from CrowdStream",
			store.ProgramListItem{ProgramRef: store.ProgramRef{Platform: "bugcrowd"}, Accepted90d: ptrTo(4)},
			func(m apiProgramMetaV2) apiValueMeta { return m.Numbers.Accepted90d.apiValueMeta }, want{false, "derived"}},
		{"yeswehack's received from under 90 days of totals is an estimate",
			store.ProgramListItem{ProgramRef: store.ProgramRef{Platform: "yeswehack"}, Reports90d: ptrTo(40), CountedDays: ptrTo(30)},
			func(m apiProgramMetaV2) apiValueMeta { return m.Numbers.Received90d.apiValueMeta }, want{true, "derived"}},
		{"intigriti's received from 90 days of totals is worked out, not estimated",
			store.ProgramListItem{ProgramRef: store.ProgramRef{Platform: "intigriti"}, Reports90d: ptrTo(40), CountedDays: ptrTo(90)},
			func(m apiProgramMetaV2) apiValueMeta { return m.Numbers.Received90d.apiValueMeta }, want{false, "derived"}},
		{"yeswehack's valid reports are estimated from its totals",
			store.ProgramListItem{ProgramRef: store.ProgramRef{Platform: "yeswehack"}, Resolved90d: ptrTo(7), Resolved90dEstimated: true},
			func(m apiProgramMetaV2) apiValueMeta { return m.Numbers.Resolved90d.apiValueMeta }, want{true, "derived"}},
		{"hackerone's resolved reports are counted from Hacktivity",
			store.ProgramListItem{ProgramRef: store.ProgramRef{Platform: "hackerone"}, Resolved90d: ptrTo(7)},
			func(m apiProgramMetaV2) apiValueMeta { return m.Numbers.Resolved90d.apiValueMeta }, want{false, "derived"}},
		{"a bounty table's range is the platform's",
			store.ProgramListItem{ProgramRef: store.ProgramRef{Platform: "hackerone"}, BountyMax: ptrTo(5000.0), BountyFrom: store.PayFromTable},
			func(m apiProgramMetaV2) apiValueMeta { return m.Bounty.apiValueMeta }, want{false, "hackerone"}},
		{"a range from the most it has paid is an estimate",
			store.ProgramListItem{ProgramRef: store.ProgramRef{Platform: "intigriti"}, BountyMax: ptrTo(5000.0), BountyFrom: store.PayFromStats},
			func(m apiProgramMetaV2) apiValueMeta { return m.Bounty.apiValueMeta }, want{true, "derived"}},
		{"scope counts are the platform's",
			store.ProgramListItem{ProgramRef: store.ProgramRef{Platform: "yeswehack"}, InScope: 3},
			func(m apiProgramMetaV2) apiValueMeta { return m.InScope.apiValueMeta }, want{false, "yeswehack"}},
		{"a program added by hand is typed in",
			store.ProgramListItem{ProgramRef: store.ProgramRef{Platform: store.Independent}, BountyMax: ptrTo(500.0), BountyFrom: store.PayFromListing, InScope: 2},
			func(m apiProgramMetaV2) apiValueMeta { return m.Bounty.apiValueMeta }, want{false, "manual"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.field(programMetaV2(tt.item, times))
			if got.Estimated != tt.want.estimated || got.Source != tt.want.source {
				t.Errorf("estimated %v, source %q; want %v, %q", got.Estimated, got.Source, tt.want.estimated, tt.want.source)
			}
		})
	}
	m := programMetaV2(tests[0].item, times)
	if m.Numbers.Paid90d.UpdatedAt == nil || !m.Numbers.Paid90d.UpdatedAt.Equal(at) || m.Numbers.Paid90d.Value == nil || *m.Numbers.Paid90d.Value != 1000 {
		t.Errorf("paid_90d = %+v; want the value with its time", m.Numbers.Paid90d)
	}
	if m.Numbers.PaidTotal.UpdatedAt != nil {
		t.Errorf("paid_total dated %v; nothing dates it", m.Numbers.PaidTotal.UpdatedAt)
	}
}

// v2 gives v1's values, each with what it is; both give the program's
// updated_at and last_synced_at.
func TestAPIV2Values(t *testing.T) {
	e := newTestEnv(t)
	c := e.client()
	var v1 struct {
		InScope int        `json:"in_scope_entries"`
		Updated time.Time  `json:"updated_at"`
		Synced  *time.Time `json:"last_synced_at"`
		Numbers struct {
			Paid90d *float64 `json:"paid_90d"`
		} `json:"numbers"`
	}
	var v2 struct {
		InScope apiMetaInt    `json:"in_scope_entries"`
		Updated time.Time     `json:"updated_at"`
		Bounty  apiMetaBounty `json:"bounty"`
		Numbers struct {
			Paid90d     apiMetaNumber `json:"paid_90d"`
			Received90d apiMetaCount  `json:"reports_received_90d"`
		} `json:"numbers"`
	}
	for path, out := range map[string]any{"/api/v1/programs/fake/acme": &v1, "/api/v2/programs/fake/acme": &v2} {
		res, body := e.do(c, "GET", path, nil, nil)
		if res.StatusCode != http.StatusOK {
			t.Fatalf("%s = %d: %s", path, res.StatusCode, body)
		}
		if err := json.Unmarshal([]byte(body), out); err != nil {
			t.Fatal(err)
		}
	}
	if v1.Updated.IsZero() || v1.Synced == nil || !v2.Updated.Equal(v1.Updated) {
		t.Errorf("updated_at %v / %v, last_synced_at %v; want both set, the same in v1 and v2", v1.Updated, v2.Updated, v1.Synced)
	}
	if v2.InScope.Value != 2 || v2.InScope.Source != "fake" || v2.InScope.UpdatedAt == nil { // v1 lists have it; v1's program doesn't
		t.Errorf("v2 in_scope_entries = %+v; want 2 from fake, dated", v2.InScope)
	}
	p := v2.Numbers.Paid90d
	if p.Value == nil || v1.Numbers.Paid90d == nil || *p.Value != *v1.Numbers.Paid90d || p.Estimated || p.Source != "fake" || p.UpdatedAt == nil {
		t.Errorf("v2 paid_90d = %+v (v1: %v); want v1's value, published by fake, dated", p, v1.Numbers.Paid90d)
	}
	if v2.Numbers.Received90d.Value == nil || *v2.Numbers.Received90d.Value != 340 {
		t.Errorf("v2 reports_received_90d = %+v; want 340", v2.Numbers.Received90d)
	}
}

// Program, list and match answers carry an ETag and Last-Modified; a
// request naming the current one gets 304 and no body, until the answer
// changes. A read-only copy answers as it did.
func TestConditionalRequests(t *testing.T) {
	e := newTestEnv(t)
	c := e.client()
	for _, path := range []string{"/api/v1/programs/fake/acme", "/api/v2/programs/fake/acme", "/api/v1/programs?state=all",
		"/api/v2/programs", "/api/v1/invites", "/api/v2/invites", "/api/v1/match?host=shop.acme.example"} {
		res, body := e.do(c, "GET", path, nil, nil)
		etag, mod := res.Header.Get("ETag"), res.Header.Get("Last-Modified")
		if res.StatusCode != http.StatusOK || !strings.HasPrefix(etag, `W/"`) || mod == "" || body == "" {
			t.Fatalf("%s = %d, ETag %q, Last-Modified %q", path, res.StatusCode, etag, mod)
		}
		if cc := res.Header.Get("Cache-Control"); cc != "no-cache" {
			t.Errorf("%s: Cache-Control %q, want no-cache", path, cc)
		}
		tests := []struct {
			name   string
			header http.Header
			want   int
		}{
			{"If-None-Match", http.Header{"If-None-Match": {etag}}, http.StatusNotModified},
			{"strong form of the tag", http.Header{"If-None-Match": {strings.TrimPrefix(etag, "W/")}}, http.StatusNotModified},
			{"one of several", http.Header{"If-None-Match": {`"other", ` + etag}}, http.StatusNotModified},
			{"another tag", http.Header{"If-None-Match": {`W/"other"`}}, http.StatusOK},
			{"If-Modified-Since", http.Header{"If-Modified-Since": {mod}}, http.StatusNotModified},
			{"If-Modified-Since earlier", http.Header{"If-Modified-Since": {"Mon, 01 Jan 2024 00:00:00 GMT"}}, http.StatusOK},
			{"If-None-Match wins", http.Header{"If-None-Match": {`W/"other"`}, "If-Modified-Since": {mod}}, http.StatusOK},
		}
		for _, tt := range tests {
			res, body := e.do(c, "GET", path, nil, tt.header)
			if res.StatusCode != tt.want {
				t.Errorf("%s, %s: %d, want %d", path, tt.name, res.StatusCode, tt.want)
			}
			if res.StatusCode == http.StatusNotModified && (body != "" || res.Header.Get("ETag") != etag) {
				t.Errorf("%s, %s: 304 with body %q, ETag %q", path, tt.name, body, res.Header.Get("ETag"))
			}
		}
	}

	// A change gives a new ETag: the old one gets the whole answer.
	path := "/api/v1/programs/fake/acme"
	res, _ := e.do(c, "GET", path, nil, nil)
	old := res.Header.Get("ETag")
	if res, _ := e.do(c, "POST", "/programs/fake/acme/tags", url.Values{"tags": {"later"}}, nil); res.StatusCode >= 400 {
		t.Fatalf("tagging = %d", res.StatusCode)
	}
	res, body := e.do(c, "GET", path, nil, http.Header{"If-None-Match": {old}})
	if res.StatusCode != http.StatusOK || res.Header.Get("ETag") == old || !strings.Contains(body, `"later"`) {
		t.Errorf("after a change: %d, ETag %q (was %q)", res.StatusCode, res.Header.Get("ETag"), old)
	}

	ro := newTestEnv(t, func(d *Deps) { d.ReadOnly = true })
	res, _ = ro.do(ro.client(), "GET", path, nil, http.Header{"If-None-Match": {"*"}})
	if res.StatusCode != http.StatusOK || res.Header.Get("ETag") != "" || res.Header.Get("Cache-Control") != demoCache {
		t.Errorf("read-only copy: %d, ETag %q, Cache-Control %q; want it as before", res.StatusCode, res.Header.Get("ETag"), res.Header.Get("Cache-Control"))
	}
}

// since keeps the programs and targets that changed after it.
func TestSinceFilters(t *testing.T) {
	e := newTestEnv(t)
	c := e.client()
	future := time.Now().Add(time.Hour).UTC().Format(time.RFC3339)
	total := func(path string) int {
		t.Helper()
		res, body := e.do(c, "GET", path, nil, nil)
		if res.StatusCode != http.StatusOK {
			t.Fatalf("%s = %d: %s", path, res.StatusCode, body)
		}
		var out struct {
			Total int `json:"total"`
		}
		if err := json.Unmarshal([]byte(body), &out); err != nil {
			t.Fatal(err)
		}
		return out.Total
	}
	for _, base := range []string{"/api/v1/programs?state=all", "/api/v2/programs?state=all", "/api/v1/invites?state=all"} {
		all := total(base)
		if all == 0 {
			t.Fatalf("%s lists nothing", base)
		}
		if n := total(base + "&since=3650d"); n != all {
			t.Errorf("%s since 10 years: %d, want %d", base, n, all)
		}
		if n := total(base + "&since=" + url.QueryEscape(future)); n != 0 {
			t.Errorf("%s since an hour from now: %d, want 0", base, n)
		}
	}
	if res, _ := e.do(c, "GET", "/api/v1/programs?since=yesterday", nil, nil); res.StatusCode != http.StatusBadRequest {
		t.Errorf("since=yesterday: %d, want 400", res.StatusCode)
	}
	// Reading changes nothing: nothing has changed since now.
	mark := time.Now().UTC()
	if n := total("/api/v1/programs?since=" + url.QueryEscape(mark.Format(time.RFC3339Nano))); n != 0 {
		t.Errorf("nothing changed since the mark, yet %d programs listed", n)
	}

	targets := func(q string) string {
		t.Helper()
		res, body := e.do(c, "GET", "/api/v1/targets?"+q, nil, nil)
		if res.StatusCode != http.StatusOK {
			t.Fatalf("targets?%s = %d: %s", q, res.StatusCode, body)
		}
		return body
	}
	if got := targets("kind=wildcards&since=3650d"); !strings.Contains(got, "*.acme.example") {
		t.Errorf("wildcards since 10 years = %q", got)
	}
	if got := targets("kind=all&since=" + url.QueryEscape(future)); got != "" {
		t.Errorf("targets since an hour from now = %q, want none", got)
	}
	if got := targets("kind=wildcards&format=json&since=" + url.QueryEscape(future)); !strings.Contains(got, `"total": 0`) {
		t.Errorf("JSON targets since an hour from now = %s", got)
	}
}

// comment=1 starts a text export with a generated_at line and leaves the
// rest as it was; every export has the time in a header.
func TestExportComments(t *testing.T) {
	e := newTestEnv(t)
	c := e.client()
	tests := []struct {
		name, method, path, body, prefix string
	}{
		{"targets", "GET", "/api/v1/targets?kind=all", "", "# generated_at="},
		{"bulk match", "POST", "/api/v1/match?format=txt", "shop.acme.example\nnothing.invalid\n", "# generated_at="},
		{"ZAP context", "GET", "/api/v1/programs/fake/acme/scope.zap.context", "", ""},
		{"Burp scope", "GET", "/api/v1/programs/fake/acme/scope.burp.json", "", ""},
		{"Caido scope", "GET", "/api/v1/programs/fake/acme/scope.caido.json", "", ""},
	}
	send := func(method, path, body string) (*http.Response, string) {
		t.Helper()
		req, err := http.NewRequestWithContext(t.Context(), method, e.srv.URL+path, strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		res, err := c.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = res.Body.Close() }()
		var b strings.Builder
		buf := make([]byte, 4096)
		for {
			n, err := res.Body.Read(buf)
			b.Write(buf[:n])
			if err != nil {
				break
			}
		}
		return res, b.String()
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			res, plain := send(tt.method, tt.path, tt.body)
			if res.StatusCode != http.StatusOK || plain == "" {
				t.Fatalf("%d %q", res.StatusCode, plain)
			}
			at := res.Header.Get("X-HuntDeck-Generated-At")
			if _, err := time.Parse(time.RFC3339, at); err != nil {
				t.Errorf("X-HuntDeck-Generated-At = %q", at)
			}
			sep := "?"
			if strings.Contains(tt.path, "?") {
				sep = "&"
			}
			res, commented := send(tt.method, tt.path+sep+"comment=1", tt.body)
			stamp := res.Header.Get("X-HuntDeck-Generated-At")
			switch {
			case tt.prefix != "":
				line, rest, _ := strings.Cut(commented, "\n")
				if line != tt.prefix+stamp || rest != plain {
					t.Errorf("first line %q (want %q), the rest changed: %v", line, tt.prefix+stamp, rest != plain)
				}
			case strings.HasSuffix(tt.path, ".zap.context"):
				decl, rest, _ := strings.Cut(commented, "\n")
				line, rest, _ := strings.Cut(rest, "\n")
				pdecl, prest, _ := strings.Cut(plain, "\n")
				if line != "<!-- generated_at="+stamp+" -->" || decl != pdecl || rest != prest {
					t.Errorf("ZAP with comment=1 = %q", commented[:min(200, len(commented))])
				}
			default: // JSON: unchanged
				if commented != plain {
					t.Errorf("JSON export changed by comment=1")
				}
			}
		})
	}
}
