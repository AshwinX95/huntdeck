package syncer

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"time"

	"github.com/mdpsec/huntdeck/internal/platform"
	"github.com/mdpsec/huntdeck/internal/store"
)

// policyChange records an edit to a program's policy. A policy only counts
// as changed when both sides are known: an empty one means the response
// didn't carry it, and the first one seen is a baseline.
func policyChange(old, cur string) []store.Change {
	if old == "" || cur == "" || normText(old) == normText(cur) {
		return nil
	}
	return []store.Change{{Kind: store.ChangePolicyChanged, Field: store.FieldPolicy, OldValue: old, NewValue: cur}}
}

// rulesChanges compares newly fetched stats with what was stored: the
// bounty table, the reward notes and the program's own exclusions.
// Nothing is recorded against values never stored before.
func rulesChanges(prev store.KnownRules, st platform.ProgramStats) []store.Change {
	out := rewardsChange(prev, st.Rewards)
	if !prev.ProfileKnown {
		return out
	}
	texts := []struct{ field, old, cur string }{
		{store.FieldCampaign, prev.Profile.Campaign, st.Profile.Campaign},
		{store.FieldExclusions, exclusionsText(prev.Profile.ScopeExclusions), exclusionsText(st.Profile.ScopeExclusions)},
	}
	for _, t := range texts {
		if normText(t.old) != normText(t.cur) {
			out = append(out, store.Change{Kind: store.ChangePolicyChanged, Field: t.field, OldValue: t.old, NewValue: t.cur})
		}
	}
	// Brief sections count like the policy: only when both sides are known,
	// so the first time they're read isn't a change.
	for _, t := range []struct{ field, old, cur string }{
		{store.FieldInScope, prev.Profile.InScope, st.Profile.InScope},
		{store.FieldOutScope, prev.Profile.OutScope, st.Profile.OutScope},
		{store.FieldSeverity, prev.Profile.Severity, st.Profile.Severity},
		{store.FieldFAQ, prev.Profile.FAQ, st.Profile.FAQ},
	} {
		if c := policyChange(t.old, t.cur); c != nil {
			c[0].Field = t.field
			out = append(out, c...)
		}
	}
	return out
}

// rewardsChange records a new bounty table, if the old one is known and
// the platform reported one (nil: it didn't say).
func rewardsChange(prev store.KnownRules, rewards []platform.RewardRow) []store.Change {
	if !prev.RewardsKnown || rewards == nil || sameRewards(prev.Rewards, rewards) {
		return nil
	}
	return []store.Change{{Kind: store.ChangeRewardsChanged, Field: "rewards",
		OldValue: rewardsJSON(prev.Rewards), NewValue: rewardsJSON(rewards)}}
}

// stateNewsAge is how old a state change found in a brief can be and still
// be news worth notifying.
const stateNewsAge = 48 * time.Hour

// recordDetail stores the rules and bounty table that came with a scope
// download, recording what changed.
func recordDetail(ctx context.Context, tx *store.Tx, runID int64, row *store.ProgramRow, d platform.ScopeDetail) error {
	var changes []store.Change
	if d.Policy != "" {
		changes = policyChange(row.Policy, d.Policy)
		if err := tx.SetPolicy(ctx, row.ID, d.Policy); err != nil {
			return err
		}
	}
	if d.Rewards != nil {
		prev, err := tx.KnownRules(ctx, row.ID)
		if err != nil {
			return err
		}
		changes = append(changes, rewardsChange(prev, d.Rewards)...)
		if err := tx.SetRewards(ctx, row.ID, d.Rewards); err != nil {
			return err
		}
	}
	if d.Testing != nil {
		if err := tx.SetTesting(ctx, row.ID, *d.Testing); err != nil {
			return err
		}
	}
	if d.Facts != nil {
		if err := tx.SetFacts(ctx, row.ID, *d.Facts); err != nil {
			return err
		}
	}
	if d.Requirements != nil {
		if err := tx.SetRequirements(ctx, row.ID, *d.Requirements); err != nil {
			return err
		}
	}
	if d.State != "" && d.State != row.State {
		// Dated when the brief version showing it was published; one found
		// late (stateNewsAge) counts as already notified.
		at := tx.Now()
		if d.StateSince != nil && d.StateSince.Before(at) {
			at = *d.StateSince
		}
		if err := tx.SetState(ctx, row.ID, runID, row.State, d.State, at, tx.Now().Sub(at) > stateNewsAge); err != nil {
			return err
		}
		row.State = d.State
	}
	for _, c := range changes {
		c.PollRunID, c.ProgramID = runID, row.ID
		if err := tx.AddChange(ctx, c); err != nil {
			return err
		}
	}
	return nil
}

// normText ignores line endings and trailing spaces, which platforms
// change without anyone editing anything.
func normText(s string) string {
	lines := strings.Split(strings.ReplaceAll(s, "\r\n", "\n"), "\n")
	for i, l := range lines {
		lines[i] = strings.TrimRight(l, " \t")
	}
	return strings.TrimSpace(strings.Join(lines, "\n"))
}

// exclusionsText is a program's exclusions as one line each.
func exclusionsText(ex []platform.Exclusion) string {
	lines := make([]string, len(ex))
	for i, e := range ex {
		lines[i] = e.Name
		if e.Details != "" {
			lines[i] += ": " + strings.Join(strings.Fields(e.Details), " ")
		}
	}
	return strings.Join(lines, "\n")
}

// sameRewards compares bounty tables ignoring row order, which some
// platforms don't keep stable.
func sameRewards(a, b []platform.RewardRow) bool {
	key := func(rows []platform.RewardRow) []string {
		out := make([]string, len(rows))
		for i, r := range rows {
			out[i] = rewardsJSON([]platform.RewardRow{r})
		}
		slices.Sort(out)
		return out
	}
	return slices.Equal(key(a), key(b))
}

func rewardsJSON(rows []platform.RewardRow) string {
	if rows == nil {
		rows = []platform.RewardRow{}
	}
	b, err := json.Marshal(rows)
	if err != nil { // plain structs of strings and numbers always marshal
		return "[]"
	}
	return string(b)
}
