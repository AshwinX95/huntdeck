package web

import (
	"encoding/json"
	"strconv"
	"strings"

	"github.com/mdpsec/huntdeck/internal/platform"
	"github.com/mdpsec/huntdeck/internal/store"
)

// rewardChange is one difference between two bounty tables: a row added
// or removed (Severity empty), or one severity's amount.
type rewardChange struct {
	Scope    string
	Severity string
	Before   string // "" when the row is new
	After    string // "" when the row was removed
}

var severities = []string{"critical", "high", "medium", "low"}

func severityRanges(r platform.RewardRow) []platform.RewardRange {
	return []platform.RewardRange{r.Critical, r.High, r.Medium, r.Low}
}

// rewardsDiff compares two bounty tables (platform.RewardRow lists as
// JSON), matching rows by the asset they cover.
func rewardsDiff(oldJSON, newJSON, currency string) []rewardChange {
	var before, after []platform.RewardRow
	_ = json.Unmarshal([]byte(oldJSON), &before) // bad JSON reads as no table
	_ = json.Unmarshal([]byte(newJSON), &after)
	keyed := func(rows []platform.RewardRow) ([]string, map[string]platform.RewardRow) {
		var keys []string
		m := map[string]platform.RewardRow{}
		seen := map[string]int{}
		for _, r := range rows {
			k := r.Scope
			if seen[r.Scope]++; seen[r.Scope] > 1 {
				k += "\x00" + strconv.Itoa(seen[r.Scope])
			}
			keys = append(keys, k)
			m[k] = r
		}
		return keys, m
	}
	oldKeys, oldRows := keyed(before)
	newKeys, newRows := keyed(after)
	label := func(r platform.RewardRow) string {
		if r.Scope == "" {
			return "All assets"
		}
		return r.Scope
	}
	var out []rewardChange
	for _, k := range newKeys {
		n := newRows[k]
		o, ok := oldRows[k]
		if !ok {
			out = append(out, rewardChange{Scope: label(n), After: rowSummary(n, currency)})
			continue
		}
		on, nn := severityRanges(o), severityRanges(n)
		for i, sev := range severities {
			if on[i] != nn[i] {
				before, after := rewardText(on[i], currency, false), rewardText(nn[i], currency, false)
				if before == after {
					before, after = rewardText(on[i], currency, true), rewardText(nn[i], currency, true)
				}
				out = append(out, rewardChange{Scope: label(n), Severity: sev, Before: before, After: after})
			}
		}
	}
	for _, k := range oldKeys {
		if _, ok := newRows[k]; !ok {
			out = append(out, rewardChange{Scope: label(oldRows[k]), Before: rowSummary(oldRows[k], currency)})
		}
	}
	return out
}

// rewardText is a severity's range in a bounty table change, compact as
// the table shows it; full gives the amounts to the unit, for a change
// the compact ones would hide ("$5k → $5k").
func rewardText(r platform.RewardRange, currency string, full bool) string {
	switch {
	case r.Max <= 0:
		return "none"
	case full:
		return rewardFull(r.Min, r.Max, currency)
	}
	return reward(r.Min, r.Max, currency)
}

// rowSummary describes a whole row: "critical $5k · high $2k".
func rowSummary(r platform.RewardRow, currency string) string {
	var parts []string
	for i, rr := range severityRanges(r) {
		if rr.Max > 0 {
			parts = append(parts, severities[i]+" "+rewardText(rr, currency, false))
		}
	}
	if len(parts) == 0 {
		return "no bounty"
	}
	return strings.Join(parts, " · ")
}

// describeReward is one reward change as a short phrase.
func describeReward(c rewardChange) string {
	switch {
	case c.Before == "":
		return "new row " + c.Scope + ": " + c.After
	case c.After == "":
		return "row removed " + c.Scope
	}
	return c.Scope + " " + c.Severity + ": " + c.Before + " → " + c.After
}

// ruleLabels name the texts a policy change can be about.
var ruleLabels = map[string]string{
	store.FieldPolicy:     "Policy",
	store.FieldCampaign:   "Reward notes",
	store.FieldExclusions: "Program exclusions",
	store.FieldInScope:    "In-scope notes",
	store.FieldOutScope:   "Out of scope",
	store.FieldSeverity:   "Severity assessment",
	store.FieldFAQ:        "FAQ",
}

// describeRules is the feed line for a policy or bounty table change.
func describeRules(c store.ChangeView) string {
	if c.Kind == store.ChangeRewardsChanged {
		diff := rewardsDiff(c.OldValue, c.NewValue, c.Currency)
		switch len(diff) {
		case 0:
			return "Bounty table reordered"
		case 1:
			return "Bounty table: " + describeReward(diff[0])
		}
		return "Bounty table: " + describeReward(diff[0]) + " (+" + strconv.Itoa(len(diff)-1) + " more)"
	}
	label := ruleLabels[c.Field]
	if label == "" {
		label = c.Field
	}
	switch {
	case strings.TrimSpace(c.OldValue) == "":
		return label + " added"
	case strings.TrimSpace(c.NewValue) == "":
		return label + " removed"
	}
	_, added, removed := textDiff(c.OldValue, c.NewValue)
	return label + " edited (" + lineCount("+", added) + ", " + lineCount("−", removed) + ")"
}

func lineCount(sign string, n int) string {
	if n == 1 {
		return sign + "1 line"
	}
	return sign + strconv.Itoa(n) + " lines"
}

// hasDetail reports whether a change has its own page: a diff, or an
// announcement's text.
func hasDetail(kind string) bool {
	return kind == store.ChangePolicyChanged || kind == store.ChangeRewardsChanged || kind == store.ChangeAnnouncement
}
