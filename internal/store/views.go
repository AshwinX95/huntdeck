package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/mdpsec/huntdeck/internal/platform"
	"github.com/mdpsec/huntdeck/internal/scope"
)

// ProgramDetail is everything stored about one program.
type ProgramDetail struct {
	ProgramRef
	PlatformID   string
	OffersBounty bool
	Currency     string
	FastPayments bool
	SafeHarbor   bool
	TriageActive bool
	OpenScope    bool
	StartedAt    *time.Time
	JoinedAt     *time.Time
	// InvitePending is true while the user's invitation waits to be
	// accepted (platforms that list them: platform.InviteLister); InvitedAt
	// is when it was sent, where known.
	InvitePending bool
	InvitedAt     *time.Time
	// Collaboration, Industry, Country, TriageService and ReportCost are as
	// in platform.Program; nil or empty until a platform has said.
	Collaboration *bool
	Industry      string
	Country       string
	TriageService string
	ReportCost    *int
	// BountySplitting, OffersSwag, SafeHarborLevel and Disclosure are as in
	// platform.Program and platform.Facts; nil or empty until a platform
	// has said.
	BountySplitting *bool
	OffersSwag      *bool
	SafeHarborLevel string
	Disclosure      *bool
	FirstSeenAt     time.Time
	LastSeenAt      time.Time
	RemovedAt       *time.Time
	ScopeSyncedAt   *time.Time
	// ScopeCheckedAt is when the scope was last known to be current: fetched,
	// or reported unchanged by the platform.
	ScopeCheckedAt *time.Time
	// ScopeUnavailableAt is when the platform last refused to show the scope.
	ScopeUnavailableAt *time.Time
	Entries            []EntryView // live entries first, then removed
	Stats              []UserStatsSnapshot
	PublicStats        *ProgramStats // latest public stats, if the platform has them
	Hunt               *HuntDetail   // nil until scored
	Policy             string        // program rules, Markdown
	// CredentialAssets are the scope entries with test credentials from the
	// platform.
	CredentialAssets []string
	Requirements     ProgramRequirements
	// Testing is the program's rules for test traffic; nil until a platform
	// has said.
	Testing *platform.TestingRules
	Ignored bool
	Tags    []string
}

// EntryView is a scope entry with its derived targets.
type EntryView struct {
	ID                int64
	Key               string // the platform's ID for the entry, if it has one
	Credentials       bool   // the platform hands out test credentials for it
	InScope           bool
	AssetType         string
	PlatformAssetType string
	Identifier        string
	BountyEligible    bool
	MaxSeverity       string
	Instruction       string
	FirstSeenAt       time.Time
	RemovedAt         *time.Time
	// AddedAt is when the entry was added to the program, if known;
	// AddedByPlatform says the date is the platform's rather than when
	// HuntDeck saw it appear.
	AddedAt         *time.Time
	AddedByPlatform bool
	AddedBound      bool // added on or before AddedAt: the platform's history starts there
	CVSS            platform.CVSSRequirements
	Targets         []scope.Target
}

// Program returns one program's details, or ErrNotFound.
func (s *Store) Program(ctx context.Context, platformName, handle string) (*ProgramDetail, error) {
	var d ProgramDetail
	var started, joined, invited, removed, synced, checked, unavailable, hunt sql.NullString
	var firstSeen, lastSeen, rewards, tags string
	var testingKnown bool
	var testing platform.TestingRules
	var collab, cost, splitting, swag, disclosure sql.NullInt64
	err := s.db.R.QueryRowContext(ctx, `
		SELECT id, platform, handle, name, url, private, state, platform_id, offers_bounty, currency, fast_payments,
			safe_harbor, triage_active, open_scope, started_at, joined_at, first_seen_at, last_seen_at, removed_at, scope_synced_at,
			scope_checked_at, scope_unavailable_at, policy, COALESCE(rewards, '[]'), hunt_detail, ignored, `+tagsColumn+`,
			testing_known, COALESCE(test_rate_limit, 0), test_no_automation, test_user_agent, test_header, test_platform_email,
			collaboration, industry, country, triage_service, report_cost, bounty_splitting, offers_swag, safe_harbor_level,
			coordinated_disclosure, invite_pending, invited_at, `+requirementColumns+`
		FROM programs p WHERE platform = ? AND handle = ?`, platformName, handle).
		Scan(append([]any{&d.ID, &d.Platform, &d.Handle, &d.Name, &d.URL, &d.Private, &d.State, &d.PlatformID, &d.OffersBounty,
			&d.Currency, &d.FastPayments, &d.SafeHarbor, &d.TriageActive, &d.OpenScope, &started, &joined,
			&firstSeen, &lastSeen, &removed, &synced, &checked, &unavailable, &d.Policy, &rewards, &hunt, &d.Ignored, &tags,
			&testingKnown, &testing.RateLimit, &testing.NoAutomation, &testing.UserAgent, &testing.Header, &testing.PlatformEmail,
			&collab, &d.Industry, &d.Country, &d.TriageService, &cost, &splitting, &swag, &d.SafeHarborLevel, &disclosure, &d.InvitePending, &invited},
			d.Requirements.scanTargets()...)...)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	d.StartedAt, d.JoinedAt, d.InvitedAt = parseTSPtr(started), parseTSPtr(joined), parseTSPtr(invited)
	d.FirstSeenAt, d.LastSeenAt = parseTS(firstSeen), parseTS(lastSeen)
	d.RemovedAt, d.ScopeSyncedAt, d.ScopeUnavailableAt = parseTSPtr(removed), parseTSPtr(synced), parseTSPtr(unavailable)
	d.ScopeCheckedAt = parseTSPtr(checked)
	d.Tags = splitTags(tags)
	d.Collaboration, d.BountySplitting, d.OffersSwag, d.Disclosure = nullBool(collab), nullBool(splitting), nullBool(swag), nullBool(disclosure)
	d.ReportCost = intPtr(cost)
	if testingKnown {
		d.Testing = &testing
	}
	if d.Hunt, err = parseHunt(hunt); err != nil {
		return nil, err
	}

	rows, err := s.db.R.QueryContext(ctx, `
		SELECT e.id, e.entry_key, e.in_scope, e.asset_type, e.platform_asset_type, e.identifier, e.bounty_eligible, e.max_severity,
			e.instruction, e.first_seen_at, e.removed_at, e.added_at, e.platform_created_at IS NOT NULL, e.added_bound,
			e.cvss_confidentiality, e.cvss_integrity, e.cvss_availability, t.kind, t.value, t.host, t.apex
		FROM scope_entries e LEFT JOIN scope_targets t ON t.scope_entry_id = e.id
		WHERE e.program_id = ?
		ORDER BY e.removed_at IS NOT NULL, e.in_scope DESC, e.bounty_eligible DESC, lower(e.identifier), e.id, t.id`, d.ID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var e EntryView
		var first string
		var rem, added, kind, value, host, apex sql.NullString
		if err := rows.Scan(&e.ID, &e.Key, &e.InScope, &e.AssetType, &e.PlatformAssetType, &e.Identifier, &e.BountyEligible,
			&e.MaxSeverity, &e.Instruction, &first, &rem, &added, &e.AddedByPlatform, &e.AddedBound,
			&e.CVSS.Confidentiality, &e.CVSS.Integrity, &e.CVSS.Availability, &kind, &value, &host, &apex); err != nil {
			return nil, err
		}
		if n := len(d.Entries); n == 0 || d.Entries[n-1].ID != e.ID {
			e.FirstSeenAt, e.RemovedAt, e.AddedAt = parseTS(first), parseTSPtr(rem), parseTSPtr(added)
			d.Entries = append(d.Entries, e)
		}
		if kind.Valid {
			last := &d.Entries[len(d.Entries)-1]
			last.Targets = append(last.Targets, scope.Target{Kind: kind.String, Value: value.String, Host: host.String, Apex: apex.String})
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if d.Stats, err = s.UserStatsHistory(ctx, platformName, handle); err != nil {
		return nil, err
	}
	if d.PublicStats, err = s.LatestProgramStats(ctx, d.ID); err != nil || d.PublicStats == nil {
		return &d, err
	}
	// Platforms whose stats don't carry the bounty table have it from the
	// scope download.
	if len(d.PublicStats.Rewards) == 0 {
		if err := json.Unmarshal([]byte(rewards), &d.PublicStats.Rewards); err != nil {
			return nil, fmt.Errorf("rewards: %w", err)
		}
	}
	for _, id := range d.PublicStats.Profile.CredentialScopes {
		for i := range d.Entries {
			if d.Entries[i].Key == id {
				d.Entries[i].Credentials = true
				d.CredentialAssets = append(d.CredentialAssets, d.Entries[i].Identifier)
			}
		}
	}
	return &d, nil
}
