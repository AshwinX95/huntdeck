package huntscore

import (
	"fmt"
	"math"
	"slices"
	"sort"
	"strconv"
	"time"

	"github.com/mdpsec/huntdeck/internal/humanize"
	"github.com/mdpsec/huntdeck/internal/platform"
	"github.com/mdpsec/huntdeck/internal/store"
)

// volume is how many reports a program got in about 90 days, from
// whatever the platform shares, and what that is.
func volume(p store.ScoreProgram) (float64, string, bool) {
	switch {
	case p.Reports90d != nil:
		return float64(*p.Reports90d), strconv.Itoa(*p.Reports90d) + " reports in 90 days", true
	case p.Accepted90d != nil:
		return float64(*p.Accepted90d), strconv.Itoa(*p.Accepted90d) + " accepted reports in 90 days", true
	case p.Rewarded90d != nil:
		// A program keeping CrowdStream private: the rise in its count of
		// rewarded reports, the nearest to accepted reports it shares.
		n := strconv.Itoa(int(math.Round(*p.Rewarded90d)))
		if p.RewardedDays != nil && *p.RewardedDays < 90 {
			return *p.Rewarded90d, "≈" + n + " rewarded reports in 90 days (from " + strconv.Itoa(*p.RewardedDays) + " days of saved counts)", true
		}
		return *p.Rewarded90d, n + " rewarded reports in 90 days", true
	case p.Reports7d != nil:
		return float64(*p.Reports7d) * 90 / 7, strconv.Itoa(*p.Reports7d) + " reports in the last 7 days", true
	case p.ReportsMonth != nil:
		return float64(*p.ReportsMonth) * 3, strconv.Itoa(*p.ReportsMonth) + " reports this month", true
	}
	return 0, "", false
}

// crowding is report volume for the size of the scope: wildcards count
// most, and big scopes count less than linearly (hunters cluster).
func crowding(p store.ScoreProgram) (float64, bool) {
	v, _, ok := volume(p)
	if !ok {
		return 0, false
	}
	surface := 1 + 4*float64(p.Wildcards) + float64(p.Domains) + 0.5*float64(p.URLs) + float64(p.IPs) + 2*float64(p.CIDRs)
	return v / math.Log(math.E+surface), true
}

func competition(p store.ScoreProgram, r ranks, now time.Time) store.HuntPart {
	part := store.HuntPart{Key: Competition, Score: unknownScore}
	if pct, ok := r.pct("crowding", p); ok {
		part.Known = true
		part.Score = 100 * (1 - pct)
		_, what, _ := volume(p)
		cmp := fmt.Sprintf("busier than %d%%", int(math.Round(pct*100)))
		if pct < 0.5 {
			cmp = fmt.Sprintf("quieter than %d%%", int(math.Round((1-pct)*100)))
		}
		part.Reasons = append(part.Reasons, fmt.Sprintf("%s for %s: %s of %s programs",
			what, plural(p.InScope, "in-scope asset", "in-scope assets"), cmp, platformLabel(p.Platform)))
	} else {
		part.Reasons = append(part.Reasons, platformLabel(p.Platform)+" doesn't share report volume")
	}
	if p.Private {
		part.Score += 30
		part.Reasons = append(part.Reasons, "Private: invite-only, far fewer hunters")
	}
	if p.StartedAt != nil {
		switch age := now.Sub(*p.StartedAt); {
		case age < 180*24*time.Hour:
			part.Score += 10
			part.Reasons = append(part.Reasons, "Launched "+ago(age)+": not picked over yet")
		case age > 4*365*24*time.Hour:
			part.Score -= 10
			part.Reasons = append(part.Reasons, "Running since "+p.StartedAt.Format("2006")+": well picked over")
		}
	}
	part.Score = math.Max(0, math.Min(100, part.Score))
	return part
}

// topAmount is a bounty table's highest critical (or, failing that, any)
// amount.
func topAmount(rows []platform.RewardRow) float64 {
	var crit, anyMax float64
	for _, r := range rows {
		crit = math.Max(crit, r.Critical.Max)
		for _, v := range []float64{r.Low.Max, r.Medium.Max, r.High.Max, r.Critical.Max} {
			anyMax = math.Max(anyMax, v)
		}
	}
	if crit > 0 {
		return crit
	}
	return anyMax
}

func payout(p store.ScoreProgram, r ranks) store.HuntPart {
	part := store.HuntPart{Key: Payout}
	if !p.OffersBounty {
		part.Known = true
		part.Reasons = []string{"No bounties (disclosure program)"}
		return part
	}
	var sum, weight float64
	add := func(metric string, w float64, reason func(v float64) string) {
		pct, ok := r.pct(metric, p)
		if !ok {
			return
		}
		sum += w * pct
		weight += w
		v, _ := metricValue(metric, p)
		part.Reasons = append(part.Reasons, fmt.Sprintf("%s (top %d%% on %s)", reason(v), max(1, int(math.Round((1-pct)*100))), platformLabel(p.Platform)))
	}
	add("top", 0.5, func(v float64) string {
		if v > topAmount(p.Rewards) {
			return "Top bounty " + humanize.Money(v, p.Currency) // from its stats or listing, past its table
		}
		return "Bounties up to " + humanize.Money(v, p.Currency)
	})
	add("paid90", 0.3, func(v float64) string { return humanize.Money(v, p.Currency) + " paid in the last 90 days" })
	add("avg", 0.2, func(v float64) string { return "Average bounty " + humanize.Money(v, p.Currency) })
	if weight == 0 {
		part.Score = unknownScore
		part.Reasons = []string{"Pays bounties; " + platformLabel(p.Platform) + " doesn't publish the amounts"}
		return part
	}
	part.Known = true
	part.Score = 100 * sum / weight
	return part
}

func response(p store.ScoreProgram, r ranks) store.HuntPart {
	part := store.HuntPart{Key: Response, Score: unknownScore}
	var sum, n float64
	add := func(metric string, reason string) {
		if pct, ok := r.pct(metric, p); ok {
			sum += pct
			n++
			part.Reasons = append(part.Reasons, reason)
		}
	}
	if p.Efficiency != nil {
		add("efficiency", fmt.Sprintf("%d%% on time", *p.Efficiency))
	}
	if p.FirstResponseHours != nil {
		add("firstResponse", "First response in about "+humanize.Hours(*p.FirstResponseHours))
	}
	if p.ValidationHours != nil {
		add("validation", "Most reports validated within "+humanize.Hours(*p.ValidationHours))
	}
	if n == 0 {
		part.Reasons = []string{platformLabel(p.Platform) + " doesn't share response times"}
		return part
	}
	part.Known = true
	part.Score = 100 * sum / n
	return part
}

// metricValue is a program's raw value for a ranked metric; lower-is-better
// metrics are negated so that higher always ranks better.
func metricValue(metric string, p store.ScoreProgram) (float64, bool) {
	f := func(v *float64) (float64, bool) {
		if v == nil {
			return 0, false
		}
		return *v, true
	}
	switch metric {
	case "crowding":
		return crowding(p)
	case "top":
		if !p.OffersBounty {
			return 0, false
		}
		top := topAmount(p.Rewards)
		if p.TopBounty != nil {
			top = math.Max(top, *p.TopBounty)
		}
		return top, top > 0
	case "paid90":
		if !p.OffersBounty {
			return 0, false
		}
		return f(p.Paid90d)
	case "avg":
		if !p.OffersBounty {
			return 0, false
		}
		return f(p.AvgBounty)
	case "efficiency":
		if p.Efficiency == nil {
			return 0, false
		}
		return float64(*p.Efficiency), true
	case "firstResponse":
		v, ok := f(p.FirstResponseHours)
		return -v, ok
	case "validation":
		v, ok := f(p.ValidationHours)
		return -v, ok
	}
	return 0, false
}

var rankedMetrics = []string{"crowding", "top", "paid90", "avg", "efficiency", "firstResponse", "validation"}

// ranks holds, per metric and platform, each program's percentile: the
// share of the platform's programs with that metric that it beats (ties
// count half).
type ranks map[string]map[int64]float64

func newRanks(programs []store.ScoreProgram) ranks {
	r := ranks{}
	for _, m := range rankedMetrics {
		r[m] = map[int64]float64{}
		byPlatform := map[string][]store.ScoreProgram{}
		for _, p := range programs {
			if _, ok := metricValue(m, p); ok {
				byPlatform[p.Platform] = append(byPlatform[p.Platform], p)
			}
		}
		for _, ps := range byPlatform {
			vals := make([]float64, len(ps))
			for i, p := range ps {
				vals[i], _ = metricValue(m, p)
			}
			sorted := slices.Clone(vals)
			sort.Float64s(sorted)
			for i, p := range ps {
				below := sort.SearchFloat64s(sorted, vals[i])
				equal := sort.SearchFloat64s(sorted, math.Nextafter(vals[i], math.Inf(1))) - below
				r[m][p.ID] = (float64(below) + float64(equal)/2) / float64(len(sorted))
			}
		}
	}
	return r
}

func (r ranks) pct(metric string, p store.ScoreProgram) (float64, bool) {
	v, ok := r[metric][p.ID]
	return v, ok
}
