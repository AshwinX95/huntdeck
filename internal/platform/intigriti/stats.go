package intigriti

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/mdpsec/huntdeck/internal/platform"
)

// The researcher API has no program stats, but the public program pages on
// app.intigriti.com do, from the JSON the site itself loads (no token;
// verified live 2026-10-28):
//
//	GET /api/core/public/programs                     every listed program
//	GET /api/core/public/programs/{company}/{handle}  one program's page
//
// The listing gives each program's id, company and handle, and its
// confidentialityLevel; only public ones (4) have a page without logging in.
// A page carries, all time:
//
//	{"programId": "…", "totalPayout": {"value": 84052.0, "currency": "USD"},
//	 "averagePayout": {"value": 850.0, "currency": "USD"},
//	 "submissionCount": 742, "acceptedSubmissionCount": 96}
//
// The payouts are null for programs without bounties or that keep them
// private, and the counts are 0 where a program keeps those private (Yahoo
// shows 0 reports submitted next to 682 accepted), so a 0 count means not
// shown. Pages are large (about
// 250 KB, with the program's whole history), so they are read once a day:
// one request for the listing plus one per public program (172 in 2026-10).
const statsBatch = 25

type publicListedJSON struct {
	ProgramID            string `json:"programId"`
	CompanyHandle        string `json:"companyHandle"`
	Handle               string `json:"handle"`
	ConfidentialityLevel int    `json:"confidentialityLevel"`
}

type publicPageJSON struct {
	ProgramID     string `json:"programId"`
	TotalPayout   *money `json:"totalPayout"`
	AveragePayout *money `json:"averagePayout"`
	Submissions   *int   `json:"submissionCount"`
	Accepted      *int   `json:"acceptedSubmissionCount"`
	// HasUpdates says whether the program has posted any updates to
	// researchers (false on every program without one, checked 2026-10-28).
	HasUpdates *bool `json:"hasUpdates"`
	// The bounty table: every version the page keeps (the logged-in page),
	// or just the current one (the public preview).
	BountyTables []bountyTableJSON `json:"bountyTables"`
	BountyTable  *bountyTableJSON  `json:"bountyTable"`
	// The brief's sections, every version.
	InScopes            []briefJSON `json:"inScopes"`
	OutOfScopes         []briefJSON `json:"outOfScopes"`
	SeverityAssessments []briefJSON `json:"severityAssessments"`
	FAQs                []briefJSON `json:"faqs"`
	// Credentials is set on programs that hand out test accounts (logged
	// in only); the accounts themselves come only on request.
	Credentials *struct {
		Available bool `json:"credentialsAvailable"`
		Max       int  `json:"maxCredentials"`
	} `json:"credentials"`
	// LastActivity is the program's last 10 events, as on its page.
	LastActivity []struct {
		Type      int   `json:"type"`
		Timestamp int64 `json:"timestamp"`
	} `json:"lastActivity"`
}

// briefJSON is one version of one of the brief's sections.
type briefJSON struct {
	CreatedAt int64 `json:"createdAt"`
	Content   struct {
		Content string `json:"content"` // Markdown
	} `json:"content"`
}

// Event types in a page's lastActivity, from the web app's own enum
// (2026-10): 1–8 are brief changes (description, in scope, out of scope,
// assets, bounties, severity, FAQ, rules), 9 a report submitted, 10 one
// accepted, 11 one closed, 12–18 the program launched, suspended, changing
// confidentiality or unsuspended, and 19 an update published.
const eventAccepted = 10

// latestBrief is the newest version's text, or "".
func latestBrief(versions []briefJSON) string {
	var best *briefJSON
	for i := range versions {
		if best == nil || versions[i].CreatedAt > best.CreatedAt {
			best = &versions[i]
		}
	}
	if best == nil {
		return ""
	}
	return strings.TrimSpace(best.Content.Content)
}

// bountyTableJSON is one version of a program's bounty table: a row per
// tier, each with bounty ranges by CVSS score.
type bountyTableJSON struct {
	CreatedAt int64 `json:"createdAt"`
	Content   struct {
		Currency string `json:"currency"`
		Rows     []struct {
			TierID int `json:"bountyTierId"`
			Ranges []struct {
				MinScore  float64 `json:"minScore"`
				MaxScore  float64 `json:"maxScore"`
				MinBounty *money  `json:"minBounty"`
				MaxBounty *money  `json:"maxBounty"`
			} `json:"bountyRanges"`
		} `json:"bountyRows"`
	} `json:"content"`
}

// FetchProgramStats implements platform.StatsFetcher from the public pages.
// With a web login, the browser sync (WebSync) reads the fuller logged-in
// stats instead.
func (c *Client) FetchProgramStats(ctx context.Context, handles []string, save platform.StatsSaver) error {
	return c.fetchPublicStats(ctx, handles, save)
}

// fetchPublicStats reads the public program pages. Programs without one
// (private, apply-to-join, or open only to logged-in researchers) and ones
// whose page can't be read are left out.
func (c *Client) fetchPublicStats(ctx context.Context, handles []string, save platform.StatsSaver) error {
	var listed []publicListedJSON
	if err := c.getPublic(ctx, c.public.String()+"/programs", &listed); err != nil {
		return fmt.Errorf("intigriti: public program list: %w", err)
	}
	pages := make(map[string]string, len(listed)) // program id → page path
	for _, p := range listed {
		if p.ConfidentialityLevel == levelPublic && p.CompanyHandle != "" && p.Handle != "" {
			pages[p.ProgramID] = "/programs/" + url.PathEscape(p.CompanyHandle) + "/" + url.PathEscape(p.Handle)
		}
	}
	for start := 0; start < len(handles); start += statsBatch {
		batch := handles[start:min(start+statsBatch, len(handles))]
		out := make(map[string]platform.ProgramStats, len(batch))
		for _, h := range batch {
			path, ok := pages[h]
			if !ok {
				continue
			}
			var pj publicPageJSON
			err := c.getPublic(ctx, c.public.String()+path, &pj)
			switch {
			case ctx.Err() != nil:
				return fmt.Errorf("intigriti: program stats for %s: %w", h, ctx.Err())
			case err != nil || pj.ProgramID != h:
				continue // e.g. a program that just went away or moved
			}
			if s, ok := convertStats(pj); ok {
				out[h] = s
			}
		}
		if err := save(batch, out); err != nil {
			return err
		}
	}
	return nil
}

// convertStats maps a public page's numbers; ok is false if it has none.
func convertStats(pj publicPageJSON) (platform.ProgramStats, bool) {
	shown := func(n *int) *int {
		if n == nil || *n == 0 {
			return nil
		}
		return n
	}
	s := platform.ProgramStats{TotalReports: shown(pj.Submissions), AcceptedReports: shown(pj.Accepted)}
	if m := pj.TotalPayout; m != nil {
		v := m.Value
		s.Currency, s.BountiesPaid = m.Currency, &v
	}
	if m := pj.AveragePayout; m != nil && (s.Currency == "" || m.Currency == s.Currency) {
		v := m.Value
		s.Currency, s.AvgBountyLow, s.AvgBountyHigh = m.Currency, &v, &v
	}
	if t := currentTable(pj); t != nil && (s.Currency == "" || t.Content.Currency == "" || t.Content.Currency == s.Currency) {
		s.Rewards = rewardRows(*t)
		if s.Currency == "" {
			s.Currency = t.Content.Currency
		}
	}
	s.Profile = platform.ProgramProfile{InScope: latestBrief(pj.InScopes), OutScope: latestBrief(pj.OutOfScopes),
		Severity: latestBrief(pj.SeverityAssessments), FAQ: latestBrief(pj.FAQs)}
	if c := pj.Credentials; c != nil {
		s.Profile.CredentialPool = &platform.CredentialPool{Available: c.Available, Max: c.Max}
	}
	for _, a := range pj.LastActivity {
		if a.Type == eventAccepted && a.Timestamp > 0 {
			if at := time.Unix(a.Timestamp, 0).UTC(); s.LastAcceptedAt == nil || at.After(*s.LastAcceptedAt) {
				s.LastAcceptedAt = &at
			}
		}
	}
	return s, s.TotalReports != nil || s.AcceptedReports != nil || s.BountiesPaid != nil || s.AvgBountyHigh != nil || s.Rewards != nil
}

// currentTable is the newest bounty table on a page, or nil.
func currentTable(pj publicPageJSON) *bountyTableJSON {
	t := pj.BountyTable
	for i := range pj.BountyTables {
		if t == nil || pj.BountyTables[i].CreatedAt > t.CreatedAt {
			t = &pj.BountyTables[i]
		}
	}
	return t
}

// rewardRows turns a bounty table into one row per tier, its CVSS bands
// grouped into the usual severities (low under 4, medium under 7, high
// under 9, critical from 9; Intigriti's "exceptional" band counts as
// critical). Nil if no row pays anything.
func rewardRows(t bountyTableJSON) []platform.RewardRow {
	var out []platform.RewardRow
	for _, row := range t.Content.Rows {
		r := platform.RewardRow{Scope: tierName(row.TierID)}
		paid := false
		for _, b := range row.Ranges {
			if b.MaxBounty == nil || b.MaxBounty.Value <= 0 || b.MaxScore <= 0 {
				continue
			}
			sev := &r.Critical
			switch {
			case b.MinScore < 4:
				sev = &r.Low
			case b.MinScore < 7:
				sev = &r.Medium
			case b.MinScore < 9:
				sev = &r.High
			}
			lo := 0.0
			if b.MinBounty != nil {
				lo = b.MinBounty.Value
			}
			if sev.Max == 0 || lo < sev.Min {
				sev.Min = lo
			}
			sev.Max = max(sev.Max, b.MaxBounty.Value)
			paid = true
		}
		if paid {
			out = append(out, r)
		}
	}
	slices.SortStableFunc(out, func(a, b platform.RewardRow) int { return strings.Compare(a.Scope, b.Scope) })
	return out
}

// tierName names a bounty tier the way scope entries' MaxSeverity does.
func tierName(id int) string {
	switch id {
	case 4:
		return "Tier 1"
	case 3:
		return "Tier 2"
	case 2:
		return "Tier 3"
	case 1:
		return "No Bounty"
	}
	return fmt.Sprintf("Tier %d", id)
}

// getPublic reads a public page's JSON. It sends no token: these pages are
// open to anyone, and the token is only for the researcher API.
func (c *Client) getPublic(ctx context.Context, u string, into any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	res, err := c.http.Do(ctx, req)
	if err != nil {
		return err
	}
	if res.StatusCode != http.StatusOK {
		return fmt.Errorf("HTTP %d", res.StatusCode)
	}
	if !strings.Contains(res.Header.Get("Content-Type"), "json") {
		return errors.New("not JSON (a challenge page?)")
	}
	if err := json.Unmarshal(res.Body, into); err != nil {
		return fmt.Errorf("decode response: %w", err)
	}
	return nil
}
