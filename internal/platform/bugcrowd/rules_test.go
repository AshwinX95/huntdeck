package bugcrowd

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/mdpsec/huntdeck/internal/platform"
)

func TestFetchScopeDetail(t *testing.T) {
	f := newFake(t)
	c := f.client(t, Options{})
	es, d, err := c.FetchScopeDetail(context.Background(), program(f, "acme-bb", "/engagements/acme-bb"))
	if err != nil || len(es) != 7 {
		t.Fatalf("FetchScopeDetail = %d entries, %v", len(es), err)
	}
	if d.Policy != "Hello\n\nTargets" {
		t.Errorf("policy = %q", d.Policy)
	}
	// Tier 1 pays P1 and P2; the points tier and out-of-scope group pay nothing.
	want := []platform.RewardRow{{Scope: "Tier 1", Critical: platform.RewardRange{Min: 5000, Max: 15000},
		High: platform.RewardRange{Min: 1500, Max: 4000}}}
	if fmt.Sprint(d.Rewards) != fmt.Sprint(want) {
		t.Errorf("rewards = %v, want %v", d.Rewards, want)
	}
	if f := d.Facts; f == nil || f.SafeHarbor != "partial" || f.Collaboration == nil || !*f.Collaboration ||
		f.Disclosure == nil || *f.Disclosure || f.OffersSwag != nil {
		t.Errorf("facts = %+v, want partial safe harbor, collaboration, no coordinated disclosure", f)
	}
	if f := d.Facts; f == nil || f.StartedAt == nil || !f.StartedAt.Equal(time.Date(2026, 8, 11, 18, 0, 0, 0, time.UTC)) {
		t.Errorf("facts = %+v, want started Aug 11 2026 18:00", f)
	}
	if r := d.Requirements; r == nil || !r.Application || !r.IDVerified || d.State != platform.StateOpen {
		t.Errorf("requirements %+v, state %q; want application and ID check, open", r, d.State)
	}
}

func TestHTMLToMarkdown(t *testing.T) {
	tests := []struct{ name, in, want string }{
		{"empty", "  ", ""},
		{"headings and paragraphs", "<h1>Our Mission:</h1>\n\n<p>Help   people\nget jobs.</p><p>Second</p>",
			"# Our Mission:\n\nHelp people get jobs.\n\nSecond"},
		{"lists", "<ul><li>One</li><li>Two<ul><li>Nested</li></ul></li></ul><ol><li>First</li></ol>",
			"* One\n* Two\n  * Nested\n\n1. First"},
		{"inline", `<p><strong>Do not</strong> test <em>prod</em>; see <a href="https://docs.example.com/x">the docs</a>, <code>*.example.com</code></p>`,
			"**Do not** test *prod*; see [the docs](https://docs.example.com/x), `*.example.com`"},
		{"unsafe links lose the link, keep the text", `<a href="javascript:alert(1)">click</a>`, "click"},
		{"entities and scripts", `<p>A &amp; B &lt;tag&gt;</p><script>alert(1)</script><style>p{}</style>`, "A & B <tag>"},
		{"pre keeps spacing", "<pre>GET /x\n  Host: a</pre>", "```\nGET /x\n  Host: a\n```"},
		{"line breaks and tables", "<p>a<br>b</p><table><tr><th>P1</th><td>$5k</td></tr></table>", "a\nb\n\nP1 | $5k |"},
	}
	for _, tt := range tests {
		if got := htmlToMarkdown(tt.in); got != tt.want {
			t.Errorf("%s:\n got %q\nwant %q", tt.name, got, tt.want)
		}
	}
}

func TestEngagementState(t *testing.T) {
	for in, want := range map[string]string{
		"":                   "",
		"in_progress":        platform.StateOpen,
		"in_progress_paused": platform.StatePaused,
		"paused":             platform.StatePaused,
		"ended":              platform.StateClosed,
		"something_new":      "",
	} {
		if got := engagementState(in); got != want {
			t.Errorf("engagementState(%q) = %q, want %q", in, got, want)
		}
	}
}
