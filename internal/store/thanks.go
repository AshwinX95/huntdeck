package store

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// Thanks returns a program's saved hunters with their valid reports (see
// SaveThanks), and when its whole list was last read; nil and nil if never.
func (s *Store) Thanks(ctx context.Context, programID int64) (map[string]int, *time.Time, error) {
	var read sql.NullString
	err := s.db.R.QueryRowContext(ctx, `SELECT thanks_read_at FROM programs WHERE id = ?`, programID).Scan(&read)
	if errors.Is(err, sql.ErrNoRows) || err == nil && !read.Valid {
		return nil, nil, nil
	}
	if err != nil {
		return nil, nil, err
	}
	rows, err := s.db.R.QueryContext(ctx, `SELECT hunter, valid_reports FROM program_thanks WHERE program_id = ?`, programID)
	if err != nil {
		return nil, nil, err
	}
	defer func() { _ = rows.Close() }()
	out := map[string]int{}
	for rows.Next() {
		var h string
		var n int
		if err := rows.Scan(&h, &n); err != nil {
			return nil, nil, err
		}
		out[h] = n
	}
	return out, parseTSPtr(read), rows.Err()
}

// SaveThanks records hunters' valid reports on a program. complete says
// they are the whole list: hunters no longer on it are dropped and the
// list is marked read now.
func (t *Tx) SaveThanks(ctx context.Context, programID int64, hunters map[string]int, complete bool) error {
	if complete {
		if _, err := t.tx.ExecContext(ctx, `DELETE FROM program_thanks WHERE program_id = ?`, programID); err != nil {
			return err
		}
		if _, err := t.tx.ExecContext(ctx, `UPDATE programs SET thanks_read_at = ? WHERE id = ?`, t.now, programID); err != nil {
			return err
		}
	}
	for h, n := range hunters {
		if _, err := t.tx.ExecContext(ctx, `
			INSERT INTO program_thanks (program_id, hunter, valid_reports) VALUES (?, ?, ?)
			ON CONFLICT (program_id, hunter) DO UPDATE SET valid_reports = excluded.valid_reports`,
			programID, h, n); err != nil {
			return err
		}
	}
	return nil
}
