package web

import (
	"net/http"
	"strings"
	"time"

	"github.com/mdpsec/huntdeck/internal/scope"
	"github.com/mdpsec/huntdeck/internal/store"
)

type matchResult struct {
	Program store.ProgramRef
	InScope bool
	Via     scope.Target
}

type matchData struct {
	Query   string
	Results []matchResult
	// Past are removed scope entries that matched: what was once in or out
	// of scope, newest removal first.
	Past []pastMatch
	// Under is live scope at or under the host that doesn't decide it
	// (store.ScopeUnder): wildcards on the host itself, which cover its
	// subdomains but not it, and its listed subdomains; UnderTotal how
	// many there are, Host the host checked. When nothing decides the host
	// or is under it, Under is scope that mentions it instead
	// (store.ScopeMentioning: "canva-apps" finds *.canva-apps.cn), and
	// Mentions is true.
	Under      []matchResult
	UnderTotal int
	Mentions   bool
	Host       string
	Checked    bool
}

// HostWildcard is a wildcard on the checked host itself, such as
// "*.example.com" for example.com, if one is listed under it.
func (d matchData) HostWildcard() string {
	for _, m := range d.Under {
		if m.Via.Kind == scope.KindWildcard && m.Via.Host == d.Host {
			return m.Via.Value
		}
	}
	return ""
}

// pastMatch is a removed scope entry that matches the checked host.
type pastMatch struct {
	matchResult
	Identifier string
	RemovedAt  time.Time
}

// pastShown bounds the removed entries a scope check lists, underShown
// the scope under the host.
const (
	pastShown  = 100
	underShown = 50
)

func (s *server) match(w http.ResponseWriter, r *http.Request) {
	d := matchData{Query: strings.TrimSpace(r.URL.Query().Get("q"))}
	if d.Query != "" {
		res, err := s.matchHost(r, d.Query)
		if err != nil {
			s.serverError(w, r, err)
			return
		}
		if d.Past, err = s.matchPast(r, d.Query); err != nil {
			s.serverError(w, r, err)
			return
		}
		d.Results, d.Checked, d.Host = res, true, hostText(d.Query)
		if d.Under, d.UnderTotal, err = s.matchUnder(r, d.Query); err != nil {
			s.serverError(w, r, err)
			return
		}
		if len(d.Results) == 0 && len(d.Under) == 0 {
			if d.Under, d.UnderTotal, err = s.matchMentioning(r, d.Query); err != nil {
				s.serverError(w, r, err)
				return
			}
			d.Mentions = len(d.Under) > 0
		}
	}
	s.render(w, r, http.StatusOK, "match", pageData{Title: "Scope check", Nav: "match", Data: d})
}

func (s *server) matchHost(r *http.Request, host string) ([]matchResult, error) {
	rules, progs, err := s.Store.MatchRules(r.Context(), host)
	if err != nil {
		return nil, err
	}
	var out []matchResult
	for _, m := range scope.Match(rules, host) {
		out = append(out, matchResult{Program: progs[m.ProgramID], InScope: m.InScope, Via: m.Rule.Target})
	}
	return out, nil
}

// matchUnder lists live scope at or under host that doesn't decide it
// (store.ScopeUnder), the first underShown, and how much there is.
func (s *server) matchUnder(r *http.Request, host string) ([]matchResult, int, error) {
	rules, progs, total, err := s.Store.ScopeUnder(r.Context(), host, underShown)
	var out []matchResult
	for _, ru := range rules {
		out = append(out, matchResult{Program: progs[ru.ProgramID], InScope: ru.InScope, Via: ru.Target})
	}
	return out, total, err
}

// matchMentioning lists live scope whose target contains the host (or,
// if it isn't one, the text) checked (store.ScopeMentioning), the first
// underShown, and how much there is.
func (s *server) matchMentioning(r *http.Request, q string) ([]matchResult, int, error) {
	rules, progs, total, err := s.Store.ScopeMentioning(r.Context(), hostText(q), underShown)
	var out []matchResult
	for _, ru := range rules {
		out = append(out, matchResult{Program: progs[ru.ProgramID], InScope: ru.InScope, Via: ru.Target})
	}
	return out, total, err
}

// hostText is the host in q (a host, IP or URL), or q as it is.
func hostText(q string) string {
	if hosts, _, ok := scope.Candidates(q); ok {
		return hosts[0]
	}
	return strings.TrimSpace(q)
}

// matchPast checks host against scope that programs have removed, each
// removed entry on its own.
func (s *server) matchPast(r *http.Request, host string) ([]pastMatch, error) {
	entries, err := s.Store.PastEntries(r.Context(), host)
	if err != nil {
		return nil, err
	}
	var out []pastMatch
	for _, e := range entries {
		var rules []scope.Rule
		for _, t := range scope.Targets(e.AssetType, e.Identifier) {
			rules = append(rules, scope.Rule{ProgramID: e.Program.ID, InScope: e.InScope, Target: t})
		}
		for _, m := range scope.Match(rules, host) {
			out = append(out, pastMatch{matchResult{e.Program, m.InScope, m.Rule.Target}, e.Identifier, e.RemovedAt})
		}
		if len(out) == pastShown {
			break
		}
	}
	return out, nil
}
