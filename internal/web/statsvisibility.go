package web

import (
	"context"
	"net/http"
	"net/url"
	"slices"

	"github.com/mdpsec/huntdeck/internal/store"
)

// The Stats pages count every program by default; a toggle (?visibility=,
// as on Programs) narrows them to the public ones or the private ones the
// user was invited to. A program counts as it is now: one that went
// private counts as private in every month.

// statsVisibility is the request's ?visibility=: "public", "private", or ""
// for both.
func statsVisibility(r *http.Request) string {
	return oneOf(r.URL.Query().Get("visibility"), "", "public", "private")
}

// visSegment is one choice of the toggle: All, Public or Private, with
// its open programs.
type visSegment struct {
	Label, URL string
	Count      int
	On         bool
}

// visToggle is the Stats pages' visibility toggle.
type visToggle struct {
	Segments []visSegment
	// Value is the current choice, "" for all.
	Value string
}

// Query is "?visibility=…" for links that keep the choice, "" for all.
func (v visToggle) Query() string {
	if v.Value == "" {
		return ""
	}
	return "?visibility=" + v.Value
}

// statsToggle builds the toggle for r on platformName's programs ("" for
// every platform): each choice links r's page with it, back on the first
// page.
func (s *server) statsToggle(ctx context.Context, r *http.Request, platformName string) (visToggle, error) {
	public, private, err := s.Store.OpenByVisibility(ctx, platformName)
	if err != nil {
		return visToggle{}, err
	}
	cur := statsVisibility(r)
	link := func(v string) string {
		q := r.URL.Query()
		q.Del("page")
		q.Del("visibility")
		if v != "" {
			q.Set("visibility", v)
		}
		return (&url.URL{Path: r.URL.Path, RawQuery: q.Encode()}).String()
	}
	t := visToggle{Value: cur}
	for _, c := range []struct {
		label, value string
		n            int
	}{{"All", "", public + private}, {"Public", "public", public}, {"Private", "private", private}} {
		t.Segments = append(t.Segments, visSegment{Label: c.label, URL: link(c.value), Count: c.n, On: c.value == cur})
	}
	return t, nil
}

// statsOverviewFor is name's overview in in, or an empty one where the
// chosen visibility leaves a configured platform no programs; ok is false
// for a platform there's nothing of.
func (s *server) statsOverviewFor(in statsInputs, name, visibility string) (store.PlatformOverview, bool) {
	if i := slices.IndexFunc(in.Overviews, func(o store.PlatformOverview) bool { return o.Platform == name }); i >= 0 {
		return in.Overviews[i], true
	}
	if visibility != "" && slices.Contains(s.platformNames(), name) {
		return emptyOverview(name), true
	}
	return store.PlatformOverview{}, false
}

// emptyOverview stands in for a platform with no programs of the chosen
// visibility, so its page shows empty rather than missing.
func emptyOverview(name string) store.PlatformOverview {
	return store.PlatformOverview{Platform: name, Months: make([]store.PlatformMonth, overviewMonths)}
}
