package web

import (
	"context"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/mdpsec/huntdeck/internal/platform"
	"github.com/mdpsec/huntdeck/internal/scope"
	"github.com/mdpsec/huntdeck/internal/store"
)

// Hunt board sizes. Wide screens (2200px and up) show the extra targets
// and picks the page always carries; narrower ones show the first
// boardRows and boardPickRows.
const (
	boardTargets  = 30 // fresh attack surface rows on wide screens
	boardRows     = 12 // and on others
	boardPicks    = 10
	boardPickRows = 7
	boardCards    = 8 // scope diff cards on wide screens (6 on others, 4 below 1700px)
	boardDiffRows = 3 // lines per card
	boardNames    = 6 // programs named per status line
	// boardJoinedTargets is how many of a just-joined program's targets
	// the fresh attack surface lists; "+N more" links the rest.
	boardJoinedTargets = 3
)

// boardDays are the fresh attack surface's "added in the last" choices.
var boardDays = []string{"7d", "14d", "30d"}

// boardKinds are its target types, "" for all.
var boardKinds = []struct{ Key, Label string }{
	{"", "All"}, {scope.KindWildcard, "Wildcards"}, {scope.KindDomain, "Domains"},
	{scope.KindURL, "URLs"}, {scope.KindIP, "IPs"}, {scope.KindCIDR, "IP ranges"}, {scope.KindApp, "Apps"},
}

type dashboardData struct {
	Summaries  []store.PlatformSummary
	Totals     totals
	Changes24h int
	Changes7d  int
	Platforms  []PlatformInfo
	// Checked is when each platform's listing was last read, in the
	// platforms' order: the status line under the totals.
	Checked []store.PlatformChecked
	// FilterPlatforms are the platforms fresh targets can be narrowed to:
	// Independent too, once there are programs added by hand.
	FilterPlatforms []string

	// Fresh attack surface: the best recently added targets.
	Filter      boardFilter
	Targets     []store.TargetRow
	TargetTotal int

	Diffs        []boardDiff // the week's changes, one card per program, at most boardCards
	DiffPrograms int         // programs with changes this week
	Status       boardStatus
	Week         store.BoardCounts
	TopHunts     []store.HuntPick // highest HuntScores you can hunt now
	Invites      boardInvites
}

// totals sums the platform summaries for the dashboard's headline numbers.
type totals struct {
	Programs, Private, Open, Bounty, InScope, Wildcards, Domains int
}

// boardFilter is the fresh attack surface's filter, from the query string.
type boardFilter struct {
	// Kinds are the chosen kind chips, in boardKinds' order; none is all.
	Kinds    []string
	Not      string // the exclude field as given (exclude.go)
	Platform string
	Bounty   bool   // bounty-eligible targets only (the default)
	Added    string // one of boardDays
	Sort     string // "new" (newest first, the default), "score" (best first) or "bounty" (highest first)
	Reverse  bool   // the sort turned around
}

func boardFilterFrom(q url.Values) boardFilter {
	f := boardFilter{Platform: canonicalPlatform(q.Get("platform")), Not: strings.TrimSpace(q.Get("not")),
		Bounty: q.Get("bounty") != "0", Added: oneOf(q.Get("added"), "14d", "7d", "30d"), Sort: oneOf(q.Get("sort"), "new", "score", "bounty"), Reverse: q.Get("rev") == "1"}
	var chosen []string
	for k := range strings.SplitSeq(q.Get("kind"), ",") {
		k = strings.ToLower(strings.TrimSpace(k))
		if k == "apps" {
			k = scope.KindApp
		} else if k != scope.KindApp {
			k = normaliseKind(k)
		}
		chosen = append(chosen, k)
	}
	for _, k := range boardKinds {
		if k.Key != "" && slices.Contains(chosen, k.Key) {
			f.Kinds = append(f.Kinds, k.Key)
		}
	}
	return f
}

// Has says whether kind chip k is on ("" for All: no chip chosen).
func (f boardFilter) Has(k string) bool {
	if k == "" {
		return len(f.Kinds) == 0
	}
	return slices.Contains(f.Kinds, k)
}

// AppsOnly says the list holds nothing but apps.
func (f boardFilter) AppsOnly() bool { return len(f.Kinds) == 1 && f.Kinds[0] == scope.KindApp }

// Exclude is the words the list leaves out.
func (f boardFilter) Exclude() []string {
	_, ex := searchTerms("", f.Not)
	return ex
}

// Hidden are the filter's parameters but the exclude field, for its form.
func (f boardFilter) Hidden() []param {
	f.Not = ""
	var out []param
	for k, v := range f.query() {
		out = append(out, param{k, v[0]})
	}
	slices.SortFunc(out, func(a, b param) int { return strings.Compare(a.Name, b.Name) })
	return out
}

// NotURL is the board without the exclude field.
func (f boardFilter) NotURL() string { f.Not = ""; return boardURL(f.query()) }

// query is the filter as board query parameters, defaults left out.
func (f boardFilter) query() url.Values {
	q := url.Values{}
	if len(f.Kinds) > 0 {
		q.Set("kind", strings.Join(f.Kinds, ","))
	}
	if f.Not != "" {
		q.Set("not", f.Not)
	}
	if f.Platform != "" {
		q.Set("platform", f.Platform)
	}
	if !f.Bounty {
		q.Set("bounty", "0")
	}
	if f.Added != "14d" {
		q.Set("added", f.Added)
	}
	if f.Sort != "new" {
		q.Set("sort", f.Sort)
	}
	if f.Reverse {
		q.Set("rev", "1")
	}
	return q
}

func boardURL(q url.Values) string {
	if len(q) == 0 {
		return "/"
	}
	return "/?" + q.Encode()
}

// KindURL, PlatformURL, AddedURL, BountyURL and SortURL are the board with
// one filter changed.
// KindURL turns kind chip k on or off ("": All, every kind).
func (f boardFilter) KindURL(k string) string {
	switch {
	case k == "":
		f.Kinds = nil
	case slices.Contains(f.Kinds, k):
		f.Kinds = slices.DeleteFunc(slices.Clone(f.Kinds), func(x string) bool { return x == k })
	default:
		f.Kinds = append(slices.Clone(f.Kinds), k)
		slices.SortFunc(f.Kinds, func(a, b string) int { return boardKindRank(a) - boardKindRank(b) })
	}
	return boardURL(f.query())
}

func boardKindRank(k string) int {
	return slices.IndexFunc(boardKinds, func(x struct{ Key, Label string }) bool { return x.Key == k })
}

func (f boardFilter) PlatformURL(p string) string { f.Platform = p; return boardURL(f.query()) }
func (f boardFilter) AddedURL(a string) string    { f.Added = a; return boardURL(f.query()) }
func (f boardFilter) BountyURL() string           { f.Bounty = !f.Bounty; return boardURL(f.query()) }
func (f boardFilter) SortURL(s string) string {
	f.Reverse = s == f.Sort && !f.Reverse // the current sort again turns it around
	f.Sort = s
	return boardURL(f.query())
}

// SortClass is a sortable column header's class.
func (f boardFilter) SortClass(s string) string { return sortClass(f.Sort == s, f.Reverse) }

// targetQuery is the filter for the Targets page and the targets API.
func (f boardFilter) targetQuery() url.Values {
	q := url.Values{"sort": {"best"}, "added": {f.Added}, "kind": {kindAll}}
	if len(f.Kinds) == 1 {
		q.Set("kind", f.Kinds[0])
	}
	if f.Platform != "" {
		q.Set("platform", f.Platform)
	}
	if f.Bounty {
		q.Set("bounty", "1")
	}
	return q
}

// TargetsURL is the Targets page with the same filter, "" for several
// kinds or excluded words, which it doesn't take.
func (f boardFilter) TargetsURL() string {
	if len(f.Kinds) > 1 || f.Not != "" {
		return ""
	}
	return "/targets?" + f.targetQuery().Encode()
}

// boardDiff is a program's changes over the week.
type boardDiff struct {
	Program                   store.ProgramRef
	Latest                    time.Time
	Added, Removed, Edited    int // in-scope entries added and removed; other scope edits
	NewProgram, Bounty, Other bool
	Lines                     []boardLine
	More                      int // lines left out
}

// boardLine is one line of a diff card: "add", "del" or "mod".
type boardLine struct {
	Class, Text string
	ChangeID    int64 // for changes with a detail page
}

// boardStatus sums up the week's paused and reopened programs, leaving
// flip-floppers (paused and reopened again and again) out of both.
type boardStatus struct {
	Reopened, Paused []store.ProgramRef
	FlipFloppers     []flipFlopper
}

type flipFlopper struct {
	Program store.ProgramRef
	Flips   int
}

func (s *server) dashboard(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	d := dashboardData{Filter: boardFilterFrom(r.URL.Query())}
	if err := s.boardHead(ctx, &d); err != nil {
		s.serverError(w, r, err)
		return
	}
	if err := s.boardTargets(r, &d); err != nil {
		s.serverError(w, r, err)
		return
	}
	changes, err := s.Store.BoardChanges(ctx, time.Now().Add(-7*24*time.Hour), false, 2000)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	d.Diffs, d.DiffPrograms = boardDiffs(changes, boardCards)
	if d.Invites, err = store.Memo(ctx, s.Store, "board-invites", func(ctx context.Context) (boardInvites, error) {
		return s.boardInvites(ctx, time.Now())
	}); err != nil {
		s.serverError(w, r, err)
		return
	}
	d.Platforms, d.FilterPlatforms = s.Platforms, s.listPlatforms(ctx)
	if d.Checked, err = s.Store.PlatformsChecked(ctx); err != nil {
		s.serverError(w, r, err)
		return
	}
	order := s.platformNames()
	rank := func(name string) int {
		if i := slices.Index(order, name); i >= 0 {
			return i
		}
		return len(order)
	}
	slices.SortStableFunc(d.Checked, func(a, b store.PlatformChecked) int { return rank(a.Platform) - rank(b.Platform) })
	s.render(w, r, http.StatusOK, "dashboard", pageData{Title: "Hunt board", Nav: "dashboard", Data: d})
}

// boardHead reads the board's summary, which /api/v1/board gives too: the
// headline totals and change counts, the week's counts and status moves,
// and the top hunts.
func (s *server) boardHead(ctx context.Context, d *dashboardData) error {
	var err error
	if d.Summaries, err = store.Memo(ctx, s.Store, "summary", s.Store.Summary); err != nil {
		return err
	}
	counts, err := s.changeCounts(ctx, nil)
	if err != nil {
		return err
	}
	d.Changes24h, d.Changes7d = changesData{Counts: counts}.count("", nil, "", 0), changesData{Counts: counts}.count("", nil, "", 1)
	week := time.Now().Add(-7 * 24 * time.Hour)
	states, err := s.Store.BoardChanges(ctx, week, true, 2000)
	if err != nil {
		return err
	}
	// Reopened is what /programs?resumed=7d lists, which "+N more" links to.
	reopened, _, err := s.Store.Programs(ctx, store.ProgramFilter{State: "open", ResumedSince: week, Sort: "resumed", Limit: 1000})
	if err != nil {
		return err
	}
	d.Status = statusOf(states, reopened)
	if d.Week, err = s.Store.BoardCounts(ctx, week); err != nil {
		return err
	}
	// New wildcards and domains as the cards' Targets pages count them,
	// "added in the last 7 days" (dated assets, not this week's changes).
	since := addedSince("7d", s.now(ctx))
	for _, k := range []struct {
		kind string
		n    *int
	}{{scope.KindWildcard, &d.Week.Wildcards}, {scope.KindDomain, &d.Week.Domains}} {
		if _, *k.n, err = s.Store.TargetRows(ctx, store.TargetFilter{Kind: k.kind, AddedSince: since}, "", 1, 0); err != nil {
			return err
		}
	}
	if d.TopHunts, err = store.Memo(ctx, s.Store, "top-hunts", func(ctx context.Context) ([]store.HuntPick, error) {
		return s.Store.TopHunts(ctx, boardPicks)
	}); err != nil {
		return err
	}
	for _, sm := range d.Summaries {
		t := &d.Totals
		t.Programs += sm.Programs
		t.Private += sm.Private
		t.Open += sm.Open
		t.Bounty += sm.Bounty
		t.InScope += sm.InScopeEntries
		t.Wildcards += sm.Targets[scope.KindWildcard]
		t.Domains += sm.Targets[scope.KindDomain]
	}
	return nil
}

// boardTargets loads the fresh attack surface.
func (s *server) boardTargets(r *http.Request, d *dashboardData) error {
	f := d.Filter
	rows, total, err := s.Store.BestTargets(r.Context(), store.TargetFilter{Kinds: f.Kinds, Exclude: f.Exclude(), Platform: f.Platform, BountyOnly: f.Bounty,
		AddedSince: addedSince(f.Added, s.now(r.Context())), Order: boardOrders[f.Sort], Reverse: f.Reverse, JoinedPerProgram: boardJoinedTargets}, "", boardTargets, 0)
	if err != nil {
		return err
	}
	d.Targets, d.TargetTotal = rows, total
	return nil
}

// boardOrders maps the board's sorts to the store's.
var boardOrders = map[string]string{"new": store.TargetsByNewest, "score": store.TargetsByScore, "bounty": store.TargetsByBounty}

// boardDiffs groups changes (newest first) into one card per program, the
// most recently changed first, and returns at most limit of them and the
// number of programs.
func boardDiffs(changes []store.BoardChange, limit int) ([]boardDiff, int) {
	var out []boardDiff
	at := map[int64]int{}
	for _, c := range changes {
		i, ok := at[c.ProgramID]
		if !ok {
			i = len(out)
			at[c.ProgramID] = i
			out = append(out, boardDiff{Program: c.Program, Latest: c.OccurredAt})
		}
		d := &out[i]
		var line boardLine
		switch c.Kind {
		case store.ChangeScopeAdded:
			if c.Field == "out_of_scope" { // more exclusions: a scope edit, not new surface
				d.Edited++
				line = boardLine{Class: "mod", Text: c.NewValue + "  (out of scope)"}
				break
			}
			d.Added++
			line = boardLine{Class: "add", Text: c.NewValue}
		case store.ChangeScopeRemoved:
			d.Removed++
			line = boardLine{Class: "del", Text: c.OldValue}
		case store.ChangeScopeUpdated:
			d.Edited++
			line = boardLine{Class: "mod", Text: strings.TrimSpace(c.Identifier + "  " + strings.ReplaceAll(c.Field, "_", " ") + " " + c.OldValue + " → " + c.NewValue)}
		case store.ChangeProgramAdded:
			d.NewProgram = true
			line = boardLine{Class: "add", Text: "program listed"}
		case store.ChangeProgramRemoved:
			d.Other = true
			line = boardLine{Class: "del", Text: "program no longer listed"}
		case store.ChangeBountyChanged, store.ChangeRewardsChanged:
			d.Bounty = true
			line = boardLine{Class: "mod", Text: describeChange(c.ChangeView), ChangeID: detailID(c.ChangeView)}
		default:
			d.Other = true
			line = boardLine{Class: "mod", Text: describeChange(c.ChangeView), ChangeID: detailID(c.ChangeView)}
		}
		if len(d.Lines) < boardDiffRows {
			d.Lines = append(d.Lines, line)
		} else {
			d.More++
		}
	}
	return out[:min(limit, len(out))], len(out)
}

// detailID is a change's ID if it has a detail page, else 0.
func detailID(c store.ChangeView) int64 {
	if hasDetail(c.Kind) {
		return c.ID
	}
	return 0
}

// statusOf sums up the week's state changes (newest first) by each
// program's latest state. Reopened are the Programs list's "back open"
// ones (reopened, as resumedAt judges it), so the count matches the list
// it links to; a program last reopened that the list leaves out keeps
// flipping (its flips around the reopen, not just this week's, make it
// one), so it's among the flip-floppers.
func statusOf(states []store.BoardChange, reopened []store.ProgramListItem) boardStatus {
	var st boardStatus
	back := map[int64]bool{}
	for _, p := range reopened {
		back[p.ID] = true
		st.Reopened = append(st.Reopened, p.ProgramRef)
	}
	flips := map[int64]int{}
	var order []store.BoardChange // each program's latest change
	for _, c := range states {
		if flips[c.ProgramID] == 0 {
			order = append(order, c)
		}
		flips[c.ProgramID]++
	}
	for _, c := range order {
		switch {
		case back[c.ProgramID]:
		case flips[c.ProgramID] >= store.FlipFlops || c.NewValue == platform.StateOpen:
			st.FlipFloppers = append(st.FlipFloppers, flipFlopper{Program: c.Program, Flips: flips[c.ProgramID]})
		default:
			st.Paused = append(st.Paused, c.Program)
		}
	}
	slices.SortStableFunc(st.FlipFloppers, func(a, b flipFlopper) int { return b.Flips - a.Flips })
	return st
}

// boardColumns are the fresh targets' columns its picker shows, hides and
// reorders, in the table's default order: the Programs list's where they
// share them (program first, then a score, bounty, total paid). The
// program always shows, first, as on the Programs list; a narrow box drops
// Type and Bounty (putting them under the target) and then Added, unless
// the picker says otherwise.
var boardColumns = columnPicker{Name: "board-targets", Label: "Columns", Icon: "sliders", Fixed: "Program", Columns: []tableColumn{
	{Key: "target", Label: "Target"}, {Key: "kind", Label: "Type"}, {Key: "score", Label: "Target score"},
	{Key: "pay", Label: "Bounty"}, {Key: "ptotal", Label: "Total paid"}, {Key: "added", Label: "Added"},
	{Key: "acts", Label: "Copy and open"},
}}

// Columns is the fresh targets' column picker.
func (d dashboardData) Columns() columnPicker { return boardColumns }
