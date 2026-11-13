package huntscore

import (
	"strconv"
	"strings"
	"time"

	"github.com/mdpsec/huntdeck/internal/scope"
	"github.com/mdpsec/huntdeck/internal/store"
)

func fresh(p store.ScoreProgram, entries []store.ScoreEntry, raisedAt time.Time, now time.Time) store.HuntPart {
	part := store.HuntPart{Key: Fresh, Known: true}
	var points float64
	var week, month, wild, roots int
	var newest *store.ScoreEntry
	for i, e := range entries {
		age := now.Sub(e.AddedAt)
		w := decay(age, 14) * assetWeight(e.AssetType)
		switch {
		case !p.OffersBounty:
			w *= 0.5
		case !e.Bounty:
			w *= 0.4
		}
		w *= severityWeight(e.MaxSeverity)
		if e.NewRoot {
			w *= 1.5
		}
		points += w
		if age <= 7*24*time.Hour {
			week++
		}
		if age <= 30*24*time.Hour {
			month++
			if e.AssetType == scope.AssetWildcard {
				wild++
			}
			if e.NewRoot {
				roots++
			}
		}
		if newest == nil && !scope.HiddenName(entries[i].Identifier) {
			newest = &entries[i]
		}
	}
	if month > 0 {
		var extra []string
		if wild > 0 {
			extra = append(extra, plural(wild, "wildcard", "wildcards"))
		}
		if roots > 0 {
			extra = append(extra, plural(roots, "new root domain", "new root domains"))
		}
		r := plural(month, "asset", "assets") + " added in the last 30 days"
		if week > 0 && week < month {
			r += " (" + strconv.Itoa(week) + " this week)"
		}
		if len(extra) > 0 {
			r += ": " + strings.Join(extra, ", ")
		}
		part.Reasons = append(part.Reasons, r)
	}
	if newest != nil {
		part.Reasons = append(part.Reasons, "Newest: "+newest.Identifier+", "+ago(now.Sub(newest.AddedAt)))
	}
	event := func(at *time.Time, within time.Duration, halfLife, weight float64, what string) {
		if at == nil || now.Sub(*at) > within || at.After(now) {
			return
		}
		points += weight * decay(now.Sub(*at), halfLife)
		part.Reasons = append(part.Reasons, what+" "+ago(now.Sub(*at)))
	}
	invited := p.InvitedAt
	if invited == nil {
		invited = p.JoinedAt
	}
	if invited == nil && p.NewPrivate {
		invited = p.NewAt
	}
	if p.Private {
		event(invited, 30*24*time.Hour, 14, 8, "Invited")
	}
	launched := p.StartedAt
	if p.NewAt != nil && !p.NewPrivate {
		launched = p.NewAt
	}
	event(launched, 30*24*time.Hour, 14, 6, "New program, launched")
	event(p.ResumedAt, 14*24*time.Hour, 7, 4, "Reopened")
	if !raisedAt.IsZero() {
		event(&raisedAt, 30*24*time.Hour, 14, 5, "Bounty table raised")
	}
	part.Score = saturate(points, 8)
	if len(part.Reasons) == 0 {
		part.Reasons = []string{"No new scope in the last 90 days"}
	}
	return part
}

func assetWeight(assetType string) float64 {
	switch assetType {
	case scope.AssetWildcard:
		return 3
	case scope.AssetDomain:
		return 1.5
	case scope.AssetURL, scope.AssetAPI, scope.AssetIP, scope.AssetAndroid, scope.AssetIOS:
		return 1
	case scope.AssetCIDR:
		return 2
	}
	return 0.5
}

// severityWeight values an asset by the worst severity it pays for; most
// assets don't say, which counts as high.
func severityWeight(sev string) float64 {
	switch strings.ToLower(sev) {
	case "critical":
		return 1.2
	case "medium":
		return 0.8
	case "low":
		return 0.6
	case "none":
		return 0.3
	}
	return 1
}
