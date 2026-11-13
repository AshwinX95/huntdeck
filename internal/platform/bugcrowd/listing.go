package bugcrowd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/mdpsec/huntdeck/internal/platform"
)

type engagementJSON struct {
	Name          string  `json:"name"`
	BriefURL      string  `json:"briefUrl"`
	AccessStatus  string  `json:"accessStatus"`
	IsPrivate     bool    `json:"isPrivate"`
	IsDemo        bool    `json:"isDemo"`
	EndsAt        *string `json:"endsAt"`
	RewardSummary *struct {
		MinReward string `json:"minReward"` // "$100", or "Points"
		MaxReward string `json:"maxReward"`
	} `json:"rewardSummary"`
	// "Platform", "Priority Triage", "P1 24/7" or "Self-managed" (2026-10).
	ServiceLevel string `json:"serviceLevel"`
	IndustryName string `json:"industryName"`
	ScopeRank    *int   `json:"scopeRank"` // 1–4, or null when unrated
	// The engagement's kind: iconVariant "bug-bounty" or "vdp" (2026-11).
	Type *struct {
		IconVariant string `json:"iconVariant"`
	} `json:"productEngagementType"`
}

// ListPrograms implements platform.Platform.
func (c *Client) ListPrograms(ctx context.Context) (platform.Listing, error) {
	var out platform.Listing
	if c.authenticated() {
		if err := c.ensureSession(ctx); err != nil {
			return out, err
		}
	}

	seen := map[string]bool{}   // programs kept
	listed := map[string]bool{} // every item, including skipped demos
	ranks := map[string]int{}
	unpriced := map[string]bool{} // bug bounties showing no rewards
	total := -1
	var itemErr error // a malformed item makes the listing incomplete, not empty
	for pageNo := 1; ; pageNo++ {
		if pageNo > maxPages {
			return out, fmt.Errorf("bugcrowd: list engagements: more than %d pages", maxPages)
		}
		var p listingPage
		// Sorted by name so the order doesn't shift while paging.
		u := fmt.Sprintf("%s/engagements.json?category=&sort_by=name&sort_direction=asc&page=%d", c.base, pageNo)
		if err := c.getJSON(ctx, u, &p); err != nil {
			return out, fmt.Errorf("bugcrowd: list engagements page %d: %w", pageNo, err)
		}
		if total < 0 {
			total = p.PaginationMeta.TotalCount
			if total <= 0 && len(p.Engagements) > 0 {
				// Without a total we can't tell a full listing from a cut-off one.
				return out, errors.New("bugcrowd: list engagements: response has no paginationMeta.totalCount (format changed?)")
			}
		}
		for _, raw := range p.Engagements {
			var id struct {
				BriefURL string `json:"briefUrl"`
			}
			_ = json.Unmarshal(raw, &id)
			listed[id.BriefURL] = true
			prog, ok, err := c.parseEngagement(raw)
			if err != nil {
				if itemErr == nil {
					itemErr = fmt.Errorf("bugcrowd: list engagements page %d: %w", pageNo, err)
				}
				continue
			}
			if !ok || seen[prog.Handle] {
				continue
			}
			seen[prog.Handle] = true
			var r struct {
				ScopeRank *int `json:"scopeRank"`
			}
			_ = json.Unmarshal(raw, &r)
			ranks[prog.Handle] = deref(r.ScopeRank)
			if prog.BountyUnknown {
				unpriced[prog.Handle] = true
			}
			out.Programs = append(out.Programs, prog)
		}
		if len(p.Engagements) == 0 || len(listed) >= total {
			break
		}
	}
	if len(listed) < total {
		return out, fmt.Errorf("bugcrowd: listing returned %d of %d engagements; it changed while paging", len(listed), total)
	}
	if itemErr != nil {
		return out, itemErr
	}

	if c.authenticated() {
		ok, err := c.sessionValid(ctx)
		if err != nil {
			return out, fmt.Errorf("bugcrowd: session check after listing: %w", err)
		}
		if !ok {
			return out, fmt.Errorf("bugcrowd: session ended while listing, so private programs may be missing: %w", ErrSessionExpired)
		}
		// Invited programs aren't in the listing. Without them it isn't
		// whole: an invited program missing from it must not be taken as
		// gone.
		if out.Programs, err = c.addInvited(ctx, out.Programs); err != nil {
			return out, err
		}
	}
	c.rankMu.Lock()
	c.ranks, c.unpriced, c.ranksAt = ranks, unpriced, c.now()
	c.rankMu.Unlock()
	out.Complete = true
	return out, nil
}

// listedUnpriced reports whether a listing in the last two days showed the
// program as a bug bounty without rewards.
func (c *Client) listedUnpriced(handle string) bool {
	c.rankMu.Lock()
	defer c.rankMu.Unlock()
	return c.unpriced != nil && c.now().Sub(c.ranksAt) <= 48*time.Hour && c.unpriced[handle]
}

// listedRank is a program's scope rank from a listing in the last two
// days; ok is false without one, and rank 0 means unrated.
func (c *Client) listedRank(handle string) (rank int, ok bool) {
	c.rankMu.Lock()
	defer c.rankMu.Unlock()
	if c.ranks == nil || c.now().Sub(c.ranksAt) > 48*time.Hour {
		return 0, false
	}
	rank, ok = c.ranks[handle]
	return rank, ok
}

func deref(p *int) int {
	if p == nil {
		return 0
	}
	return *p
}

// listingPage is one page of /engagements.json.
type listingPage struct {
	Engagements    []json.RawMessage `json:"engagements"`
	PaginationMeta struct {
		TotalCount int `json:"totalCount"`
	} `json:"paginationMeta"`
}

// ListRecent implements platform.RecentLister: the first page of the
// listing sorted by start date, newest first (24 programs, one request,
// plus a session check when logged in). Private invites to older programs
// don't show up here; the complete listing finds them.
func (c *Client) ListRecent(ctx context.Context) (platform.Listing, error) {
	var out platform.Listing
	if c.authenticated() {
		if err := c.ensureSession(ctx); err != nil {
			return out, err
		}
	}
	var p listingPage
	u := c.base.String() + "/engagements.json?category=&sort_by=starts&sort_direction=desc&page=1"
	if err := c.getJSON(ctx, u, &p); err != nil {
		return out, fmt.Errorf("bugcrowd: list newest engagements: %w", err)
	}
	var itemErr error
	for _, raw := range p.Engagements {
		prog, ok, err := c.parseEngagement(raw)
		switch {
		case err != nil && itemErr == nil:
			itemErr = fmt.Errorf("bugcrowd: list newest engagements: %w", err)
		case err == nil && ok:
			out.Programs = append(out.Programs, prog)
		}
	}
	if c.authenticated() && itemErr == nil {
		// New invitations show up between complete listings too.
		var err error
		out.Programs, err = c.addInvited(ctx, out.Programs)
		return out, err
	}
	return out, itemErr
}

// parseEngagement converts one listing item. ok is false for items to skip.
func (c *Client) parseEngagement(raw json.RawMessage) (platform.Program, bool, error) {
	var e engagementJSON
	if err := json.Unmarshal(raw, &e); err != nil {
		return platform.Program{}, false, fmt.Errorf("decode engagement: %w", err)
	}
	if e.IsDemo {
		return platform.Program{}, false, nil
	}
	handle, ok := handleFromBrief(e.BriefURL)
	if !ok {
		return platform.Program{}, false, fmt.Errorf("engagement %q has unexpected briefUrl %q", e.Name, e.BriefURL)
	}
	p := platform.Program{
		Handle:  handle,
		Name:    strings.TrimSpace(e.Name),
		URL:     c.base.String() + e.BriefURL,
		Private: e.IsPrivate || (e.AccessStatus != "" && e.AccessStatus != "open"),
		State:   platform.StateOpen,
		Raw:     raw,
		// The service level is the triage Bugcrowd provides; self-managed
		// programs triage their own reports.
		TriageService: strings.TrimSpace(e.ServiceLevel),
		Industry:      strings.TrimSpace(e.IndustryName),
	}
	if e.EndsAt != nil {
		if t, err := time.Parse(time.RFC3339, *e.EndsAt); err == nil && t.Before(c.now()) {
			p.State = platform.StateClosed
		}
	}
	// The listing shows paused engagements like open ones; the brief
	// says which (engagement.state).
	p.PauseUnknown = p.State == platform.StateOpen
	if e.RewardSummary != nil {
		p.Currency, p.OffersBounty = currency(e.RewardSummary.MaxReward)
		if p.OffersBounty {
			p.BountyMin, p.BountyMax = reward(e.RewardSummary.MinReward), reward(e.RewardSummary.MaxReward)
		}
	} else if e.Type != nil && e.Type.IconVariant == "bug-bounty" {
		// A bug bounty showing no rewards: whether it pays is the stats'
		// call, from its average payout.
		p.BountyUnknown = true
	}
	return p, true, nil
}

// handleFromBrief turns "/engagements/acme-mbb" into "acme-mbb". Older
// programs have briefs like "/acme"; their handle is the path itself.
func handleFromBrief(brief string) (string, bool) {
	if strings.ContainsAny(brief, "?#\\") {
		return "", false
	}
	handle, ok := strings.CutPrefix(brief, "/engagements/")
	if !ok {
		handle, ok = strings.CutPrefix(brief, "/")
	}
	if !ok || handle == "" || strings.Contains(handle, "/") || handle == "engagements" {
		return "", false
	}
	return handle, true
}

// currency reads "$15,000" or "€500" and reports whether it's money.
func currency(maxReward string) (string, bool) {
	s := strings.TrimSpace(maxReward)
	if !strings.ContainsAny(s, "0123456789") {
		return "", false // "Points"
	}
	switch {
	case strings.HasPrefix(s, "$"):
		return "USD", true
	case strings.HasPrefix(s, "€"):
		return "EUR", true
	case strings.HasPrefix(s, "£"):
		return "GBP", true
	}
	return "", true
}

// reward reads the amount in "$15,000" or "€500"; nil for "Points" or
// nothing.
func reward(s string) *float64 {
	digits := strings.Map(func(r rune) rune {
		if r >= '0' && r <= '9' || r == '.' {
			return r
		}
		if r == ',' {
			return -1
		}
		if r == '$' || r == '€' || r == '£' || unicode.IsSpace(r) {
			return -1
		}
		return 'x' // anything else isn't an amount
	}, s)
	v, err := strconv.ParseFloat(digits, 64)
	if err != nil || v <= 0 {
		return nil
	}
	return &v
}
