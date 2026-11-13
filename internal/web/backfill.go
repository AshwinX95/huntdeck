package web

import (
	"math"
	"time"

	"github.com/mdpsec/huntdeck/internal/store"
)

// Where a platform publishes only running totals (Intigriti; Bugcrowd's
// rewarded reports), a program's 90-day figure is worked out once there is
// a week of saved totals (syncer.count90d), so its line would start a week
// after the first save. backfillCounted draws the days before that from
// the totals there are: the rise since the first saved total, scaled to 90
// days, once there are backfillMinDays of them; the whole total for a
// program launched less than 90 days before the day. Like the figure, each
// point is an estimate (≈).

// backfillMinDays is the fewest days of saved totals a backfilled point
// rests on.
const backfillMinDays = 3

// backfillCounted returns estimated points from totals for the days before
// the first of stored (all of totals' days when stored is empty).
func backfillCounted(totals, stored []store.DayValue, started *time.Time) []store.DayValue {
	if len(totals) < 2 {
		return nil
	}
	day := func(t time.Time) string { return t.UTC().Format(time.DateOnly) }
	first := totals[0]
	var out []store.DayValue
	for _, t := range totals[1:] {
		if len(stored) > 0 && day(t.At) >= day(stored[0].At) {
			break
		}
		v := math.Max(t.Value-first.Value, 0)
		switch k := t.At.Sub(first.At).Hours() / 24; {
		case started != nil && t.At.Sub(*started) < window90:
			v = t.Value
		case k < backfillMinDays:
			continue
		default:
			v = v * window90.Hours() / 24 / k
		}
		out = append(out, store.DayValue{At: t.At, Value: math.Min(v, t.Value)})
	}
	return out
}

// withBackfill is stored with backfillCounted's points before it.
func withBackfill(totals, stored []store.DayValue, started *time.Time) []store.DayValue {
	return append(backfillCounted(totals, stored, started), stored...)
}

// validDays estimates valid reports in the 90 days to each of received's
// days as that day's received times the share of all reports accepted:
// accepted over total from the latest totals saved by then, else the
// earliest saved. Nil without any.
func validDays(received, accepted, total []store.DayValue) []store.DayValue {
	type share struct {
		at time.Time
		v  float64
	}
	day := func(t time.Time) string { return t.UTC().Format(time.DateOnly) }
	totals := map[string]float64{}
	for _, t := range total {
		totals[day(t.At)] = t.Value
	}
	var shares []share
	for _, a := range accepted {
		if t := totals[day(a.At)]; t > 0 && a.Value <= t {
			shares = append(shares, share{a.At, a.Value / t})
		}
	}
	if len(shares) == 0 {
		return nil
	}
	out := make([]store.DayValue, len(received))
	j := 0
	for i, r := range received {
		for j < len(shares)-1 && day(shares[j+1].at) <= day(r.At) {
			j++
		}
		out[i] = store.DayValue{At: r.At, Value: math.Round(r.Value * shares[j].v)}
	}
	return out
}
