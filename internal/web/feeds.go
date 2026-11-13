package web

import (
	"encoding/xml"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/mdpsec/huntdeck/internal/store"
)

// Atom feeds of changes, for feed readers: /changes.atom takes the Changes
// page's filters (kind, scope, platform, rewards, watchlist; not its
// period: a feed is the newest), and /programs/{platform}/{handle}/changes.atom is one
// program's. Each holds the newest feedEntries changes, a flip-flopping
// program's counted once, as the pages show them. Pages link theirs in
// their head, for readers to find, and beside the list.

// feedEntries is how many changes a feed holds.
const feedEntries = 50

type atomFeed struct {
	XMLName xml.Name    `xml:"http://www.w3.org/2005/Atom feed"`
	ID      string      `xml:"id"`
	Title   string      `xml:"title"`
	Updated string      `xml:"updated"`
	Links   []atomLink  `xml:"link"`
	Author  atomAuthor  `xml:"author"`
	Entries []atomEntry `xml:"entry"`
}

type atomLink struct {
	Rel  string `xml:"rel,attr,omitempty"`
	Type string `xml:"type,attr,omitempty"`
	Href string `xml:"href,attr"`
}

type atomAuthor struct {
	Name string `xml:"name"`
}

type atomEntry struct {
	ID       string       `xml:"id"`
	Title    string       `xml:"title"`
	Updated  string       `xml:"updated"`
	Link     atomLink     `xml:"link"`
	Category atomCategory `xml:"category"`
	Summary  string       `xml:"summary"`
}

type atomCategory struct {
	Term string `xml:"term,attr"`
}

// feedBase is the address feeds link to: the published one, else the one
// the request came by.
func (s *server) feedBase(r *http.Request) string {
	if s.PublicURL != "" {
		return s.PublicURL
	}
	return baseURL(r)
}

// changesFeed serves /changes.atom.
func (s *server) changesFeed(w http.ResponseWriter, r *http.Request) {
	f, _ := changeFilterFrom(r.URL.Query())
	f.Since, f.Limit, f.Offset = time.Time{}, feedEntries, 0
	page := "/changes"
	if q := changesFeedQuery(r.URL.Query()); q != "" {
		page += "?" + q
	}
	s.writeFeed(w, r, f, "HuntDeck: changes", page)
}

// programChangesFeed serves a program's changes.atom.
func (s *server) programChangesFeed(w http.ResponseWriter, r *http.Request) {
	p, err := s.Store.Program(r.Context(), canonicalPlatform(r.PathValue("platform")), r.PathValue("handle"))
	if err != nil {
		s.renderError(w, r, http.StatusNotFound, "No such program.")
		return
	}
	f := store.ChangeFilter{ProgramID: p.ID, Newest: true, CollapseFlips: true, Limit: feedEntries}
	s.writeFeed(w, r, f, "HuntDeck: "+p.Name+" ("+platformLabel(p.Platform)+")", programRefPath(p.ProgramRef)+"?tab=changes")
}

func (s *server) writeFeed(w http.ResponseWriter, r *http.Request, f store.ChangeFilter, title, page string) {
	list, _, err := s.Store.Changes(r.Context(), f)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	base := s.feedBase(r)
	feed := atomFeed{ID: base + r.URL.RequestURI(), Title: title, Author: atomAuthor{Name: "HuntDeck"},
		Updated: time.Unix(0, 0).UTC().Format(time.RFC3339),
		Links: []atomLink{{Rel: "self", Type: "application/atom+xml", Href: base + r.URL.RequestURI()},
			{Rel: "alternate", Type: "text/html", Href: base + page}}}
	if len(list) > 0 {
		feed.Updated = list[0].OccurredAt.UTC().Format(time.RFC3339)
	}
	for _, c := range list {
		link := base + programRefPath(c.Program)
		if hasDetail(c.Kind) {
			link = base + "/changes/" + strconv.FormatInt(c.ID, 10)
		}
		text := describeChange(c)
		feed.Entries = append(feed.Entries, atomEntry{
			ID: base + "/changes/" + strconv.FormatInt(c.ID, 10), Title: c.Program.Name + ": " + text,
			Updated: c.OccurredAt.UTC().Format(time.RFC3339), Link: atomLink{Rel: "alternate", Href: link},
			Category: atomCategory{Term: c.Kind},
			Summary:  text + " (" + platformLabel(c.Program.Platform) + ", " + visibilityOf(c.Program.Private) + " program)",
		})
	}
	w.Header().Set("Content-Type", "application/atom+xml; charset=utf-8")
	w.Header().Set("X-Robots-Tag", "noindex")
	_, _ = w.Write([]byte(xml.Header))
	enc := xml.NewEncoder(w)
	enc.Indent("", "  ")
	_ = enc.Encode(feed)
}

// changesFeedQuery is the Changes page's filters a feed keeps, encoded.
func changesFeedQuery(q url.Values) string {
	keep := url.Values{}
	for _, k := range []string{"group", "scope", "change", "platform", "bounty", "watch"} {
		if v := q.Get(k); v != "" {
			keep.Set(k, v)
		}
	}
	return keep.Encode()
}

// FeedURL is the Changes page's feed, with its filters.
func (d changesData) FeedURL() string {
	if q := changesFeedQuery(d.query); q != "" {
		return "/changes.atom?" + q
	}
	return "/changes.atom"
}
