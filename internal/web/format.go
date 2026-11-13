package web

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/mdpsec/huntdeck/internal/humanize"
)

// Money and durations come from internal/humanize, one format for every
// page: compact where people read them ("$8.3k", "7d 18h"), the full
// amount only in tooltips (the *Full helpers), and an estimate never to
// the dollar.

func fmtTime(t time.Time, loc *time.Location, layout string) string {
	if t.IsZero() {
		return "—"
	}
	return t.In(loc).Format(layout)
}

func ago(t time.Time) string {
	if t.IsZero() {
		return "never"
	}
	d := time.Since(t)
	if d < 0 {
		return "just now"
	}
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	case d < 48*time.Hour:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd ago", int(d.Hours()/24))
	}
}

// until is how long until t: "in 52m", "in 3h", "in 2d"; "now" once past.
func until(t time.Time) string {
	d := time.Until(t)
	switch {
	case d < time.Minute:
		return "now"
	case d < time.Hour:
		return fmt.Sprintf("in %dm", int(d.Minutes()))
	case d < 48*time.Hour:
		return fmt.Sprintf("in %dh", int(d.Hours()))
	default:
		return fmt.Sprintf("in %dd", int(d.Hours()/24))
	}
}

func agoPtr(t *time.Time) string {
	if t == nil {
		return "never"
	}
	return ago(*t)
}

// humanDuration is a duration as pages show it (humanize.Duration), or
// "off" for none: how long a run took.
func humanDuration(d time.Duration) string {
	if d <= 0 {
		return "off"
	}
	return humanize.Duration(d)
}

// chartTip is a chart point's tooltip: when, and its value then
// ("Oct 31 · $38,300", "Oct 2026 · 5"), nothing more.
func chartTip(when, value string) string { return when + " · " + value }

// amountRange is a range shown as two amounts, compact ("$500 – $2k"), or
// a single amount when both ends match or one is missing; "" for neither.
func amountRange(lo, hi *float64, currency string) string {
	return rangeOf(lo, hi, currency, humanize.MoneyRange)
}

// amountRangeFull is amountRange to the unit, for tooltips.
func amountRangeFull(lo, hi *float64, currency string) string {
	return rangeOf(lo, hi, currency, humanize.MoneyRangeFull)
}

func rangeOf(lo, hi *float64, currency string, format func(lo, hi float64, currency string) string) string {
	switch {
	case lo == nil && hi == nil:
		return ""
	case lo == nil:
		return format(*hi, *hi, currency)
	case hi == nil:
		return format(*lo, *lo, currency)
	}
	return format(*lo, *hi, currency)
}

// reward is a bounty table's range for a severity, compact, or "" when it
// pays nothing; lo 0 means up to hi.
func reward(lo, hi float64, currency string) string {
	return rewardOf(lo, hi, currency, humanize.MoneyRange)
}

// rewardFull is reward to the unit, for tooltips.
func rewardFull(lo, hi float64, currency string) string {
	return rewardOf(lo, hi, currency, humanize.MoneyRangeFull)
}

func rewardOf(lo, hi float64, currency string, format func(lo, hi float64, currency string) string) string {
	switch {
	case hi <= 0:
		return ""
	case lo > 0 && lo < hi:
		return format(lo, hi, currency)
	}
	return format(hi, hi, currency)
}

// num formats a count with thousands separators.
func num(n any) string {
	switch v := n.(type) {
	case int:
		return thousands(strconv.Itoa(v))
	case int64:
		return thousands(strconv.FormatInt(v, 10))
	}
	return fmt.Sprint(n)
}

// thousands adds comma separators to a string of digits, keeping any sign.
func thousands(digits string) string { return humanize.Thousands(digits) }

// freshFor is how long programs and scope count as new for highlighting.
const freshFor = 7 * 24 * time.Hour

// recentFor is how long a scope entry's added date is highlighted.
const recentFor = 30 * 24 * time.Hour

// age is a compact time since t, for asset ages: "today", "5d", "3w",
// "4mo", "2y 3mo".
func age(t time.Time) string {
	days := int(time.Since(t).Hours() / 24)
	switch {
	case days < 1:
		return "today"
	case days < 14:
		return fmt.Sprintf("%dd", days)
	case days < 60:
		return fmt.Sprintf("%dw", days/7)
	case days < 365:
		return fmt.Sprintf("%dmo", days*12/365)
	}
	y, mo := days/365, (days%365)*12/365
	if mo == 0 {
		return fmt.Sprintf("%dy", y)
	}
	return fmt.Sprintf("%dy %dmo", y, mo)
}

// deref returns *p, or the zero value for nil.
func deref[T any](p *T) T {
	var zero T
	if p == nil {
		return zero
	}
	return *p
}

// firstLine returns the first non-empty line of Markdown without heading
// or emphasis markers, for previews.
func firstLine(md string) string {
	for _, l := range strings.Split(md, "\n") {
		l = strings.TrimSpace(strings.Trim(strings.TrimSpace(l), "#*_ "))
		if l != "" {
			return l
		}
	}
	return ""
}

// plural is one or many to go with n.
func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}
