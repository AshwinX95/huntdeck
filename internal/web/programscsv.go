package web

import (
	"encoding/csv"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/mdpsec/huntdeck/internal/store"
)

// The Programs list as CSV (GET /programs.csv): the same filters and sort
// as the page, every matching program, and the columns the table shows
// (?cols=, which app.js adds from the table as it is; without it, all of
// them). Each column is one or more fields; the program's platform,
// handle, name, visibility and status always lead.

// csvColumn is one of the list's columns (programColumns' keys) as CSV
// fields.
type csvColumn struct {
	key    string
	header []string
	values func(store.ProgramListItem) []string
}

var csvLead = []string{"platform", "handle", "name", "visibility", "status"}

var csvColumns = []csvColumn{
	{"hunt", []string{"huntscore"}, func(p store.ProgramListItem) []string {
		if p.Hunt == nil {
			return []string{""}
		}
		return []string{strconv.FormatFloat(p.Hunt.Score, 'f', 1, 64)}
	}},
	{"bounty", []string{"bounty_min", "bounty_max", "bounty_currency"}, func(p store.ProgramListItem) []string {
		return []string{csvFloat(p.BountyMin), csvFloat(p.BountyMax), p.Currency}
	}},
	{"ptotal", []string{"paid_total", "paid_total_estimated", "stats_currency"}, func(p store.ProgramListItem) []string {
		return []string{csvFloat(p.PaidAll), csvBool(p.PaidAll != nil && p.PaidAllEstimated), p.StatsCurrency}
	}},
	{"avg", []string{"avg_bounty_low", "avg_bounty_high", "stats_currency"}, func(p store.ProgramListItem) []string {
		return []string{csvFloat(p.AvgBountyLow), csvFloat(p.AvgPayout), p.StatsCurrency}
	}},
	{"paid", []string{"paid_90d", "paid_90d_estimated", "stats_currency"}, func(p store.ProgramListItem) []string {
		return []string{csvFloat(p.Paid90d), csvBool(p.Paid90d != nil && p.Paid90dIsEstimate()), p.StatsCurrency}
	}},
	{"comp", []string{"reports_received_90d", "reports_received_90d_estimated"}, func(p store.ProgramListItem) []string {
		return []string{csvInt(p.Reports90d), csvBool(p.Reports90d != nil && p.Reports90dIsEstimate())}
	}},
	{"resolved", []string{"valid_reports_90d", "valid_reports_90d_estimated"}, func(p store.ProgramListItem) []string {
		return []string{csvInt(p.Resolved90d), csvBool(p.Resolved90d != nil && p.Resolved90dEstimated)}
	}},
	{"resp", []string{"first_response_hours", "validation_hours"}, func(p store.ProgramListItem) []string {
		return []string{csvFloat(p.FirstResponseHours), csvFloat(p.ValidationHours)}
	}},
	{"scope", []string{"in_scope", "out_of_scope", "wildcards", "domains"}, func(p store.ProgramListItem) []string {
		return []string{strconv.Itoa(p.InScope), strconv.Itoa(p.OutOfScope), strconv.Itoa(p.Wildcards), strconv.Itoa(p.Domains)}
	}},
	{"hunters", []string{"hunters"}, func(p store.ProgramListItem) []string { return []string{csvInt(p.Hunters)} }},
	{"new", []string{"new_scope_30d"}, func(p store.ProgramListItem) []string { return []string{strconv.Itoa(p.Fresh)} }},
	{"launched", []string{"launched_at"}, func(p store.ProgramListItem) []string { return []string{csvTime(p.StartedAt)} }},
	{"changed", []string{"scope_changed_at"}, func(p store.ProgramListItem) []string { return []string{csvTime(p.ScopeChangedAt)} }},
	{"discl", []string{"disclosed"}, func(p store.ProgramListItem) []string { return []string{strconv.Itoa(p.Disclosed)} }},
	{"req", []string{"requirements"}, func(p store.ProgramListItem) []string {
		var labels []string
		for _, c := range requires(p.Platform, p.Requirements) {
			labels = append(labels, c.Label)
		}
		if p.Credentials {
			labels = append(labels, "test credentials")
		}
		return []string{strings.Join(labels, "; ")}
	}},
	{"open", []string{"url"}, func(p store.ProgramListItem) []string { return []string{p.URL} }},
}

// csvColumnsFor reads ?cols=: the keys in order, unknown ones skipped;
// none means every column. A money column's currency comes once.
func csvColumnsFor(v string) []csvColumn {
	if strings.TrimSpace(v) == "" {
		return csvColumns
	}
	var out []csvColumn
	for key := range strings.SplitSeq(v, ",") {
		if i := slices.IndexFunc(csvColumns, func(c csvColumn) bool { return c.key == key }); i >= 0 &&
			!slices.ContainsFunc(out, func(c csvColumn) bool { return c.key == key }) {
			out = append(out, csvColumns[i])
		}
	}
	if len(out) == 0 {
		return csvColumns
	}
	return out
}

// csvMax caps a read-only copy's export, as its targets' is.
const csvMax = 5000

func (s *server) programsCSV(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	f := programFilterFrom(q)
	if q.Get("sort") == "" {
		f.Sort = programsDefaultSort
	}
	f.Limit = 1 << 20
	if s.ReadOnly {
		f.Limit = csvMax
	}
	progs, _, err := s.Store.Programs(r.Context(), f)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	cols := csvColumnsFor(q.Get("cols"))
	header := slices.Clone(csvLead)
	var keep []bool // a stats_currency already given is left out
	for _, c := range cols {
		for _, h := range c.header {
			dup := h == "stats_currency" && slices.Contains(header, h)
			keep = append(keep, !dup)
			if !dup {
				header = append(header, h)
			}
		}
	}
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="huntdeck-programs.csv"`)
	cw := csv.NewWriter(w)
	_ = cw.Write(header)
	for _, p := range progs {
		row := []string{p.Platform, p.Handle, csvText(p.Name), visibilityOf(p.Private), p.State}
		if p.RemovedAt != nil {
			row[4] = "removed"
		}
		i := 0
		for _, c := range cols {
			for _, v := range c.values(p) {
				if keep[i] {
					row = append(row, csvText(v))
				}
				i++
			}
		}
		if err := cw.Write(row); err != nil {
			return // the client went away
		}
	}
	cw.Flush()
}

func visibilityOf(private bool) string {
	if private {
		return "private"
	}
	return "public"
}

func csvFloat(v *float64) string {
	if v == nil {
		return ""
	}
	return strconv.FormatFloat(*v, 'f', -1, 64)
}

func csvInt(v *int) string {
	if v == nil {
		return ""
	}
	return strconv.Itoa(*v)
}

func csvBool(b bool) string {
	if b {
		return "true"
	}
	return ""
}

func csvTime(t *time.Time) string {
	if t == nil {
		return ""
	}
	return t.UTC().Format(time.RFC3339)
}

// csvText keeps a spreadsheet from running text as a formula: a program's
// name is the platform's, and could start with "=".
func csvText(v string) string {
	if v != "" && strings.ContainsRune("=+-@\t\r", rune(v[0])) {
		return "'" + v
	}
	return v
}

// CSVURL is the list's CSV export, with its filters and sort.
func (d programsData) CSVURL() string {
	keep := url.Values{}
	for k, v := range d.query {
		if k != "page" {
			keep[k] = v
		}
	}
	if len(keep) == 0 {
		return "/programs.csv"
	}
	return "/programs.csv?" + keep.Encode()
}
