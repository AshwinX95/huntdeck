package hackerone

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/mdpsec/huntdeck/internal/httpclient"
	"github.com/mdpsec/huntdeck/internal/platform"
)

// fakeGraphQL answers program stats queries from testdata/graphql_teams.json
// the way hackerone.com does: one alias per handle, and a "Team does not
// exist" error for unknown handles.
type fakeGraphQL struct {
	t        *testing.T
	srv      *httptest.Server
	teams    map[string]json.RawMessage
	requests int
	sizes    []int
	fail     func(n int) (status int, body string) // optional per-request override, n from 1
	// archivedAsks records follow-up archived-scope queries ("handle after cursor").
	archivedAsks []string
}

func newFakeGraphQL(t *testing.T) *fakeGraphQL {
	f := &fakeGraphQL{t: t}
	b, err := os.ReadFile("testdata/graphql_teams.json")
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(b, &f.teams); err != nil {
		t.Fatal(err)
	}
	f.srv = httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeGraphQL) serve(w http.ResponseWriter, r *http.Request) {
	f.requests++
	if r.Method != http.MethodPost || r.Header.Get("Authorization") != "" {
		f.t.Errorf("stats request: method %s, Authorization %q; want POST without credentials", r.Method, r.Header.Get("Authorization"))
	}
	if f.fail != nil {
		if status, body := f.fail(f.requests); status != 0 {
			w.WriteHeader(status)
			_, _ = w.Write([]byte(body))
			return
		}
	}
	var req struct {
		Query     string            `json:"query"`
		Variables map[string]string `json:"variables"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		f.t.Fatal(err)
	}
	if strings.HasPrefix(req.Query, "query ArchivedScope") {
		f.archivedAsks = append(f.archivedAsks, req.Variables["h0"]+" after "+req.Variables["after"])
		b, err := os.ReadFile("testdata/graphql_archived_page2.json")
		if err != nil {
			f.t.Fatal(err)
		}
		_, _ = fmt.Fprintf(w, `{"data": %s}`, b)
		return
	}
	f.sizes = append(f.sizes, len(req.Variables))
	data := map[string]json.RawMessage{}
	var errs []map[string]any
	for i := range len(req.Variables) {
		alias := fmt.Sprintf("t%d", i)
		if !strings.Contains(req.Query, alias+": team(handle: $h"+fmt.Sprint(i)+")") {
			f.t.Fatalf("query lacks alias %s: %s", alias, req.Query)
		}
		if team, ok := f.teams[req.Variables[fmt.Sprintf("h%d", i)]]; ok {
			data[alias] = team
		} else {
			data[alias] = json.RawMessage("null")
			errs = append(errs, map[string]any{"message": "Team does not exist", "path": []string{alias}, "type": "NOT_FOUND"})
		}
	}
	_ = json.NewEncoder(w).Encode(map[string]any{"data": data, "errors": errs})
}

func (f *fakeGraphQL) client(t *testing.T) *Client {
	t.Helper()
	c, err := New(Options{
		Username: "user", Token: "token", GraphQLURL: f.srv.URL, StatsPerSecond: 1e6,
		HTTP: httpclient.New(httpclient.Options{
			RatePerSecond: 1000, Burst: 100, MaxAttempts: 2,
			Sleep: func(context.Context, time.Duration) error { return nil },
		}),
	})
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func ptr[T any](v T) *T { return &v }

func TestFetchProgramStats(t *testing.T) {
	f := newFakeGraphQL(t)
	got, batches, err := collectStats(f.client(t), []string{"acme", "beta-vdp", "gone", "gamma"})
	if err != nil {
		t.Fatal(err)
	}
	if fmt.Sprint(batches) != "[[acme beta-vdp gone] [gamma]]" {
		t.Errorf("saved batches = %v, want one per request", batches)
	}
	if f.requests != 2 || fmt.Sprint(f.sizes) != "[3 1]" {
		t.Errorf("requests = %d with sizes %v, want 2 with [3 1]", f.requests, f.sizes)
	}
	if _, ok := got["gone"]; ok || len(got) != 3 {
		t.Errorf("got stats for %d programs (gone present: %v), want acme, beta-vdp and gamma", len(got), ok)
	}

	acme := got["acme"]
	want := platform.ProgramStats{
		Currency: "USD", ResolvedReports: ptr(120), ReportsLast90d: ptr(340),
		BountiesPaid: ptr(1250000.0), BountiesPaidLast90d: ptr(41500.0),
		AvgBountyLow: ptr(500.0), AvgBountyHigh: ptr(2000.0), TopBountyLow: ptr(5000.0), TopBountyHigh: ptr(15000.0),
		ResponseEfficiency: ptr(96), LastResolvedAt: ptr(time.Date(2026, 10, 20, 8, 15, 0, 0, time.UTC)), HackersThanked: ptr(210),
		Rewards: []platform.RewardRow{
			{Scope: "*.acme.example", Low: platform.RewardRange{Max: 200}, Medium: platform.RewardRange{Max: 500},
				High: platform.RewardRange{Max: 2000}, Critical: platform.RewardRange{Max: 5000}},
			{Scope: "api.acme.example", Low: platform.RewardRange{Min: 100, Max: 400}, Medium: platform.RewardRange{Max: 1000},
				High: platform.RewardRange{Min: 2500, Max: 4000}, Critical: platform.RewardRange{Min: 10000, Max: 15000}},
		},
		SignalRequirement:  ptr(3.0),
		Requirements:       &platform.Requirements{MinSignal: ptr(3.0), IDVerified: true},
		Facts:              &platform.Facts{OffersSwag: ptr(true), StartedAt: ptr(time.Date(2026, 10, 8, 10, 29, 16, 713000000, time.UTC))},
		FirstResponseHours: ptr(5.0), TriageHours: ptr(66.0), BountyHours: ptr(98.0), ResolutionHours: ptr(1062.0),
		BySeverity: platform.SeverityStats{
			Low:      platform.SeverityStat{AvgBounty: ptr(200.0), Reports: ptr(6)},
			Medium:   platform.SeverityStat{AvgBounty: ptr(521.0), Reports: ptr(43)},
			High:     platform.SeverityStat{AvgBounty: ptr(2184.0), Reports: ptr(34)},
			Critical: platform.SeverityStat{Reports: ptr(0)},
		},
		Profile: platform.ProgramProfile{
			Website:          "https://www.acme.example",
			Introduction:     "Acme takes security seriously.",
			Campaign:         "# Campaign\n\nDouble bounties on *.acme.example.",
			ScopeExclusions:  []platform.Exclusion{{Name: "Rate limiting", Details: "Missing rate limits on login."}},
			StandardsKnown:   true,
			CredentialScopes: []string{"5002"},
		},
	}
	if g, w := statsString(acme), statsString(want); g != w {
		t.Errorf("acme stats\n got %s\nwant %s", g, w)
	}

	beta := got["beta-vdp"]
	// A program without a bounty table has an empty one, not an unknown one.
	if beta.BountiesPaid != nil || beta.AvgBountyLow != nil || beta.ResponseEfficiency != nil ||
		beta.Rewards == nil || len(beta.Rewards) != 0 || beta.ResolvedReports == nil || *beta.ResolvedReports != 7 {
		t.Errorf("VDP stats = %s; want nulls kept as nil and an empty bounty table", statsString(beta))
	}
	if gamma := got["gamma"]; gamma.Currency != "EUR" || gamma.BountiesPaid == nil || *gamma.BountiesPaid != 0 {
		t.Errorf("gamma stats = %s; want a real zero, in EUR", statsString(gamma))
	}
	if p := got["gamma"].Profile; !p.StandardsKnown || len(p.StandardsExclusions) != 1 ||
		p.StandardsExclusions[0] != (platform.Exclusion{Name: "Pays within one month", Details: "Quarterly payouts."}) {
		t.Errorf("gamma standards exclusions = %+v", p)
	}
	if s := got["gamma"].SignalRequirement; s != nil {
		t.Errorf("gamma signal requirement = %v; -10 means none", *s)
	}
	if r := got["gamma"].Requirements; r == nil || r.MinSignal != nil || !r.Cleared || r.IDVerified {
		t.Errorf("gamma requirements = %+v; want cleared only", r)
	}
	if f := got["gamma"].Facts; f != nil {
		t.Errorf("gamma facts = %+v; offers_swag isn't given, so nil", f)
	}
	if p := got["beta-vdp"].Profile; p.StandardsKnown || p.Introduction != "" {
		t.Errorf("beta-vdp profile = %+v; want unknown", p)
	}
}

func statsString(s platform.ProgramStats) string {
	b, _ := json.Marshal(s)
	return string(b)
}

func TestFetchProgramStatsFailures(t *testing.T) {
	for _, tt := range []struct {
		name      string
		fail      func(n int) (int, string)
		wantErr   string
		wantStats int
	}{
		{"server error on second batch", func(n int) (int, string) {
			if n >= 2 {
				return http.StatusBadGateway, ""
			}
			return 0, ""
		}, "HTTP 502", 3},
		{"schema changed", func(int) (int, string) {
			return http.StatusOK, `{"errors":[{"message":"Field 'resolved_report_count' doesn't exist on type 'Team'","path":["query","t0","resolved_report_count"]}]}`
		}, "doesn't exist on type", 0},
		{"blocked", func(int) (int, string) { return http.StatusForbidden, "<html>challenge</html>" }, "HTTP 403", 0},
		{"no data", func(int) (int, string) { return http.StatusOK, `{}` }, "no data", 0},
	} {
		t.Run(tt.name, func(t *testing.T) {
			f := newFakeGraphQL(t)
			f.fail = tt.fail
			got, _, err := collectStats(f.client(t), []string{"acme", "beta-vdp", "gamma", "acme"})
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("err = %v, want it to mention %q", err, tt.wantErr)
			}
			if len(got) != tt.wantStats {
				t.Errorf("kept stats for %d programs, want %d", len(got), tt.wantStats)
			}
		})
	}
}

func TestFlexAmount(t *testing.T) {
	for _, tt := range []struct {
		in   string
		want *float64
		err  bool
	}{
		{`null`, nil, false},
		{`1500`, ptr(1500.0), false},
		{`1500.5`, ptr(1500.5), false},
		{`"$1,500"`, ptr(1500.0), false},
		{`""`, nil, false},
		{`"lots"`, nil, true},
	} {
		var a flexAmount
		err := json.Unmarshal([]byte(tt.in), &a)
		if (err != nil) != tt.err || fmt.Sprint(deref(a.v)) != fmt.Sprint(deref(tt.want)) || (a.v == nil) != (tt.want == nil) {
			t.Errorf("%s → %v, %v; want %v (err %v)", tt.in, a.v, err, tt.want, tt.err)
		}
	}
}

// collectStats runs FetchProgramStats, gathering what each save received.
func collectStats(c *Client, handles []string) (map[string]platform.ProgramStats, [][]string, error) {
	all := map[string]platform.ProgramStats{}
	var batches [][]string
	err := c.FetchProgramStats(context.Background(), handles, func(covered []string, stats map[string]platform.ProgramStats) error {
		batches = append(batches, covered)
		for h, s := range stats {
			all[h] = s
		}
		return nil
	})
	return all, batches, err
}

func TestArchivedScopeComesWithStats(t *testing.T) {
	f := newFakeGraphQL(t)
	c := f.client(t)
	got, _, err := collectStats(c, []string{"zeta", "beta-vdp"})
	if err != nil {
		t.Fatal(err)
	}

	var lines []string
	for _, a := range got["zeta"].Archived {
		lines = append(lines, fmt.Sprintf("%s %s in=%v added=%s removed=%s", a.Entry.PlatformID, a.Entry.Identifier, a.Entry.InScope,
			a.Entry.CreatedAt.Format("2006-01-02"), a.ArchivedAt.Format("2006-01-02")))
	}
	want := []string{
		"9001 old.zeta.example.com in=true added=2025-03-10 removed=2026-09-01",
		"9002 legacy.zeta.example.com in=true added=2024-01-02 removed=2026-05-19",
		"9004 2019.zeta.example.com in=true added=2019-06-01 removed=2020-01-01", // from the second page
	}
	if strings.Join(lines, "\n") != strings.Join(want, "\n") {
		t.Errorf("zeta archived:\n%s\nwant:\n%s", strings.Join(lines, "\n"), strings.Join(want, "\n"))
	}
	if len(got["beta-vdp"].Archived) != 0 {
		t.Errorf("beta-vdp archived = %v", got["beta-vdp"].Archived)
	}
	if strings.Join(f.archivedAsks, ",") != "zeta after MTAw" {
		t.Errorf("follow-up queries = %v; want one for zeta's second page", f.archivedAsks)
	}
}
