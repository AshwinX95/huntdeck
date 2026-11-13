package syncer

import (
	"math"

	"github.com/mdpsec/huntdeck/internal/platform"
	"github.com/mdpsec/huntdeck/internal/store"
)

// countedDays is the window of the 90-day figures worked out from totals.
const countedDays = 90

// minScaledDays is how many days of saved totals a scaled estimate needs:
// fewer swing too much with a single day's reports.
const minScaledDays = 7

// needsCounting reports whether st's 90-day figures have to be worked out
// from its running totals: the platform publishes a report count or a total
// paid (YesWeHack, Intigriti) but no 90-day figures.
func needsCounting(st platform.ProgramStats) bool {
	return (st.TotalReports != nil || st.BountiesPaid != nil) && st.ReportsLast90d == nil && st.BountiesPaidLast90d == nil
}

// needsRewardedCount reports whether st's paid in 90 days could be worked
// out from its count of rewarded reports and average payout (Bugcrowd),
// for a program whose CrowdStream doesn't list the reports themselves.
func needsRewardedCount(st platform.ProgramStats) bool {
	return st.RewardedReports != nil && st.AvgBountyHigh != nil && st.BountiesPaidLast90d == nil && st.BountiesPaid == nil
}

// countRewarded90d estimates st's bounties paid in 90 days as the reports
// rewarded in them, worked out from the saved counts like count90d's
// figures, times the average payout (the platform's, over the last three
// months). It is always an estimate, however many days are saved.
func countRewarded90d(st *platform.ProgramStats, base store.SavedTotals, ok bool) {
	now := float64(*st.RewardedReports)
	var then *float64
	if base.Rewarded != nil {
		v := float64(*base.Rewarded)
		then = &v
	}
	n, days := countedFigure(&now, then, nil, nil, base, ok)
	if n == nil {
		return
	}
	paid := *n * *st.AvgBountyHigh
	st.BountiesPaidLast90d, st.RewardedLast90d, st.CountedDays = &paid, n, &days
}

// count90d sets st's 90-day figures from the totals saved before (base, if
// ok). With 90 days of them a figure is counted: today's total less that of
// 90 days ago. With fewer it is an estimate: where the program's
// leaderboards were read whole (Intigriti), today's total × the share of
// all its points earned in the last 90 days, from the first day; else the
// reports over the days there are plus the platform's last-7-days count for
// the rest (where it publishes one), or else the change over the days there
// are scaled to 90 once there are a week of them. CountedDays records how
// many days were saved.
//
// A program launched less than 90 days ago (base.Age) has had every report
// and bounty in the window, so its totals are the figures, counted. No
// figure exceeds the total it comes from: scaling a young, busy program's
// week would.
func count90d(st *platform.ProgramStats, base store.SavedTotals, ok bool) {
	share := leaderboardShare(*st)
	figure := func(now, then *float64, weekly *int) *float64 {
		v, _ := countedFigure(now, then, weekly, share, base, ok)
		return v
	}
	_, whole := countedFigure(nil, nil, nil, nil, base, ok)
	var reportsNow, reportsThen *float64
	if st.TotalReports != nil {
		v := float64(*st.TotalReports)
		reportsNow = &v
	}
	if base.Reports != nil {
		v := float64(*base.Reports)
		reportsThen = &v
	}
	reports := figure(reportsNow, reportsThen, st.ReportsLast7d)
	paid := figure(st.BountiesPaid, base.Paid, nil)
	if reports == nil && paid == nil {
		return
	}
	if reports != nil {
		n := int(math.Round(*reports))
		st.ReportsLast90d = &n
	}
	st.BountiesPaidLast90d, st.CountedDays = paid, &whole
}

// countedFigure works one running total out over 90 days, as count90d
// describes: now is today's total, then the saved one in base (if ok),
// weekly the platform's last-7-days count, share the leaderboards' (nil
// without). days is how many days of saved totals it rests on, countedDays
// once it is counted rather than estimated.
func countedFigure(now, then *float64, weekly *int, share *float64, base store.SavedTotals, ok bool) (v *float64, days int) {
	young := base.Age != nil && *base.Age < countedDays
	switch {
	case young:
		days = countedDays
	case ok:
		days = int(math.Round(base.Days))
	}
	if now == nil {
		return nil, days
	}
	if young {
		x := *now
		return &x, days
	}
	have := ok && then != nil
	var x float64
	if have {
		x = math.Max(*now-*then, 0)
	}
	switch {
	case have && days >= countedDays:
	case share != nil:
		x = *now * *share
	case weekly != nil && have:
		x += float64(*weekly) * (countedDays - base.Days) / 7
	case weekly != nil:
		x = float64(*weekly) * countedDays / 7
	case have && days >= minScaledDays:
		x = x * countedDays / base.Days
	default:
		return nil, days
	}
	x = math.Min(x, *now)
	return &x, days
}

// leaderboardShare is the share of all the reputation points researchers
// earned on the program that they earned in the last 90 days, where its
// leaderboards were read whole; nil otherwise.
func leaderboardShare(st platform.ProgramStats) *float64 {
	if st.Points90d == nil || st.PointsAll == nil || *st.PointsAll <= 0 {
		return nil
	}
	v := math.Min(float64(*st.Points90d)/float64(*st.PointsAll), 1)
	return &v
}
