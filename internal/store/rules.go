package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"

	"github.com/mdpsec/huntdeck/internal/platform"
)

// SetPolicy stores a program's rules.
func (t *Tx) SetPolicy(ctx context.Context, programID int64, policy string) error {
	_, err := t.tx.ExecContext(ctx, `UPDATE programs SET policy = ? WHERE id = ?`, policy, programID)
	return err
}

// SetTesting stores a program's rules for test traffic.
func (t *Tx) SetTesting(ctx context.Context, programID int64, r platform.TestingRules) error {
	var rate *float64
	if r.RateLimit > 0 {
		rate = &r.RateLimit
	}
	_, err := t.tx.ExecContext(ctx, `UPDATE programs SET testing_known = 1, test_rate_limit = ?, test_no_automation = ?,
			test_user_agent = ?, test_header = ?, test_platform_email = ?
		WHERE id = ?`, rate, b2i(r.NoAutomation), r.UserAgent, r.Header, b2i(r.PlatformEmail), programID)
	return err
}

// SetRewards stores a program's current bounty table.
func (t *Tx) SetRewards(ctx context.Context, programID int64, rewards []platform.RewardRow) error {
	b, err := json.Marshal(rewards)
	if err != nil {
		return err
	}
	if rewards == nil {
		b = []byte("[]")
	}
	_, err = t.tx.ExecContext(ctx, `UPDATE programs SET rewards = ? WHERE id = ?`, string(b), programID)
	return err
}

// KnownRules is what HuntDeck last stored of a program's rules, to compare
// newly fetched ones with.
type KnownRules struct {
	Rewards      []platform.RewardRow
	RewardsKnown bool // false until a platform reported a bounty table
	Profile      platform.ProgramProfile
	ProfileKnown bool // false until stats were first stored
}

// KnownRules returns a program's stored bounty table and profile.
func (t *Tx) KnownRules(ctx context.Context, programID int64) (KnownRules, error) {
	var k KnownRules
	var rewards sql.NullString
	var profile string
	if err := t.tx.QueryRowContext(ctx, `SELECT rewards, profile, stats_at IS NOT NULL FROM programs WHERE id = ?`, programID).
		Scan(&rewards, &profile, &k.ProfileKnown); err != nil {
		return k, err
	}
	if rewards.Valid {
		k.RewardsKnown = true
		if err := json.Unmarshal([]byte(rewards.String), &k.Rewards); err != nil {
			return k, fmt.Errorf("rewards: %w", err)
		}
	}
	if err := json.Unmarshal([]byte(profile), &k.Profile); err != nil {
		return k, fmt.Errorf("profile: %w", err)
	}
	return k, nil
}
