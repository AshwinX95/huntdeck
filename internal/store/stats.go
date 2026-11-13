package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"math"
	"strconv"
	"time"

	"github.com/mdpsec/huntdeck/internal/platform"
)

// RecordProgramStats stores a program's public stats. There is one snapshot
// per program per UTC day: a second fetch on the same day replaces it.
func (t *Tx) RecordProgramStats(ctx context.Context, programID int64, s platform.ProgramStats) error {
	rewards, err := json.Marshal(s.Rewards)
	if err != nil {
		return err
	}
	if s.Rewards == nil {
		rewards = []byte("[]")
	}
	bySeverity, err := json.Marshal(s.BySeverity)
	if err != nil {
		return err
	}
	profile, err := json.Marshal(s.Profile)
	if err != nil {
		return err
	}
	var last sql.NullString
	if err := t.tx.QueryRowContext(ctx, `SELECT stats_at FROM programs WHERE id = ?`, programID).Scan(&last); err != nil {
		return err
	}
	if last.Valid && len(last.String) >= 10 && last.String[:10] == t.now[:10] {
		if _, err := t.tx.ExecContext(ctx, `DELETE FROM program_public_stats WHERE program_id = ? AND captured_at = ?`,
			programID, last.String); err != nil {
			return err
		}
	}
	//nolint:gosec // constant placeholders; values are bound parameters
	if _, err := t.tx.ExecContext(ctx, `
		INSERT INTO program_public_stats (program_id, captured_at, currency, resolved_reports, reports_90d, bounties_paid,
			bounties_paid_90d, avg_bounty_low, avg_bounty_high, top_bounty_low, top_bounty_high, response_efficiency,
			last_resolved_at, hackers_thanked, rewards, first_response_hours, triage_hours, bounty_hours,
			resolution_hours, signal_requirement, by_severity, rewarded_reports, total_reports, reports_7d,
			validation_hours, reports_month, accepted_reports, counted_days, hunters, last_submission_at, scope_rank,
			last_accepted_at, rewarded_90d, points_90d, points_all, hunters_90d, known_issues, known_issues_unique)
		VALUES (`+placeholders(38)+`)`,
		programID, t.now, s.Currency, s.ResolvedReports, s.ReportsLast90d, s.BountiesPaid, s.BountiesPaidLast90d,
		s.AvgBountyLow, s.AvgBountyHigh, s.TopBountyLow, s.TopBountyHigh, s.ResponseEfficiency,
		tsPtr(s.LastResolvedAt), s.HackersThanked, string(rewards), measured(s.FirstResponseHours), measured(s.TriageHours),
		measured(s.BountyHours), measured(s.ResolutionHours), s.SignalRequirement, string(bySeverity), s.RewardedReports, s.TotalReports,
		s.ReportsLast7d, measured(s.ValidationHours), s.ReportsThisMonth, s.AcceptedReports, s.CountedDays, s.Hunters, tsPtr(s.LastSubmissionAt),
		s.ScopeRank, tsPtr(s.LastAcceptedAt), s.RewardedLast90d, s.Points90d, s.PointsAll, s.Hunters90d,
		s.KnownIssues, s.KnownIssuesUnique); err != nil {
		return err
	}
	if _, err := t.tx.ExecContext(ctx, `UPDATE programs SET stats_at = ?, profile = ? WHERE id = ?`, t.now, string(profile), programID); err != nil {
		return err
	}
	if s.Rewards != nil {
		if err := t.SetRewards(ctx, programID, s.Rewards); err != nil {
			return err
		}
	}
	if s.Requirements != nil {
		if err := t.SetRequirements(ctx, programID, *s.Requirements); err != nil {
			return err
		}
	}
	if s.Facts != nil {
		return t.SetFacts(ctx, programID, *s.Facts)
	}
	return nil
}

// SavedTotals are the running totals a program's 90-day counts start from
// (see platform.ProgramStats.CountedDays).
type SavedTotals struct {
	Days    float64  // how long before now they were saved
	Reports *int     // every report submitted, where the platform shows it
	Paid    *float64 // bounties paid, likewise
	// Rewarded is reports that earned a bounty, where the platform counts
	// them (Bugcrowd).
	Rewarded *int
	// Age is how many days ago the program launched, where the platform
	// says; set even without saved totals.
	Age *float64
}

// TotalsBaseline returns a program's saved totals from window ago: the
// latest saved then or earlier, or else the earliest saved. Today's (UTC)
// snapshot doesn't count, since the one being recorded replaces it. ok is
// false when there are none; base.Age is set either way.
func (t *Tx) TotalsBaseline(ctx context.Context, programID int64, window time.Duration) (base SavedTotals, ok bool, err error) {
	now, err := time.Parse(TimeFormat, t.now)
	if err != nil {
		return base, false, err
	}
	var started sql.NullString
	if err := t.tx.QueryRowContext(ctx, `SELECT started_at FROM programs WHERE id = ?`, programID).Scan(&started); err != nil {
		return base, false, err
	}
	if started.Valid && started.String != "" {
		if age := now.Sub(parseTS(started.String)).Hours() / 24; age >= 0 {
			base.Age = &age
		}
	}
	since := ts(now.Add(-window))
	var captured string
	var reports, rewarded sql.NullInt64
	var paid sql.NullFloat64
	err = t.tx.QueryRowContext(ctx, `
		SELECT captured_at, total_reports, bounties_paid, rewarded_reports FROM program_public_stats
		WHERE program_id = ? AND (total_reports IS NOT NULL OR bounties_paid IS NOT NULL OR rewarded_reports IS NOT NULL) AND captured_at < ?
		ORDER BY captured_at > ?, CASE WHEN captured_at <= ? THEN captured_at END DESC, captured_at
		LIMIT 1`, programID, t.now[:10], since, since).Scan(&captured, &reports, &paid, &rewarded)
	if errors.Is(err, sql.ErrNoRows) {
		return base, false, nil
	}
	if err != nil {
		return base, false, err
	}
	base.Days, base.Reports, base.Paid, base.Rewarded = now.Sub(parseTS(captured)).Hours()/24, intPtr(reports), floatPtr(paid), intPtr(rewarded)
	return base, true, nil
}

// LiveProgramIDs returns handle → ID for a platform's programs not marked
// removed.
func (s *Store) LiveProgramIDs(ctx context.Context, platformName string) (map[string]int64, error) {
	rows, err := s.db.R.QueryContext(ctx, `SELECT handle, id FROM programs WHERE platform = ? AND removed_at IS NULL`, platformName)
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

// StatsDue returns the handles of a platform's live programs whose stats are
// missing or older than before, least recently fetched first.
func (s *Store) StatsDue(ctx context.Context, platformName string, before time.Time) ([]string, error) {
	rows, err := s.db.R.QueryContext(ctx, `
		SELECT handle FROM programs
		WHERE platform = ? AND removed_at IS NULL AND (stats_at IS NULL OR stats_at < ?)
		ORDER BY stats_at IS NOT NULL, stats_at, handle`, platformName, ts(before))
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []string
	for rows.Next() {
		var h string
		if err := rows.Scan(&h); err != nil {
			return nil, err
		}
		out = append(out, h)
	}
	return out, rows.Err()
}

// ProgramStats is a stored stats snapshot. Nil fields weren't shown by the
// platform.
type ProgramStats struct {
	CapturedAt time.Time
	platform.ProgramStats
}

// AcceptedShare is the share of every report submitted that was accepted,
// all time, as a whole percentage ("52%"); "" without both totals.
func (p ProgramStats) AcceptedShare() string {
	if p.AcceptedReports == nil || p.TotalReports == nil || *p.TotalReports <= 0 || *p.AcceptedReports > *p.TotalReports {
		return ""
	}
	return strconv.Itoa(int(math.Round(float64(*p.AcceptedReports)*100/float64(*p.TotalReports)))) + "%"
}

// PaidPerReport is bounties paid per report received over the last 90 days,
// or nil when either number is missing or no reports came in.
func (p ProgramStats) PaidPerReport() *float64 {
	if p.BountiesPaidLast90d == nil || p.ReportsLast90d == nil || *p.ReportsLast90d == 0 {
		return nil
	}
	v := *p.BountiesPaidLast90d / float64(*p.ReportsLast90d)
	return &v
}

// SeverityRanges summarises the bounty table per severity, the way platforms
// show it above the table: the lowest and highest amount across all rows,
// plus the average bounty and each severity's share of resolved reports.
func (p ProgramStats) SeverityRanges() []SeverityRange {
	out := []SeverityRange{{Severity: "low"}, {Severity: "medium"}, {Severity: "high"}, {Severity: "critical"}}
	stats := []platform.SeverityStat{p.BySeverity.Low, p.BySeverity.Medium, p.BySeverity.High, p.BySeverity.Critical}
	var total int
	for _, s := range stats {
		if s.Reports != nil {
			total += *s.Reports
		}
	}
	for i, s := range stats {
		out[i].AvgBounty = s.AvgBounty
		if s.Reports != nil && total > 0 {
			share := int(math.Round(100 * float64(*s.Reports) / float64(total)))
			out[i].SharePct = &share
		}
	}
	for _, r := range p.Rewards {
		for i, rr := range []platform.RewardRange{r.Low, r.Medium, r.High, r.Critical} {
			if rr.Max <= 0 {
				continue
			}
			lo := rr.Min
			if lo <= 0 {
				lo = rr.Max
			}
			if out[i].Min == 0 || lo < out[i].Min {
				out[i].Min = lo
			}
			out[i].Max = max(out[i].Max, rr.Max)
		}
	}
	return out
}

// SeverityRange is the bounty range for one severity across a bounty table,
// with its average bounty and share of resolved reports where published.
type SeverityRange struct {
	Severity  string
	Min, Max  float64
	AvgBounty *float64
	SharePct  *int
}

// LatestProgramStats returns a program's newest stats snapshot, or nil.
func (s *Store) LatestProgramStats(ctx context.Context, programID int64) (*ProgramStats, error) {
	row := s.db.R.QueryRowContext(ctx, `SELECT `+statsColumns+`
		FROM programs p JOIN program_public_stats s ON s.program_id = p.id AND s.captured_at = p.stats_at
		WHERE p.id = ?`, programID)
	st, err := scanStats(row.Scan)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	return st, err
}

// ProgramStatsHistory returns a program's stats snapshots, oldest first.
func (s *Store) ProgramStatsHistory(ctx context.Context, programID int64) ([]ProgramStats, error) {
	rows, err := s.db.R.QueryContext(ctx, `SELECT `+statsColumns+`
		FROM program_public_stats s JOIN programs p ON p.id = s.program_id
		WHERE s.program_id = ? ORDER BY s.captured_at`, programID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []ProgramStats
	for rows.Next() {
		st, err := scanStats(rows.Scan)
		if err != nil {
			return nil, err
		}
		out = append(out, *st)
	}
	return out, rows.Err()
}

// statsColumns reads a snapshot; the profile comes from programs.profile,
// so queries using it must alias programs as p.
const statsColumns = `s.captured_at, s.currency, s.resolved_reports, s.reports_90d, s.bounties_paid, s.bounties_paid_90d,
	s.avg_bounty_low, s.avg_bounty_high, s.top_bounty_low, s.top_bounty_high, s.response_efficiency,
	s.last_resolved_at, s.hackers_thanked, s.rewards, s.first_response_hours, s.triage_hours, s.bounty_hours,
	s.resolution_hours, s.signal_requirement, s.by_severity, s.rewarded_reports, s.total_reports, s.reports_7d,
	s.validation_hours, s.reports_month, s.accepted_reports, s.counted_days, s.hunters, s.last_submission_at,
	s.scope_rank, p.profile, s.last_accepted_at, s.rewarded_90d, s.points_90d, s.points_all, s.hunters_90d,
	s.known_issues, s.known_issues_unique`

func scanStats(scan func(...any) error) (*ProgramStats, error) {
	var st ProgramStats
	var captured string
	var resolved, reports90, efficiency, thanked, rewarded, total, reports7, month, accepted, counted, hunters, scopeRank sql.NullInt64
	var points90, pointsAll, hunters90, known, knownUnique sql.NullInt64
	var lastSubmission, lastAccepted sql.NullString
	var paid, paid90, avgLo, avgHi, topLo, topHi, rewarded90 sql.NullFloat64
	var firstResponse, triage, bounty, resolution, signal, validation sql.NullFloat64
	var lastResolved sql.NullString
	var rewards, bySeverity, profile string
	if err := scan(&captured, &st.Currency, &resolved, &reports90, &paid, &paid90, &avgLo, &avgHi, &topLo, &topHi,
		&efficiency, &lastResolved, &thanked, &rewards, &firstResponse, &triage, &bounty, &resolution, &signal,
		&bySeverity, &rewarded, &total, &reports7, &validation, &month, &accepted, &counted, &hunters, &lastSubmission, &scopeRank, &profile, &lastAccepted, &rewarded90,
		&points90, &pointsAll, &hunters90, &known, &knownUnique); err != nil {
		return nil, err
	}
	st.FirstResponseHours, st.TriageHours = floatPtr(firstResponse), floatPtr(triage)
	st.BountyHours, st.ResolutionHours = floatPtr(bounty), floatPtr(resolution)
	st.SignalRequirement, st.ValidationHours = floatPtr(signal), floatPtr(validation)
	st.RewardedReports, st.TotalReports, st.ReportsLast7d = intPtr(rewarded), intPtr(total), intPtr(reports7)
	st.ReportsThisMonth, st.AcceptedReports, st.CountedDays = intPtr(month), intPtr(accepted), intPtr(counted)
	st.Hunters, st.LastSubmissionAt, st.ScopeRank = intPtr(hunters), parseTSPtr(lastSubmission), intPtr(scopeRank)
	st.RewardedLast90d = floatPtr(rewarded90)
	st.Points90d, st.PointsAll, st.Hunters90d = intPtr(points90), intPtr(pointsAll), intPtr(hunters90)
	st.KnownIssues, st.KnownIssuesUnique = intPtr(known), intPtr(knownUnique)
	if err := json.Unmarshal([]byte(bySeverity), &st.BySeverity); err != nil {
		return nil, err
	}
	if err := json.Unmarshal([]byte(profile), &st.Profile); err != nil {
		return nil, err
	}
	st.CapturedAt = parseTS(captured)
	st.ResolvedReports, st.ReportsLast90d = intPtr(resolved), intPtr(reports90)
	st.ResponseEfficiency, st.HackersThanked = intPtr(efficiency), intPtr(thanked)
	st.BountiesPaid, st.BountiesPaidLast90d = floatPtr(paid), floatPtr(paid90)
	st.AvgBountyLow, st.AvgBountyHigh = floatPtr(avgLo), floatPtr(avgHi)
	st.TopBountyLow, st.TopBountyHigh = floatPtr(topLo), floatPtr(topHi)
	st.LastResolvedAt, st.LastAcceptedAt = parseTSPtr(lastResolved), parseTSPtr(lastAccepted)
	if err := json.Unmarshal([]byte(rewards), &st.Rewards); err != nil {
		return nil, err
	}
	return &st, nil
}

func intPtr(v sql.NullInt64) *int {
	if !v.Valid {
		return nil
	}
	n := int(v.Int64)
	return &n
}

func nullBool(v sql.NullInt64) *bool {
	if !v.Valid {
		return nil
	}
	b := v.Int64 != 0
	return &b
}

func floatPtr(v sql.NullFloat64) *float64 {
	if !v.Valid {
		return nil
	}
	return &v.Float64
}

// measured is a time a platform gives in hours, nil for none: 0 is no
// measurement, not an instant answer (HackerOne gives it for programs with
// no recent figures, thousands of resolved reports or not).
func measured(h *float64) *float64 {
	if h == nil || *h <= 0 {
		return nil
	}
	return h
}
