package store

import (
	"context"
	"database/sql"
	"math"
	"slices"
	"time"

	"github.com/mdpsec/huntdeck/internal/platform"
)

// resolutionCursorKey names the setting holding the latest activity time
// read from a platform's all-programs resolution feed.
func resolutionCursorKey(platformName string) string {
	return "resolutions." + platformName + ".cursor"
}

// ResolutionCursor returns where the platform's resolution feed was last
// read up to, or nil if it never was.
func (s *Store) ResolutionCursor(ctx context.Context, platformName string) (*time.Time, error) {
	key := resolutionCursorKey(platformName)
	m, err := s.Settings(ctx, key)
	if err != nil {
		return nil, err
	}
	v, ok := m[key]
	if !ok {
		return nil, nil
	}
	t, err := time.Parse(TimeFormat, v)
	if err != nil {
		return nil, nil //nolint:nilerr // a bad value just means reading the feed from the start
	}
	return &t, nil
}

// SetResolutionCursor records where the platform's resolution feed was read
// up to.
func (t *Tx) SetResolutionCursor(ctx context.Context, platformName string, at time.Time) error {
	return t.PutSetting(ctx, resolutionCursorKey(platformName), ts(at))
}

// ResolutionsDue returns the platform's live programs whose resolution
// history hasn't been fetched yet.
func (s *Store) ResolutionsDue(ctx context.Context, platformName string) ([]ProgramRef, error) {
	rows, err := s.db.R.QueryContext(ctx, `
		SELECT id, platform, handle, name, url, private, state FROM programs
		WHERE platform = ? AND removed_at IS NULL AND resolutions_synced_at IS NULL
		ORDER BY id`, platformName)
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

// MarkResolutionsSynced records that a program's resolution history was
// fetched in full.
func (t *Tx) MarkResolutionsSynced(ctx context.Context, programID int64) error {
	_, err := t.tx.ExecContext(ctx, `UPDATE programs SET resolutions_synced_at = ? WHERE id = ?`, t.now, programID)
	return err
}

// SaveResolution stores one resolved report against a program and reports
// whether it was new. A report seen again keeps its earliest resolving
// activity and gains a bounty if one is shown now.
func (t *Tx) SaveResolution(ctx context.Context, programID int64, r platform.Resolution) (bool, error) {
	res, err := t.tx.ExecContext(ctx, `
		INSERT INTO resolved_reports (program_id, report_id, submitted_at, resolved_at, action, bounty, reporter,
			first_seen_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT (program_id, report_id) DO NOTHING`,
		programID, r.ID, tsPtr(r.SubmittedAt), ts(r.ActivityAt), r.Action, r.Bounty, r.Reporter, t.now, t.now)
	if err != nil {
		return false, err
	}
	if n, err := res.RowsAffected(); err != nil || n > 0 {
		return n > 0, err
	}
	_, err = t.tx.ExecContext(ctx, `
		UPDATE resolved_reports SET
			action = CASE WHEN ?1 < resolved_at THEN ?2 ELSE action END,
			resolved_at = MIN(resolved_at, ?1),
			submitted_at = COALESCE(submitted_at, ?3),
			bounty = COALESCE(?4, bounty),
			reporter = CASE WHEN ?5 <> '' THEN ?5 ELSE reporter END,
			updated_at = ?6
		WHERE program_id = ?7 AND report_id = ?8`,
		ts(r.ActivityAt), r.Action, tsPtr(r.SubmittedAt), r.Bounty, r.Reporter, t.now, programID, r.ID)
	return false, err
}

// ResolutionMonth is one calendar month (UTC) of a program's resolved
// reports.
type ResolutionMonth struct {
	Month    time.Time // first day of the month
	Resolved int
	// Submitted counts resolved reports that were submitted this month,
	// whenever they were resolved: valid reports coming in. Recent months
	// grow as their reports get resolved.
	Submitted int
	Bounties  float64 // shown bounties, in the program's currency
	Rewarded  int     // reports that got a bounty (whether its amount shows or not)
	// MedianDays is the median days from submission to resolution of the
	// month's reports with an exact resolution date; nil without any.
	MedianDays *float64
}

// ResolutionSummary describes a program's resolved reports.
type ResolutionSummary struct {
	Total       int
	First, Last *time.Time // oldest and newest resolution
	Last90d     int        // resolved in the last 90 days
	Prev90d     int        // resolved in the 90 days before that
	Median90d   *float64   // median days to resolve, last 90 days
	MedianPrev  *float64   // the same for the 90 days before
	Bounties90d float64    // shown bounties, last 90 days
	WithBounty  int        // reports with a shown bounty, all time
	// BountyAverage, when set, is the program's average bounty: it hides
	// amounts, so its bounty-awarded reports count at that in Bounties90d
	// and the months' Bounties (estimated; see hiddenbounties.go), and
	// Rewarded90d counts them.
	BountyAverage *float64
	Rewarded90d   int
	Months        []ResolutionMonth
	HistoryFetch  *time.Time // when the full history was fetched, if it was
}

// ProgramResolutions summarises a program's resolved reports as of now,
// with months going back up to months calendar months.
func (s *Store) ProgramResolutions(ctx context.Context, programID int64, now time.Time, months int) (ResolutionSummary, error) {
	var sum ResolutionSummary
	var synced sql.NullString
	if err := s.db.R.QueryRowContext(ctx, `SELECT resolutions_synced_at FROM programs WHERE id = ?`, programID).Scan(&synced); err != nil {
		return sum, err
	}
	sum.HistoryFetch = parseTSPtr(synced)
	hidden, err := s.hiddenBountyAverages(ctx, `p.id = ?`, programID)
	if err != nil {
		return sum, err
	}
	avg, hides := hidden[programID]
	if hides {
		sum.BountyAverage = &avg
	}
	rows, err := s.db.R.QueryContext(ctx, `
		SELECT submitted_at, resolved_at, action, bounty FROM resolved_reports
		WHERE program_id = ? ORDER BY resolved_at`, programID)
	if err != nil {
		return sum, err
	}
	defer func() { _ = rows.Close() }()

	now = now.UTC()
	start := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC).AddDate(0, -(months - 1), 0)
	sum.Months = make([]ResolutionMonth, months)
	days := make([][]float64, months)
	for i := range sum.Months {
		sum.Months[i].Month = start.AddDate(0, i, 0)
	}
	var recent, prev []float64
	for rows.Next() {
		var submitted sql.NullString
		var resolvedS, action string
		var bounty sql.NullFloat64
		if err := rows.Scan(&submitted, &resolvedS, &action, &bounty); err != nil {
			return sum, err
		}
		resolved := parseTS(resolvedS)
		sum.Total++
		if sum.First == nil {
			sum.First = &resolved
		}
		sum.Last = &resolved
		if bounty.Valid {
			sum.WithBounty++
		}
		sub := parseTSPtr(submitted)
		var took *float64 // days to resolve, when the resolution date is exact
		if sub != nil && action == platform.ActionResolved && !resolved.Before(*sub) {
			d := resolved.Sub(*sub).Hours() / 24
			took = &d
		}
		if sub != nil && !sub.Before(start) {
			if i := monthIndex(*sub, start); i < months {
				sum.Months[i].Submitted++
			}
		}
		paid, _ := estimatedBounty(action, bounty, avg, hides)
		rewarded := action == platform.ActionBounty || bounty.Valid
		age := now.Sub(resolved)
		switch {
		case age < 90*24*time.Hour:
			sum.Last90d++
			sum.Bounties90d += paid
			if rewarded {
				sum.Rewarded90d++
			}
			if took != nil {
				recent = append(recent, *took)
			}
		case age < 180*24*time.Hour:
			sum.Prev90d++
			if took != nil {
				prev = append(prev, *took)
			}
		}
		if !resolved.Before(start) {
			if i := monthIndex(resolved, start); i < months {
				sum.Months[i].Resolved++
				sum.Months[i].Bounties += paid
				if rewarded {
					sum.Months[i].Rewarded++
				}
				if took != nil {
					days[i] = append(days[i], *took)
				}
			}
		}
	}
	for i := range sum.Months {
		sum.Months[i].MedianDays = median(days[i])
	}
	sum.Median90d, sum.MedianPrev = median(recent), median(prev)
	return sum, rows.Err()
}

func median(v []float64) *float64 {
	if len(v) == 0 {
		return nil
	}
	s := slices.Clone(v)
	slices.Sort(s)
	m := s[len(s)/2]
	if len(s)%2 == 0 {
		m = (s[len(s)/2-1] + s[len(s)/2]) / 2
	}
	m = math.Round(m*10) / 10
	return &m
}

// monthIndex is how many calendar months t is after start (UTC).
func monthIndex(t, start time.Time) int {
	t = t.UTC()
	return (t.Year()-start.Year())*12 + int(t.Month()-start.Month())
}

// MonthCounts are a program's reports per month, oldest month first:
// resolved ones by when they were resolved and by when they were submitted
// (HackerOne Hacktivity), or accepted ones by when they were accepted,
// resolved and submitted (Bugcrowd CrowdStream).
type MonthCounts struct {
	Resolved, Submitted []int
	Accepted            []int      // nil unless the platform lists accepted reports
	Since               *time.Time // accepted reports are complete from here; months before it aren't
}

// ResolvedByMonth counts each program's resolved and accepted reports per
// calendar month (UTC) for the months calendar months up to now. Programs
// without any get no entry, unless their accepted-report feed was read.
func (s *Store) ResolvedByMonth(ctx context.Context, programIDs []int64, now time.Time, months int) (map[int64]MonthCounts, error) {
	out := map[int64]MonthCounts{}
	if len(programIDs) == 0 {
		return out, nil
	}
	now = now.UTC()
	start := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC).AddDate(0, -(months - 1), 0)
	ids := make([]any, len(programIDs))
	for i, id := range programIDs {
		ids[i] = id
	}
	in := placeholders(len(programIDs))
	block := append([]any{ts(start)}, ids...)
	var args []any
	for range 5 {
		args = append(args, block...)
	}
	newCounts := func() MonthCounts {
		return MonthCounts{Resolved: make([]int, months), Submitted: make([]int, months)}
	}
	//nolint:gosec // only placeholders are concatenated; values are bound
	since, err := s.db.R.QueryContext(ctx, `SELECT id, accepted_since FROM programs
		WHERE accepted_since IS NOT NULL AND id IN (`+in+`)`, ids...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = since.Close() }()
	for since.Next() {
		var id int64
		var at string
		if err := since.Scan(&id, &at); err != nil {
			return nil, err
		}
		c, t := newCounts(), parseTS(at)
		c.Accepted, c.Since = make([]int, months), &t
		out[id] = c
	}
	if err := since.Err(); err != nil {
		return nil, err
	}

	//nolint:gosec // only placeholders are concatenated; values are bound
	rows, err := s.db.R.QueryContext(ctx, `
		SELECT program_id, 'r', substr(resolved_at, 1, 7), COUNT(*) FROM resolved_reports
		WHERE resolved_at >= ? AND program_id IN (`+in+`) GROUP BY 1, 3
		UNION ALL
		SELECT program_id, 's', substr(submitted_at, 1, 7), COUNT(*) FROM resolved_reports
		WHERE submitted_at >= ? AND program_id IN (`+in+`) GROUP BY 1, 3
		UNION ALL
		SELECT program_id, 'r', substr(resolved_at, 1, 7), COUNT(*) FROM accepted_reports
		WHERE resolved_at >= ? AND program_id IN (`+in+`) GROUP BY 1, 3
		UNION ALL
		SELECT program_id, 's', substr(submitted_at, 1, 7), COUNT(*) FROM accepted_reports
		WHERE submitted_at >= ? AND program_id IN (`+in+`) GROUP BY 1, 3
		UNION ALL
		SELECT program_id, 'a', substr(accepted_at, 1, 7), COUNT(*) FROM accepted_reports
		WHERE accepted_at >= ? AND program_id IN (`+in+`) GROUP BY 1, 3`, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var id int64
		var which, month string
		var n int
		if err := rows.Scan(&id, &which, &month, &n); err != nil {
			return nil, err
		}
		m, err := time.Parse("2006-01", month)
		if err != nil {
			continue
		}
		i := monthIndex(m, start)
		if i < 0 || i >= months {
			continue
		}
		c, ok := out[id]
		if !ok {
			c = newCounts()
		}
		switch which {
		case "r":
			c.Resolved[i] += n
		case "s":
			c.Submitted[i] += n
		case "a":
			if c.Accepted == nil {
				c.Accepted = make([]int, months)
			}
			c.Accepted[i] += n
		}
		out[id] = c
	}
	return out, rows.Err()
}

// ReportTimes is when a program's reports were resolved (HackerOne
// Hacktivity, Bugcrowd CrowdStream) and accepted (CrowdStream), oldest
// first. CompleteFrom is when the lists are complete from: nil for
// Hacktivity, which is read in full, or the start of the accepted-report
// feed's window.
type ReportTimes struct {
	Resolved, Accepted []time.Time
	// AcceptedPaid are the accepted reports likely to be paid: all but
	// priority 5 (informational).
	AcceptedPaid []time.Time
	CompleteFrom *time.Time
}

// ReportTimes returns when each program's reports were resolved and
// accepted since since. Programs whose reports aren't listed get no entry.
func (s *Store) ReportTimes(ctx context.Context, programIDs []int64, since time.Time) (map[int64]ReportTimes, error) {
	out := map[int64]ReportTimes{}
	if len(programIDs) == 0 {
		return out, nil
	}
	ids := make([]any, len(programIDs))
	for i, id := range programIDs {
		ids[i] = id
	}
	in := placeholders(len(programIDs))
	//nolint:gosec // only placeholders are concatenated; values are bound
	listed, err := s.db.R.QueryContext(ctx, `SELECT id, accepted_since FROM programs
		WHERE (resolutions_synced_at IS NOT NULL OR accepted_since IS NOT NULL) AND id IN (`+in+`)`, ids...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = listed.Close() }()
	for listed.Next() {
		var id int64
		var from sql.NullString
		if err := listed.Scan(&id, &from); err != nil {
			return nil, err
		}
		out[id] = ReportTimes{CompleteFrom: parseTSPtr(from)}
	}
	if err := listed.Err(); err != nil {
		return nil, err
	}

	block := append([]any{ts(since)}, ids...)
	args := slices.Concat(block, block, block, block)
	//nolint:gosec // only placeholders are concatenated; values are bound
	rows, err := s.db.R.QueryContext(ctx, `
		SELECT program_id, 'r', resolved_at FROM resolved_reports WHERE resolved_at >= ? AND program_id IN (`+in+`)
		UNION ALL
		SELECT program_id, 'r', resolved_at FROM accepted_reports WHERE resolved_at >= ? AND program_id IN (`+in+`)
		UNION ALL
		SELECT program_id, 'a', accepted_at FROM accepted_reports WHERE accepted_at >= ? AND program_id IN (`+in+`)
		UNION ALL
		SELECT program_id, 'p', accepted_at FROM accepted_reports a WHERE accepted_at >= ? AND program_id IN (`+in+`)
			AND `+payablePriority+`
		ORDER BY 1, 3`, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var id int64
		var kind, at string
		if err := rows.Scan(&id, &kind, &at); err != nil {
			return nil, err
		}
		t, ok := out[id]
		if !ok {
			continue
		}
		switch kind {
		case "r":
			t.Resolved = append(t.Resolved, parseTS(at))
		case "a":
			t.Accepted = append(t.Accepted, parseTS(at))
		default:
			t.AcceptedPaid = append(t.AcceptedPaid, parseTS(at))
		}
		out[id] = t
	}
	return out, rows.Err()
}
