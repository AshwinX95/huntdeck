package web

import (
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/mdpsec/huntdeck/internal/scope"
	"github.com/mdpsec/huntdeck/internal/store"
)

type targetsData struct {
	Filter    store.TargetFilter
	KindParam string
	Query     string
	Rows      []store.TargetRow
	Pager     pager
	ExportURL string
	// Added is the "added in the last" filter as given, e.g. "7d".
	Added     string
	Platforms []string
	Tags      []store.TagCount
	Best      bool // sorted by asset score ("best new targets")
	AZ        bool // sorted by value rather than newest first
	Facets    store.TargetFacets
	Counted   bool // Facets are known (not for best new targets, which scores only some)
	query     url.Values
}

func (d targetsData) list() listQuery { return listQuery{"/targets", d.query} }

// Rail is the filter rail's groups.
func (d targetsData) Rail() []railGroup {
	l, fc := d.list(), d.Facets
	kinds := railGroup{Title: "Kind"}
	all := 0
	for _, k := range scope.Kinds {
		all += fc.Kinds[k]
	}
	kinds.Options = append(kinds.Options, l.choice(kindLabel(kindAll), "Wildcards, domains, URLs, IPs and CIDR ranges together", "kind", kindAll,
		d.KindParam == kindAll, all, d.Counted))
	for _, k := range targetKinds {
		o := l.choice(kindLabel(k), "", "kind", k, d.KindParam == k, fc.Kinds[k], d.Counted && k != scope.KindRoot)
		if k == scope.KindWildcard {
			o = l.fallback(kindLabel(k), "", "kind", d.KindParam == k, fc.Kinds[k], d.Counted)
		}
		if k == scope.KindRoot {
			o.Title = "Root domains of in-scope wildcards, domains and URLs, for recon"
		}
		kinds.Options = append(kinds.Options, o)
	}
	groups := []railGroup{kinds}
	if g := d.assetGroup(); len(g.Options) > 0 {
		groups = append(groups, g)
	}
	if !d.Best && d.KindParam != scope.KindRoot {
		groups = append(groups, railGroup{Title: "Scope", Options: []railOption{
			l.fallback("In scope", "", "scope", !d.Filter.OutOfScope, 0, false),
			l.choice("Out of scope", "", "scope", "out", d.Filter.OutOfScope, 0, false),
		}})
	}
	if len(d.Platforms) > 1 {
		g := railGroup{Title: "Platform"}
		for _, p := range d.Platforms {
			o := l.option(platformLabel(p), "", "platform", p, fc.Platforms[p], d.Counted)
			o.Platform = p
			g.Options = append(g.Options, o)
		}
		groups = append(groups, g)
	}
	added := railGroup{Title: "Added"}
	for i, v := range addedOptions {
		o := l.option("Last "+addedLabels[v], "Added to its program in the last "+addedLabels[v]+", where the platform or HuntDeck dates it", "added", v, 0, false)
		if i < len(fc.Added) {
			o.Count, o.Counted = fc.Added[i], d.Counted
		}
		added.Options = append(added.Options, o)
	}
	if d.Added != "" && !slices.Contains(addedOptions, d.Added) && !d.Filter.AddedSince.IsZero() { // from an older link
		added.Options = append(added.Options, l.choice("Since "+d.Filter.AddedSince.Format("2006-01-02"), "", "added", d.Added, true, 0, false))
	}
	program := railGroup{Title: "Program", Options: []railOption{
		l.option("Bounty-eligible", "Eligible for a bounty, on a program that pays", "bounty", "1", fc.Bounty, d.Counted),
		l.option("Private", "On private programs", "private", "1", fc.Private, d.Counted),
		l.option("Include paused", "Also paused and closed programs", "include_paused", "1", 0, false),
	}}
	if !d.Best {
		program.Options = append(program.Options, l.option("Include ignored", "Programs you ignore are left out unless this is on", "include_ignored", "1", 0, false))
	}
	groups = append(groups, added, program)
	if len(d.Tags) > 0 {
		g := railGroup{Title: "Tags"}
		for _, t := range d.Tags {
			g.Options = append(g.Options, l.option(t.Tag, "", "tag", t.Tag, 0, false))
		}
		groups = append(groups, g)
	}
	return groups
}

// AddedText is the "added" filter in words for the best-new subtitle: "in
// the last 14 days", "since 2026-10-28"; without one, the scoring window's
// 90 days (only targets that recent are scored).
func (d targetsData) AddedText() string {
	if l, ok := addedLabels[d.Added]; ok && !d.Filter.AddedSince.IsZero() {
		return "in the last " + l
	}
	if !d.Filter.AddedSince.IsZero() {
		return "since " + d.Filter.AddedSince.Format("2006-01-02")
	}
	return "in the last 90 days"
}

// RailView is the filter rail with its search box.
func (d targetsData) RailView() railView {
	return railView{Path: "/targets", Search: d.Query, Placeholder: "Contains…", Label: "Targets containing",
		Hidden: d.list().hidden("q"), Groups: d.Rail(), Views: d.Views()}
}

// Pills are the active filters.
func (d targetsData) Pills() []pill { return d.list().pills(d.Rail()) }

// ClearURL drops every filter but the kind and sort.
func (d targetsData) ClearURL() string { return d.list().keeping("kind", "sort") }

// SortURL is the list sorted by key ("" for newest first), from page 1;
// the current sort's turns it around (bounty, the one that turns).
func (d targetsData) SortURL(key string) string {
	l := listQuery{d.list().path, url.Values{}}
	for k, v := range d.query {
		if k != "rev" {
			l.query[k] = v
		}
	}
	if key == store.TargetsByBounty && d.Filter.Order == key && !d.Filter.Reverse {
		l.query.Set("rev", "1")
	}
	return l.with("sort", key)
}

// SortClass is a sortable heading's class.
func (d targetsData) SortClass(key string) string {
	return sortClass(!d.Best && d.Filter.Order == key, d.Filter.Reverse)
}

// targetFilterFrom reads the target filter shared by the UI and API; now
// is in the user's time zone, for "added in the last".
func targetFilterFrom(q url.Values, now time.Time) store.TargetFilter {
	kind := normaliseKind(q.Get("kind")) // "" for kind=all: every kind
	var assets []string
	if a := assetKindOf(q.Get("kind")); a != nil {
		kind, assets = "", a.Types
	}
	return store.TargetFilter{
		AssetTypes:     assets,
		Kind:           kind,
		OutOfScope:     q.Get("scope") == "out" && kind != scope.KindRoot, // roots come from in-scope targets only
		Platform:       canonicalPlatform(q.Get("platform")),
		BountyOnly:     isTrue(q.Get("bounty")),
		PrivateOnly:    isTrue(q.Get("private")),
		IncludePaused:  isTrue(q.Get("include_paused")),
		IncludeIgnored: isTrue(q.Get("include_ignored")),
		Tag:            tagParam(q.Get("tag")),
		AddedSince:     addedSince(q.Get("added"), now),
	}
}

// addedOptions are the Targets page's "added in the last" choices; the
// hunt board's (7, 14 and 30 days) are among them.
var addedOptions = []string{"7d", "14d", "30d", "90d", "365d"}

// addedLabels name addedOptions, and the Disclosed page's.
var addedLabels = map[string]string{"7d": "7 days", "14d": "14 days", "30d": "30 days", "90d": "90 days", "365d": "year"}

// kindAll is the kind parameter listing every kind of target at once.
const kindAll = "all"

// addedSince parses an "added" filter: a number of days ("7d") counted from
// the start of today, or a date ("2026-10-01"), both in now's time zone.
// Anything else is no filter.
func addedSince(v string, now time.Time) time.Time {
	if days, ok := strings.CutSuffix(v, "d"); ok {
		if n, err := strconv.Atoi(days); err == nil && n > 0 && n <= 3650 {
			y, m, d := now.Date()
			return time.Date(y, m, d-n, 0, 0, 0, 0, now.Location())
		}
		return time.Time{}
	}
	if t, err := time.ParseInLocation("2006-01-02", v, now.Location()); err == nil {
		return t
	}
	return time.Time{}
}

func (s *server) targets(w http.ResponseWriter, r *http.Request) {
	const perPage = 100
	q := r.URL.Query()
	now := s.now(r.Context())
	f := targetFilterFrom(q, now)
	kindParam := f.Kind
	switch a := assetKindOf(q.Get("kind")); {
	case a != nil:
		kindParam = a.Key
	case strings.EqualFold(q.Get("kind"), kindAll):
		kindParam = kindAll
	case f.Kind == "":
		f.Kind, kindParam = scope.KindWildcard, scope.KindWildcard
	}
	query := strings.TrimSpace(q.Get("q"))
	best, az := q.Get("sort") == "best" && len(f.AssetTypes) == 0, q.Get("sort") == "az" // assets aren't scored
	list := s.Store.TargetRows
	switch {
	case best:
		list = s.Store.BestTargets
	case q.Get("sort") == "bounty":
		f.Order, f.Reverse = store.TargetsByBounty, q.Get("rev") == "1"
	case !az:
		f.Order = store.TargetsByNewest
	}
	rows, total, err := list(r.Context(), f, query, perPage, (pageFrom(r)-1)*perPage)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	tags, err := s.Store.Tags(r.Context())
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	var facets store.TargetFacets
	if !best {
		cutoffs := make([]time.Time, len(addedOptions))
		for i, v := range addedOptions {
			cutoffs[i] = addedSince(v, now)
		}
		if facets, err = s.Store.TargetFacetsFor(r.Context(), f, query, cutoffs); err != nil {
			s.serverError(w, r, err)
			return
		}
	}
	export := url.Values{"kind": {kindParam}, "format": {"txt"}}
	for _, k := range []string{"q", "scope", "platform", "bounty", "private", "include_paused", "include_ignored", "tag", "added", "sort"} {
		if v := q.Get(k); v != "" {
			export.Set(k, v)
		}
	}
	s.render(w, r, http.StatusOK, "targets", pageData{Title: "Targets", Nav: "targets", Data: targetsData{
		Filter: f, KindParam: kindParam, Query: query, Rows: rows, Pager: newPager(r, perPage, total),
		ExportURL: "/api/v1/targets?" + export.Encode(), Platforms: s.listPlatforms(r.Context()),
		Added: q.Get("added"), Best: best, AZ: az, Tags: tags, Facets: facets, Counted: !best, query: q,
	}})
}

// normaliseKind accepts "wildcards" or "wildcard" etc.
// targetKinds are the kinds the targets page and export offer.
var targetKinds = append(slices.Clone(scope.Kinds), scope.KindRoot)

func normaliseKind(k string) string {
	k = strings.TrimSuffix(strings.ToLower(strings.TrimSpace(k)), "s")
	for _, known := range targetKinds {
		if k == known {
			return k
		}
	}
	return ""
}
