package huntscore

import (
	"fmt"
	"math"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/mdpsec/huntdeck/internal/scope"
	"github.com/mdpsec/huntdeck/internal/store"
)

// ComputeTargets scores the targets of recently added entries, and of
// entries new to the user (in scope when they got access to a private
// program), given their programs' scores.
func ComputeTargets(d store.ScoreData, scores []store.HuntScore, now time.Time) []store.TargetScore {
	progs := map[int64]store.ScoreProgram{}
	for _, p := range d.Programs {
		progs[p.ID] = p
	}
	details := map[int64]store.HuntDetail{}
	for _, sc := range scores {
		details[sc.ProgramID] = sc.Detail
	}
	var out []store.TargetScore
	for _, e := range slices.Concat(d.Recent, d.Joined) {
		p, ok := progs[e.ProgramID]
		det, scored := details[e.ProgramID]
		if !ok || !scored || det.Gate != "" {
			continue
		}
		pv, pReason := programValue(det)
		for _, t := range e.Targets {
			out = append(out, targetScore(p, e, t, pv, pReason, now))
		}
	}
	return out
}

// targetScore rates one target: how new it is (to the program, or to the
// user for a joined entry), scaled by what it's worth (asset), how few
// programs list it (unique) and its program's value apart from freshness,
// which the target's own age stands in for.
func targetScore(p store.ScoreProgram, e store.ScoreEntry, t store.ScoreTarget, programValue float64, programReason string, now time.Time) store.TargetScore {
	age := max(0, now.Sub(e.AddedAt))
	asset := targetWeight(t.Kind) * severityWeight(e.MaxSeverity) / 1.2
	var reasons []string
	if e.Joined {
		reasons = append(reasons, kindLabels[t.Kind]+" new to you: in scope when you joined "+ago(age))
	} else {
		reasons = append(reasons, kindLabels[t.Kind]+" added "+ago(age))
	}
	switch {
	case !p.OffersBounty:
		asset *= 0.5
		reasons = append(reasons, "Program pays no bounties")
	case !e.Bounty:
		asset *= 0.4
		reasons = append(reasons, "Not bounty-eligible")
	case e.MaxSeverity != "" && !strings.EqualFold(e.MaxSeverity, "none"):
		reasons = append(reasons, "Bounty-eligible up to "+strings.ToLower(e.MaxSeverity))
	default:
		reasons = append(reasons, "Bounty-eligible")
	}
	if e.NewRoot {
		asset *= 1.3
		reasons = append(reasons, "Root domain new to the program")
	}
	unique := 1.0
	switch t.Shared {
	case 0:
		reasons = append(reasons, "No other program lists it")
	case 1:
		unique = 0.5
		reasons = append(reasons, "Also listed by 1 other program")
	default:
		unique = 0.25
		reasons = append(reasons, "Also listed by "+strconv.Itoa(t.Shared)+" other programs")
	}
	reasons = append(reasons, programReason)
	score := 100 * decay(age, 28) * (0.35*math.Min(1, asset) + 0.25*unique + 0.40*programValue)
	if !p.OffersBounty {
		score *= vdpFactor
	}
	return store.TargetScore{ProgramID: p.ID, Kind: t.Kind, Value: t.Value, Apex: t.Apex, Bounty: p.OffersBounty && e.Bounty,
		AddedAt: e.AddedAt, Joined: e.Joined, Score: round1(score), Reasons: reasons}
}

// programValue is a program's score without its Fresh part, 0–1, and a
// reason saying so.
func programValue(d store.HuntDetail) (float64, string) {
	var sum, total float64
	var parts []string
	for _, p := range d.Parts {
		if p.Key == Fresh {
			continue
		}
		sum += p.Weight * p.Score
		total += p.Weight
		parts = append(parts, fmt.Sprintf("%s %.0f", strings.ToLower(partLabels[p.Key]), p.Score))
	}
	if total == 0 {
		return 0, ""
	}
	return sum / total / 100, fmt.Sprintf("Program HuntScore %.0f (%s)", d.Score, strings.Join(parts, ", "))
}

var kindLabels = map[string]string{scope.KindWildcard: "Wildcard", scope.KindDomain: "Domain", scope.KindURL: "URL",
	scope.KindIP: "IP", scope.KindCIDR: "IP range", scope.AssetAndroid: "Android app", scope.AssetIOS: "iOS app"}

// targetWeight values a target by kind: a wildcard is a whole subdomain
// space, a URL one application.
func targetWeight(kind string) float64 {
	switch kind {
	case scope.KindWildcard:
		return 1
	case scope.KindCIDR:
		return 0.7
	case scope.KindDomain:
		return 0.6
	}
	return 0.45
}

// decay is a weight halving every halfLife days.
func decay(age time.Duration, halfLife float64) float64 {
	return math.Pow(0.5, math.Max(0, age.Hours()/24)/halfLife)
}

// saturate maps points onto 0–100, flattening as they grow.
func saturate(points, scale float64) float64 {
	return 100 * (1 - math.Exp(-points/scale))
}
