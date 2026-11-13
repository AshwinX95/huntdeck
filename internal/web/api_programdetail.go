package web

import (
	"time"

	"github.com/mdpsec/huntdeck/internal/platform"
	"github.com/mdpsec/huntdeck/internal/store"
)

// What a program's page shows besides its scope and numbers, for
// /api/v1/programs/{platform}/{handle}: its facts, requirements and rules
// for testing, the policy and the rest of its brief, and announcements.

// apiProgramFacts are what the platform says about how the program runs;
// null or "" where it hasn't said.
type apiProgramFacts struct {
	TriageActive    bool   `json:"triage_active"`
	OpenScope       bool   `json:"open_scope"`
	Collaboration   *bool  `json:"collaboration"` // reports can be shared with other hackers
	BountySplitting *bool  `json:"bounty_splitting"`
	OffersSwag      *bool  `json:"offers_swag"`
	Disclosure      *bool  `json:"disclosure_allowed"`
	SafeHarborLevel string `json:"safe_harbor_level"` // "full" or "partial"
	Industry        string `json:"industry"`
	Country         string `json:"country"`
	TriageService   string `json:"triage_service"`
	ReportCost      *int   `json:"report_cost"`
	KnownIssues     *int   `json:"known_issues"`        // findings already reported, so a match is a duplicate (Bugcrowd)
	KnownUnique     *int   `json:"known_issues_unique"` // of them, the distinct ones
}

// apiTestingRules are the program's rules for test traffic.
type apiTestingRules struct {
	RateLimit     *float64 `json:"rate_limit_per_second"` // null: none given
	NoAutomation  bool     `json:"no_automation"`
	UserAgent     string   `json:"user_agent"` // to send; "" when not required
	Header        string   `json:"header"`     // "Name: value" to send; "" when not required
	PlatformEmail bool     `json:"platform_email"`
}

// apiProgramBrief is the program's brief besides its scope, Markdown.
type apiProgramBrief struct {
	Policy              string                   `json:"policy"`
	Introduction        string                   `json:"introduction"`
	RewardNotes         string                   `json:"reward_notes"` // the current campaign or notes on the bounty table
	Website             string                   `json:"website"`
	InScopeNotes        string                   `json:"in_scope_notes"`
	OutOfScopeNotes     string                   `json:"out_of_scope_notes"`
	SeverityNotes       string                   `json:"severity_notes"`
	FAQ                 string                   `json:"faq"`
	ScopeExclusions     []platform.Exclusion     `json:"scope_exclusions"`     // findings the program won't take, besides the platform's standard list
	StandardsExclusions []platform.Exclusion     `json:"standards_exclusions"` // platform standards it opted out of
	StandardsKnown      bool                     `json:"standards_known"`      // the platform reported standards compliance
	CredentialPool      *platform.CredentialPool `json:"credential_pool"`      // test credentials for the whole program, on request
}

type apiAnnouncement struct {
	ID          string    `json:"id"`
	Title       string    `json:"title"`
	Body        string    `json:"body"` // Markdown
	PublishedAt time.Time `json:"published_at"`
}

func factsJSON(p *store.ProgramDetail) apiProgramFacts {
	out := apiProgramFacts{TriageActive: p.TriageActive, OpenScope: p.OpenScope, Collaboration: p.Collaboration,
		BountySplitting: p.BountySplitting, OffersSwag: p.OffersSwag, Disclosure: p.Disclosure, SafeHarborLevel: p.SafeHarborLevel,
		Industry: p.Industry, Country: p.Country, TriageService: p.TriageService, ReportCost: p.ReportCost}
	if s := p.PublicStats; s != nil {
		out.KnownIssues, out.KnownUnique = s.KnownIssues, s.KnownIssuesUnique
	}
	return out
}

func testingJSON(t *platform.TestingRules) *apiTestingRules {
	if t == nil {
		return nil
	}
	out := &apiTestingRules{NoAutomation: t.NoAutomation, UserAgent: t.UserAgent, Header: t.Header, PlatformEmail: t.PlatformEmail}
	if t.RateLimit > 0 {
		out.RateLimit = ptrTo(t.RateLimit)
	}
	return out
}

func briefJSON(p *store.ProgramDetail) apiProgramBrief {
	out := apiProgramBrief{Policy: p.Policy, ScopeExclusions: []platform.Exclusion{}, StandardsExclusions: []platform.Exclusion{}}
	if p.PublicStats == nil {
		return out
	}
	pr := p.PublicStats.Profile
	out.Introduction, out.RewardNotes, out.Website = pr.Introduction, pr.Campaign, pr.Website
	out.InScopeNotes, out.OutOfScopeNotes, out.SeverityNotes, out.FAQ = pr.InScope, pr.OutScope, pr.Severity, pr.FAQ
	out.ScopeExclusions, out.StandardsExclusions = nonNil(pr.ScopeExclusions), nonNil(pr.StandardsExclusions)
	out.StandardsKnown, out.CredentialPool = pr.StandardsKnown, pr.CredentialPool
	return out
}

func announcementsJSON(s *store.ProgramStats) []apiAnnouncement {
	out := []apiAnnouncement{}
	if s == nil {
		return out
	}
	for _, a := range s.Announcements {
		out = append(out, apiAnnouncement{a.ID, a.Title, a.Body, a.PublishedAt})
	}
	return out
}

func numbersJSON(p store.ProgramListItem) apiProgramNumbers {
	return apiProgramNumbers{Currency: p.StatsCurrency, Paid90d: rounded(p.Paid90d), Paid90dEstimated: p.Paid90d != nil && p.Paid90dIsEstimate(),
		PaidTotal: rounded(p.PaidAll), PaidTotalEstimated: p.PaidAllEstimated, AvgBountyLow: rounded(p.AvgBountyLow),
		AvgBountyHigh: rounded(p.AvgPayout), RewardedReports: p.Rewarded, Received90d: p.Reports90d, CountedDays: p.CountedDays,
		Received90dEstimated: p.Reports90dIsEstimate(), Resolved90d: p.Resolved90d, Resolved90dEstimated: p.Resolved90dEstimated,
		Accepted90d: p.Accepted90d, Reports7d: p.Reports7d,
		ReportsThisMonth: p.ReportsMonth, TotalReports: p.TotalReports, AcceptedTotal: p.AcceptedAll, Hunters: p.Hunters, Hunters90d: p.Hunters90d,
		FirstResponseHours: p.FirstResponseHours, ValidationHours: p.ValidationHours}
}
