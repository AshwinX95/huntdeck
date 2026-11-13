package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"time"

	"github.com/mdpsec/huntdeck/internal/platform"
)

// What notifications read about changes: a poll's, and flip-flopping
// programs' pauses once they've lasted.

// NotifyChange is a recorded change with what notifications need about its
// program and scope entry.
type NotifyChange struct {
	ID         int64
	RunID      int64 // the poll that recorded it (0 if gone)
	OccurredAt time.Time
	Kind       string
	Field      string
	OldValue   string
	NewValue   string
	Program    NotifyProgram
	Entry      *NotifyEntry // for scope changes
	// For state changes: how many its program had in the FlipWindow up to
	// and including it (FlipFlops or more is flip-flopping), and when the
	// one before it was.
	Flips       int
	PrevStateAt *time.Time
	// LongPause marks a flip-flopping program's pause, held back until it
	// had lasted a while (see LongPauses).
	LongPause bool
}

// NotifyProgram is a program as notifications describe and filter it.
type NotifyProgram struct {
	ProgramRef
	OffersBounty bool
	Ignored      bool
	Tags         []string
	HuntScore    *float64
	TopBounty    *float64 // highest bounty in its table or stats, if known
	Currency     string
}

// NotifyEntry is the scope entry a change is about.
type NotifyEntry struct {
	Identifier  string
	AssetType   string
	InScope     bool
	Bounty      bool
	MaxSeverity string
	NewRoot     bool // brought a root domain the program didn't have
}

// NotifyChanges returns the changes a poll recorded, in order, but for
// those that count as already notified (old news).
func (s *Store) NotifyChanges(ctx context.Context, runID int64) ([]NotifyChange, error) {
	return s.notifyChanges(ctx, `c.poll_run_id = ? AND c.notified_at IS NULL`, runID)
}

// NotifyChangesSince returns every change recorded since a time, in order,
// notified or not, for previews of what a channel would have been sent.
// It's cached until the data changes.
func (s *Store) NotifyChangesSince(ctx context.Context, since time.Time) ([]NotifyChange, error) {
	key := "notify-since:" + ts(since)
	return cached(ctx, s, key, func() ([]NotifyChange, error) {
		return s.notifyChanges(ctx, `c.occurred_at >= ?`, ts(since))
	})
}

// notifyChanges returns the changes where (on changes c, programs p) says,
// in order.
func (s *Store) notifyChanges(ctx context.Context, where string, args ...any) ([]NotifyChange, error) {
	//nolint:gosec // constant fragments; values are bound parameters
	rows, err := s.db.R.QueryContext(ctx, `
		SELECT c.id, COALESCE(c.poll_run_id, 0), c.occurred_at, c.kind, c.field, c.old_value, c.new_value,
			CASE WHEN c.kind = 'state_changed' THEN `+flipsUpTo+` ELSE 0 END,
			(SELECT MAX(f.occurred_at) FROM changes f WHERE c.kind = 'state_changed' AND f.program_id = c.program_id
				AND f.kind = 'state_changed' AND (f.occurred_at < c.occurred_at OR (f.occurred_at = c.occurred_at AND f.id < c.id))),
			p.id, p.platform, p.handle, p.name, p.url, p.private, p.state, p.offers_bounty, p.ignored, `+tagsColumn+`,
			p.hunt_score, p.rewards, `+topBountySQL+`, COALESCE(ps.currency, p.currency),
			e.id IS NOT NULL, COALESCE(e.identifier, ''), COALESCE(e.asset_type, ''), COALESCE(e.in_scope, 0),
			COALESCE(e.bounty_eligible, 0), COALESCE(e.max_severity, ''),
			e.id IS NOT NULL AND EXISTS (SELECT 1 FROM scope_targets t WHERE t.scope_entry_id = e.id AND t.kind IN `+webKinds+` AND t.apex <> ''
				AND NOT EXISTS (SELECT 1 FROM scope_targets t2 JOIN scope_entries e2 ON e2.id = t2.scope_entry_id
					WHERE t2.program_id = t.program_id AND t2.kind IN `+webKinds+` AND t2.apex <> '' AND t2.apex = t.apex
						AND e2.id <> e.id AND e2.in_scope = 1 AND e2.first_seen_at < e.first_seen_at))
		FROM changes c
		JOIN programs p ON p.id = c.program_id
		LEFT JOIN program_public_stats ps ON ps.program_id = p.id AND ps.captured_at = p.stats_at
		LEFT JOIN scope_entries e ON e.id = c.scope_entry_id
		WHERE `+where+`
		ORDER BY c.id`, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []NotifyChange
	for rows.Next() {
		var c NotifyChange
		var e NotifyEntry
		var occurred, tags string
		var rewards, prev sql.NullString
		var hunt, statsTop sql.NullFloat64
		var hasEntry bool
		p := &c.Program
		if err := rows.Scan(&c.ID, &c.RunID, &occurred, &c.Kind, &c.Field, &c.OldValue, &c.NewValue, &c.Flips, &prev,
			&p.ID, &p.Platform, &p.Handle, &p.Name, &p.URL, &p.Private, &p.State, &p.OffersBounty, &p.Ignored, &tags,
			&hunt, &rewards, &statsTop, &p.Currency,
			&hasEntry, &e.Identifier, &e.AssetType, &e.InScope, &e.Bounty, &e.MaxSeverity, &e.NewRoot); err != nil {
			return nil, err
		}
		c.OccurredAt, c.PrevStateAt = parseTS(occurred), parseTSPtr(prev)
		p.Tags, p.HuntScore = splitTags(tags), floatPtr(hunt)
		var table []platform.RewardRow
		if rewards.Valid {
			_ = json.Unmarshal([]byte(rewards.String), &table) // a bad table reads as none
		}
		p.TopBounty = topBounty(table, floatPtr(statsTop))
		if hasEntry {
			c.Entry = &e
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// topBountySQL is the larger of a program's stats' top bounty (ps) and its
// listing's (p), NULL if neither gives one: with the bounty table
// (topBounty), the one "top bounty" every summary shows (why it scores,
// top picks, notifications).
const topBountySQL = `NULLIF(MAX(COALESCE(ps.top_bounty_high, 0), COALESCE(p.bounty_max, 0)), 0)`

// topBounty is the highest amount in a bounty table or top (from
// topBountySQL), or nil if neither says.
func topBounty(table []platform.RewardRow, statsTop *float64) *float64 {
	top := 0.0
	known := false
	for _, r := range table {
		for _, v := range []float64{r.Low.Max, r.Medium.Max, r.High.Max, r.Critical.Max} {
			top, known = max(top, v), true
		}
	}
	if statsTop != nil {
		top, known = max(top, *statsTop), true
	}
	if !known {
		return nil
	}
	return &top
}

// LongPauses are flip-flopping programs' pauses (see FlipFlops), held back
// from notifications when they happened, that have now lasted since before
// lasted (and began after since): each its program's latest state change,
// still in force and not yet notified, from a poll already notified.
func (s *Store) LongPauses(ctx context.Context, lasted, since time.Time) ([]NotifyChange, error) {
	out, err := s.notifyChanges(ctx, `c.kind = 'state_changed' AND c.new_value <> 'open' AND c.new_value = p.state
		AND c.notified_at IS NULL AND c.occurred_at <= ? AND c.occurred_at > ?
		AND EXISTS (SELECT 1 FROM poll_runs r WHERE r.id = c.poll_run_id AND r.notified_at IS NOT NULL)
		AND NOT EXISTS (SELECT 1 FROM changes l WHERE l.program_id = c.program_id AND l.kind = 'state_changed'
			AND (l.occurred_at > c.occurred_at OR (l.occurred_at = c.occurred_at AND l.id > c.id)))
		AND `+flipsUpTo+` >= 3`, ts(lasted), ts(since)) // FlipFlops, as TestFlipSQL checks
	for i := range out {
		out[i].LongPause = true
	}
	return out, err
}

// QueueLongPauses queues the notifications for long pauses and marks the
// changes notified, in one transaction, so each is notified once.
func (s *Store) QueueLongPauses(ctx context.Context, changeIDs []int64, outbox []OutboxMessage, pending []PendingEvent) error {
	return s.Write(ctx, func(tx *Tx) error {
		if err := tx.queue(ctx, outbox, pending); err != nil {
			return err
		}
		for _, id := range changeIDs {
			if _, err := tx.tx.ExecContext(ctx, `UPDATE changes SET notified_at = ? WHERE id = ?`, tx.now, id); err != nil {
				return err
			}
		}
		return nil
	})
}
