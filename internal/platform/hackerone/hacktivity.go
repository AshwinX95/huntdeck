package hackerone

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"

	"github.com/mdpsec/huntdeck/internal/platform"
)

// Disclosed reports and undisclosed resolutions come from the official
// Hacktivity endpoint (GET /hackers/hacktivity), which takes a Lucene query.
// Undisclosed items carry no title or severity, only when the report was
// submitted, its latest public action (resolved, bounty or swag awarded)
// and when, and sometimes the bounty; their IDs are report IDs. Pages hold at most
// 50 items whatever page[size] asks for, and the "next" link is missing even
// when there are more, so paging continues until a short page. Programs are
// filtered by name ("team"), not handle, so per-program results are checked
// against the handle. Verified live 2026-10-26.
//
// A private program's items are capped past the first 50: each later page
// holds only its first 10 items, so a page asked for 50 that brings 10
// isn't the last; paging carries on 10 at a time (verified live
// 2026-11-09: one program's history was 305 items, of which pages of 50
// gave 60).
const (
	hacktivityPage = 50
	cappedPage     = 10
)

type hacktivityJSON struct {
	ID         json.Number `json:"id"` // a number in Hacktivity (a string elsewhere in the API)
	Attributes struct {
		Title          string   `json:"title"`
		SeverityRating *string  `json:"severity_rating"`
		CWE            *string  `json:"cwe"`
		CVEIDs         []string `json:"cve_ids"`
		Substate       *string  `json:"substate"`
		Awarded        *float64 `json:"total_awarded_amount"`
		SubmittedAt    *string  `json:"submitted_at"`
		DisclosedAt    *string  `json:"disclosed_at"`
		Votes          *int     `json:"votes"`
		URL            string   `json:"url"`
	} `json:"attributes"`
	Relationships struct {
		Summary struct {
			Data struct {
				Attributes struct {
					Summary string `json:"hacktivity_summary"`
				} `json:"attributes"`
			} `json:"data"`
		} `json:"report_generated_content"`
		Reporter struct {
			Data struct {
				Attributes struct {
					Username string `json:"username"`
				} `json:"attributes"`
			} `json:"data"`
		} `json:"reporter"`
		Program struct {
			Data struct {
				Attributes struct {
					Handle string `json:"handle"`
				} `json:"attributes"`
			} `json:"data"`
		} `json:"program"`
	} `json:"relationships"`
}

// FetchDisclosures implements platform.DisclosureFetcher.
func (c *Client) FetchDisclosures(ctx context.Context, p *platform.Program, maxPages int, save platform.DisclosureSaver) error {
	return c.hacktivity(ctx, p, "disclosed:true", "-disclosed_at", maxPages, func(items []json.RawMessage) (bool, error) {
		out := make([]platform.Disclosure, 0, len(items))
		for _, raw := range items {
			d, ok, err := convertDisclosure(raw)
			if err != nil {
				return false, err
			}
			if ok && (p == nil || d.ProgramHandle == p.Handle) {
				out = append(out, d)
			}
		}
		return save(out)
	})
}

// FetchResolutions implements platform.ResolutionFetcher: Hacktivity items
// that aren't disclosed, by latest activity.
func (c *Client) FetchResolutions(ctx context.Context, p *platform.Program, maxPages int, save platform.ResolutionSaver) error {
	return c.hacktivity(ctx, p, "disclosed:false", "-latest_disclosable_activity_at", maxPages, func(items []json.RawMessage) (bool, error) {
		out := make([]platform.Resolution, 0, len(items))
		for _, raw := range items {
			r, ok, err := convertResolution(raw)
			if err != nil {
				return false, err
			}
			if ok && (p == nil || r.ProgramHandle == p.Handle) {
				out = append(out, r)
			}
		}
		return save(out)
	})
}

// hacktivity pages through Hacktivity items matching filter (and p's
// program, if p is set), sorted by sort, handing each page's raw items to
// fn until it says stop, the last page, or maxPages pages' worth of items.
func (c *Client) hacktivity(ctx context.Context, p *platform.Program, filter, sort string, maxPages int,
	fn func([]json.RawMessage) (bool, error)) error {
	query := filter
	if p != nil {
		query = `team:"` + luceneEscape(p.Name) + `" AND ` + filter
	}
	size := hacktivityPage
	for offset := 0; offset < maxPages*hacktivityPage; {
		page := offset/size + 1
		v := url.Values{}
		v.Set("queryString", query)
		v.Set("sort", sort)
		v.Set("page[size]", fmt.Sprint(size))
		v.Set("page[number]", fmt.Sprint(page))
		var res struct {
			Data []json.RawMessage `json:"data"`
		}
		if err := c.getJSON(ctx, c.endpoint("/hackers/hacktivity?"+v.Encode()), &res); err != nil {
			return fmt.Errorf("hackerone: hacktivity page %d: %w", page, err)
		}
		more, err := fn(res.Data)
		if err != nil {
			return fmt.Errorf("hackerone: hacktivity page %d: %w", page, err)
		}
		switch n := len(res.Data); {
		case !more:
			return nil
		case n == size:
			offset += size
		case n == cappedPage && size > cappedPage:
			// Capped (see above): on 10 at a time. offset is a multiple
			// of 50, so of 10 too.
			size = cappedPage
			offset += cappedPage
		default:
			return nil // the last page
		}
	}
	return nil
}

// resolutionJSON is the part of an undisclosed Hacktivity item we read.
type resolutionJSON struct {
	ID         json.Number `json:"id"`
	Attributes struct {
		Disclosed   bool     `json:"disclosed"`
		Action      *string  `json:"latest_disclosable_action"`
		ActivityAt  *string  `json:"latest_disclosable_activity_at"`
		SubmittedAt *string  `json:"submitted_at"`
		Awarded     *float64 `json:"total_awarded_amount"`
	} `json:"attributes"`
	Relationships struct {
		Reporter struct {
			Data struct {
				Attributes struct {
					Username string `json:"username"`
				} `json:"attributes"`
			} `json:"data"`
		} `json:"reporter"`
		Program struct {
			Data struct {
				Attributes struct {
					Handle string `json:"handle"`
				} `json:"attributes"`
			} `json:"data"`
		} `json:"program"`
	} `json:"relationships"`
}

// resolutionActions maps Hacktivity actions that mean a report was resolved
// (by then) to Resolution actions.
var resolutionActions = map[string]string{
	"Activities::BugResolved":   platform.ActionResolved,
	"Activities::BountyAwarded": platform.ActionBounty,
	"Activities::SwagAwarded":   platform.ActionSwag,
}

// convertResolution turns one undisclosed Hacktivity item into a
// Resolution. Items whose latest action doesn't date a resolution are
// skipped (ok is false).
func convertResolution(raw json.RawMessage) (platform.Resolution, bool, error) {
	var h resolutionJSON
	if err := json.Unmarshal(raw, &h); err != nil {
		return platform.Resolution{}, false, fmt.Errorf("decode hacktivity item: %w", err)
	}
	a := h.Attributes
	action, known := resolutionActions[deref(a.Action)]
	at := parseTime(a.ActivityAt)
	handle := h.Relationships.Program.Data.Attributes.Handle
	if a.Disclosed || !known || at == nil || h.ID == "" || strings.ContainsAny(string(h.ID), "/?#") || handle == "" {
		return platform.Resolution{}, false, nil
	}
	return platform.Resolution{
		ID:            h.ID.String(),
		ProgramHandle: handle,
		SubmittedAt:   parseTime(a.SubmittedAt),
		ActivityAt:    *at,
		Action:        action,
		Bounty:        a.Awarded,
		Reporter:      h.Relationships.Reporter.Data.Attributes.Username,
	}, true, nil
}

// convertDisclosure turns one Hacktivity item into a Disclosure. Items
// that aren't disclosed reports are skipped (ok is false).
func convertDisclosure(raw json.RawMessage) (platform.Disclosure, bool, error) {
	var h hacktivityJSON
	if err := json.Unmarshal(raw, &h); err != nil {
		return platform.Disclosure{}, false, fmt.Errorf("decode hacktivity item: %w", err)
	}
	a := h.Attributes
	disclosed := parseTime(a.DisclosedAt)
	handle := h.Relationships.Program.Data.Attributes.Handle
	if h.ID == "" || strings.ContainsAny(string(h.ID), "/?#") || handle == "" || disclosed == nil || !strings.HasPrefix(a.URL, "https://hackerone.com/") {
		return platform.Disclosure{}, false, nil
	}
	d := platform.Disclosure{
		ID:            h.ID.String(),
		ProgramHandle: handle,
		Title:         strings.TrimSpace(a.Title),
		Severity:      strings.ToLower(deref(a.SeverityRating)),
		Weakness:      deref(a.CWE),
		CVEIDs:        a.CVEIDs,
		Substate:      deref(a.Substate),
		Bounty:        a.Awarded,
		SubmittedAt:   parseTime(a.SubmittedAt),
		DisclosedAt:   *disclosed,
		URL:           a.URL,
		Reporter:      h.Relationships.Reporter.Data.Attributes.Username,
		Summary:       strings.TrimSpace(h.Relationships.Summary.Data.Attributes.Summary),
		Raw:           withoutWriteUp(raw),
	}
	if a.Votes != nil {
		d.Votes = *a.Votes
	}
	return d, true, nil
}

// withoutWriteUp drops the report body, if the API ever includes it: only
// the metadata and the link are kept.
func withoutWriteUp(raw json.RawMessage) json.RawMessage {
	var item map[string]json.RawMessage
	var attrs map[string]json.RawMessage
	if json.Unmarshal(raw, &item) != nil || json.Unmarshal(item["attributes"], &attrs) != nil {
		return raw
	}
	delete(attrs, "vulnerability_information")
	b, err := json.Marshal(attrs)
	if err != nil {
		return raw
	}
	item["attributes"] = b
	if out, err := json.Marshal(item); err == nil {
		return out
	}
	return raw
}

// luceneEscape escapes s for use inside a quoted Lucene phrase.
func luceneEscape(s string) string {
	return strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(s)
}
