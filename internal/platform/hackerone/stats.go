package hackerone

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/mdpsec/huntdeck/internal/platform"
)

// Program statistics aren't in the Hacker API. They come from the GraphQL
// endpoint behind hackerone.com program pages, which is unofficial and may
// change. It is queried anonymously (the API token isn't sent there), which
// covers public programs, at most statsBatch programs per request: HackerOne
// rejects more than three aliases of one field.
const (
	defaultGraphQLURL = "https://hackerone.com/graphql"
	statsBatch        = 3
	// minSignal is the lowest possible Signal; a requirement at or below it
	// means anyone can submit.
	minSignal = -10
	// archivedPage is HackerOne's page size for archived scope; programs
	// with more get up to archivedPages more requests.
	archivedPage  = 100
	archivedPages = 20
)

// archivedFields lists a page of a team's archived scope entries.
const archivedFields = `pageInfo { hasNextPage endCursor } nodes { _id asset_type asset_identifier eligible_for_submission
eligible_for_bounty max_severity instruction created_at updated_at archived_at }`

const statsFields = `handle currency website offers_swag launched_at only_id_verified_hackers only_cleared_hackers resolved_report_count reports_received_last_90_days last_report_resolved_at
formatted_bounties_paid_last_90_days formatted_total_bounties_paid_amount
average_bounty_lower_amount average_bounty_upper_amount top_bounty_lower_amount top_bounty_upper_amount
response_efficiency_percentage thanks_items { total_count } signal_requirements_setting { target_signal }
most_recent_sla_snapshot { average_time_to_first_program_response average_time_to_report_triage
average_time_to_bounty_awarded average_time_to_report_resolved }
profile_metrics_snapshot { average_bounty_per_severity_low average_bounty_per_severity_medium
average_bounty_per_severity_high average_bounty_per_severity_critical report_count_per_severity_low
report_count_per_severity_medium report_count_per_severity_high report_count_per_severity_critical }
structured_scopes_with_credentials(all_credentials_revoked: false) { _id }
declarative_policy { introduction scope_exclusions { category details }
platform_standards_exclusions { platform_standard justification } }
bounty_table { description bounty_table_rows(first: 100) { nodes { use_range low low_minimum medium medium_minimum
high high_minimum critical critical_minimum structured_scope { asset_identifier } } } }
archived_scopes: structured_scopes(first: 100, archived: true) { ` + archivedFields + ` }`

// statsQuery builds a query for n programs with handles passed as variables
// $h0..$h(n-1) and results under aliases t0..t(n-1).
func statsQuery(n int) string {
	var vars, fields []string
	for i := range n {
		vars = append(vars, fmt.Sprintf("$h%d: String!", i))
		fields = append(fields, fmt.Sprintf("t%d: team(handle: $h%d) { %s }", i, i, statsFields))
	}
	return "query ProgramStats(" + strings.Join(vars, ", ") + ") { " + strings.Join(fields, " ") + " }"
}

type teamStatsJSON struct {
	Handle             string     `json:"handle"`
	Currency           string     `json:"currency"`
	Website            *string    `json:"website"`
	OffersSwag         *bool      `json:"offers_swag"`
	LaunchedAt         *string    `json:"launched_at"` // public launch; started_accepting_at can be months before, invite-only
	IDVerifiedOnly     *bool      `json:"only_id_verified_hackers"`
	ClearedOnly        *bool      `json:"only_cleared_hackers"`
	ResolvedReports    *int       `json:"resolved_report_count"`
	ReportsLast90d     *int       `json:"reports_received_last_90_days"`
	LastResolvedAt     *string    `json:"last_report_resolved_at"`
	PaidLast90d        flexAmount `json:"formatted_bounties_paid_last_90_days"`
	PaidTotal          flexAmount `json:"formatted_total_bounties_paid_amount"`
	AvgLow             flexAmount `json:"average_bounty_lower_amount"`
	AvgHigh            flexAmount `json:"average_bounty_upper_amount"`
	TopLow             flexAmount `json:"top_bounty_lower_amount"`
	TopHigh            flexAmount `json:"top_bounty_upper_amount"`
	ResponseEfficiency *int       `json:"response_efficiency_percentage"`
	Thanks             *struct {
		TotalCount *int `json:"total_count"`
	} `json:"thanks_items"`
	Signal *struct {
		Target *float64 `json:"target_signal"`
	} `json:"signal_requirements_setting"`
	CredentialScopes []struct {
		ID string `json:"_id"`
	} `json:"structured_scopes_with_credentials"`
	SLA *struct {
		FirstResponse *float64 `json:"average_time_to_first_program_response"`
		Triage        *float64 `json:"average_time_to_report_triage"`
		Bounty        *float64 `json:"average_time_to_bounty_awarded"`
		Resolved      *float64 `json:"average_time_to_report_resolved"`
	} `json:"most_recent_sla_snapshot"`
	Metrics *struct {
		AvgLow        flexAmount `json:"average_bounty_per_severity_low"`
		AvgMedium     flexAmount `json:"average_bounty_per_severity_medium"`
		AvgHigh       flexAmount `json:"average_bounty_per_severity_high"`
		AvgCritical   flexAmount `json:"average_bounty_per_severity_critical"`
		CountLow      *int       `json:"report_count_per_severity_low"`
		CountMedium   *int       `json:"report_count_per_severity_medium"`
		CountHigh     *int       `json:"report_count_per_severity_high"`
		CountCritical *int       `json:"report_count_per_severity_critical"`
	} `json:"profile_metrics_snapshot"`
	Policy *struct {
		Introduction    *string `json:"introduction"`
		ScopeExclusions []struct {
			Category string  `json:"category"`
			Details  *string `json:"details"`
		} `json:"scope_exclusions"`
		StandardsExclusions *[]struct {
			Standard      string  `json:"platform_standard"`
			Justification *string `json:"justification"`
		} `json:"platform_standards_exclusions"`
	} `json:"declarative_policy"`
	BountyTable *struct {
		Description *string `json:"description"`
		Rows        *struct {
			Nodes []bountyRowJSON `json:"nodes"`
		} `json:"bounty_table_rows"`
	} `json:"bounty_table"`
	Archived *archivedJSON `json:"archived_scopes"`
}

type archivedJSON struct {
	PageInfo struct {
		HasNextPage bool   `json:"hasNextPage"`
		EndCursor   string `json:"endCursor"`
	} `json:"pageInfo"`
	Nodes []struct {
		ID                    string  `json:"_id"`
		AssetType             string  `json:"asset_type"`
		AssetIdentifier       string  `json:"asset_identifier"`
		EligibleForSubmission bool    `json:"eligible_for_submission"`
		EligibleForBounty     bool    `json:"eligible_for_bounty"`
		MaxSeverity           *string `json:"max_severity"`
		Instruction           *string `json:"instruction"`
		CreatedAt             *string `json:"created_at"`
		UpdatedAt             *string `json:"updated_at"`
		ArchivedAt            *string `json:"archived_at"`
	} `json:"nodes"`
}

// archived converts a page of archived scope; entries without an ID or
// archive time are skipped.
func (a *archivedJSON) archived() []platform.ArchivedScope {
	if a == nil {
		return nil
	}
	var out []platform.ArchivedScope
	for _, n := range a.Nodes {
		at := parseTime(n.ArchivedAt)
		if n.ID == "" || at == nil {
			continue
		}
		var s scopeJSON
		s.ID = n.ID
		s.Attributes.AssetType, s.Attributes.AssetIdentifier = n.AssetType, n.AssetIdentifier
		s.Attributes.EligibleForSubmission, s.Attributes.EligibleForBounty = n.EligibleForSubmission, n.EligibleForBounty
		s.Attributes.MaxSeverity, s.Attributes.Instruction = n.MaxSeverity, n.Instruction
		s.Attributes.CreatedAt, s.Attributes.UpdatedAt = n.CreatedAt, n.UpdatedAt
		out = append(out, platform.ArchivedScope{Entry: convertScopes([]scopeJSON{s})[0], ArchivedAt: *at})
	}
	return out
}

type bountyRowJSON struct {
	UseRange        bool       `json:"use_range"`
	Low             flexAmount `json:"low"`
	LowMinimum      flexAmount `json:"low_minimum"`
	Medium          flexAmount `json:"medium"`
	MediumMinimum   flexAmount `json:"medium_minimum"`
	High            flexAmount `json:"high"`
	HighMinimum     flexAmount `json:"high_minimum"`
	Critical        flexAmount `json:"critical"`
	CriticalMinimum flexAmount `json:"critical_minimum"`
	StructuredScope *struct {
		AssetIdentifier string `json:"asset_identifier"`
	} `json:"structured_scope"`
}

// flexAmount is a money amount sent as a number, or as a string such as
// "$1,500" (several fields are named formatted_*).
type flexAmount struct{ v *float64 }

func (a *flexAmount) UnmarshalJSON(b []byte) error {
	s := strings.TrimSpace(string(b))
	if s == "null" {
		return nil
	}
	if strings.HasPrefix(s, `"`) {
		if err := json.Unmarshal(b, &s); err != nil {
			return err
		}
		s = strings.NewReplacer("$", "", ",", "", "€", "", "£", "").Replace(strings.TrimSpace(s))
		if s == "" {
			return nil
		}
	}
	f, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return fmt.Errorf("amount %s: %w", b, err)
	}
	a.v = &f
	return nil
}

func (a flexAmount) float() float64 {
	if a.v == nil {
		return 0
	}
	return *a.v
}

type graphQLError struct {
	Message string `json:"message"`
	Path    []any  `json:"path"`
}

// FetchProgramStats implements platform.StatsFetcher, one request and one
// save per statsBatch programs. It stops at the first failed request, since
// that usually means the endpoint changed or is down.
func (c *Client) FetchProgramStats(ctx context.Context, handles []string, save platform.StatsSaver) error {
	for start := 0; start < len(handles); start += statsBatch {
		batch := handles[start:min(start+statsBatch, len(handles))]
		out := make(map[string]platform.ProgramStats, len(batch))
		if err := c.fetchStatsBatch(ctx, batch, out); err != nil {
			return fmt.Errorf("hackerone: program stats for %s: %w", strings.Join(batch, ", "), err)
		}
		if err := save(batch, out); err != nil {
			return err
		}
	}
	return nil
}

func (c *Client) fetchStatsBatch(ctx context.Context, handles []string, out map[string]platform.ProgramStats) error {
	vars := map[string]any{}
	for i, h := range handles {
		vars[fmt.Sprintf("h%d", i)] = h
	}
	var data map[string]*teamStatsJSON
	if err := c.postGraphQL(ctx, statsQuery(len(handles)), vars, len(handles), &data); err != nil {
		return err
	}
	for i, h := range handles {
		t := data[fmt.Sprintf("t%d", i)]
		if t == nil {
			continue
		}
		s := convertStats(t)
		if t.Archived != nil && t.Archived.PageInfo.HasNextPage {
			more, err := c.moreArchived(ctx, h, t.Archived.PageInfo.EndCursor)
			if err != nil {
				return fmt.Errorf("archived scope of %s: %w", h, err)
			}
			s.Archived = append(s.Archived, more...)
		}
		out[h] = s
	}
	return nil
}

// moreArchived fetches the archived scope after cursor, up to archivedPages
// pages.
func (c *Client) moreArchived(ctx context.Context, handle, cursor string) ([]platform.ArchivedScope, error) {
	const q = `query ArchivedScope($h0: String!, $after: String) { t0: team(handle: $h0) {
archived_scopes: structured_scopes(first: 100, archived: true, after: $after) { ` + archivedFields + ` } } }`
	var out []platform.ArchivedScope
	for range archivedPages {
		var data map[string]*teamStatsJSON
		if err := c.postGraphQL(ctx, q, map[string]any{"h0": handle, "after": cursor}, 1, &data); err != nil {
			return out, err
		}
		t := data["t0"]
		if t == nil || t.Archived == nil {
			break
		}
		out = append(out, t.Archived.archived()...)
		if !t.Archived.PageInfo.HasNextPage || t.Archived.PageInfo.EndCursor == "" {
			break
		}
		cursor = t.Archived.PageInfo.EndCursor
	}
	return out, nil
}

// postGraphQL runs one stats query: with the website session where one is
// set up and live, which covers private programs too, else anonymously,
// which covers public ones. A login that fails leaves the query anonymous
// (Settings shows the failure).
func (c *Client) postGraphQL(ctx context.Context, query string, vars map[string]any, aliases int, into any) error {
	if c.loggedIn() && c.ensureSession(ctx, false) == nil {
		status, err := c.gql(ctx, true, query, vars, aliases, into)
		if status != http.StatusForbidden {
			return err
		}
		if c.ensureSession(ctx, true) == nil {
			_, err = c.gql(ctx, true, query, vars, aliases, into)
			return err
		}
	}
	_, err := c.gql(ctx, false, query, vars, aliases, into)
	return err
}

func isAlias(p any, n int) bool {
	s, ok := p.(string)
	if !ok || !strings.HasPrefix(s, "t") {
		return false
	}
	i, err := strconv.Atoi(s[1:])
	return err == nil && i >= 0 && i < n
}

func convertStats(t *teamStatsJSON) platform.ProgramStats {
	s := platform.ProgramStats{
		Currency:            strings.ToUpper(t.Currency),
		ResolvedReports:     t.ResolvedReports,
		ReportsLast90d:      t.ReportsLast90d,
		BountiesPaid:        t.PaidTotal.v,
		BountiesPaidLast90d: t.PaidLast90d.v,
		AvgBountyLow:        t.AvgLow.v,
		AvgBountyHigh:       t.AvgHigh.v,
		TopBountyLow:        t.TopLow.v,
		TopBountyHigh:       t.TopHigh.v,
		ResponseEfficiency:  t.ResponseEfficiency,
		LastResolvedAt:      parseTime(t.LastResolvedAt),
	}
	if t.Thanks != nil {
		s.HackersThanked = t.Thanks.TotalCount
	}
	if t.Signal != nil && t.Signal.Target != nil && *t.Signal.Target > minSignal {
		s.SignalRequirement = t.Signal.Target
	}
	s.Profile.Website = strings.TrimSpace(deref(t.Website))
	s.Requirements = &platform.Requirements{
		MinSignal:  s.SignalRequirement,
		IDVerified: deref(t.IDVerifiedOnly),
		Cleared:    deref(t.ClearedOnly),
	}
	if f := (platform.Facts{OffersSwag: t.OffersSwag, StartedAt: parseTime(t.LaunchedAt)}); f != (platform.Facts{}) {
		s.Facts = &f
	}
	for _, c := range t.CredentialScopes {
		if c.ID != "" {
			s.Profile.CredentialScopes = append(s.Profile.CredentialScopes, c.ID)
		}
	}
	if t.SLA != nil {
		s.FirstResponseHours, s.TriageHours = t.SLA.FirstResponse, t.SLA.Triage
		s.BountyHours, s.ResolutionHours = t.SLA.Bounty, t.SLA.Resolved
	}
	if m := t.Metrics; m != nil {
		s.BySeverity = platform.SeverityStats{
			Low:      platform.SeverityStat{AvgBounty: m.AvgLow.v, Reports: m.CountLow},
			Medium:   platform.SeverityStat{AvgBounty: m.AvgMedium.v, Reports: m.CountMedium},
			High:     platform.SeverityStat{AvgBounty: m.AvgHigh.v, Reports: m.CountHigh},
			Critical: platform.SeverityStat{AvgBounty: m.AvgCritical.v, Reports: m.CountCritical},
		}
	}
	if p := t.Policy; p != nil {
		s.Profile.Introduction = strings.TrimSpace(deref(p.Introduction))
		for _, e := range p.ScopeExclusions {
			s.Profile.ScopeExclusions = append(s.Profile.ScopeExclusions,
				platform.Exclusion{Name: humanize(e.Category), Details: strings.TrimSpace(deref(e.Details))})
		}
		if p.StandardsExclusions != nil {
			s.Profile.StandardsKnown = true
			for _, e := range *p.StandardsExclusions {
				s.Profile.StandardsExclusions = append(s.Profile.StandardsExclusions,
					platform.Exclusion{Name: humanize(e.Standard), Details: strings.TrimSpace(deref(e.Justification))})
			}
		}
	}
	if t.BountyTable != nil {
		s.Profile.Campaign = strings.TrimSpace(deref(t.BountyTable.Description))
	}
	s.Rewards = []platform.RewardRow{} // the response is the whole table: none means none
	if t.BountyTable != nil && t.BountyTable.Rows != nil {
		for _, r := range t.BountyTable.Rows.Nodes {
			row := platform.RewardRow{
				Low:      rewardRange(r.UseRange, r.LowMinimum, r.Low),
				Medium:   rewardRange(r.UseRange, r.MediumMinimum, r.Medium),
				High:     rewardRange(r.UseRange, r.HighMinimum, r.High),
				Critical: rewardRange(r.UseRange, r.CriticalMinimum, r.Critical),
			}
			if r.StructuredScope != nil {
				row.Scope = strings.TrimSpace(r.StructuredScope.AssetIdentifier)
			}
			s.Rewards = append(s.Rewards, row)
		}
	}
	s.Archived = t.Archived.archived()
	return s
}

func rewardRange(useRange bool, minimum, value flexAmount) platform.RewardRange {
	r := platform.RewardRange{Max: value.float()}
	if useRange && minimum.float() > 0 && minimum.float() < r.Max {
		r.Min = minimum.float()
	}
	return r
}

// humanize turns an enum such as "out_of_scope_bugs" into "Out of scope bugs".
func humanize(s string) string {
	s = strings.TrimSpace(strings.ReplaceAll(strings.ToLower(s), "_", " "))
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}
