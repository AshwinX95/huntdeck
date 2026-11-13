// Package huntscore rates how worth hunting each program is right now: its
// HuntScore, 0 to 100, from four parts.
//
//   - Fresh: new attack surface. Recently added in-scope assets, each
//     worth less as it ages (half every two weeks) and more when it pays,
//     is severe, is a wildcard or brings a root domain new to the program;
//     plus new invites, launches, reopenings and raised bounty tables.
//   - Competition: how few others are likely hunting it. Report volume for
//     the size of the scope, ranked against the platform's other programs,
//     with invite-only (private) programs well ahead.
//   - Payout: bounty amounts and what was paid lately, ranked within the
//     platform. Programs without bounties score 0 here, and their whole
//     score is scaled down (vdpFactor).
//   - Response: how quickly and reliably the program answers.
//
// Numbers are ranked within each platform, so units and coverage never mix
// across platforms. A part the platform gives nothing for counts as a bit
// below average (unknownScore), so known numbers win ties, and lowers the
// score's confidence. Every part lists its reasons.
// Ignored, paused and closed programs keep a score but are gated: never
// picked. Programs added by hand (store.Independent) aren't scored.
//
// Recently added in-scope targets get an asset score too (Targets): mostly
// how new they are, then what they're worth (kind, bounty, severity, a new
// root domain), whether other programs list them, and their program's
// competition, payout and response. Targets of gated programs aren't scored.
//
// Scoring reads only stored data and makes no requests.
package huntscore

import (
	"context"
	"fmt"
	"time"

	"github.com/mdpsec/huntdeck/internal/platform"
	"github.com/mdpsec/huntdeck/internal/store"
)

// Window is how far back new scope and events count.
const Window = 90 * 24 * time.Hour

// vdpFactor scales the score of programs without bounties: new scope there
// is worth hunting, but less than the same on a program that pays.
const vdpFactor = 0.75

// unknownScore is what a part scores when the platform gives nothing to
// judge it by.
const unknownScore = 45

// Part keys, in display order.
const (
	Fresh       = "fresh"
	Competition = "competition"
	Payout      = "payout"
	Response    = "response"
)

// Preset is a named weighting of the parts.
type Preset struct {
	Key, Label string
	Weights    map[string]float64
}

// Presets are the weightings to choose from; the first is the default.
var Presets = []Preset{
	{"balanced", "Balanced", map[string]float64{Fresh: 0.35, Competition: 0.30, Payout: 0.25, Response: 0.10}},
	{"fresh", "New surface", map[string]float64{Fresh: 0.55, Competition: 0.20, Payout: 0.15, Response: 0.10}},
	{"payout", "Big payouts", map[string]float64{Fresh: 0.20, Competition: 0.20, Payout: 0.50, Response: 0.10}},
	{"quiet", "Low competition", map[string]float64{Fresh: 0.25, Competition: 0.50, Payout: 0.15, Response: 0.10}},
}

// PresetSetting is the settings key holding the chosen preset.
const PresetSetting = "huntscore.preset"

// FindPreset returns the preset with key, or the default.
func FindPreset(key string) Preset {
	for _, p := range Presets {
		if p.Key == key {
			return p
		}
	}
	return Presets[0]
}

// Refresh scores every live program with the saved preset and stores the
// results.
func Refresh(ctx context.Context, st *store.Store, now time.Time) error {
	set, err := st.Settings(ctx, PresetSetting)
	if err != nil {
		return err
	}
	d, err := st.ScoreData(ctx, now.Add(-Window))
	if err != nil {
		return fmt.Errorf("huntscore: read: %w", err)
	}
	preset := FindPreset(set[PresetSetting])
	scores := Compute(d, now, preset)
	if err := st.SaveHuntScores(ctx, scores, ComputeTargets(d, scores, now)); err != nil {
		return fmt.Errorf("huntscore: save: %w", err)
	}
	return nil
}

// Compute scores every program in d.
func Compute(d store.ScoreData, now time.Time, preset Preset) []store.HuntScore {
	entries := map[int64][]store.ScoreEntry{}
	for _, e := range d.Recent {
		entries[e.ProgramID] = append(entries[e.ProgramID], e)
	}
	raised := map[int64]time.Time{}
	for _, c := range d.Rewards {
		if topAmount(c.New) > topAmount(c.Old) {
			raised[c.ProgramID] = c.At
		}
	}
	r := newRanks(d.Programs)
	out := make([]store.HuntScore, 0, len(d.Programs))
	for _, p := range d.Programs {
		if p.Platform == store.Independent {
			continue // added by hand: no stats to judge them by, and no platform to rank them within
		}
		parts := []store.HuntPart{
			fresh(p, entries[p.ID], raised[p.ID], now),
			competition(p, r, now),
			payout(p, r),
			response(p, r),
		}
		det := store.HuntDetail{Parts: parts, Preset: preset.Key, At: now.UTC()}
		var known, total float64
		for i := range det.Parts {
			w := preset.Weights[det.Parts[i].Key]
			det.Parts[i].Weight = w
			det.Parts[i].Score = round1(det.Parts[i].Score)
			det.Score += w * det.Parts[i].Score
			total += w
			if det.Parts[i].Known {
				known += w
			}
		}
		det.Score /= total
		if !p.OffersBounty {
			det.Score *= vdpFactor
		}
		det.Score = round1(det.Score)
		switch share := known / total; {
		case share >= 0.85:
			det.Confidence = "high"
		case share >= 0.6:
			det.Confidence = "medium"
		default:
			det.Confidence = "low"
		}
		switch {
		case p.Ignored:
			det.Gate = "ignored"
		case p.State != platform.StateOpen:
			det.Gate = p.State
		}
		out = append(out, store.HuntScore{ProgramID: p.ID, Detail: det})
	}
	return out
}
