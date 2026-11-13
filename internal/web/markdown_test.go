package web

import (
	"io/fs"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestMarkdownIsSafe(t *testing.T) {
	for _, tt := range []struct {
		name, in string
		want     []string
		never    []string
	}{
		{"formatting", "## Rules\n\n* **Must** report\n* one bug\n\n| a | b |\n|---|---|\n| 1 | 2 |",
			[]string{"<h3>Rules</h3>", "<li><strong>Must</strong> report</li>", "<table>", "<td>1</td>"}, nil},
		{"raw html dropped", "Hi <script>alert(1)</script> <img src=x onerror=alert(1)>\n\n<div onclick=x>block</div>",
			[]string{"Hi"}, []string{"<script", "<img", "onerror", "onclick", "<div"}},
		{"safe link", "[policy](https://example.com/policy?a=1&b=2)",
			[]string{`<a href="https://example.com/policy?a=1&amp;b=2" target="_blank" rel="noopener noreferrer nofollow">policy</a>`}, nil},
		{"javascript link", "[click](javascript:alert(1)) [data](data:text/html,x) [rel](/admin)",
			[]string{"click", "data", "rel"}, []string{"<a", "javascript:", "data:text"}},
		{"image becomes link", "![diagram](https://cdn.example.net/x.png)",
			[]string{`<a href="https://cdn.example.net/x.png"`, ">diagram</a>"}, []string{"<img"}},
		{"autolink", "See https://example.com/a and security@example.com",
			[]string{`<a href="https://example.com/a" target="_blank" rel="noopener noreferrer nofollow">https://example.com/a</a>`,
				`<a href="mailto:security@example.com"`}, nil},
		{"quote in url", `[x](https://example.com/"onmouseover=alert(1))`, nil, []string{`"onmouseover`}},
		{"placeholders show as written", "Send *X-Bug-Bounty: <username>-acme* and name it acme-<your email>-<random-string>",
			[]string{"X-Bug-Bounty: &lt;username&gt;-acme", "acme-&lt;your email&gt;-&lt;random-string&gt;"}, nil},
		{"line breaks stay, other tags go", "one<br/>two <b>bold</b> <iframe src=x></iframe>",
			[]string{"one<br>two", "bold"}, []string{"<b>", "<iframe", "&lt;b&gt;", "&lt;iframe"}},
	} {
		got := string(markdown(tt.in))
		for _, w := range tt.want {
			if !strings.Contains(got, w) {
				t.Errorf("%s: output lacks %q:\n%s", tt.name, w, got)
			}
		}
		for _, n := range tt.never {
			if strings.Contains(got, n) {
				t.Errorf("%s: output contains %q:\n%s", tt.name, n, got)
			}
		}
	}
}

// Every palette offered in Settings is defined in the stylesheet and applied
// by theme.js.
func TestPalettesDefined(t *testing.T) {
	css, err := fs.ReadFile(staticFS, "static/css/00-theme.css")
	if err != nil {
		t.Fatal(err)
	}
	js, err := fs.ReadFile(staticFS, "static/theme.js")
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range palettes {
		if !strings.Contains(string(css), `[data-palette="`+p.Key+`"]`) {
			t.Errorf("00-theme.css doesn't define the %s palette", p.Key)
		}
		if p.Key != "midnight" && !strings.Contains(string(js), `"`+p.Key+`"`) {
			t.Errorf("theme.js doesn't apply the %s palette", p.Key)
		}
	}
}

func TestWebsiteLink(t *testing.T) {
	for _, tt := range []struct{ in, url, label string }{
		{"https://www.8x8.com", "https://www.8x8.com", "8x8.com"},
		{"http://livenation.com", "http://livenation.com", "livenation.com"},
		{"javascript:alert(1)", "", ""},
		{"livenation.com", "", ""},
		{"", "", ""},
	} {
		got := website(tt.in)
		if (got == nil) != (tt.url == "") || (got != nil && (string(got.URL) != tt.url || got.Label != tt.label)) {
			t.Errorf("website(%q) = %+v, want %q %q", tt.in, got, tt.url, tt.label)
		}
	}
}

func TestAge(t *testing.T) {
	day := 24 * time.Hour
	for _, tt := range []struct {
		ago  time.Duration
		want string
	}{
		{time.Hour, "today"}, {3 * day, "3d"}, {20 * day, "2w"}, {100 * day, "3mo"}, {365 * day, "1y"}, {500 * day, "1y 4mo"},
	} {
		if got := age(time.Now().Add(-tt.ago)); got != tt.want {
			t.Errorf("age(%v ago) = %q, want %q", tt.ago, got, tt.want)
		}
	}
}

func TestAddedSince(t *testing.T) {
	now := time.Date(2026, 10, 26, 15, 30, 0, 0, time.UTC)
	for _, tt := range []struct {
		in   string
		want time.Time
	}{
		{"7d", time.Date(2026, 10, 19, 0, 0, 0, 0, time.UTC)},
		{"2026-10-01", time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)},
		{"", time.Time{}}, {"0d", time.Time{}}, {"-3d", time.Time{}}, {"week", time.Time{}}, {"99999d", time.Time{}},
	} {
		if got := addedSince(tt.in, now); !got.Equal(tt.want) {
			t.Errorf("addedSince(%q) = %v, want %v", tt.in, got, tt.want)
		}
	}
	// Days count from midnight where the user is: 01:30 on the 27th in
	// Brisbane is still the 26th in UTC.
	bne, err := time.LoadLocation("Australia/Brisbane")
	if err != nil {
		t.Fatal(err)
	}
	if got, want := addedSince("1d", now.In(bne)), time.Date(2026, 10, 26, 0, 0, 0, 0, bne); !got.Equal(want) {
		t.Errorf("addedSince(1d) in Brisbane = %v, want %v", got, want)
	}
	if got, want := addedSince("2026-10-01", now.In(bne)), time.Date(2026, 9, 30, 14, 0, 0, 0, time.UTC); !got.Equal(want) {
		t.Errorf("addedSince(date) in Brisbane = %v, want %v", got, want)
	}
}

// Headings move down under the page's h1 and the panel's h2, keeping how
// they nest; the contents list them as written.
func TestMarkdownDoc(t *testing.T) {
	html, toc := markdownDoc("# Overview\n\nHi.\n\n## Rules of `engagement`\n\n### Tips\n\n#### Deep\n", "policy")
	want := []tocEntry{{"policy-1", "Overview", 1}, {"policy-2", "Rules of engagement", 2}, {"policy-3", "Tips", 3}}
	if !slices.Equal(toc, want) {
		t.Errorf("toc = %+v, want %+v", toc, want)
	}
	for _, w := range []string{`<h3 id="policy-1">Overview</h3>`, `<h4 id="policy-2">Rules of <code>engagement</code></h4>`, "<h6>Deep</h6>"} {
		if !strings.Contains(string(html), w) {
			t.Errorf("html lacks %q:\n%s", w, html)
		}
	}
	if html, _ := markdownDoc("### Already small\n\n#### Smaller", "faq"); !strings.Contains(string(html), `<h3 id="faq-1">Already small</h3>`) {
		t.Errorf("small headings moved: %s", html)
	}
	if html := string(markdown("# Big\n\n###### Tiny")); !strings.Contains(html, "<h3>Big</h3>") || !strings.Contains(html, "<h6>Tiny</h6>") {
		t.Errorf("markdown headings: %s", html)
	}
}

// Links to Intigriti program pages open them signed in from here.
func TestMarkdownResearcherLinks(t *testing.T) {
	got := string(markdown("See [the program](https://app.intigriti.com/programs/acme/acme/detail) or https://APP.intigriti.com/programs/x/y and " +
		"https://app.intigriti.com/researcher/programs/a/b/leaderboard?x=1, not https://app.intigriti.com/researcher/programs/overview"))
	for _, want := range []string{`href="https://app.intigriti.com/researcher/program-redirect/acme/acme"`,
		`href="https://app.intigriti.com/researcher/program-redirect/x/y"`, `href="https://app.intigriti.com/researcher/program-redirect/a/b"`,
		`href="https://app.intigriti.com/researcher/programs/overview"`} {
		if !strings.Contains(got, want) {
			t.Errorf("lacks %s: %s", want, got)
		}
	}
	if strings.Contains(got, `href="https://app.intigriti.com/programs/`) || strings.Contains(got, `href="https://APP.intigriti.com/programs/`) {
		t.Errorf("an old link is left: %s", got)
	}
}
