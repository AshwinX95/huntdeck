package web

import (
	"html/template"
	"strings"

	"github.com/mdpsec/huntdeck/internal/platform"
	"github.com/mdpsec/huntdeck/internal/store"
)

// policyDoc is one part of a program's own text on the Policy tab: the
// policy, the introduction, notes on scope, the FAQ. TOC lists its
// headings for the tab's contents list.
type policyDoc struct {
	ID, Title string
	Body      template.HTML
	TOC       []tocEntry
	// Items are a list of findings instead of a Body (Excluded findings).
	Items []platform.Exclusion
}

// policyDocs are a program's texts, in reading order. Each heading's ID
// starts with its document's ID, so they don't collide.
func policyDocs(p *store.ProgramDetail) []policyDoc {
	var pr platform.ProgramProfile
	if p.PublicStats != nil {
		pr = p.PublicStats.Profile
	}
	var out []policyDoc
	for _, part := range []struct{ id, title, text string }{
		{"about", "About", pr.Introduction},
		{"policy", "Program policy", p.Policy},
		{"in-scope", "In-scope notes", pr.InScope},
		{"out-of-scope", "Out of scope", pr.OutScope},
		{"severity", "Severity assessment", pr.Severity},
		{"faq", "FAQ", pr.FAQ},
		{"rewards", "Reward notes", pr.Campaign},
	} {
		if strings.TrimSpace(part.text) == "" {
			continue
		}
		body, toc := markdownDoc(part.text, part.id)
		out = append(out, policyDoc{ID: part.id, Title: part.title, Body: body, TOC: toc})
		if part.id == "out-of-scope" || (part.id == "policy" && pr.OutScope == "") {
			out = appendExclusions(out, pr.ScopeExclusions)
		}
	}
	if !hasDoc(out, "excluded") {
		out = appendExclusions(out, pr.ScopeExclusions)
	}
	return out
}

// appendExclusions adds the findings a program won't accept, if any.
func appendExclusions(docs []policyDoc, ex []platform.Exclusion) []policyDoc {
	if len(ex) == 0 || hasDoc(docs, "excluded") {
		return docs
	}
	return append(docs, policyDoc{ID: "excluded", Title: "Excluded findings", Items: ex})
}

// hasDoc says whether docs has one with that ID.
func hasDoc(docs []policyDoc, id string) bool {
	for _, d := range docs {
		if d.ID == id {
			return true
		}
	}
	return false
}
