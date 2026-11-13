package web

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/mdpsec/huntdeck/internal/platform"
	"github.com/mdpsec/huntdeck/internal/store"
)

func TestSameColumns(t *testing.T) {
	e := func(bounty bool, sev string) store.EntryView {
		return store.EntryView{BountyEligible: bounty, MaxSeverity: sev}
	}
	tests := []struct {
		name             string
		list             []store.EntryView
		bounty, severity bool
		same             string
	}{
		{"empty", nil, true, true, ""},
		{"all the same", []store.EntryView{e(true, "critical"), e(true, "critical")}, false, false, "All bounty-eligible, up to critical"},
		{"no bounties, unrated", []store.EntryView{e(false, ""), e(false, "")}, false, false, "No bounties"},
		{"severity differs", []store.EntryView{e(true, "high"), e(true, "critical")}, false, true, "All bounty-eligible"},
		{"both differ", []store.EntryView{e(true, "none"), e(false, "low")}, true, true, ""},
	}
	for _, tt := range tests {
		b, s, same := sameColumns(tt.list)
		if b != tt.bounty || s != tt.severity || same != tt.same {
			t.Errorf("%s: got %v %v %q, want %v %v %q", tt.name, b, s, same, tt.bounty, tt.severity, tt.same)
		}
	}
}

func TestNotePreview(t *testing.T) {
	for _, tt := range []struct{ note, want string }{
		{"Short note", ""},
		{"  Trailing space\n\n", ""},
		{"First line\nSecond line", "First line"},
		{strings.Repeat("long ", 30), strings.TrimSpace(strings.Repeat("long ", 30))},
	} {
		if got := notePreview(tt.note); got != tt.want {
			t.Errorf("notePreview(%q) = %q, want %q", tt.note, got, tt.want)
		}
	}
}

func TestNoteRest(t *testing.T) {
	for _, tt := range []struct{ note, want string }{
		{"One line", ""},
		{"\n\nTitle\n\nBody one\nBody two\n", "Body one\nBody two"},
	} {
		if got := noteRest(tt.note); got != tt.want {
			t.Errorf("noteRest(%q) = %q, want %q", tt.note, got, tt.want)
		}
	}
}

func TestHuntChange(t *testing.T) {
	day := func(d int, score float64) store.HuntDay {
		return store.HuntDay{Day: time.Date(2026, 10, d, 0, 0, 0, 0, time.UTC), Score: score}
	}
	for _, tt := range []struct {
		days        []store.HuntDay
		short, long string
	}{
		{nil, "", ""},
		{[]store.HuntDay{day(1, 40)}, "", ""},
		{[]store.HuntDay{day(1, 40), day(5, 43.4)}, "▲ 3", "up 3 since Oct 1"},
		{[]store.HuntDay{day(2, 50), day(5, 41)}, "▼ 9", "down 9 since Oct 2"},
		{[]store.HuntDay{day(3, 50), day(5, 50.2)}, "no change", "no change since Oct 3"},
	} {
		if s, l := huntChange(tt.days); s != tt.short || l != tt.long {
			t.Errorf("huntChange(%v) = %q, %q; want %q, %q", tt.days, s, l, tt.short, tt.long)
		}
	}
}

func TestPolicyDocs(t *testing.T) {
	ex := []platform.Exclusion{{Name: "Self-XSS"}}
	tests := []struct {
		name string
		p    store.ProgramDetail
		want string // the docs' IDs, in order
	}{
		{"nothing", store.ProgramDetail{}, ""},
		{"policy only", store.ProgramDetail{Policy: "# Rules\n\n## Scope"}, "policy"},
		{"exclusions after out of scope", store.ProgramDetail{Policy: "x", PublicStats: &store.ProgramStats{ProgramStats: platform.ProgramStats{
			Profile: platform.ProgramProfile{Introduction: "hi", OutScope: "not this", FAQ: "q", ScopeExclusions: ex}}}}, "about,policy,out-of-scope,excluded,faq"},
		{"exclusions after the policy", store.ProgramDetail{Policy: "x", PublicStats: &store.ProgramStats{ProgramStats: platform.ProgramStats{
			Profile: platform.ProgramProfile{Campaign: "2x", ScopeExclusions: ex}}}}, "policy,excluded,rewards"},
		{"exclusions alone", store.ProgramDetail{PublicStats: &store.ProgramStats{ProgramStats: platform.ProgramStats{
			Profile: platform.ProgramProfile{ScopeExclusions: ex}}}}, "excluded"},
	}
	for _, tt := range tests {
		var ids []string
		for _, d := range policyDocs(&tt.p) {
			ids = append(ids, d.ID)
		}
		if got := strings.Join(ids, ","); got != tt.want {
			t.Errorf("%s: docs %q, want %q", tt.name, got, tt.want)
		}
	}
	docs := policyDocs(&store.ProgramDetail{Policy: "# Rules\n\n## Scope\n\n#### Too deep"})
	if toc := docs[0].TOC; len(toc) != 2 || toc[0].ID != "policy-1" || toc[1].Text != "Scope" || toc[1].Level != 2 {
		t.Errorf("policy TOC = %+v", toc)
	}
}

// The Scope tab narrows by type and search, switches between in, out and
// removed, and keeps the choices in its links.
func TestProgramScopeTab(t *testing.T) {
	e := newTestEnv(t)
	c := e.client()
	tests := []struct {
		query      string
		want, lack []string
	}{
		{"tab=scope&type=url", []string{"https://api.acme.example/v2", `<a class="fchip on" href="?tab=scope&amp;type=url" aria-current="true">URLs <em>1</em></a>`,
			`<input type="hidden" name="type" value="url">`}, []string{`<td class="asset">*.acme.example`}},
		{"tab=scope&q=API", []string{"https://api.acme.example/v2", `value="API"`, `href="?q=API&amp;show=out&amp;tab=scope"`}, []string{`<td class="asset">*.acme.example`}},
		{"tab=scope&q=nothing-here", []string{"Nothing matches.", `<a class="link" href="?tab=scope">Clear</a>`}, nil},
		{"tab=scope&type=bogus", []string{`<a class="fchip on" href="?tab=scope" aria-current="true">All <em>2</em></a>`}, nil},
		{"tab=scope&show=removed", []string{`<a class="on" aria-current="true" href="?show=removed&amp;tab=scope">Removed <em>0</em></a>`, "None listed."}, nil},
		{"tab=bogus", []string{`<a class="on" aria-current="page" href="?">Overview</a>`}, nil},
	}
	for _, tt := range tests {
		res, body := e.do(c, http.MethodGet, "/programs/fake/acme?"+tt.query, nil, nil)
		if res.StatusCode != http.StatusOK {
			t.Errorf("%s: %d", tt.query, res.StatusCode)
			continue
		}
		for _, w := range tt.want {
			if !strings.Contains(body, w) {
				t.Errorf("%s: lacks %q", tt.query, w)
			}
		}
		for _, l := range tt.lack {
			if strings.Contains(body, l) {
				t.Errorf("%s: has %q", tt.query, l)
			}
		}
	}
}

func TestHuntReason(t *testing.T) {
	for _, tt := range []struct{ in, want string }{
		{"Bounties up to $8,000 (top 49% on HackerOne)", "Bounties up to $8,000 (<b>top 49% on HackerOne</b>)"},
		{"1655 reports in 90 days for 84 in-scope assets: busier than 95% of HackerOne programs", "1655 reports in 90 days for 84 in-scope assets: <b>busier than 95% of HackerOne programs</b>"},
		{"Newest: <script>x</script>, 2 days ago", "Newest: &lt;script&gt;x&lt;/script&gt;, 2 days ago"},
	} {
		if got := string(huntReason(tt.in)); got != tt.want {
			t.Errorf("huntReason(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

// Each number shows once: the overview leaves out what the headline and
// the head give, and the report charts live on the Reports tab.
func TestProgramOverviewNoRepeats(t *testing.T) {
	e := newTestEnv(t)
	c := e.client()
	_, body := e.do(c, http.MethodGet, "/programs/fake/acme", nil, nil)
	for _, lack := range []string{"Response efficiency</dt>", ">First response</dt>", "<dt>Bounties</dt>", "<dt>Launched</dt>", "<dt>First seen</dt>",
		"Take the scope", "Change weighting", "among the picks", "by severity ·"} {
		if strings.Contains(body, lack) {
			t.Errorf("overview has %q", lack)
		}
	}
	for _, want := range []string{"96% on time", `<p class="hp-why">`, "Paid per report", "<b>Test credentials for 1 asset</b>",
		`New in scope <span class="count">1</span>`, `<td class="asset">https://api.acme.example/v2</td>`, "Recent changes", "<h2>Platform stats</h2>",
		"Known issues</dt><dd>1,234 · 987 distinct</dd>"} {
		if !strings.Contains(body, want) {
			t.Errorf("overview lacks %q", want)
		}
	}
}

func TestStatsTab(t *testing.T) {
	stats := &store.ProgramStats{}
	tests := []struct {
		name string
		d    programData
		want bool
	}{
		{"stats saved, too few days to chart", programData{P: &store.ProgramDetail{PublicStats: stats}}, false},
		{"stats over time", programData{P: &store.ProgramDetail{PublicStats: stats}, History: []historyChart{{Title: "x"}}}, true},
		{"disclosed reports", programData{P: &store.ProgramDetail{}, DisclosedSum: store.DisclosureSummary{Total: 2}}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := false
			for _, tab := range tt.d.tabs() {
				got = got || tab.Key == "stats"
			}
			if got != tt.want {
				t.Errorf("stats tab = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestAmountRange(t *testing.T) {
	f := func(v float64) *float64 { return &v }
	tests := []struct {
		lo, hi   *float64
		want     string
		wantFull string
	}{
		{f(100), f(10000), "$100 – $10k", "$100 – $10,000"},
		{f(12000), f(133700), "$12k – $134k", "$12,000 – $133,700"},
		{f(1500), f(250000), "$1.5k – $250k", "$1,500 – $250,000"},
		{nil, f(133700), "$134k", "$133,700"},
		{f(500), nil, "$500", "$500"},
		{nil, nil, "", ""},
	}
	for _, tt := range tests {
		if got := amountRange(tt.lo, tt.hi, "USD"); got != tt.want {
			t.Errorf("amountRange = %q, want %q", got, tt.want)
		}
		if got := amountRangeFull(tt.lo, tt.hi, "USD"); got != tt.wantFull {
			t.Errorf("amountRangeFull = %q, want %q", got, tt.wantFull)
		}
	}
}
