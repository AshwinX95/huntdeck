package bugcrowd

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/mdpsec/huntdeck/internal/platform"
)

// Accepted reports: CrowdStream, the public activity feed on each brief,
// lists every accepted report without its details (verified live
// 2026-10-26):
//
//	GET /engagements/{code}/crowdstream.json?page=N   one program
//	GET /crowdstream.json?page=N                      every public program
//
// 20 items a page, with pagination_meta{total_pages,totalCount} and a
// cutoff_date_label: "6 months" for one program, "7 days" for all. Items
// accepted within that window come first, latest accepted first; after them
// come older reports that were disclosed, newest disclosure first, back to
// 2017. Each item has the report's id, engagement_path, created_at
// (submitted, RFC 3339), accepted_at (a day: "23 Oct 2026"), closed_at
// (resolved, RFC 3339, or null), substate ("unresolved", "resolved"),
// priority (1 to 5), sometimes amount ("$600") and researcher_username.
// A report accepted long ago and resolved today stays where its acceptance
// put it, so resolutions only show up by reading a program's feed again.
// One request per 20 reports in the window: Indeed's 188 take ten.

type crowdstreamJSON struct {
	Cutoff  string     `json:"cutoff_date_label"`
	Results []itemJSON `json:"results"`
	Meta    struct {
		TotalPages int `json:"total_pages"`
	} `json:"pagination_meta"`
}

type itemJSON struct {
	ID         string  `json:"id"`
	Path       string  `json:"engagement_path"`
	Code       string  `json:"engagement_code"`
	CreatedAt  string  `json:"created_at"`
	AcceptedAt string  `json:"accepted_at"`
	ClosedAt   *string `json:"closed_at"`
	Substate   string  `json:"substate"`
	Priority   int     `json:"priority"`
	Amount     *string `json:"amount"`
	Researcher string  `json:"researcher_username"`
	Pinned     bool    `json:"is_pinned"`
}

// windowSlack keeps reports accepted on the window's first day, which the
// feed may count by its own clock.
const windowSlack = 48 * time.Hour

// FetchAccepted implements platform.AcceptedFetcher. It stops at the first
// report accepted before the window, so disclosed reports older than it are
// never fetched. A program whose feed can't be read at all is skipped
// without an error, unless the session or the WAF is the reason.
func (c *Client) FetchAccepted(ctx context.Context, p *platform.Program, maxPages int, save platform.AcceptedSaver) error {
	base := c.base.String() + "/crowdstream.json"
	what := "all programs"
	if p != nil {
		if _, ok := handleFromBrief("/engagements/" + p.Handle); !ok {
			return fmt.Errorf("bugcrowd: crowdstream: bad handle %q", p.Handle)
		}
		base = c.base.String() + "/engagements/" + p.Handle + "/crowdstream.json"
		what = p.Handle
	}
	for n := 1; n <= maxPages; n++ {
		var res crowdstreamJSON
		if err := c.getJSON(ctx, fmt.Sprintf("%s?page=%d", base, n), &res); err != nil {
			if p != nil && n == 1 && !fatal(err) {
				return nil // no CrowdStream (a legacy brief, a 404): nothing to save
			}
			return fmt.Errorf("bugcrowd: crowdstream %s page %d: %w", what, n, err)
		}
		page, end := c.acceptedPage(res)
		more, err := save(page)
		if err != nil || !more || end || len(res.Results) == 0 || n >= res.Meta.TotalPages {
			return err
		}
	}
	return nil
}

// acceptedPage converts one page; end is true once it reaches reports
// accepted before the window.
func (c *Client) acceptedPage(res crowdstreamJSON) (page platform.AcceptedPage, end bool) {
	if since, ok := window(res.Cutoff, c.now()); ok {
		page.Since = since
	}
	for _, it := range res.Results {
		r, ok := it.report()
		if !ok {
			continue
		}
		if !page.Since.IsZero() && r.AcceptedAt.Before(page.Since.Add(-windowSlack)) {
			if !it.Pinned {
				end = true
			}
			continue
		}
		page.Reports = append(page.Reports, r)
	}
	return page, end
}

// report converts one item; ok is false for items without an ID, a
// program or an acceptance day.
func (it itemJSON) report() (platform.AcceptedReport, bool) {
	handle, ok := handleFromBrief(it.Path)
	if !ok {
		handle, ok = it.Code, it.Code != "" && !strings.ContainsAny(it.Code, "/?#")
	}
	accepted, err := time.Parse("2 Jan 2006", strings.TrimSpace(it.AcceptedAt))
	if !ok || it.ID == "" || err != nil {
		return platform.AcceptedReport{}, false
	}
	r := platform.AcceptedReport{ID: it.ID, ProgramHandle: handle, AcceptedAt: accepted.UTC(),
		Priority: it.Priority, Reporter: it.Researcher}
	if t, err := time.Parse(time.RFC3339, it.CreatedAt); err == nil {
		t = t.UTC()
		r.SubmittedAt = &t
	}
	if it.Substate == "resolved" && it.ClosedAt != nil {
		if t, err := time.Parse(time.RFC3339, *it.ClosedAt); err == nil {
			t = t.UTC()
			r.ResolvedAt = &t
		}
	}
	if r.Priority < 1 || r.Priority > 5 {
		r.Priority = 0
	}
	if it.Amount != nil {
		if v, _, err := money(*it.Amount); err == nil {
			r.Bounty = &v
		}
	}
	return r, true
}

// window turns a cutoff label like "6 months" or "7 days" into where the
// window starts.
func window(label string, now time.Time) (time.Time, bool) {
	n, unit, ok := strings.Cut(strings.TrimSpace(label), " ")
	v, err := strconv.Atoi(n)
	if !ok || err != nil || v <= 0 {
		return time.Time{}, false
	}
	now = now.UTC()
	switch strings.TrimSuffix(strings.ToLower(unit), "s") {
	case "day":
		return now.AddDate(0, 0, -v), true
	case "week":
		return now.AddDate(0, 0, -7*v), true
	case "month":
		return now.AddDate(0, -v, 0), true
	case "year":
		return now.AddDate(-v, 0, 0), true
	}
	return time.Time{}, false
}
