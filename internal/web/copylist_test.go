package web

import (
	"slices"
	"strings"
	"testing"

	"github.com/mdpsec/huntdeck/internal/scope"
	"github.com/mdpsec/huntdeck/internal/store"
)

// "Copy all targets" holds what tools take: hosts, URLs, IPs and ranges,
// never an app's name or an asset with no address.
func TestCopyAllTargets(t *testing.T) {
	entry := func(typ, id string) store.EntryView {
		return store.EntryView{InScope: true, AssetType: typ, Identifier: id, Targets: scope.Targets(typ, id)}
	}
	out := func(typ, id string) store.EntryView {
		e := entry(typ, id)
		e.InScope = false
		return e
	}
	d := programData{P: &store.ProgramDetail{Entries: []store.EntryView{
		entry(scope.AssetWildcard, "*.example.com"), entry(scope.AssetURL, "api.example.net"), entry(scope.AssetURL, "https://app.example.org/v2"),
		entry(scope.AssetCIDR, "192.0.2.0/24"), entry(scope.AssetAndroid, "com.example.android"), entry(scope.AssetOther, "Other"),
		entry(scope.AssetIOS, "Example iOS app"), entry(scope.AssetWildcard, "*.brand.{com,net}"),
		out(scope.AssetURL, "admin.example.com"), out(scope.AssetOther, "Physical offices"), out(scope.AssetCIDR, "10.0.0.0/8"),
	}}}
	d.splitEntries()
	if want := []string{"example.com", "example.net", "example.org", "brand.com", "brand.net"}; !slices.Equal(d.Roots, want) {
		t.Errorf("roots = %q, want %q", d.Roots, want)
	}
	if want := []string{"admin.example.com", "10.0.0.0/8"}; !slices.Equal(d.OutTargets, want) {
		t.Errorf("out-of-scope copy list = %q, want %q", d.OutTargets, want)
	}
	want := []string{"*.example.com", "api.example.net", "https://app.example.org/v2", "192.0.2.0/24", "*.brand.com", "*.brand.net"}
	if !slices.Equal(d.Identifiers, want) {
		t.Errorf("copy list = %q, want %q", d.Identifiers, want)
	}
}

// Odd platform figures read sensibly: HackerOne's lowest average bracket
// is "up to $50", not "$0 – $50"; a median to resolve of half a year or
// more says a backlog was cleared.
func TestOddFigures(t *testing.T) {
	f := func(v float64) *float64 { return &v }
	p := store.ProgramListItem{AvgBountyLow: f(0), AvgPayout: f(50), StatsCurrency: "USD"}
	p.Platform = "hackerone"
	if got := avgBounty(p); got != "up to $50" {
		t.Errorf("avgBounty = %q", got)
	}
	if got := avgBountyTitle(p); !strings.HasSuffix(got, "up to $50") {
		t.Errorf("avgBountyTitle = %q", got)
	}
	p.AvgBountyLow = f(200)
	p.AvgPayout = f(300)
	if got := avgBounty(p); got != "$200 – $300" {
		t.Errorf("avgBounty = %q", got)
	}
	for _, tt := range []struct {
		days float64
		also string
	}{{30, ""}, {180, ""}, {420, "backlog cleared"}} {
		r := platformStats{Resolve: &lineChart{}, ResolveLast: f(tt.days)}
		if c := r.ResolveCell(); c.Also != tt.also {
			t.Errorf("%v days: also %q, want %q", tt.days, c.Also, tt.also)
		}
	}
}
