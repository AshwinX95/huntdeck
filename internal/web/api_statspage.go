package web

import (
	"math"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/mdpsec/huntdeck/internal/store"
)

// The Stats pages' numbers as JSON: /api/v1/stats/platforms is the Stats
// page (every platform, and all of them together), and
// /api/v1/stats/platforms/{platform} a platform's stats page (its numbers,
// then its open programs'). Built by the pages' own code, so the two
// agree; where a page draws a line, the API gives its points.

type apiStatsOverviewJSON struct {
	Month     string                `json:"month"` // the last finished month, the one "last month" numbers are: "2026-10"
	All       apiStatsPlatformRow   `json:"all"`
	Platforms []apiStatsPlatformRow `json:"platforms"`
}

type apiStatsPlatformJSON struct {
	Month    string              `json:"month"`
	Platform apiStatsPlatformRow `json:"platform"`
	// Columns are the numbers the platform publishes, as sorts: received,
	// valid, bounties, paid, resolve.
	Columns  []string             `json:"columns"`
	Sort     string               `json:"sort"`
	Total    int                  `json:"total"`
	Programs []apiStatsProgramRow `json:"programs"`
}

// apiStatsPlatformRow is a platform, or all of them ("all").
type apiStatsPlatformRow struct {
	Platform        string     `json:"platform"`
	Open            int        `json:"open"`
	Paying          int        `json:"paying"`
	Free            int        `json:"free"`
	Assets          int        `json:"assets_in_scope"`
	Wildcards       int        `json:"wildcards_in_scope"`
	ScopeAdded30d   int        `json:"scope_added_30d"`
	ScopeRemoved30d int        `json:"scope_removed_30d"`
	ValidSince      *time.Time `json:"valid_counted_since"` // valid_reports are complete from here; null: as far back as the months go
	apiStatsSeries
}

// apiStatsProgramRow is a program on its platform's stats page.
type apiStatsProgramRow struct {
	Program      apiProgramRef `json:"program"`
	OffersBounty bool          `json:"offers_bounty"`
	Ignored      bool          `json:"ignored"`
	apiStatsSeries
}

// apiStatsSeries are the numbers a platform and a program both have.
type apiStatsSeries struct {
	// ValidKind is what valid_reports count: "resolved" (HackerOne's
	// Hacktivity), "accepted" (Bugcrowd's CrowdStream), "valid" for
	// both, "" for neither.
	ValidKind           string          `json:"valid_kind"`
	Received90d         *int            `json:"reports_received_90d"` // null: not published
	Received90dEst      bool            `json:"reports_received_90d_estimated"`
	ReceivedPrograms    int             `json:"reports_received_90d_programs"` // how many programs that adds up
	Paid90d             []apiMoney      `json:"paid_90d"`                      // by currency, largest first
	Paid90dEst          bool            `json:"paid_90d_estimated"`
	PaidPrograms        int             `json:"paid_90d_programs"`
	DaysToDecision      *float64        `json:"days_to_decision"` // where there are no resolutions (Intigriti): median of programs' average days to a decision in 90 days
	Months              []apiStatsMonth `json:"months"`           // a year, oldest first, this month last
	ReceivedByDay       []apiDayValue   `json:"reports_received_90d_by_day"`
	PaidUSDByDay        []apiDayValue   `json:"paid_90d_usd_by_day"` // USD programs; empty where most is paid in another currency (see paid_90d_by_day)
	PaidByDay           []apiDayValue   `json:"paid_90d_by_day"`     // programs paying in paid_90d_by_day_currency
	PaidByDayCurrency   string          `json:"paid_90d_by_day_currency"`
	DaysToDecisionByDay []apiDayValue   `json:"days_to_decision_by_day"`
}

type apiMoney struct {
	Currency string  `json:"currency"`
	Amount   float64 `json:"amount"`
}

// apiStatsMonth is a month. Open, paying, assets and wildcards are at its
// end (now, for this month) and platforms only, as are programs that
// added scope and reports disclosed; null is a number there isn't.
type apiStatsMonth struct {
	Month          string   `json:"month"`    // "2026-10"
	Complete       bool     `json:"complete"` // false for this month, still filling in
	Open           *int     `json:"open"`
	Paying         *int     `json:"paying"`
	Assets         *int     `json:"assets_in_scope"`
	Wildcards      *int     `json:"wildcards_in_scope"`
	ScopeAdded     int      `json:"scope_added"`
	ScopePrograms  *int     `json:"programs_added_scope"`
	Disclosed      *int     `json:"disclosed"`
	Launched       *int     `json:"launched"` // null where the platform doesn't say when programs launched
	Valid          *int     `json:"valid_reports"`
	ValidEstimated bool     `json:"valid_reports_estimated"`
	Bounties       *float64 `json:"bounties_shown_usd"` // for valid_reports, USD programs
	// BountiesEstimated: some count at their program's average bounty,
	// as it hides amounts (HackerOne).
	BountiesEstimated bool `json:"bounties_estimated"`
	// BountiesOther are the month's bounties of programs paying in other
	// currencies (Intigriti's euros), largest first.
	BountiesOther []apiMoney `json:"bounties_other"`
	ResolveDays   *float64   `json:"median_days_to_resolve"`
}

type apiDayValue struct {
	Date  string  `json:"date"` // "2026-11-05"
	Value float64 `json:"value"`
}

func (s *server) apiStatsPlatforms(w http.ResponseWriter, r *http.Request) {
	in, err := s.statsInputs(r.Context(), statsVisibility(r))
	if err != nil {
		s.apiServerError(w, r, err)
		return
	}
	d := buildStats(in.Overviews, s.platformNames(), in.Items, in.Days, in.Start)
	out := apiStatsOverviewJSON{Month: lastMonth(in.Start), All: platformRowJSON(d.All), Platforms: []apiStatsPlatformRow{}}
	out.All.Platform = "all"
	for _, row := range d.Rows {
		out.Platforms = append(out.Platforms, platformRowJSON(row))
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *server) apiStatsPlatform(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	name := canonicalPlatform(r.PathValue("platform"))
	vis := statsVisibility(r)
	in, err := s.statsInputs(ctx, vis)
	if err != nil {
		s.apiServerError(w, r, err)
		return
	}
	o, ok := s.statsOverviewFor(in, name, vis)
	if !ok {
		writeJSONError(w, http.StatusNotFound, "no such platform")
		return
	}
	series, err := s.programSeries(ctx, vis)
	if err != nil {
		s.apiServerError(w, r, err)
		return
	}
	var items []store.ProgramListItem
	for _, it := range in.Items {
		if it.Platform == name {
			items = append(items, it)
		}
	}
	days := map[string][]store.ProgramDay{}
	for col, by := range in.Days {
		days[col] = by[name]
	}
	q := r.URL.Query()
	d := buildStatsPlatform(o, items, series, days, in.Start, strings.TrimSpace(q.Get("q")), isTrue(q.Get("bounty")),
		q.Get("sort"), isTrue(q.Get("rev")))
	limit := clampInt(q.Get("limit"), statsPlatformPerPage, 1, 100) // each row carries its lines: 500 was about 9 MB
	offset := clampInt(q.Get("offset"), 0, 0, 1<<30)
	out := apiStatsPlatformJSON{Month: lastMonth(in.Start), Platform: platformRowJSON(d.Platform), Columns: []string{}, Sort: d.Sort,
		Total: len(d.Rows), Programs: []apiStatsProgramRow{}}
	for _, k := range []string{"received", "valid", "bounties", "paid", "resolve"} {
		if d.Columns[k] {
			out.Columns = append(out.Columns, k)
		}
	}
	d.show(offset, offset+limit)
	for _, row := range d.Rows {
		out.Programs = append(out.Programs, apiStatsProgramRow{Program: refJSON(row.Item.ProgramRef), OffersBounty: row.Item.OffersBounty,
			Ignored: row.Item.Ignored, apiStatsSeries: seriesJSON(row)})
	}
	writeJSON(w, http.StatusOK, out)
}

// lastMonth is the last finished month of the months charted from start.
func lastMonth(start time.Time) string {
	return start.AddDate(0, overviewMonths-2, 0).Format("2006-01")
}

func platformRowJSON(row platformStats) apiStatsPlatformRow {
	return apiStatsPlatformRow{Platform: row.Platform, Open: row.Open, Paying: row.Paying, Free: row.Free(), Assets: row.Assets,
		Wildcards: row.Wildcards, ScopeAdded30d: row.Added30, ScopeRemoved30d: row.Removed30, ValidSince: row.overview.ValidSince,
		apiStatsSeries: seriesJSON(row)}
}

// seriesJSON gives a row's numbers and the points of its lines. A
// program's row (Item set) has no month-end levels: the Programs list has
// its own.
func seriesJSON(row platformStats) apiStatsSeries {
	o := row.overview
	out := apiStatsSeries{ValidKind: row.ValidKind, Received90d: row.ReceivedNow, Received90dEst: row.ReceivedEstimated,
		ReceivedPrograms: row.ReceivedPrograms, Paid90d: []apiMoney{}, Paid90dEst: row.PaidEstimated, PaidPrograms: row.PaidPrograms,
		DaysToDecision: rounded(row.DecisionNow), Months: []apiStatsMonth{}, ReceivedByDay: daysJSON(row.receivedDays),
		PaidUSDByDay: []apiDayValue{}, PaidByDay: daysJSON(row.paidDays), PaidByDayCurrency: "USD", DaysToDecisionByDay: daysJSON(row.decisionDays)}
	if len(row.Paid) > 0 {
		out.PaidByDayCurrency = row.Paid[0].Currency
	}
	if out.PaidByDayCurrency == "USD" {
		out.PaidUSDByDay = out.PaidByDay
	}
	for _, m := range row.Paid {
		out.Paid90d = append(out.Paid90d, apiMoney{m.Currency, round2(m.Amount)})
	}
	platform := row.Item == nil
	bounties := slices.ContainsFunc(o.Months, func(m store.PlatformMonth) bool { return m.Bounties > 0 })
	for i, m := range o.Months {
		if i >= len(row.months) {
			break
		}
		am := apiStatsMonth{Month: row.months[i].Format("2006-01"), Complete: i < len(o.Months)-1, ScopeAdded: m.ScopeAdded, BountiesOther: []apiMoney{},
			ValidEstimated: m.Estimated, ResolveDays: rounded(medianOf(m.ResolveDays))}
		if platform {
			am.Open, am.Paying, am.Assets, am.Wildcards = ptrTo(m.Open), ptrTo(m.Paying), ptrTo(m.Assets), ptrTo(m.Wildcards)
			am.ScopePrograms = ptrTo(m.ScopePrograms)
			if row.DisclosedLine != nil { // a platform that publishes disclosures
				am.Disclosed = ptrTo(m.Disclosed)
			}
		}
		if o.Launches {
			am.Launched = ptrTo(m.Launched)
		}
		if known, _ := monthKnown(row.months[i], o.ValidSince); o.ValidKind != "" && (o.ValidSince == nil || known) {
			am.Valid = ptrTo(m.Valid)
			if bounties {
				am.Bounties = ptrTo(round2(m.Bounties))
			}
			am.BountiesEstimated = m.BountiesEstimated
			for _, c := range bountyCurrencies([]store.PlatformMonth{m}) {
				if c != "USD" {
					am.BountiesOther = append(am.BountiesOther, apiMoney{c, round2(m.BountiesOther[c])})
				}
			}
		}
		out.Months = append(out.Months, am)
	}
	return out
}

func daysJSON(line []store.DayValue) []apiDayValue {
	out := make([]apiDayValue, 0, len(line))
	for _, d := range line {
		out = append(out, apiDayValue{Date: d.At.UTC().Format(time.DateOnly), Value: round2(d.Value)})
	}
	return out
}

// round2 rounds to cents: estimates and medians carry no more.
func round2(v float64) float64 { return math.Round(v*100) / 100 }

func rounded(v *float64) *float64 {
	if v == nil {
		return nil
	}
	return ptrTo(round2(*v))
}

func ptrTo[T any](v T) *T { return &v }
