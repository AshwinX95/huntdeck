package web

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/mdpsec/huntdeck/internal/platform"
	"github.com/mdpsec/huntdeck/internal/store"
	"github.com/mdpsec/huntdeck/internal/syncer"
)

func TestRequires(t *testing.T) {
	sig := func(v float64) *float64 { return &v }
	for _, tt := range []struct {
		name string
		reqs platform.Requirements
		want string
	}{
		{"none", platform.Requirements{}, ""},
		{"Signal 0 and below aren't shown", platform.Requirements{MinSignal: sig(0)}, ""},
		{"negative Signal", platform.Requirements{MinSignal: sig(-1)}, ""},
		{"positive Signal", platform.Requirements{MinSignal: sig(3)}, "Signal 3.0"},
		{"the rest", platform.Requirements{IDVerified: true, EEA: true, TwoFactor: true}, "ID check,EEA,2FA"},
	} {
		var got []string
		for _, c := range requires("intigriti", store.ProgramRequirements{Known: true, Requirements: tt.reqs}) {
			got = append(got, c.Label)
		}
		if g := strings.Join(got, ","); g != tt.want {
			t.Errorf("%s: chips %q, want %q", tt.name, g, tt.want)
		}
	}
}

// A program's testing rules and its EEA and 2FA requirements show on its
// page.
func TestProgramPageTestingRules(t *testing.T) {
	e := newTestEnv(t)
	c := e.client()
	ctx := context.Background()
	p, err := e.store.Program(ctx, "fake", "acme")
	if err != nil {
		t.Fatal(err)
	}
	err = e.store.Write(ctx, func(tx *store.Tx) error {
		if err := tx.SetRequirements(ctx, p.ID, platform.Requirements{EEA: true, TwoFactor: true}); err != nil {
			return err
		}
		return tx.SetTesting(ctx, p.ID, platform.TestingRules{RateLimit: 5, UserAgent: "UA <x>", Header: "X-Bug-Bounty: me", PlatformEmail: true})
	})
	if err != nil {
		t.Fatal(err)
	}
	res, body := e.do(c, http.MethodGet, "/programs/fake/acme", nil, nil)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("program page: %d", res.StatusCode)
	}
	for _, want := range []string{
		"<b>At most 5 requests a second</b>", "<b>User-Agent: UA &lt;x&gt;</b>", `data-copy-text="UA &lt;x&gt;"`,
		`data-copy-text="X-Bug-Bounty: me"`, "Sign up with your", "<b>Residents of the European Economic Area only</b>",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("program page lacks %q", want)
		}
	}
	if strings.Contains(body, `<li class="alert">`) {
		t.Error("a requirement is shown as an alert")
	}

	// No automation at all is shown as such.
	_ = e.store.Write(ctx, func(tx *store.Tx) error {
		return tx.SetTesting(ctx, p.ID, platform.TestingRules{NoAutomation: true})
	})
	if _, body := e.do(c, http.MethodGet, "/programs/fake/acme", nil, nil); !strings.Contains(body, `<li class="alert">`) || !strings.Contains(body, "<b>No automated tools</b>") {
		t.Error("no-automation rule not shown")
	}
}

// The facts some listings give, the brief's sections and a credential pool
// show on the program page; a listing that doesn't say keeps them.
func TestProgramPageFacts(t *testing.T) {
	e := newTestEnv(t)
	c := e.client()
	ctx := context.Background()
	yes, cost := true, 3
	acme := platform.Program{Handle: "acme", Name: "Acme Corp", URL: "https://platform.example/acme", State: platform.StateOpen, OffersBounty: true,
		Collaboration: &yes, ReportCost: &cost, Country: "FR", Industry: "Retail", TriageService: "Priority Triage",
		Requirements: &platform.Requirements{Terms: true}}
	e.fake.SetPrograms(acme)
	poll := func() {
		t.Helper()
		if _, err := e.syncer.Poll(ctx, e.fake, syncer.Options{Mode: syncer.ModeQuick, Trigger: "manual"}); err != nil {
			t.Fatal(err)
		}
	}
	poll()
	p, err := e.store.Program(ctx, "fake", "acme")
	if err != nil {
		t.Fatal(err)
	}
	accepted := time.Date(2026, 10, 20, 0, 0, 0, 0, time.UTC)
	err = e.store.Write(ctx, func(tx *store.Tx) error {
		return tx.RecordProgramStats(ctx, p.ID, platform.ProgramStats{LastAcceptedAt: &accepted, Profile: platform.ProgramProfile{
			OutScope: "Not **shop**.", FAQ: "### Credentials?\nOn request.", CredentialPool: &platform.CredentialPool{Max: 2}}})
	})
	if err != nil {
		t.Fatal(err)
	}
	// The next listing says none of it.
	e.fake.SetPrograms(platform.Program{Handle: "acme", Name: "Acme Corp", URL: "https://platform.example/acme", State: platform.StateOpen, OffersBounty: true})
	poll()

	_, body := e.do(c, http.MethodGet, "/programs/fake/acme", nil, nil)
	for _, want := range []string{
		"<dt>Collaboration</dt><dd>Allowed", "<dt>Report cost</dt><dd>3 credits", "<dt>Triage</dt><dd>Priority Triage</dd>",
		"<dt>Industry</dt><dd>Retail</dd>", "<dt>Country</dt><dd>FR</dd>", "<b>You must accept the program&#39;s own terms",
		"Last accepted</dt>", "up to 2 accounts each; none left right now",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("program page lacks %q", want)
		}
	}
	// The brief's sections are on the Policy tab, with their headings listed.
	_, body = e.do(c, http.MethodGet, "/programs/fake/acme?tab=policy", nil, nil)
	for _, want := range []string{
		"<h2>Out of scope</h2>", "Not <strong>shop</strong>.", "<h2>FAQ</h2>", `<a class="t3" href="#faq-1">Credentials?</a>`, `<h3 id="faq-1">Credentials?</h3>`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("policy tab lacks %q", want)
		}
	}
	// The pool counts for the test credentials filter.
	if _, body := e.do(c, http.MethodGet, "/programs?creds=1", nil, nil); !strings.Contains(body, "/programs/fake/acme") {
		t.Error("the credentials filter misses a program with a credential pool")
	}
}

func TestNotesOnOneLine(t *testing.T) {
	reqs := []reqChip{{Label: "Apply", Title: "apply"}, {Label: "ID check", Title: "id"}, {Label: "2FA", Title: "2fa"}}
	n := notes(reqs, true)
	if n.First == nil || n.First.Label != "Apply" || n.More != 3 || !strings.Contains(n.MoreTitle, "creds: ") {
		t.Errorf("notes = %+v", n)
	}
	if n := notes(nil, false); n.First != nil || n.More != 0 {
		t.Errorf("no notes = %+v", n)
	}
}
