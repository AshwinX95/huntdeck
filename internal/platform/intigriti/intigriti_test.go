package intigriti

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/mdpsec/huntdeck/internal/httpclient"
	"github.com/mdpsec/huntdeck/internal/platform"
	"github.com/mdpsec/huntdeck/internal/scope"
)

const token = "test-token"

func server(t *testing.T, routes map[string]string) (*Client, map[string]int) {
	t.Helper()
	hits := map[string]int{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		key := r.URL.Path
		if r.URL.RawQuery != "" {
			key += "?" + r.URL.RawQuery
		}
		hits[key]++
		if r.Header.Get("Authorization") != "Bearer "+token {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		name, ok := routes[key]
		switch {
		case !ok:
			http.NotFound(w, r)
		case strings.HasPrefix(name, "status:"):
			var code int
			_, _ = fmt.Sscanf(name, "status:%d", &code)
			w.WriteHeader(code)
		default:
			b, err := os.ReadFile(filepath.Join("testdata", name))
			if err != nil {
				t.Fatal(err)
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write(b)
		}
	}))
	t.Cleanup(srv.Close)
	c, err := New(Options{Token: token, BaseURL: srv.URL, HTTP: httpclient.New(httpclient.Options{RatePerSecond: 1000, MaxAttempts: 1})})
	if err != nil {
		t.Fatal(err)
	}
	return c, hits
}

var listRoutes = map[string]string{
	"/v1/programs?limit=500&offset=0":            "programs_0.json",
	"/v1/programs?limit=500&offset=2":            "programs_2.json",
	"/v1/programs/activities?limit=500&offset=0": "activities.json",
}

func TestListPrograms(t *testing.T) {
	c, _ := server(t, listRoutes)
	l, err := c.ListPrograms(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !l.Complete || len(l.Programs) != 3 {
		t.Fatalf("complete=%v programs=%d", l.Complete, len(l.Programs))
	}
	want := []string{
		"11111111-1111-4111-8111-111111111111 Acme public open bounty=true EUR 50..5000 https://app.intigriti.com/researcher/program-redirect/acmecorp/acme apply=false",
		"22222222-2222-4222-8222-222222222222 Globex Private private paused bounty=true USD -..2500 https://app.intigriti.com/researcher/program-redirect/globexco/globex apply=false",
		"33333333-3333-4333-8333-333333333333 Initech VDP public closed bounty=false  -..- https://app.intigriti.com/researcher/program-redirect/initech/initech apply=true",
	}
	for i, p := range l.Programs {
		vis := map[bool]string{true: "private", false: "public"}[p.Private]
		got := fmt.Sprintf("%s %s %s %s bounty=%v %s %s %s apply=%v", p.Handle, p.Name, vis, p.State, p.OffersBounty, p.Currency, bountyRange(p), p.URL,
			p.Requirements != nil && p.Requirements.Application)
		if got != want[i] {
			t.Errorf("program %d:\n got %s\nwant %s", i, got, want[i])
		}
	}
}

func TestScopeChangesFromActivities(t *testing.T) {
	c, _ := server(t, listRoutes)
	l, err := c.ListPrograms(context.Background())
	if err != nil || l.ChangesErr != nil {
		t.Fatal(err, l.ChangesErr)
	}
	// The fixture holds 5 of 9 events, so it covers back to its oldest.
	if want := time.Unix(1790000000, 0).UTC(); l.ChangesSince == nil || !l.ChangesSince.Equal(want) {
		t.Errorf("ChangesSince = %v, want %v", l.ChangesSince, want)
	}
	want := map[string]*time.Time{
		"11111111-1111-4111-8111-111111111111": ptr(time.Unix(1792933752, 0).UTC()), // newest domains version
		"22222222-2222-4222-8222-222222222222": nil,                                 // status change only
		"33333333-3333-4333-8333-333333333333": nil,                                 // rules change only
	}
	for _, p := range l.Programs {
		w := want[p.Handle]
		if (w == nil) != (p.ScopeChangedAt == nil) || (w != nil && !w.Equal(*p.ScopeChangedAt)) {
			t.Errorf("%s: ScopeChangedAt = %v, want %v", p.Handle, p.ScopeChangedAt, w)
		}
	}
}

func TestActivityFeedFailureKeepsListing(t *testing.T) {
	routes := map[string]string{"/v1/programs/activities?limit=500&offset=0": "status:500"}
	for k, v := range listRoutes {
		if routes[k] == "" {
			routes[k] = v
		}
	}
	c, _ := server(t, routes)
	l, err := c.ListPrograms(context.Background())
	if err != nil || !l.Complete || len(l.Programs) != 3 {
		t.Fatalf("listing: complete=%v programs=%d err=%v", l.Complete, len(l.Programs), err)
	}
	if l.ChangesErr == nil || l.ChangesSince != nil {
		t.Errorf("ChangesErr = %v, ChangesSince = %v; want an error and no coverage", l.ChangesErr, l.ChangesSince)
	}
}

func ptr[T any](v T) *T { return &v }

func TestListingFailures(t *testing.T) {
	c, _ := server(t, map[string]string{"/v1/programs?limit=500&offset=0": "programs_0.json", "/v1/programs?limit=500&offset=2": "status:503"})
	l, err := c.ListPrograms(context.Background())
	if err == nil || l.Complete || len(l.Programs) != 2 {
		t.Fatalf("partial: complete=%v programs=%d err=%v", l.Complete, len(l.Programs), err)
	}

	bad, err := New(Options{Token: "wrong", BaseURL: c.base.String(), HTTP: c.http})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := bad.ListPrograms(context.Background()); !errors.Is(err, ErrAuth) {
		t.Fatalf("bad token: err = %v", err)
	}
	if cr := bad.Credentials(); len(cr) != 1 || cr[0].Name != "API token" || cr[0].LastFail == nil || !strings.Contains(cr[0].Failure, "401") {
		t.Errorf("credentials after a refusal = %+v", cr)
	}
	if cr := c.Credentials(); len(cr) != 1 || cr[0].LastOK == nil || cr[0].Failure != "" {
		t.Errorf("credentials after a listing = %+v", cr)
	}
}

func TestFetchScope(t *testing.T) {
	c, _ := server(t, map[string]string{
		"/v1/programs/11111111-1111-4111-8111-111111111111": "program_acme.json",
		"/v1/programs/33333333-3333-4333-8333-333333333333": "program_nodomains.json",
		"/v1/programs/44444444-4444-4444-8444-444444444444": "status:403",
	})
	es, err := c.FetchScope(context.Background(), platform.Program{Handle: "acme", PlatformID: "11111111-1111-4111-8111-111111111111"})
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, e := range es {
		got = append(got, fmt.Sprintf("%s|%v|%s|%s|%s|%v|%s|%s", e.PlatformID, e.InScope, e.AssetType, e.PlatformAssetType, e.Identifier, e.BountyEligible, e.MaxSeverity, e.Instruction))
	}
	want := []string{
		"d1|true|wildcard|Wildcard|*.acme.example.com|true|Tier 1|Everything under acme",
		"d2|true|url|Url|https://shop.acme.example.com|true|Tier 3|",
		"d3|true|cidr|IpRange|198.51.100.0/24|false|No Bounty|",
		"d4|true|android|Android|com.example.acme|true|Tier 2|",
		"d5|false|domain|Url|blog.acme.example.com|false||Hosted by a third party",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("entries:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}

	for _, id := range []string{"33333333-3333-4333-8333-333333333333", "44444444-4444-4444-8444-444444444444"} {
		if _, err := c.FetchScope(context.Background(), platform.Program{Handle: id, PlatformID: id}); !errors.Is(err, platform.ErrScopeUnavailable) {
			t.Errorf("%s: err = %v", id, err)
		}
	}
	_, d, err := c.FetchScopeDetail(context.Background(), platform.Program{Handle: "acme", PlatformID: "11111111-1111-4111-8111-111111111111"})
	if err != nil || d.Policy != "#### Our promise\n\n* We respond quickly" || d.Rewards != nil {
		t.Errorf("detail = %+v, %v; want the rules and no bounty table", d, err)
	}
	wantRules := platform.TestingRules{RateLimit: 5, UserAgent: "Intigriti-researcher1", Header: "X-Bug-Bounty: researcher1", PlatformEmail: true}
	if d.Testing == nil || *d.Testing != wantRules {
		t.Errorf("testing rules = %+v, want %+v", d.Testing, wantRules)
	}
}

// automatedTooling: null is no rule, 0 forbids automated tools, more is a
// rate.
func TestTestingRules(t *testing.T) {
	f := func(v float64) *float64 { return &v }
	for _, tt := range []struct {
		tooling *float64
		want    platform.TestingRules
	}{
		{nil, platform.TestingRules{}},
		{f(0), platform.TestingRules{NoAutomation: true}},
		{f(2.5), platform.TestingRules{RateLimit: 2.5}},
	} {
		if got := (testingJSON{AutomatedTooling: tt.tooling}).rules(); *got != tt.want {
			t.Errorf("automatedTooling %v: %+v, want %+v", tt.tooling, *got, tt.want)
		}
	}
}

func TestAssetType(t *testing.T) {
	tests := []struct {
		t     enum
		ident string
		want  string
	}{
		{enum{1, "Url"}, "app.example.com", scope.AssetDomain},
		{enum{1, "Url"}, "https://app.example.com/x", scope.AssetURL},
		{enum{7, "Wildcard"}, "*.example.com", scope.AssetWildcard},
		{enum{4, "IpRange"}, "192.0.2.1", scope.AssetIP},
		{enum{5, "Device"}, "Router", scope.AssetHardware},
		{enum{6, "Other"}, "Anything", scope.AssetOther},
		{enum{99, "Android"}, "com.example", scope.AssetAndroid},
	}
	for _, tt := range tests {
		if got := assetType(tt.t, tt.ident); got != tt.want {
			t.Errorf("assetType(%v, %q) = %q, want %q", tt.t, tt.ident, got, tt.want)
		}
	}
}

func TestPrivate(t *testing.T) {
	for level, want := range map[int]bool{1: true, 2: false, 3: false, 4: false, 9: true} {
		if got := private(level); got != want {
			t.Errorf("private(%d) = %v, want %v", level, got, want)
		}
	}
}

func TestWebURL(t *testing.T) {
	tests := map[string]string{
		"https://app.intigriti.com/programs/a/b/detail":                      "https://app.intigriti.com/researcher/program-redirect/a/b",
		"https://app.intigriti.com/researcher/programs/a/b/detail":           "https://app.intigriti.com/researcher/program-redirect/a/b",
		"https://login.intigriti.com/account/login?redirect=/programs/a/b/x": "https://app.intigriti.com/researcher/program-redirect/a/b",
		"https://login.intigriti.com/account/login?redirect=/dashboard":      "https://app.intigriti.com/researcher/dashboard",
		"https://login.intigriti.com/x?redirect=//evil.example.com/programs": "https://login.intigriti.com/x?redirect=//evil.example.com/programs",
		"https://evil.example.com/programs/a/b":                              "https://app.intigriti.com/researcher/programs",
		"":                                                                   "https://app.intigriti.com/researcher/programs",
	}
	for in, want := range tests {
		if got := webURL(in); got != want {
			t.Errorf("webURL(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestScopeHistory(t *testing.T) {
	const acme, globex, initech = "11111111-1111-4111-8111-111111111111", "22222222-2222-4222-8222-222222222222", "33333333-3333-4333-8333-333333333333"
	c, hits := server(t, map[string]string{
		"/v1/programs/activities?limit=500&offset=0": "activities_all.json",
		"/v1/programs/" + acme + "/domains/v-8":      "domains_v8.json",
		"/v1/programs/" + acme + "/domains/v-9":      "domains_v9.json",
		"/v1/programs/" + globex:                     "program_acme.json", // no domains events: current version only
		"/v1/programs/" + initech:                    "status:403",        // terms not accepted: skipped
	})
	got := map[string][]platform.ScopeVersion{}
	progs := []platform.Program{{Handle: acme, PlatformID: acme}, {Handle: globex, PlatformID: globex}, {Handle: initech, PlatformID: initech}}
	err := c.ScopeHistory(context.Background(), progs, func(handle string, v []platform.ScopeVersion) error {
		got[handle] = v
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || len(got[acme]) != 2 || len(got[globex]) != 1 {
		t.Fatalf("history: %v", got)
	}
	v0, v1 := got[acme][0], got[acme][1]
	if !v0.At.Equal(time.Unix(1700000000, 0).UTC()) || !v1.At.Equal(time.Unix(1792765857, 0).UTC()) {
		t.Errorf("acme version dates %v, %v; want oldest first", v0.At, v1.At)
	}
	if v0.Entries[len(v0.Entries)-1].Identifier != "https://old.acme.example.com" || len(v1.Entries) != 5 {
		t.Errorf("acme entries: %+v / %d", v0.Entries, len(v1.Entries))
	}
	if hits["/v1/programs/activities?limit=500&offset=0"] != 1 {
		t.Errorf("feed read %d times, want once per call", hits["/v1/programs/activities?limit=500&offset=0"])
	}
}

func TestStateHistory(t *testing.T) {
	const acme, globex, initech = "11111111-1111-4111-8111-111111111111", "22222222-2222-4222-8222-222222222222", "33333333-3333-4333-8333-333333333333"
	c, hits := server(t, map[string]string{"/v1/programs/activities?limit=500&offset=0": "activities_status.json"})
	got := map[string][]platform.StateChange{}
	progs := []platform.Program{{Handle: "acme", PlatformID: acme}, {Handle: "globex", PlatformID: globex}, {Handle: "initech", PlatformID: initech}}
	err := c.StateHistory(context.Background(), progs, func(handle string, ch []platform.StateChange) error {
		got[handle] = ch
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	want := map[string][]platform.StateChange{
		"acme": {{At: time.Unix(1792800000, 0).UTC(), From: platform.StateOpen, To: platform.StateClosed}},
		"globex": {
			{At: time.Unix(1792000000, 0).UTC(), From: platform.StateOpen, To: platform.StatePaused},
			{At: time.Unix(1792900000, 0).UTC(), From: platform.StatePaused, To: platform.StateOpen},
		},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("state history = %+v, want %+v (oldest first; initech has none)", got, want)
	}
	if hits["/v1/programs/activities?limit=500&offset=0"] != 1 {
		t.Errorf("feed read %d times", hits["/v1/programs/activities?limit=500&offset=0"])
	}
}

// bountyRange formats a program's bounty range for comparisons.
func bountyRange(p platform.Program) string {
	v := func(f *float64) string {
		if f == nil {
			return "-"
		}
		return strconv.FormatFloat(*f, 'f', -1, 64)
	}
	return v(p.BountyMin) + ".." + v(p.BountyMax)
}
