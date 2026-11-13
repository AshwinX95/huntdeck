package sessionjar

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Claim ties a login's saved state (the files and directories in paths:
// its jar, browser profile and the like) to account, so another account
// never picks up its session. marker records whose they are, as a hash of
// the account name. When it names another account, or is missing (whose
// they are is unknown), paths are deleted and the next login starts
// afresh.
func Claim(marker, account string, paths ...string) (reset bool, err error) {
	sum := sha256.Sum256([]byte(strings.ToLower(strings.TrimSpace(account))))
	want := hex.EncodeToString(sum[:])
	got, err := os.ReadFile(marker) //nolint:gosec // a path in the data directory
	if err == nil && strings.TrimSpace(string(got)) == want {
		return false, nil
	}
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return false, err
	}
	for _, p := range paths {
		if _, err := os.Lstat(p); err == nil {
			reset = true
		}
		if err := os.RemoveAll(p); err != nil {
			return reset, fmt.Errorf("clear the previous account's session: %w", err)
		}
	}
	if err := os.MkdirAll(filepath.Dir(marker), 0o700); err != nil {
		return reset, err
	}
	return reset, os.WriteFile(marker, []byte(want+"\n"), 0o600)
}
