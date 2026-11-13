package web

import (
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/mdpsec/huntdeck/internal/store"
)

// scopeView is a program page's Scope tab: one of in scope, out of scope
// or removed, narrowed to an asset type and a search.
type scopeView struct {
	Show  string // "in", "out" or "removed"
	Type  string // an asset type, or "" for all
	Query string
	Order string // "added" lists newest first; removed entries always are
	// Segments switch between in, out and removed; Types narrow to an
	// asset type, "All" first.
	Segments []scopeLink
	Types    []scopeLink
	Rows     []store.EntryView
	Total    int  // rows matching, before the limit
	Limited  bool // Rows stops at scopeRowsShown
	AllURL   string
	OrderURL string
	ClearURL string // without the type and search
	// Bounty and Severity show those columns; a column whose rows all say
	// the same is left out and Same says what that is ("All
	// bounty-eligible, up to critical").
	Bounty, Severity bool
	Same             string
	// Hidden are the filter form's other choices.
	Hidden []param
}

// scopeLink is one choice on the Scope tab, with its count.
type scopeLink struct {
	Key, Label, URL string
	Count           int
	On              bool
}

// scopeRowsShown is how many rows the Scope tab shows before "show all".
const scopeRowsShown = 1000

// assetTypeOrder is the order of the Scope tab's type chips; types not
// listed follow, by name.
var assetTypeOrder = []string{"wildcard", "domain", "url", "api", "ip", "cidr", "android", "ios", "windows", "executable",
	"source_code", "hardware", "smart_contract", "ai_model", "other"}

// assetTypeLabels name asset types on the type chips.
var assetTypeLabels = map[string]string{
	"wildcard": "Wildcards", "domain": "Domains", "url": "URLs", "api": "APIs", "ip": "IPs", "cidr": "IP ranges",
	"android": "Android", "ios": "iOS", "windows": "Windows", "executable": "Executables", "source_code": "Source code",
	"hardware": "Hardware", "smart_contract": "Smart contracts", "ai_model": "AI models", "other": "Other",
}

// scopeView builds the Scope tab from the request's choices.
func (d *programData) scopeView(r *http.Request) scopeView {
	q := r.URL.Query()
	v := scopeView{
		Show:  oneOf(q.Get("show"), "in", "out", "removed"),
		Query: strings.TrimSpace(q.Get("q")),
		Order: oneOf(q.Get("order"), "", "added"),
	}
	all := isTrue(q.Get("all"))
	lists := map[string][]store.EntryView{"in": d.InScope, "out": d.OutOfScope, "removed": d.Removed}
	for _, s := range []struct{ key, label string }{{"in", "In scope"}, {"out", "Out of scope"}, {"removed", "Removed"}} {
		v.Segments = append(v.Segments, scopeLink{Key: s.key, Label: s.label, Count: len(lists[s.key]), On: s.key == v.Show,
			URL: scopeURL(s.key, "", v.Query, v.Order, false)})
	}
	list := slices.Clone(lists[v.Show])
	if v.Order == "added" && v.Show != "removed" {
		newestFirst(list, func(e store.EntryView) *time.Time { return e.AddedAt })
	}

	// Types, counted over the whole list so the chips don't move as the
	// search narrows it.
	counts := map[string]int{}
	for _, e := range list {
		counts[e.AssetType]++
	}
	if t := q.Get("type"); counts[t] > 0 {
		v.Type = t
	}
	types := make([]string, 0, len(counts))
	for t := range counts {
		types = append(types, t)
	}
	slices.SortFunc(types, func(a, b string) int {
		ia, ib := typeRank(a), typeRank(b)
		if ia != ib {
			return ia - ib
		}
		return strings.Compare(a, b)
	})
	if len(types) > 1 {
		v.Types = append(v.Types, scopeLink{Label: "All", Count: len(list), On: v.Type == "", URL: scopeURL(v.Show, "", v.Query, v.Order, false)})
		for _, t := range types {
			v.Types = append(v.Types, scopeLink{Key: t, Label: assetTypeLabel(t), Count: counts[t], On: t == v.Type,
				URL: scopeURL(v.Show, t, v.Query, v.Order, false)})
		}
	}
	v.Bounty, v.Severity, v.Same = sameColumns(list)

	needle := strings.ToLower(v.Query)
	for _, e := range list {
		if v.Type != "" && e.AssetType != v.Type {
			continue
		}
		if needle != "" && !strings.Contains(strings.ToLower(e.Identifier), needle) && !strings.Contains(strings.ToLower(e.Instruction), needle) {
			continue
		}
		v.Rows = append(v.Rows, e)
	}
	v.Total = len(v.Rows)
	if !all && len(v.Rows) > scopeRowsShown {
		v.Rows, v.Limited = v.Rows[:scopeRowsShown], true
	}
	v.AllURL = scopeURL(v.Show, v.Type, v.Query, v.Order, true)
	v.ClearURL = scopeURL(v.Show, "", "", v.Order, false)
	other := map[string]string{"": "added", "added": ""}[v.Order]
	v.OrderURL = scopeURL(v.Show, v.Type, v.Query, other, all)
	v.Hidden = []param{{Name: "tab", Value: "scope"}}
	for _, f := range []param{{"show", v.Show}, {"type", v.Type}, {"order", v.Order}} {
		if f.Value != "" && f.Value != "in" {
			v.Hidden = append(v.Hidden, f)
		}
	}
	return v
}

// typeRank is where an asset type's chip goes.
func typeRank(t string) int {
	if i := slices.Index(assetTypeOrder, t); i >= 0 {
		return i
	}
	return len(assetTypeOrder)
}

// assetTypeLabel names an asset type for a chip: "Source code".
func assetTypeLabel(t string) string {
	if l, ok := assetTypeLabels[t]; ok {
		return l
	}
	return upperFirst(strings.ReplaceAll(t, "_", " "))
}

// sameColumns says whether the Bounty and Severity columns tell the rows
// apart; for a column that doesn't, same says what every row has.
func sameColumns(list []store.EntryView) (bounty, severity bool, same string) {
	if len(list) == 0 {
		return true, true, ""
	}
	for _, e := range list[1:] {
		bounty = bounty || e.BountyEligible != list[0].BountyEligible
		severity = severity || e.MaxSeverity != list[0].MaxSeverity
	}
	var bits []string
	if !bounty {
		if list[0].BountyEligible {
			bits = append(bits, "all bounty-eligible")
		} else {
			bits = append(bits, "no bounties")
		}
	}
	if sev := list[0].MaxSeverity; !severity && sev != "" && sev != "none" {
		bits = append(bits, "up to "+sev)
	}
	return bounty, severity, upperFirst(strings.Join(bits, ", "))
}

// scopeURL links to the Scope tab with these choices.
func scopeURL(show, typ, query, order string, all bool) string {
	v := url.Values{"tab": {"scope"}}
	if show != "in" {
		v.Set("show", show)
	}
	if typ != "" {
		v.Set("type", typ)
	}
	if query != "" {
		v.Set("q", query)
	}
	if order != "" {
		v.Set("order", order)
	}
	if all {
		v.Set("all", "1")
	}
	return "?" + v.Encode()
}

// notePreview is the line a scope entry's note shows until it's opened,
// or "" when the note fits on one line as it is.
func notePreview(note string) string {
	note = strings.TrimSpace(note)
	first := firstLine(note)
	if first == note && len(note) <= 90 {
		return ""
	}
	return first
}

// noteRest is the rest of a note after the line notePreview shows.
func noteRest(note string) string {
	lines := strings.Split(strings.TrimSpace(note), "\n")
	for i, l := range lines {
		if strings.TrimSpace(l) != "" {
			return strings.TrimSpace(strings.Join(lines[i+1:], "\n"))
		}
	}
	return ""
}

// expansion is what a scope entry naming several targets covers
// ("*.example.{com,co.uk}", "a.example.com, b.example.com"): the targets
// stored for it, as Search and Targets list them, the first expansionShown
// in Text and all of them in Title. Nothing for an entry naming one.
type expansion struct{ Text, Title string }

const expansionShown = 12

func expandsTo(e store.EntryView) *expansion {
	if len(e.Targets) < 2 {
		return nil
	}
	values := make([]string, len(e.Targets))
	for i, t := range e.Targets {
		values[i] = t.Value
	}
	x := &expansion{Text: strings.Join(values[:min(len(values), expansionShown)], " · "), Title: strings.Join(values, "\n")}
	if n := len(values) - expansionShown; n > 0 {
		x.Text += " · +" + strconv.Itoa(n) + " more"
	}
	return x
}
