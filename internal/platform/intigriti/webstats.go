package intigriti

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/mdpsec/huntdeck/internal/platform"
)

// Logged in, program stats come from the web app's own calls (verified live
// 2026-10-28), instead of the public pages:
//
//	GET  /api/core/researcher/programs                    every program, one request
//	POST /api/core/researcher/programs/metrics            ["id", …] → per program:
//	     averagePaid, acceptedSubmissionAmount, totalContributorAmount
//	     (researchers who contributed), firstResponseAt (seconds),
//	     lastSubmissionAt (Unix seconds)
//	GET  /api/core/researcher/programs/{company}/{handle} the program page:
//	     submissionCount, acceptedSubmissionCount, averagePayout, totalPayout
//	GET  /api/core/researcher/statistics/programs/{id}/ninetydays
//	     {"firstResponded", "handledByTriage", "handledByCompany"}: average
//	     seconds to first response, triage and the company's decision over the
//	     last 90 days; 0 where there is none
//	GET  /api/core/researcher/programs/{id}/update
//	     the program's updates to researchers: [{id, title, description
//	     (Markdown), publishedAt}]
//	GET  /api/core/researcher/program/{id}/leaderboard/last90days and
//	     …/allTime: its leaderboards, read in sums (leaderboard.go)
//
// The list also gives each program's requirements (identityCheckedRequired,
// eeaRequired, twoFactorRequired), and the page its bounty table. The page
// and the 90-day times are one request each per program you can open
// (programs you must apply to answer 403 until you're accepted), and the
// updates one more for programs whose page says they have some
// (hasUpdates; 98 of the 165 or so readable pages in 2026-10), and the two
// leaderboards two more (2026-11). So a day's stats are 2 requests plus 4
// or 5 per program: about 820.

type webProgramJSON struct {
	ProgramID            string `json:"programId"`
	CompanyHandle        string `json:"companyHandle"`
	Handle               string `json:"handle"`
	ConfidentialityLevel int    `json:"confidentialityLevel"`
	LaunchedAt           *int64 `json:"launchedAt"`
	IdentityChecked      bool   `json:"identityCheckedRequired"`
	EEA                  bool   `json:"eeaRequired"`
	TwoFactor            bool   `json:"twoFactorRequired"`
	Terms                bool   `json:"tacRequired"`
	Collaboration        *bool  `json:"allowCollaboration"`
	Industry             string `json:"industry"`
}

type updateJSON struct {
	ID          string `json:"id"`
	Title       string `json:"title"`
	Description string `json:"description"`
	PublishedAt int64  `json:"publishedAt"`
}

type metricsJSON struct {
	ProgramID    string `json:"programId"`
	AveragePaid  *money `json:"averagePaid"`
	Accepted     *int   `json:"acceptedSubmissionAmount"`
	Contributors *int   `json:"totalContributorAmount"`
	LastReportAt *int64 `json:"lastSubmissionAt"`
}

type ninetyDaysJSON struct {
	FirstResponded   float64 `json:"firstResponded"`
	HandledByTriage  float64 `json:"handledByTriage"`
	HandledByCompany float64 `json:"handledByCompany"`
}

// webMeta is what the logged-in program list adds to the API's listing.
type webMeta struct {
	page       string // "/programs/{company}/{handle}"
	launchedAt *time.Time
	idChecked  bool
	eea        bool
	twoFactor  bool
	terms      bool
	collab     *bool
	industry   string
	level      int
	at         time.Time
}

// requirements are the program's requirements as the list gives them.
func (m webMeta) requirements() *platform.Requirements {
	return &platform.Requirements{Application: m.level == levelApplication, IDVerified: m.idChecked, EEA: m.eea, TwoFactor: m.twoFactor,
		Terms: m.terms}
}

// metaMaxAge is how long the logged-in list's details enrich listings.
const metaMaxAge = 48 * time.Hour

// fetchWebStats implements FetchProgramStats with the web app.
func (c *Client) fetchWebStats(ctx context.Context, handles []string, save platform.StatsSaver) error {
	var list []webProgramJSON
	if err := c.webJSON(ctx, "GET", "/programs", nil, &list); err != nil {
		return fmt.Errorf("intigriti: program list: %w", err)
	}
	now := time.Now()
	meta := make(map[string]webMeta, len(list))
	for _, p := range list {
		if p.ProgramID == "" || p.CompanyHandle == "" || p.Handle == "" {
			continue
		}
		m := webMeta{page: "/programs/" + url.PathEscape(p.CompanyHandle) + "/" + url.PathEscape(p.Handle), idChecked: p.IdentityChecked,
			eea: p.EEA, twoFactor: p.TwoFactor, terms: p.Terms, collab: p.Collaboration, industry: industryName(p.Industry),
			level: p.ConfidentialityLevel, at: now}
		if p.LaunchedAt != nil && *p.LaunchedAt > 0 {
			t := time.Unix(*p.LaunchedAt, 0).UTC()
			m.launchedAt = &t
		}
		meta[p.ProgramID] = m
	}
	c.metaMu.Lock()
	c.meta = meta
	c.metaMu.Unlock()

	var metrics []metricsJSON
	if err := c.webJSON(ctx, "POST", "/programs/metrics", handles, &metrics); err != nil {
		return fmt.Errorf("intigriti: program metrics: %w", err)
	}
	byID := make(map[string]metricsJSON, len(metrics))
	for _, m := range metrics {
		byID[m.ProgramID] = m
	}

	for start := 0; start < len(handles); start += statsBatch {
		batch := handles[start:min(start+statsBatch, len(handles))]
		out := make(map[string]platform.ProgramStats, len(batch))
		for _, h := range batch {
			m, ok := meta[h]
			if !ok {
				continue
			}
			var page publicPageJSON
			err := c.webJSON(ctx, "GET", m.page, nil, &page)
			switch {
			case ctx.Err() != nil:
				return fmt.Errorf("intigriti: program stats for %s: %w", h, ctx.Err())
			case errors.Is(err, platform.ErrScopeUnavailable): // must apply first: metrics only
			case err != nil:
				continue
			}
			var times ninetyDaysJSON
			var updates []updateJSON
			updatesRead := false
			if page.ProgramID == h {
				if err := c.webJSON(ctx, "GET", "/statistics/programs/"+url.PathEscape(h)+"/ninetydays", nil, &times); err != nil && ctx.Err() != nil {
					return fmt.Errorf("intigriti: response times for %s: %w", h, ctx.Err())
				}
				if page.HasUpdates != nil && !*page.HasUpdates {
					updatesRead = true // none to read
				} else {
					err := c.webJSON(ctx, "GET", "/programs/"+url.PathEscape(h)+"/update", nil, &updates)
					if err != nil && ctx.Err() != nil {
						return fmt.Errorf("intigriti: updates for %s: %w", h, ctx.Err())
					}
					updatesRead = err == nil
				}
			} else {
				page = publicPageJSON{}
			}
			s, ok := webStats(page, byID[h], times)
			if page.ProgramID == h {
				if err := c.leaderboards(ctx, h, &s); err != nil {
					return fmt.Errorf("intigriti: leaderboards for %s: %w", h, err)
				}
				ok = ok || s.Hunters90d != nil
			}
			if updatesRead {
				s.Announcements, ok = announcements(updates), true
			}
			// The list's requirements are saved with the stats too, so they
			// don't depend on a listing running after this in the same
			// process. (Programs without stats get them from the listing.)
			s.Requirements = m.requirements()
			if ok {
				out[h] = s
			}
		}
		if err := save(batch, out); err != nil {
			return err
		}
	}
	return nil
}

// webStats combines a program's page, metrics and 90-day times; ok is false
// if they say nothing.
func webStats(page publicPageJSON, m metricsJSON, t ninetyDaysJSON) (platform.ProgramStats, bool) {
	s, _ := convertStats(page)
	if s.AvgBountyHigh == nil && m.AveragePaid != nil && (s.Currency == "" || s.Currency == m.AveragePaid.Currency) {
		v := m.AveragePaid.Value
		s.Currency, s.AvgBountyLow, s.AvgBountyHigh = m.AveragePaid.Currency, &v, &v
	}
	if s.AcceptedReports == nil && m.Accepted != nil && *m.Accepted > 0 {
		s.AcceptedReports = m.Accepted
	}
	if m.Contributors != nil && *m.Contributors > 0 {
		s.Hunters = m.Contributors
	}
	if m.LastReportAt != nil && *m.LastReportAt > 0 {
		at := time.Unix(*m.LastReportAt, 0).UTC()
		s.LastSubmissionAt = &at
	}
	hours := func(sec float64) *float64 {
		if sec <= 0 {
			return nil
		}
		h := sec / 3600
		return &h
	}
	s.FirstResponseHours, s.TriageHours, s.ValidationHours = hours(t.FirstResponded), hours(t.HandledByTriage), hours(t.HandledByCompany)
	ok := s.TotalReports != nil || s.AcceptedReports != nil || s.BountiesPaid != nil || s.AvgBountyHigh != nil ||
		s.Hunters != nil || s.LastSubmissionAt != nil || s.FirstResponseHours != nil || s.LastAcceptedAt != nil ||
		s.Profile.OutScope != "" || s.Profile.InScope != "" || s.Profile.CredentialPool != nil
	return s, ok
}

// announcements turns a program's updates into announcements, newest
// first, skipping any without an ID or date.
func announcements(updates []updateJSON) []platform.Announcement {
	out := []platform.Announcement{}
	for _, u := range updates {
		if u.ID == "" || u.PublishedAt <= 0 {
			continue
		}
		out = append(out, platform.Announcement{ID: u.ID, Title: strings.TrimSpace(u.Title),
			Body: strings.TrimSpace(u.Description), PublishedAt: time.Unix(u.PublishedAt, 0).UTC()})
	}
	slices.SortStableFunc(out, func(a, b platform.Announcement) int { return b.PublishedAt.Compare(a.PublishedAt) })
	return out
}

// enrich adds the logged-in list's launch date and requirements to a listed
// program. Without a fresh list it leaves the requirements unset,
// so the stored ones stand rather than losing the ID check.
func (c *Client) enrich(p *platform.Program, level int) {
	if !c.loggedIn() {
		return
	}
	c.metaMu.Lock()
	m, ok := c.meta[p.Handle]
	c.metaMu.Unlock()
	if !ok || time.Since(m.at) > metaMaxAge {
		p.Requirements = nil
		return
	}
	p.StartedAt = m.launchedAt
	p.Collaboration, p.Industry = m.collab, m.industry
	p.Requirements = m.requirements()
	p.Requirements.Application = level == levelApplication
}

// industryName turns the list's industry key ("manufacturing_consumer")
// into words ("Manufacturing consumer").
func industryName(key string) string {
	s := strings.TrimSpace(strings.ReplaceAll(key, "_", " "))
	if s == "" {
		return ""
	}
	return strings.ToUpper(s[:1]) + s[1:]
}
