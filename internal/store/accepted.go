package store

import (
	"context"
	"database/sql"
	"time"

	"github.com/mdpsec/huntdeck/internal/platform"
)

// AcceptedDue returns the platform's live programs whose accepted-report
// feed was never read, then up to staleLimit of those last read before
// staleBefore, oldest first.
func (s *Store) AcceptedDue(ctx context.Context, platformName string, staleBefore time.Time, staleLimit int) ([]ProgramRef, error) {
	rows, err := s.db.R.QueryContext(ctx, `
		SELECT * FROM (SELECT id, platform, handle, name, url, private, state FROM programs
			WHERE platform = ?1 AND removed_at IS NULL AND accepted_synced_at IS NULL ORDER BY id)
		UNION ALL
		SELECT * FROM (SELECT id, platform, handle, name, url, private, state FROM programs
			WHERE platform = ?1 AND removed_at IS NULL AND accepted_synced_at < ?2
			ORDER BY accepted_synced_at, id LIMIT ?3)`, platformName, ts(staleBefore), staleLimit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []ProgramRef
	for rows.Next() {
		var r ProgramRef
		if err := rows.Scan(&r.ID, &r.Platform, &r.Handle, &r.Name, &r.URL, &r.Private, &r.State); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// MarkAcceptedSynced records that a program's accepted-report feed was read
// in full. since is where the feed's window started; the earliest one read
// that lists any report is kept as the point from which the program's
// reports are complete (a later read's window starts later, so in effect
// the first read's). Without a window (since nil, the program's own feed
// unreadable), a program whose reports the all-programs feed listed keeps
// the day of its earliest (MarkAcceptedListed). A feed that has never
// listed one is a program keeping CrowdStream to itself, not one without
// reports, so accepted_since stays NULL and its counts read as not shared
// rather than 0.
func (t *Tx) MarkAcceptedSynced(ctx context.Context, programID int64, since *time.Time) error {
	_, err := t.tx.ExecContext(ctx, `UPDATE programs SET accepted_synced_at = ?1,
		accepted_since = CASE
			WHEN NOT EXISTS (SELECT 1 FROM accepted_reports WHERE program_id = ?3) THEN accepted_since
			WHEN ?2 IS NULL THEN COALESCE(accepted_since, (SELECT MIN(accepted_at) FROM accepted_reports WHERE program_id = ?3))
			WHEN accepted_since IS NULL OR ?2 < accepted_since THEN ?2
			ELSE accepted_since END
		WHERE id = ?3`, t.now, tsPtr(since), programID)
	return err
}

// MarkAcceptedListed marks the platform's programs that have accepted
// reports saved but no accepted_since (the all-programs feed listed them
// before, or without, their own feed being read) as listing their
// reports, from the day of the earliest saved: a program with saved
// reports doesn't keep CrowdStream to itself, and everything that counts
// them (Stats, the Programs list, HuntScore) goes by accepted_since. Its
// own feed, once read, moves it back to that feed's window
// (MarkAcceptedSynced).
func (t *Tx) MarkAcceptedListed(ctx context.Context, platformName string) error {
	_, err := t.tx.ExecContext(ctx, `UPDATE programs
		SET accepted_since = (SELECT MIN(a.accepted_at) FROM accepted_reports a WHERE a.program_id = programs.id)
		WHERE platform = ? AND accepted_since IS NULL
			AND EXISTS (SELECT 1 FROM accepted_reports a WHERE a.program_id = programs.id)`, platformName)
	return err
}

// CrowdStreamShared reports whether a program's accepted-report feed has
// listed any report (see MarkAcceptedSynced).
func (t *Tx) CrowdStreamShared(ctx context.Context, programID int64) (bool, error) {
	var shared bool
	err := t.tx.QueryRowContext(ctx, `SELECT accepted_since IS NOT NULL FROM programs WHERE id = ?`, programID).Scan(&shared)
	return shared, err
}

// SaveAccepted stores one accepted report against a program and reports
// whether it was new. A report seen again takes its latest resolution,
// priority and shown bounty.
func (t *Tx) SaveAccepted(ctx context.Context, programID int64, r platform.AcceptedReport) (bool, error) {
	var priority any
	if r.Priority > 0 {
		priority = r.Priority
	}
	res, err := t.tx.ExecContext(ctx, `
		INSERT INTO accepted_reports (program_id, report_id, submitted_at, accepted_at, resolved_at, priority, bounty,
			reporter, first_seen_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT (program_id, report_id) DO NOTHING`,
		programID, r.ID, tsPtr(r.SubmittedAt), ts(r.AcceptedAt), tsPtr(r.ResolvedAt), priority, r.Bounty, r.Reporter, t.now, t.now)
	if err != nil {
		return false, err
	}
	if n, err := res.RowsAffected(); err != nil || n > 0 {
		return n > 0, err
	}
	_, err = t.tx.ExecContext(ctx, `
		UPDATE accepted_reports SET
			submitted_at = COALESCE(?1, submitted_at),
			resolved_at = ?2,
			priority = COALESCE(?3, priority),
			bounty = COALESCE(?4, bounty),
			reporter = CASE WHEN ?5 <> '' THEN ?5 ELSE reporter END,
			updated_at = ?6
		WHERE program_id = ?7 AND report_id = ?8`,
		tsPtr(r.SubmittedAt), tsPtr(r.ResolvedAt), priority, r.Bounty, r.Reporter, t.now, programID, r.ID)
	return false, err
}

// AcceptedMonth is one calendar month (UTC) of a program's accepted reports.
type AcceptedMonth struct {
	Month    time.Time // first day of the month
	Accepted int
	// ByPriority splits Accepted by priority: index 1 (critical) to 5
	// (informational), 0 for reports without one.
	ByPriority [6]int
	Resolved   int // reports resolved this month, whenever accepted
	// Submitted counts accepted reports submitted this month, whenever they
	// were accepted. Recent months grow as their reports get accepted.
	Submitted int
	Bounties  float64 // shown bounties of the month's accepted reports
}

// AcceptedSummary describes a program's accepted reports.
type AcceptedSummary struct {
	Total int // accepted reports kept
	Open  int // of them, not resolved
	// Since is where the reports are complete from; nil until the program's
	// feed was read in full. SyncedAt is when it last was.
	Since, SyncedAt *time.Time
	Last90d         int  // accepted in the last 90 days
	Prev90d         int  // accepted in the 90 days before that
	PrevKnown       bool // Since is at least 180 days back, so Prev90d is complete
	Resolved90d     int  // resolved in the last 90 days
	// MedianAccept90d is the median days from submission to acceptance of
	// reports accepted in the last 90 days; MedianResolve90d from submission
	// to resolution of those resolved in the last 90 days. Nil without any.
	MedianAccept90d, MedianResolve90d *float64
	Bounties90d                       float64 // shown bounties of reports accepted in the last 90 days
	Months                            []AcceptedMonth
}

// ProgramAccepted summarises a program's accepted reports as of now, with
// months going back up to months calendar months.
func (s *Store) ProgramAccepted(ctx context.Context, programID int64, now time.Time, months int) (AcceptedSummary, error) {
	var sum AcceptedSummary
	var since, synced sql.NullString
	if err := s.db.R.QueryRowContext(ctx, `SELECT accepted_since, accepted_synced_at FROM programs WHERE id = ?`,
		programID).Scan(&since, &synced); err != nil {
		return sum, err
	}
	sum.Since, sum.SyncedAt = parseTSPtr(since), parseTSPtr(synced)
	rows, err := s.db.R.QueryContext(ctx, `
		SELECT submitted_at, accepted_at, resolved_at, priority, bounty FROM accepted_reports
		WHERE program_id = ? ORDER BY accepted_at`, programID)
	if err != nil {
		return sum, err
	}
	defer func() { _ = rows.Close() }()

	now = now.UTC()
	start := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC).AddDate(0, -(months - 1), 0)
	sum.Months = make([]AcceptedMonth, months)
	for i := range sum.Months {
		sum.Months[i].Month = start.AddDate(0, i, 0)
	}
	sum.PrevKnown = sum.Since != nil && now.Sub(*sum.Since) >= 180*24*time.Hour
	inMonth := func(t time.Time) (int, bool) {
		if t.Before(start) {
			return 0, false
		}
		i := monthIndex(t, start)
		return i, i < months
	}
	var toAccept, toResolve []float64
	for rows.Next() {
		var submitted, resolved sql.NullString
		var acceptedS string
		var priority sql.NullInt64
		var bounty sql.NullFloat64
		if err := rows.Scan(&submitted, &acceptedS, &resolved, &priority, &bounty); err != nil {
			return sum, err
		}
		accepted, sub, res := parseTS(acceptedS), parseTSPtr(submitted), parseTSPtr(resolved)
		sum.Total++
		if res == nil {
			sum.Open++
		}
		switch age := now.Sub(accepted); {
		case age < 90*24*time.Hour:
			sum.Last90d++
			sum.Bounties90d += bounty.Float64
			if sub != nil {
				// Acceptance is a day: count whole days from the day submitted.
				d := accepted.Sub(sub.Truncate(24*time.Hour)).Hours() / 24
				toAccept = append(toAccept, max(d, 0))
			}
		case age < 180*24*time.Hour:
			sum.Prev90d++
		}
		if res != nil && now.Sub(*res) < 90*24*time.Hour {
			sum.Resolved90d++
			if sub != nil && !res.Before(*sub) {
				toResolve = append(toResolve, res.Sub(*sub).Hours()/24)
			}
		}
		if i, ok := inMonth(accepted); ok {
			m := &sum.Months[i]
			m.Accepted++
			m.Bounties += bounty.Float64
			p := int(priority.Int64)
			if p < 1 || p > 5 {
				p = 0
			}
			m.ByPriority[p]++
		}
		if res != nil {
			if i, ok := inMonth(*res); ok {
				sum.Months[i].Resolved++
			}
		}
		if sub != nil {
			if i, ok := inMonth(*sub); ok {
				sum.Months[i].Submitted++
			}
		}
	}
	sum.MedianAccept90d, sum.MedianResolve90d = median(toAccept), median(toResolve)
	return sum, rows.Err()
}
