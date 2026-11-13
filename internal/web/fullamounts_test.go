package web

import (
	"io/fs"
	"regexp"
	"strings"
	"testing"
)

// Pages show money compact ("$8.3k"); the *Full funcs, to the unit, are
// for tooltips only. Each call to one in a template sits in a title
// attribute.
func TestFullAmountsOnlyInTooltips(t *testing.T) {
	action := regexp.MustCompile(`\{\{.*?\}\}`)
	full := regexp.MustCompile(`\b(moneyFull|amountFull|amountRangeFull|rewardFull)\b`)
	names, err := fs.Glob(templateFS, "templates/*.html")
	if err != nil {
		t.Fatal(err)
	}
	partials, err := fs.Glob(templateFS, "templates/partials/*.html")
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range append(names, partials...) {
		b, err := fs.ReadFile(templateFS, name)
		if err != nil {
			t.Fatal(err)
		}
		for n, line := range strings.Split(string(b), "\n") {
			// Every action stands as one letter, F for a full amount, so the
			// quotes left are the HTML's.
			flat := action.ReplaceAllStringFunc(line, func(a string) string {
				if full.MatchString(a) {
					return "\x00"
				}
				return "X"
			})
			for i, c := range flat {
				if c != 0 {
					continue
				}
				before := flat[:i]
				q := strings.LastIndexByte(before, '"')
				if q < 0 || !strings.HasSuffix(before[:q], "title=") {
					t.Errorf("%s:%d: a full amount outside a tooltip: %s", name, n+1, strings.TrimSpace(line))
				}
			}
		}
	}
}
