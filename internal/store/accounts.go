package store

import (
	"context"
	"database/sql"
	"errors"
)

// accountKey is the settings key holding which account a platform's
// programs were last fully listed for: a hash, never the name itself.
func accountKey(platformName string) string { return "account." + platformName }

// ListedAccount returns the account hash SetListedAccount stored for
// platform, "" if none has been.
func (t *Tx) ListedAccount(ctx context.Context, platformName string) (string, error) {
	var v string
	err := t.tx.QueryRowContext(ctx, `SELECT value FROM settings WHERE key = ?`, accountKey(platformName)).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return v, err
}

// SetListedAccount records the hash of the account a complete listing of
// platform came from.
func (t *Tx) SetListedAccount(ctx context.Context, platformName, hash string) error {
	return t.PutSetting(ctx, accountKey(platformName), hash)
}
