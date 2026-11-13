package web

import "strings"

// Saved views (views.js): the Hunt board's, Programs' and Targets' filters,
// sort and columns under a name of the user's, kept in their browser
// (localStorage, as the watchlist and notes are), per page. A page names
// the query keys it reads, so a view saved before a key went away loads
// without it, and the table whose columns a view keeps.

// myViews is a page's Save view button and My views menu.
type myViews struct {
	Page string // the page's path: views show only where they were saved
	Keys string // the query keys the page reads, comma-separated
	Cols string // the column picker's table (data-cols), "" for none
}

func newMyViews(page string, keys []string, cols string) *myViews {
	return &myViews{Page: page, Keys: strings.Join(keys, ","), Cols: cols}
}

// The query keys each page's views keep, besides its filters' own.
var (
	targetViewKeys = []string{"kind", "q", "scope", "platform", "bounty", "private", "include_paused", "include_ignored", "tag", "added", "sort", "rev"}
	boardViewKeys  = []string{"kind", "not", "platform", "bounty", "added", "sort", "rev"}
)

func programViewKeys() []string {
	return append(append([]string{}, programFacetParams...), "sort", "rev")
}

// Views is the Programs (or Invites) page's saved views.
func (d programsData) Views() *myViews {
	return newMyViews(d.Path, programViewKeys(), programColumns.Name)
}

// Views is the Targets page's saved views.
func (d targetsData) Views() *myViews { return newMyViews("/targets", targetViewKeys, "") }

// Views is the hunt board's saved views of its fresh targets.
func (f boardFilter) Views() *myViews { return newMyViews("/", boardViewKeys, boardColumns.Name) }
