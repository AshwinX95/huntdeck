package intigriti

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/mdpsec/huntdeck/internal/httpclient"
	"github.com/mdpsec/huntdeck/internal/platform"
)

// publicServer serves the public program pages; any request carrying the
// token fails, since the token is only for the researcher API.
func publicServer(t *testing.T, routes map[string]string) (*Client, map[string]int) {
	t.Helper()
	hits := map[string]int{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits[r.URL.Path]++
		if r.Header.Get("Authorization") != "" {
			t.Errorf("%s sent an Authorization header", r.URL.Path)
		}
		name, ok := routes[r.URL.Path]
		switch {
		case !ok:
			http.NotFound(w, r)
		case name == "challenge":
			w.Header().Set("Content-Type", "text/html")
			_, _ = w.Write([]byte("<!doctype html><title>Just a moment</title>"))
		default:
			b, err := os.ReadFile(filepath.Join("testdata", name))
			if err != nil {
				t.Fatal(err)
			}
			w.Header().Set("Content-Type", "application/json; charset=utf-8")
			_, _ = w.Write(b)
		}
	}))
	t.Cleanup(srv.Close)
	c, err := New(Options{Token: token, BaseURL: srv.URL + "/api", PublicURL: srv.URL + "/public",
		HTTP: httpclient.New(httpclient.Options{RatePerSecond: 1000, MaxAttempts: 1})})
	if err != nil {
		t.Fatal(err)
	}
	return c, hits
}

// acmeRewards is public_acme.json's newest bounty table: a row per paying
// tier (No Bounty pays nothing), the CVSS bands grouped by severity, and the
// 9.0–9.4 and 9.5–10 bands both counted as critical.
var acmeRewards = []platform.RewardRow{
	{Scope: "Tier 1", Low: platform.RewardRange{Min: 100, Max: 250}, Medium: platform.RewardRange{Min: 250, Max: 1000},
		High: platform.RewardRange{Min: 1000, Max: 2500}, Critical: platform.RewardRange{Min: 2500, Max: 5000}},
	{Scope: "Tier 2", Low: platform.RewardRange{Min: 50, Max: 100}, Medium: platform.RewardRange{Min: 100, Max: 500},
		High: platform.RewardRange{Min: 500, Max: 1000}, Critical: platform.RewardRange{Min: 1000, Max: 2000}},
}

func TestFetchProgramStats(t *testing.T) {
	const (
		acme   = "11111111-1111-4111-8111-111111111111"
		globex = "22222222-2222-4222-8222-222222222222" // apply-to-join: no public page
		vdp    = "33333333-3333-4333-8333-333333333333"
		hooli  = "44444444-4444-4444-8444-444444444444" // its page now belongs to another program
		gone   = "55555555-5555-4555-8555-555555555555" // not listed
	)
	i := func(n int) *int { return &n }
	f := func(v float64) *float64 { return &v }
	tests := []struct {
		name   string
		routes map[string]string
		want   map[string]platform.ProgramStats
		err    bool
	}{
		{name: "public pages", routes: map[string]string{
			"/public/programs": "public_programs.json", "/public/programs/acmecorp/acme": "public_acme.json",
			"/public/programs/initech/vdp": "public_vdp.json", "/public/programs/hooli/hooli": "public_moved.json",
		}, want: map[string]platform.ProgramStats{
			acme: {Currency: "EUR", BountiesPaid: f(84052), AvgBountyLow: f(850), AvgBountyHigh: f(850), TotalReports: i(742), AcceptedReports: i(96),
				Rewards: acmeRewards},
			vdp: {TotalReports: i(3175)}, // 0 accepted: not shown
		}},
		{name: "a 0 count means not shown", routes: map[string]string{
			"/public/programs": "public_programs.json", "/public/programs/hooli/hooli": "public_hidden.json",
		}, want: map[string]platform.ProgramStats{
			hooli: {Currency: "USD", BountiesPaid: f(968898), AvgBountyLow: f(1449), AvgBountyHigh: f(1449), AcceptedReports: i(682)},
		}},
		{name: "a page that fails is left out", routes: map[string]string{
			"/public/programs": "public_programs.json", "/public/programs/acmecorp/acme": "challenge",
			"/public/programs/initech/vdp": "public_vdp.json",
		}, want: map[string]platform.ProgramStats{vdp: {TotalReports: i(3175)}}},
		{name: "no list, no stats", routes: map[string]string{"/public/programs": "challenge"}, err: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c, hits := publicServer(t, tt.routes)
			got := map[string]platform.ProgramStats{}
			var covered []string
			err := c.FetchProgramStats(context.Background(), []string{acme, globex, vdp, hooli, gone}, func(b []string, s map[string]platform.ProgramStats) error {
				covered = append(covered, b...)
				for h, st := range s {
					got[h] = st
				}
				return nil
			})
			if (err != nil) != tt.err {
				t.Fatalf("err = %v", err)
			}
			if tt.err {
				return
			}
			if len(covered) != 5 {
				t.Errorf("covered %v, want all five", covered)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("stats = %+v\nwant %+v", got, tt.want)
			}
			if hits["/public/programs/globexco/globex"] != 0 {
				t.Error("requested a program without a public page")
			}
		})
	}
}
