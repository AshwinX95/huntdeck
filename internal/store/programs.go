package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/mdpsec/huntdeck/internal/platform"
)

// ProgramRow is a stored program.
type ProgramRow struct {
	ID            int64
	Platform      string
	Handle        string
	Name          string
	URL           string
	Private       bool
	State         string
	OffersBounty  bool
	RemovedAt     *time.Time
	ScopeHash     string
	ScopeSyncedAt *time.Time
	// ScopeUnavailableAt is when the platform last refused to show the scope.
	ScopeUnavailableAt *time.Time
	Policy             string // "" when no platform response has carried one
}

// HadCompleteListing reports whether any earlier poll of platform fetched
// the full program listing. Until one has, every program seen is part of the
// initial import rather than "new".
func (t *Tx) HadCompleteListing(ctx context.Context, platformName string) (bool, error) {
	var n int
	err := t.tx.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM poll_runs WHERE platform = ? AND listing_complete = 1 AND status IN ('ok', 'partial')`,
		platformName).Scan(&n)
	return n > 0, err
}

// Program returns the stored program, or nil if there is none.
func (t *Tx) Program(ctx context.Context, platformName, handle string) (*ProgramRow, error) {
	var p ProgramRow
	var removed, synced, unavailable sql.NullString
	err := t.tx.QueryRowContext(ctx, `
		SELECT id, platform, handle, name, url, private, state, offers_bounty, removed_at, scope_hash, scope_synced_at, scope_unavailable_at,
			policy
		FROM programs WHERE platform = ? AND handle = ?`, platformName, handle).
		Scan(&p.ID, &p.Platform, &p.Handle, &p.Name, &p.URL, &p.Private, &p.State, &p.OffersBounty, &removed, &p.ScopeHash, &synced, &unavailable,
			&p.Policy)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	p.RemovedAt = parseTSPtr(removed)
	p.ScopeSyncedAt = parseTSPtr(synced)
	p.ScopeUnavailableAt = parseTSPtr(unavailable)
	return &p, nil
}

// UpsertProgram inserts or updates a program from the platform, marking it
// seen now and clearing any removal. It returns the program's ID. An empty
// policy keeps the stored one: listings that don't carry it say nothing.
// The listing's launch date only fills a missing one: a brief's or the
// stats' (SetFacts) is the better date and stands.
func (t *Tx) UpsertProgram(ctx context.Context, platformName string, p platform.Program) (int64, error) {
	raw := string(p.Raw)
	if raw == "" {
		raw = "{}"
	}
	var id int64
	err := t.tx.QueryRowContext(ctx, `
		INSERT INTO programs (platform, handle, platform_id, name, url, private, state, offers_bounty, currency, bounty_min, bounty_max,
			fast_payments, safe_harbor, triage_active, open_scope, started_at, joined_at, policy, raw, first_seen_at, last_seen_at,
			collaboration, industry, country, triage_service, report_cost, bounty_splitting, offers_swag)
		VALUES (`+placeholders(28)+`)
		ON CONFLICT (platform, handle) DO UPDATE SET
			platform_id = excluded.platform_id, name = excluded.name, url = excluded.url, private = excluded.private,
			state = excluded.state, offers_bounty = CASE WHEN ? THEN programs.offers_bounty ELSE excluded.offers_bounty END, currency = excluded.currency,
			bounty_min = excluded.bounty_min, bounty_max = excluded.bounty_max, fast_payments = excluded.fast_payments, safe_harbor = excluded.safe_harbor,
			triage_active = excluded.triage_active, open_scope = excluded.open_scope, started_at = COALESCE(programs.started_at, excluded.started_at),
			joined_at = COALESCE(excluded.joined_at, joined_at), policy = COALESCE(NULLIF(excluded.policy, ''), policy), raw = excluded.raw,
			last_seen_at = excluded.last_seen_at,
			collaboration = COALESCE(excluded.collaboration, collaboration), industry = COALESCE(NULLIF(excluded.industry, ''), industry),
			country = COALESCE(NULLIF(excluded.country, ''), country),
			triage_service = COALESCE(NULLIF(excluded.triage_service, ''), triage_service),
			report_cost = COALESCE(excluded.report_cost, report_cost),
			bounty_splitting = COALESCE(excluded.bounty_splitting, bounty_splitting),
			offers_swag = COALESCE(excluded.offers_swag, offers_swag),
			removed_at = NULL
		RETURNING id`,
		platformName, p.Handle, p.PlatformID, p.Name, p.URL, b2i(p.Private), p.State, b2i(p.OffersBounty), p.Currency, p.BountyMin, p.BountyMax,
		b2i(p.FastPayments), b2i(p.SafeHarbor), b2i(p.TriageActive), b2i(p.OpenScope), tsPtr(p.StartedAt), tsPtr(p.JoinedAt),
		p.Policy, raw, t.now, t.now, boolPtr(p.Collaboration), p.Industry, p.Country, p.TriageService, p.ReportCost,
		boolPtr(p.BountySplitting), boolPtr(p.OffersSwag), p.BountyUnknown).Scan(&id)
	return id, err
}

// SetFacts records the program facts a brief or the stats gave; what they
// don't say keeps the stored value.
func (t *Tx) SetFacts(ctx context.Context, programID int64, f platform.Facts) error {
	_, err := t.tx.ExecContext(ctx, `
		UPDATE programs SET collaboration = COALESCE(?, collaboration),
			safe_harbor_level = COALESCE(NULLIF(?, ''), safe_harbor_level),
			coordinated_disclosure = COALESCE(?, coordinated_disclosure), offers_swag = COALESCE(?, offers_swag),
			started_at = COALESCE(?, started_at), offers_bounty = COALESCE(?, offers_bounty)
		WHERE id = ?`,
		boolPtr(f.Collaboration), f.SafeHarbor, boolPtr(f.Disclosure), boolPtr(f.OffersSwag), tsPtr(f.StartedAt), boolPtr(f.OffersBounty), programID)
	return err
}

// SetEntryCVSS records the CVSS requirements of the program's scope entries
// by entry key, writing only those that differ.
func (t *Tx) SetEntryCVSS(ctx context.Context, programID int64, byKey map[string]platform.CVSSRequirements) error {
	for key, c := range byKey {
		if _, err := t.tx.ExecContext(ctx, `
			UPDATE scope_entries SET cvss_confidentiality = ?1, cvss_integrity = ?2, cvss_availability = ?3
			WHERE program_id = ?4 AND entry_key = ?5
				AND (cvss_confidentiality, cvss_integrity, cvss_availability) IS NOT (?1, ?2, ?3)`,
			c.Confidentiality, c.Integrity, c.Availability, programID, key); err != nil {
			return err
		}
	}
	return nil
}

// LivePrograms returns handle → ID for programs of platform not marked removed.
func (t *Tx) LivePrograms(ctx context.Context, platformName string) (map[string]int64, error) {
	rows, err := t.tx.QueryContext(ctx, `SELECT handle, id FROM programs WHERE platform = ? AND removed_at IS NULL`, platformName)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := map[string]int64{}
	for rows.Next() {
		var h string
		var id int64
		if err := rows.Scan(&h, &id); err != nil {
			return nil, err
		}
		out[h] = id
	}
	return out, rows.Err()
}

// MarkProgramRemoved records that a program disappeared from the listing.
func (t *Tx) MarkProgramRemoved(ctx context.Context, id int64) error {
	_, err := t.tx.ExecContext(ctx, `UPDATE programs SET removed_at = ? WHERE id = ?`, t.now, id)
	return err
}

// SetScopeSynced records a successful scope fetch and its hash.
func (t *Tx) SetScopeSynced(ctx context.Context, programID int64, hash string) error {
	_, err := t.tx.ExecContext(ctx, `
		UPDATE programs SET scope_hash = ?1, scope_synced_at = ?2, scope_checked_at = ?2, scope_unavailable_at = NULL WHERE id = ?3`,
		hash, t.now, programID)
	return err
}

// MarkScopeChecked records that the platform says a program's scope hasn't
// changed since it was last fetched, so the stored scope is current.
func (t *Tx) MarkScopeChecked(ctx context.Context, programID int64) error {
	_, err := t.tx.ExecContext(ctx, `UPDATE programs SET scope_checked_at = ? WHERE id = ?`, t.now, programID)
	return err
}

// MarkScopeUnavailable records that the platform refused to show a program's
// scope (for example, terms not accepted). Quick polls skip such programs
// until a full poll finds the scope available again.
func (t *Tx) MarkScopeUnavailable(ctx context.Context, programID int64) error {
	_, err := t.tx.ExecContext(ctx, `UPDATE programs SET scope_unavailable_at = ? WHERE id = ?`, t.now, programID)
	return err
}

// RefreshScopeCounts recomputes a program's cached scope counts after its
// scope entries changed.
func (t *Tx) RefreshScopeCounts(ctx context.Context, programID int64) error {
	_, err := t.tx.ExecContext(ctx, `
		UPDATE programs SET
			n_in_scope = (SELECT COUNT(*) FROM scope_entries e WHERE e.program_id = ?1 AND e.removed_at IS NULL AND e.in_scope = 1),
			n_out_scope = (SELECT COUNT(*) FROM scope_entries e WHERE e.program_id = ?1 AND e.removed_at IS NULL AND e.in_scope = 0),
			(n_wildcards, n_domains, n_urls, n_ips, n_cidrs) = (
				SELECT COUNT(DISTINCT CASE WHEN t.kind = 'wildcard' THEN t.value END),
					COUNT(DISTINCT CASE WHEN t.kind = 'domain' THEN t.value END),
					COUNT(DISTINCT CASE WHEN t.kind = 'url' THEN t.value END),
					COUNT(DISTINCT CASE WHEN t.kind = 'ip' THEN t.value END),
					COUNT(DISTINCT CASE WHEN t.kind = 'cidr' THEN t.value END)
				FROM scope_targets t JOIN scope_entries e ON e.id = t.scope_entry_id
				WHERE e.program_id = ?1 AND e.removed_at IS NULL AND e.in_scope = 1),
			scope_changed_at = `+scopeChangedAt+`
		WHERE id = ?1`, programID)
	return err
}

// scopeChangedAt is when program ?1's scope last changed: the latest scope
// change HuntDeck recorded, else the latest date its scope history gives
// (an entry added on a known date, or removed). Dates history only bounds
// (added_bound) don't count. Timestamps share one fixed format, so the
// latest string is the latest time.
const scopeChangedAt = `(SELECT MAX(at) FROM (
		SELECT MAX(c.occurred_at) AS at FROM changes c
			WHERE c.program_id = ?1 AND c.kind IN ('scope_added', 'scope_removed', 'scope_updated')
		UNION ALL SELECT MAX(e.added_at) FROM scope_entries e WHERE e.program_id = ?1 AND e.added_bound = 0
		UNION ALL SELECT MAX(e.removed_at) FROM scope_entries e WHERE e.program_id = ?1))`

// RecordUserStats stores a snapshot if it differs from the latest one.
func (t *Tx) RecordUserStats(ctx context.Context, programID int64, s platform.UserStats) error {
	var last platform.UserStats
	err := t.tx.QueryRowContext(ctx, `
		SELECT reports, valid_reports, bounty_earned FROM program_user_stats
		WHERE program_id = ? ORDER BY captured_at DESC LIMIT 1`, programID).
		Scan(&last.Reports, &last.ValidReports, &last.BountyEarned)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		if s == (platform.UserStats{}) {
			return nil // nothing worth recording yet
		}
	case err != nil:
		return err
	case last == s:
		return nil
	}
	_, err = t.tx.ExecContext(ctx, `
		INSERT INTO program_user_stats (program_id, captured_at, reports, valid_reports, bounty_earned)
		VALUES (?, ?, ?, ?, ?)`, programID, t.now, s.Reports, s.ValidReports, s.BountyEarned)
	return err
}

// HeldPlatform is a platform with programs saved, and whether any of its
// disclosed reports are saved.
type HeldPlatform struct {
	Name      string
	Discloses bool
}

// HeldPlatforms lists the platforms with programs saved, by name: a
// read-only copy's platforms, which come from its seed, not credentials.
func (s *Store) HeldPlatforms(ctx context.Context) ([]HeldPlatform, error) {
	var out []HeldPlatform
	err := s.each(ctx, `
		SELECT p.platform, EXISTS (SELECT 1 FROM disclosed_reports d JOIN programs q ON q.id = d.program_id WHERE q.platform = p.platform)
		FROM programs p GROUP BY p.platform ORDER BY p.platform`, nil, func(rows *sql.Rows) error {
		var h HeldPlatform
		if err := rows.Scan(&h.Name, &h.Discloses); err != nil {
			return err
		}
		out = append(out, h)
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("held platforms: %w", err)
	}
	return out, nil
}

// SitemapProgram is a program page for a sitemap: the program, and when it
// last changed (its newest change, else when it was first seen).
type SitemapProgram struct {
	Platform, Handle string
	Changed          time.Time
}

// PublicPrograms returns the public programs still listed, by platform and
// handle: the program pages a published read-only copy offers search
// engines.
func (s *Store) PublicPrograms(ctx context.Context) ([]SitemapProgram, error) {
	rows, err := s.db.R.QueryContext(ctx, `SELECT p.platform, p.handle,
			COALESCE((SELECT MAX(c.occurred_at) FROM changes c WHERE c.program_id = p.id), p.first_seen_at)
		FROM programs p WHERE p.private = 0 AND p.removed_at IS NULL AND p.ignored = 0 ORDER BY p.platform, p.handle`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []SitemapProgram
	for rows.Next() {
		var p SitemapProgram
		var at sql.NullString
		if err := rows.Scan(&p.Platform, &p.Handle, &at); err != nil {
			return nil, err
		}
		if t := parseTSPtr(at); t != nil {
			p.Changed = *t
		}
		out = append(out, p)
	}
	return out, rows.Err()
}
