package web

import (
	"net/http"
	"strconv"
	"strings"
	"testing"
)

// The Last 90 days columns sort by the number they show, today's 90-day
// total; the sorted one says so under its heading, and Reports received
// sorts by its own count (the Quiet programs view keeps competition).
func TestNinetyDaySorts(t *testing.T) {
	e := newTestEnv(t)
	c := e.client()
	for _, key := range ninetyDaySorts {
		_, body := e.do(c, http.MethodGet, "/programs?sort="+key, nil, nil)
		if strings.Count(body, `<small class="sorthint">by 90-day total</small>`) != 1 {
			t.Errorf("sort=%s: no hint, or more than one", key)
		}
		// The numbers shown follow the sort, either way; programs without
		// one (—) go last.
		cls := map[string]string{"paid90": "c-paid", "received": "c-comp", "resolved": "c-resolved"}[key]
		for _, rev := range []string{"", "&rev=1"} {
			_, body := e.do(c, http.MethodGet, "/programs?sort="+key+rev, nil, nil)
			var shown []float64
			missing := false
			for _, part := range strings.Split(body, `<td class="`+cls+` num`)[1:] {
				v := part[strings.Index(part, `<span class="v">`)+len(`<span class="v">`):]
				v = tagless(v[:strings.Index(v, "</td>")])
				n, ok := shownNumber(v)
				if !ok {
					missing = true
					continue
				}
				if missing {
					t.Errorf("sort=%s%s: a number after a program without one", key, rev)
				}
				shown = append(shown, n)
			}
			for i := 1; i < len(shown); i++ {
				if (rev == "" && shown[i] > shown[i-1]) || (rev != "" && shown[i] < shown[i-1]) {
					t.Errorf("sort=%s%s: shown %v out of order", key, rev, shown)
					break
				}
			}
			if len(shown) == 0 {
				t.Errorf("sort=%s%s: %d numbers shown", key, rev, len(shown))
			}
		}
	}
	_, body := e.do(c, http.MethodGet, "/programs", nil, nil)
	if !strings.Contains(body, `href="/programs?sort=received"`) || strings.Contains(body, "sorthint") {
		t.Error("Reports received doesn't sort by its count, or a hint shows unsorted")
	}
}

func tagless(s string) string {
	var b strings.Builder
	in := false
	for _, r := range s {
		switch {
		case r == '<':
			in = true
		case r == '>':
			in = false
		case !in:
			b.WriteRune(r)
		}
	}
	return strings.TrimSpace(b.String())
}

// shownNumber reads a cell's number as shown: "1,204", "≈$2.7k", "$0".
func shownNumber(s string) (float64, bool) {
	s = strings.NewReplacer(",", "", "≈", "", "$", "", "€", "", "£", "").Replace(s)
	if i := strings.IndexAny(s, " \n"); i >= 0 {
		s = s[:i]
	}
	mult := 1.0
	switch {
	case strings.HasSuffix(s, "k"):
		s, mult = strings.TrimSuffix(s, "k"), 1e3
	case strings.HasSuffix(s, "M"):
		s, mult = strings.TrimSuffix(s, "M"), 1e6
	}
	n, err := strconv.ParseFloat(s, 64)
	return n * mult, err == nil
}
