package store

import (
	"context"
	"time"
)

// Settings returns the UI-edited settings whose keys start with prefix.
func (s *Store) Settings(ctx context.Context, prefix string) (map[string]string, error) {
	rows, err := s.db.R.QueryContext(ctx, `SELECT key, value FROM settings WHERE substr(key, 1, ?) = ?`, len(prefix), prefix)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := map[string]string{}
	for rows.Next() {
		var k, v string
		if err := rows.Scan(&k, &v); err != nil {
			return nil, err
		}
		out[k] = v
	}
	return out, rows.Err()
}

// PutSetting stores a UI-edited setting.
func (t *Tx) PutSetting(ctx context.Context, key, value string) error {
	_, err := t.tx.ExecContext(ctx, `
		INSERT INTO settings (key, value, updated_at) VALUES (?, ?, ?)
		ON CONFLICT (key) DO UPDATE SET value = excluded.value, updated_at = excluded.updated_at`, key, value, t.now)
	return err
}

// DeleteSetting removes a UI-edited setting, restoring its default.
func (t *Tx) DeleteSetting(ctx context.Context, key string) error {
	_, err := t.tx.ExecContext(ctx, `DELETE FROM settings WHERE key = ?`, key)
	return err
}

// TimezoneSetting is the settings key holding the time zone chosen in
// Settings, an IANA name.
const TimezoneSetting = "display.timezone"

// Timezone returns the time zone to show dates and times in: the one
// chosen in Settings, or fallback (then UTC) if none is or it no longer loads.
func (s *Store) Timezone(ctx context.Context, fallback string) (*time.Location, error) {
	set, err := s.Settings(ctx, TimezoneSetting)
	if err != nil {
		return nil, err
	}
	for _, name := range []string{set[TimezoneSetting], fallback} {
		if name == "" || name == "Local" {
			continue
		}
		if loc, err := time.LoadLocation(name); err == nil {
			return loc, nil
		}
	}
	return time.UTC, nil
}
