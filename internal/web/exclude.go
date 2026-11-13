package web

import (
	"slices"
	"strings"
)

// Searches that leave things out: a word starting with "-" in a search
// ("acme -staging") excludes what contains it, as does each word of the
// exclude field beside the search (?not=, words separated by spaces or
// commas). The hunt board's fresh targets and Programs take them.

// maxExcludes bounds how many words a list leaves out.
const maxExcludes = 20

// searchTerms splits a search into what to find and what to leave out,
// adding not's words to the latter.
func searchTerms(q, not string) (find string, exclude []string) {
	var keep []string
	for w := range strings.FieldsSeq(q) {
		if len(w) > 1 && w[0] == '-' {
			exclude = append(exclude, w[1:])
		} else {
			keep = append(keep, w)
		}
	}
	for w := range strings.FieldsFuncSeq(not, func(r rune) bool { return r == ',' || r == ' ' || r == '\t' }) {
		exclude = append(exclude, strings.TrimPrefix(w, "-"))
	}
	var out []string
	for _, w := range exclude {
		if w = strings.ToLower(w); w != "" && !slices.Contains(out, w) && len(out) < maxExcludes {
			out = append(out, w)
		}
	}
	return strings.Join(keep, " "), out
}
