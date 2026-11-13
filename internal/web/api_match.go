package web

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/mdpsec/huntdeck/internal/scope"
	"github.com/mdpsec/huntdeck/internal/store"
)

// Bulk matching limits: about a large subdomain list, and its bytes.
const (
	matchManyMax   = 20000
	matchManyBytes = 4 << 20
	// A read-only copy is shown to anyone: a smaller list, the same check.
	matchManyDemoMax = 2000
)

// apiMatchMany checks a list of hosts, IPs or URLs, one per line in the
// body (tool output as it is). format=txt returns just the in-scope ones.
func (s *server) apiMatchMany(w http.ResponseWriter, r *http.Request) {
	maxLines := matchManyMax
	if s.ReadOnly {
		maxLines = matchManyDemoMax
	}
	r.Body = http.MaxBytesReader(w, r.Body, matchManyBytes)
	inputs, err := scope.ReadInputs(r.Body, maxLines)
	if err != nil {
		writeJSONError(w, http.StatusRequestEntityTooLarge, fmt.Sprintf("send at most %d lines (%d MB)", maxLines, matchManyBytes>>20))
		return
	}
	rules, err := s.Store.MatchRulesBulk(r.Context(), inputs)
	if errors.Is(err, store.ErrTooManyLookups) {
		writeJSONError(w, http.StatusRequestEntityTooLarge, "too many distinct names in one request: send fewer, or split the list")
		return
	}
	if err != nil {
		s.apiServerError(w, r, err)
		return
	}
	out := apiBulkMatchJSON{Results: make([]apiBulkResult, 0, len(inputs))}
	txt := r.URL.Query().Get("format") == "txt"
	var b strings.Builder
	for _, input := range inputs {
		matches := scope.Match(rules.For(input), input)
		res := apiBulkResult{Input: input, Verdict: scope.Verdict(matches), Matches: []apiMatchJSON{}}
		switch res.Verdict {
		case scope.VerdictIn:
			out.In++
			b.WriteString(input + "\n")
		case scope.VerdictOut:
			out.Out++
		default:
			out.None++
		}
		for _, m := range matches {
			res.Matches = append(res.Matches, apiMatchJSON{m.InScope, m.Rule.Target.Value, m.Rule.Target.Kind, refJSON(rules.Programs[m.ProgramID])})
		}
		out.Results = append(out.Results, res)
	}
	if txt {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		_, _ = io.WriteString(w, textComment(w, r)+b.String())
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *server) apiMatch(w http.ResponseWriter, r *http.Request) {
	host := strings.TrimSpace(r.URL.Query().Get("host"))
	if host == "" {
		writeJSONError(w, http.StatusBadRequest, "host is required")
		return
	}
	res, err := s.matchHost(r, host)
	if err != nil {
		s.apiServerError(w, r, err)
		return
	}
	past, err := s.matchPast(r, host)
	if err != nil {
		s.apiServerError(w, r, err)
		return
	}
	under, underTotal, err := s.matchUnder(r, host)
	if err != nil {
		s.apiServerError(w, r, err)
		return
	}
	out := apiHostMatchJSON{Host: host, Matches: []apiMatchJSON{}, Past: []apiPastMatch{}, Under: []apiMatchJSON{}, UnderTotal: underTotal,
		Mentioning: []apiMatchJSON{}}
	for _, m := range res {
		out.Matches = append(out.Matches, apiMatchJSON{m.InScope, m.Via.Value, m.Via.Kind, refJSON(m.Program)})
	}
	for _, m := range under {
		out.Under = append(out.Under, apiMatchJSON{m.InScope, m.Via.Value, m.Via.Kind, refJSON(m.Program)})
	}
	if len(res) == 0 && len(under) == 0 {
		mentioning, total, err := s.matchMentioning(r, host)
		if err != nil {
			s.apiServerError(w, r, err)
			return
		}
		for _, m := range mentioning {
			out.Mentioning = append(out.Mentioning, apiMatchJSON{m.InScope, m.Via.Value, m.Via.Kind, refJSON(m.Program)})
		}
		out.MentioningTotal = total
	}
	for _, m := range past {
		out.Past = append(out.Past, apiPastMatch{apiMatchJSON{m.InScope, m.Via.Value, m.Via.Kind, refJSON(m.Program)}, m.Identifier, m.RemovedAt})
	}
	writeJSON(w, http.StatusOK, out)
}
