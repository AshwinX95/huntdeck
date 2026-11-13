package web

import (
	"fmt"
	"math"
	"strconv"
	"strings"

	"github.com/mdpsec/huntdeck/internal/humanize"
	"github.com/mdpsec/huntdeck/internal/platform"
	"github.com/mdpsec/huntdeck/internal/store"
)

// response describes how quickly a program answers: its first response,
// else how long it takes to validate, compact ("6d 5h") with what it is in
// Title; nil if the platform doesn't say.
func response(p store.ProgramListItem) *responseTime {
	switch {
	case p.FirstResponseHours != nil:
		h := humanize.Hours(*p.FirstResponseHours)
		return &responseTime{Text: h, Title: "First response: " + h}
	case p.ValidationHours != nil:
		h := humanize.Hours(*p.ValidationHours)
		return &responseTime{Text: h, Title: "To validate: " + h}
	}
	return nil
}

// responseKind says what response measures for p: "first response" or
// "to validate".
func responseKind(p store.ProgramListItem) string {
	switch {
	case p.FirstResponseHours != nil:
		return "first response"
	case p.ValidationHours != nil:
		return "to validate"
	}
	return ""
}

// responseTime is a program's response time for the Programs list.
type responseTime struct{ Text, Title string }

// avgBounty is a program's average bounty, compact: "$1.7k", or the range
// HackerOne gives, "$200 – $300"; "" if the platform doesn't share it.
func avgBounty(p store.ProgramListItem) string {
	if p.AvgPayout == nil {
		return ""
	}
	if p.AvgBountyLow != nil && *p.AvgBountyLow == 0 { // HackerOne's lowest bracket, "$0 – $50"
		return "up to " + humanize.Money(*p.AvgPayout, p.StatsCurrency)
	}
	return amountRange(p.AvgBountyLow, p.AvgPayout, p.StatsCurrency)
}

// avgBountyTitle says what a program's average bounty is, in full.
func avgBountyTitle(p store.ProgramListItem) string {
	if p.AvgPayout == nil {
		return ""
	}
	v := amountRangeFull(p.AvgBountyLow, p.AvgPayout, p.StatsCurrency)
	if p.AvgBountyLow != nil && *p.AvgBountyLow == 0 {
		v = "up to " + humanize.MoneyFull(*p.AvgPayout, p.StatsCurrency)
	}
	switch p.Platform {
	case "hackerone":
		return "HackerOne's average bounty: " + v
	case "bugcrowd":
		return "Bugcrowd's average payout over the last 3 months: " + v
	case "yeswehack":
		return "YesWeHack's average reward: " + v
	case "intigriti":
		return "Intigriti's average payout: " + v
	}
	return "Average bounty: " + v
}

// huntersLabel names a platform's count of researchers on a program.
func huntersLabel(platform string) string {
	switch platform {
	case "bugcrowd":
		return "Hunters joined"
	case "yeswehack":
		return "Hunters ranked"
	case "intigriti":
		return "Hunters contributed"
	}
	return "Hunters"
}

// huntersHint explains huntersLabel.
func huntersHint(platform string) string {
	switch platform {
	case "bugcrowd":
		return "Researchers who joined the program"
	case "yeswehack":
		return "Researchers on the program's ranking: those who scored on it"
	case "intigriti":
		return "Researchers who contributed a report the program accepted"
	}
	return "Researchers on the program"
}

// validTitle says what a program's valid reports in 90 days count on its
// platform, where they're counted rather than estimated
// (validEstimateTitle).
func validTitle(platform string) string {
	switch platform {
	case "hackerone":
		return "Valid reports in the last 90 days: reports resolved on HackerOne Hacktivity"
	case "bugcrowd":
		return "Valid reports in the last 90 days: accepted reports resolved on Bugcrowd CrowdStream"
	}
	return "Valid reports in the last 90 days"
}

// validMonthTitle says what valid reports per month count on a platform,
// for the Stats pages; all of them for "".
func validMonthTitle(platform string) string {
	switch platform {
	case "hackerone":
		return "Valid reports: reports resolved on HackerOne Hacktivity, undisclosed included"
	case "bugcrowd":
		return "Valid reports: reports accepted on Bugcrowd CrowdStream, undisclosed included"
	case "intigriti":
		return "Valid reports: the rises in each program's total of accepted reports, saved daily; ≈ before the first save, from the programs' monthly leaderboards"
	case "yeswehack":
		return "Valid reports: the rises in each program's Thanks list, saved daily; ≈ before the first save, from reports received × the program's valid share"
	}
	return "Valid reports: resolved on HackerOne Hacktivity, accepted on Bugcrowd CrowdStream, the rises in Intigriti's accepted-reports totals (≈ before the first save, from leaderboards) and in YesWeHack's Thanks lists (≈ before, from reports received × the valid share)"
}

// validEstimateTitle explains a program's estimated valid reports in 90
// days (store.ProgramListItem.Resolved90dEstimated).
func validEstimateTitle(p store.ProgramListItem) string {
	if p.Reports90d == nil || p.AcceptedAll == nil || p.TotalReports == nil || *p.TotalReports == 0 {
		return ""
	}
	share := float64(*p.AcceptedAll) / float64(*p.TotalReports) * 100
	return fmt.Sprintf("Estimate of valid reports in 90 days: %s reports received in 90 days × %s%% of its reports accepted all time (%s of %s). "+
		"%s doesn't list its reports, so none are counted as resolved; see Methodology.",
		num(*p.Reports90d), formatCount(math.Round(share), ""), num(*p.AcceptedAll), num(*p.TotalReports), platformLabel(p.Platform))
}

// countedTitle explains a 90-day figure HuntDeck worked out from the
// running totals it saves daily (see syncer.count90d); what names it
// ("reports received" or "bounties paid").
func countedTitle(p store.ProgramListItem, what string) string {
	if p.CountedDays == nil {
		return ""
	}
	name, d := platformLabel(p.Platform), *p.CountedDays
	days := func(n int) string {
		if n == 1 {
			return "1 day"
		}
		return strconv.Itoa(n) + " days"
	}
	const later = " Counted exactly once HuntDeck has saved 90 days of totals; see Methodology."
	if p.Platform == "bugcrowd" {
		window := "the 90 days of counts HuntDeck has saved"
		if d < 90 {
			window = "the " + days(d) + " of counts HuntDeck has saved, scaled to 90 days"
		}
		avg := ""
		if p.AvgPayout != nil {
			avg = " (" + humanize.MoneyFull(*p.AvgPayout, p.StatsCurrency) + ")"
		}
		return "Estimate: the rise in Bugcrowd's count of rewarded reports over " + window + ", × its average payout over the last 3 months" + avg +
			". The program doesn't list its reports on CrowdStream; see Methodology."
	}
	weekly := what == "reports received" && p.Reports7d != nil
	switch {
	case d >= 90:
		return "Counted: " + what + " in the last 90 days, from " + name + "'s all-time total, which HuntDeck saves daily"
	case p.Points90d != nil && p.PointsAll != nil && *p.PointsAll > 0:
		total := ""
		switch {
		case what == "reports received" && p.TotalReports != nil:
			total = " (" + formatCount(float64(*p.TotalReports), "") + ")"
		case what == "bounties paid" && p.PaidAll != nil:
			total = " (" + humanize.MoneyFull(*p.PaidAll, p.StatsCurrency) + ")"
		}
		share := float64(*p.Points90d) / float64(*p.PointsAll) * 100
		return fmt.Sprintf("Estimate: %s's all-time total%s × the share of the program's leaderboard points earned in the last 90 days: %s of %s, %.0f%%%s. Points come from accepted reports and their duplicates, by severity.%s",
			name, total, formatCount(float64(*p.Points90d), ""), formatCount(float64(*p.PointsAll), ""), share, hunters90Phrase(p.Hunters90d), later)
	case weekly && d == 0:
		return "Estimate: " + name + "'s last-7-days count (" + strconv.Itoa(*p.Reports7d) + ") × 90/7." + later
	case weekly:
		return "Estimate: " + what + " over the " + days(d) + " of saved totals, plus " + name + "'s last-7-days count (" +
			strconv.Itoa(*p.Reports7d) + ") for the other " + days(90-d) + "." + later
	}
	return "Estimate: " + what + " over the " + days(d) + " of saved totals, scaled to 90 days." + later
}

// assetsTitle breaks a program's in-scope assets down for the Assets
// column's tooltip. The number is scope entries, so the breakdown counts
// entries and adds up to it; the wildcards and domains, distinct hosts
// the entries name (an API's URL names a domain too), come after, said
// as such: "93 in-scope entries: 2 apps, 1 API, 90 other; they name 47
// wildcards and 43 domains".
func assetsTitle(p store.ProgramListItem) string {
	n := func(v int, one, many string) string {
		if v == 1 {
			return "1 " + one
		}
		return thousands(strconv.Itoa(v)) + " " + many
	}
	if p.InScope == 0 {
		return "No assets in scope"
	}
	out := n(p.InScope, "in-scope entry", "in-scope entries")
	var parts []string
	if p.Apps > 0 {
		parts = append(parts, n(p.Apps, "app", "apps"))
	}
	if p.APIs > 0 {
		parts = append(parts, n(p.APIs, "API", "APIs"))
	}
	if len(parts) > 0 {
		if rest := p.InScope - p.Apps - p.APIs; rest > 0 {
			parts = append(parts, thousands(strconv.Itoa(rest))+" other")
		}
		out += ": " + strings.Join(parts, ", ")
	}
	var hosts []string
	if p.Wildcards > 0 {
		hosts = append(hosts, n(p.Wildcards, "wildcard", "wildcards"))
	}
	if p.Domains > 0 {
		hosts = append(hosts, n(p.Domains, "domain", "domains"))
	}
	if len(hosts) > 0 {
		verb := "they name "
		if p.InScope == 1 {
			verb = "it names "
		}
		out += "; " + verb + strings.Join(hosts, " and ")
	}
	return out
}

// paidAll is what the m-ptotal partial reads: a program's bounties paid
// all time and, for an estimate, what it's made of.
type paidAll struct {
	PaidAll          *float64
	PaidAllEstimated bool
	Rewarded         *int
	AvgPayout        *float64
	StatsCurrency    string
	Platform         string
	OffersBounty     bool
}

// paidAllOf is t's program's paid all time for m-ptotal, so the hunt
// board's Total paid reads as the Programs list's does.
func paidAllOf(t store.TargetRow) paidAll {
	return paidAll{PaidAll: t.PaidAll, PaidAllEstimated: t.PaidAllEstimated, Rewarded: t.Rewarded, AvgPayout: t.AvgPayout,
		StatsCurrency: t.Currency, Platform: t.Program.Platform, OffersBounty: t.OffersBounty}
}

// hunters90Phrase is ", by N researchers" for the leaderboard share's
// tooltip; "" without the count.
func hunters90Phrase(n *int) string {
	if n == nil {
		return ""
	}
	return ", by " + hunters90(*n) + " " + plural(*n, "researcher", "researchers")
}

// hunters90 is how many researchers earned points in the last 90 days:
// "100+" at the leaderboard's cap.
func hunters90(n int) string {
	if n >= platform.LeaderboardCap {
		return strconv.Itoa(platform.LeaderboardCap) + "+"
	}
	return strconv.Itoa(n)
}
