package store

import (
	"context"
	"database/sql"
	"strings"
	"time"

	"github.com/mdpsec/huntdeck/internal/platform"
)

// ScopeHistoryDue returns up to limit live programs of the platform whose
// scope is stored but whose scope history hasn't been read.
func (s *Store) ScopeHistoryDue(ctx context.Context, platformName string, limit int) ([]platform.Program, map[string]int64, error) {
	rows, err := s.db.R.QueryContext(ctx, `
		SELECT id, handle, COALESCE(platform_id, ''), name, url FROM programs
		WHERE platform = ? AND removed_at IS NULL AND scope_synced_at IS NOT NULL AND scope_history_at IS NULL
		ORDER BY id LIMIT ?`, platformName, limit)
	if err != nil {
		return nil, nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []platform.Program
	ids := map[string]int64{}
	for rows.Next() {
		var p platform.Program
		var id int64
		if err := rows.Scan(&id, &p.Handle, &p.PlatformID, &p.Name, &p.URL); err != nil {
			return nil, nil, err
		}
		out = append(out, p)
		ids[p.Handle] = id
	}
	return out, ids, rows.Err()
}

// StateHistoryDue returns a platform's live programs whose past state
// changes haven't been read, and their IDs by handle.
func (s *Store) StateHistoryDue(ctx context.Context, platformName string) ([]platform.Program, map[string]int64, error) {
	rows, err := s.db.R.QueryContext(ctx, `
		SELECT id, handle, COALESCE(platform_id, ''), name, url FROM programs
		WHERE platform = ? AND removed_at IS NULL AND state_history_at IS NULL
		ORDER BY id`, platformName)
	if err != nil {
		return nil, nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []platform.Program
	ids := map[string]int64{}
	for rows.Next() {
		var p platform.Program
		var id int64
		if err := rows.Scan(&id, &p.Handle, &p.PlatformID, &p.Name, &p.URL); err != nil {
			return nil, nil, err
		}
		out = append(out, p)
		ids[p.Handle] = id
	}
	return out, ids, rows.Err()
}

// MarkStateHistory records that a program's past state changes were read.
func (t *Tx) MarkStateHistory(ctx context.Context, programID int64) error {
	_, err := t.tx.ExecContext(ctx, `UPDATE programs SET state_history_at = ? WHERE id = ?`, t.now, programID)
	return err
}

// AddPastStateChange records a state change from a platform's history,
// dated when it happened. Only changes from before HuntDeck first saw the
// program are kept (polls record the rest), each once. They count as
// already notified. It reports whether the change was added.
func (t *Tx) AddPastStateChange(ctx context.Context, programID int64, c platform.StateChange) (bool, error) {
	res, err := t.tx.ExecContext(ctx, `
		INSERT INTO changes (occurred_at, program_id, kind, field, old_value, new_value, notified_at)
		SELECT ?1, ?2, ?3, 'state', ?4, ?5, ?6
		WHERE ?1 < (SELECT first_seen_at FROM programs WHERE id = ?2)
			AND NOT EXISTS (SELECT 1 FROM changes WHERE program_id = ?2 AND kind = ?3 AND occurred_at = ?1
				AND old_value = ?4 AND new_value = ?5)`,
		ts(c.At), programID, ChangeStateChanged, c.From, c.To, t.now)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n > 0, err
}

// AnnouncementsRead reports whether a program's announcements were read
// before.
func (t *Tx) AnnouncementsRead(ctx context.Context, programID int64) (bool, error) {
	var at sql.NullString
	err := t.tx.QueryRowContext(ctx, `SELECT announcements_at FROM programs WHERE id = ?`, programID).Scan(&at)
	return at.Valid, err
}

// AddAnnouncement records a program announcement, once. Past ones (from
// the first read) count as already notified and belong to no poll; others
// are news of poll runID. It reports whether the announcement was new.
func (t *Tx) AddAnnouncement(ctx context.Context, programID, runID int64, a platform.Announcement, past bool) (bool, error) {
	var run, notified any
	if past {
		notified = t.now
	} else if runID != 0 {
		run = runID
	}
	text := strings.TrimSpace(a.Title)
	if b := strings.TrimSpace(a.Body); b != "" {
		text += "\n\n" + b
	}
	res, err := t.tx.ExecContext(ctx, `
		INSERT INTO changes (occurred_at, poll_run_id, program_id, kind, field, new_value, notified_at)
		SELECT ?, ?, ?, ?, ?, ?, ?
		WHERE NOT EXISTS (SELECT 1 FROM changes WHERE program_id = ? AND kind = ? AND field = ?)`,
		ts(a.PublishedAt), run, programID, ChangeAnnouncement, a.ID, text, notified, programID, ChangeAnnouncement, a.ID)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n > 0, err
}

// MarkAnnouncements records that a program's announcements were read.
func (t *Tx) MarkAnnouncements(ctx context.Context, programID int64) error {
	_, err := t.tx.ExecContext(ctx, `UPDATE programs SET announcements_at = COALESCE(announcements_at, ?) WHERE id = ?`, t.now, programID)
	return err
}

// MarkScopeHistory records that a program's scope history was read, and
// dates its last scope change from it where HuntDeck recorded none later.
func (t *Tx) MarkScopeHistory(ctx context.Context, programID int64) error {
	_, err := t.tx.ExecContext(ctx, `UPDATE programs SET scope_history_at = ?2, scope_changed_at = `+scopeChangedAt+` WHERE id = ?1`, programID, t.now)
	return err
}

// SetState records a program going from one state to another at at, as
// a change of poll runID, and stores the new state. A change that counts
// as old news (past) is marked notified.
func (t *Tx) SetState(ctx context.Context, programID, runID int64, from, to string, at time.Time, past bool) error {
	var run, notified any
	if runID != 0 {
		run = runID
	}
	if past {
		notified = t.now
	}
	if _, err := t.tx.ExecContext(ctx, `
		INSERT INTO changes (occurred_at, poll_run_id, program_id, kind, field, old_value, new_value, notified_at)
		VALUES (?, ?, ?, ?, 'state', ?, ?, ?)`, ts(at), run, programID, ChangeStateChanged, from, to, notified); err != nil {
		return err
	}
	_, err := t.tx.ExecContext(ctx, `UPDATE programs SET state = ? WHERE id = ?`, to, programID)
	return err
}

// StateSince is when a program last changed to the state it's in, nil if
// no change to it is recorded.
func (s *Store) StateSince(ctx context.Context, programID int64) (*time.Time, error) {
	var at sql.NullString
	err := s.db.R.QueryRowContext(ctx, `
		SELECT MAX(c.occurred_at) FROM changes c JOIN programs p ON p.id = c.program_id
		WHERE c.program_id = ? AND c.kind = ? AND c.new_value = p.state`, programID, ChangeStateChanged).Scan(&at)
	if err != nil {
		return nil, err
	}
	return parseTSPtr(at), nil
}
