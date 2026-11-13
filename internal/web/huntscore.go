package web

import (
	"fmt"
	"html/template"
	"math"
	"regexp"
	"strings"

	"github.com/mdpsec/huntdeck/internal/huntscore"
	"github.com/mdpsec/huntdeck/internal/store"
)

// huntLabels name a HuntScore's parts.
var huntLabels = map[string]string{
	huntscore.Fresh:       "Fresh",
	huntscore.Competition: "Competition",
	huntscore.Payout:      "Payout",
	huntscore.Response:    "Response",
}

// huntHints say what each part measures.
var huntHints = map[string]string{
	huntscore.Fresh:       "New attack surface: recently added assets, invites, launches, reopenings, raised bounties",
	huntscore.Competition: "How few others are likely hunting it: report volume for the scope's size, invite-only programs",
	huntscore.Payout:      "Bounty amounts and recent payouts, compared with the platform's other programs",
	huntscore.Response:    "How quickly and reliably the program answers",
}

// huntGates explain why a scored program isn't picked.
var huntGates = map[string]string{
	"ignored": "Ignored by you",
	"paused":  "Paused: not accepting reports",
	"closed":  "Closed: not accepting reports",
}

// huntBand is a CSS class for a score: hi, mid or lo.
func huntBand(score float64) string {
	switch {
	case score >= 55:
		return "hi"
	case score >= 40:
		return "mid"
	}
	return "lo"
}

// huntTitle is a one-line summary of a score's parts, for tooltips.
func huntTitle(d *store.HuntDetail) string {
	if d == nil {
		return ""
	}
	var parts []string
	for _, p := range d.Parts {
		s := fmt.Sprintf("%s %.0f", huntLabels[p.Key], p.Score)
		if !p.Known {
			s += " (unknown)"
		}
		parts = append(parts, s)
	}
	t := "HuntScore " + fmt.Sprintf("%.0f", d.Score) + ": " + strings.Join(parts, " · ") + " · " + d.Confidence + " confidence"
	if g := huntGates[d.Gate]; g != "" {
		t += " · " + g
	}
	return t
}

// huntWeights describes a preset's weights, e.g. "Fresh 35% · Competition 30% · …".
func huntWeights(p huntscore.Preset) string {
	var out []string
	for _, k := range []string{huntscore.Fresh, huntscore.Competition, huntscore.Payout, huntscore.Response} {
		out = append(out, fmt.Sprintf("%s %.0f%%", huntLabels[k], p.Weights[k]*100))
	}
	return strings.Join(out, " · ")
}

// huntBar is one part's bar in a mini chart.
type huntBar struct {
	X, Y, H float64
	Key     string
	Title   string
}

// huntBars draws a score's parts as four small bars (viewBox 0 0 28 14).
func huntBars(d *store.HuntDetail) []huntBar {
	if d == nil {
		return nil
	}
	const w, h, gap = 5.5, 14.0, 2.0
	var out []huntBar
	for i, p := range d.Parts {
		bh := math.Max(1, math.Round(p.Score/100*h*10)/10)
		out = append(out, huntBar{X: float64(i) * (w + gap), Y: h - bh, H: bh, Key: p.Key,
			Title: fmt.Sprintf("%s %.0f", huntLabels[p.Key], p.Score)})
	}
	return out
}

// huntComparison finds where a HuntScore reason compares the program with
// the platform's others: "top 17% on HackerOne", "busier than 95% of
// HackerOne programs".
var huntComparison = regexp.MustCompile(`(top \d+% on [\w.-]+|(?:busier|quieter) than \d+% of [\w.-]+ programs)`)

// huntReason is a HuntScore reason with its comparison in bold, since
// that's what the program page adds to the numbers above it.
func huntReason(r string) template.HTML {
	esc := template.HTMLEscapeString(r)
	return template.HTML(huntComparison.ReplaceAllString(esc, "<b>$1</b>")) //nolint:gosec // escaped above; the pattern adds only <b>
}
