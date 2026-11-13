package bugcrowd

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/mdpsec/huntdeck/internal/platform"
	"golang.org/x/net/html"
)

// Pending invitations are on the "Invites" page (/engagements/invites),
// whose app reads GET /engagements/invites.json?status=pending&page=N (50
// a page, with paginationMeta.totalCount) and accepts one with POST
// /engagements/{invite id}/accept.json, sending the page's CSRF token as
// X-CSRF-TOKEN (verified live 2026-11-10). An invited engagement isn't in
// the engagements listing and its brief answers 404 until accepted, but
// the invitation carries the brief's description and scope, so the
// listing adds invited programs from here and their scope is read from it.

const (
	// invitesFresh is how long the invitations a listing read are reused
	// by ListInvites, which runs right after it.
	invitesFresh = 10 * time.Minute
	invitePages  = 10 // of 50 invitations
)

// inviteJSON is one pending invitation.
type inviteJSON struct {
	ID                   string     `json:"id"` // the invitation's, used to accept it
	Status               string     `json:"status"`
	EngagementState      string     `json:"engagementState"`
	IconVariant          string     `json:"iconVariant"` // "bug-bounty" or "vdp", as in the listing
	IsPrivate            bool       `json:"isPrivate"`
	IsDemo               bool       `json:"isDemo"`
	StartsAt             *time.Time `json:"startsAt"`
	TeaserTitle          string     `json:"teaserTitle"`
	ServiceLevel         string     `json:"serviceLevel"`
	IndustryName         string     `json:"industryName"`
	TotalReservedFunds   string     `json:"totalReservedFunds"` // "$5,000"
	InviteURL            string     `json:"engagementInviteUrl"`
	CollaborationEnabled *bool      `json:"collaborationEnabled"`
	SafeHarbor           *struct {
		Status string `json:"status"`
	} `json:"safeHarbor"`
	Brief struct {
		BriefURL          string       `json:"briefUrl"`
		Description       string       `json:"description"`       // HTML
		TargetInformation string       `json:"targetInformation"` // HTML
		Scope             []scopeGroup `json:"scope"`
	} `json:"engagementBriefDetails"`
}

// handle is the program's handle, as the listing will have it once the
// invitation is accepted.
func (in inviteJSON) handle() (string, bool) {
	if in.ID == "" || in.IsDemo {
		return "", false
	}
	return handleFromBrief(in.Brief.BriefURL)
}

// document is the invitation as the brief's version document, so its
// scope and details read the same way.
func (in inviteJSON) document() versionDoc {
	yes := true
	vd := versionDoc{IsLoggedIn: &yes, CollaborationEnabled: in.CollaborationEnabled}
	vd.Data.Brief.Description = in.Brief.Description
	vd.Data.Brief.TargetsOverview = in.Brief.TargetInformation
	if in.SafeHarbor != nil {
		vd.Data.Brief.SafeHarbor = &struct {
			Status string `json:"status"`
		}{in.SafeHarbor.Status}
	}
	vd.Data.Scope = in.Brief.Scope
	vd.Data.Engagement.StartsAt = in.StartsAt
	vd.Data.Engagement.State = in.EngagementState
	return vd
}

// program is the invitation as a listing entry.
func (c *Client) program(in inviteJSON, raw json.RawMessage) (platform.Program, bool) {
	handle, ok := in.handle()
	if !ok {
		return platform.Program{}, false
	}
	link := c.base.String() + in.Brief.BriefURL
	if strings.HasPrefix(in.InviteURL, "/engagements/invites/") && !strings.ContainsAny(in.InviteURL, "?#\\") {
		link = c.base.String() + in.InviteURL // the brief answers 404 until accepted
	}
	p := platform.Program{
		Handle: handle, Name: strings.TrimSpace(in.TeaserTitle), URL: link, Private: true,
		State: platform.StateOpen, StartedAt: in.StartsAt, Raw: raw,
		TriageService: strings.TrimSpace(in.ServiceLevel), Industry: strings.TrimSpace(in.IndustryName),
		Collaboration: in.CollaborationEnabled,
	}
	if st := engagementState(in.EngagementState); st != "" {
		p.State = st
	} else {
		p.PauseUnknown = true
	}
	p.Currency, _ = currency(in.TotalReservedFunds)
	for _, row := range in.document().detail().Rewards {
		for _, r := range []platform.RewardRange{row.Critical, row.High, row.Medium, row.Low} {
			if r.Max <= 0 {
				continue
			}
			p.OffersBounty = true
			if p.BountyMax == nil || r.Max > *p.BountyMax {
				p.BountyMax = &r.Max
			}
			if lo := r.Min; lo > 0 && (p.BountyMin == nil || lo < *p.BountyMin) {
				p.BountyMin = &lo
			}
		}
	}
	if !p.OffersBounty {
		p.Currency = ""
		p.BountyUnknown = in.IconVariant == "bug-bounty"
	}
	return p, true
}

// pendingInvites reads every pending invitation with the session, and
// keeps them for ListInvites and the scope.
func (c *Client) pendingInvites(ctx context.Context) ([]inviteJSON, []json.RawMessage, error) {
	if !c.authenticated() {
		return nil, nil, platform.ErrNoWebLogin
	}
	var out []inviteJSON
	var raws []json.RawMessage
	seen := 0
	for pageNo := 1; pageNo <= invitePages; pageNo++ {
		var page struct {
			Invites        []json.RawMessage `json:"invites"`
			PaginationMeta *struct {
				TotalCount int `json:"totalCount"`
			} `json:"paginationMeta"`
		}
		u := fmt.Sprintf("%s/engagements/invites.json?status=pending&page=%d", c.base, pageNo)
		if err := c.getJSON(ctx, u, &page); err != nil {
			return nil, nil, fmt.Errorf("invitations page %d: %w", pageNo, err)
		}
		if page.PaginationMeta == nil {
			return nil, nil, errors.New("invitations: response has no paginationMeta (format changed?)")
		}
		for _, raw := range page.Invites {
			seen++
			var in inviteJSON
			if err := json.Unmarshal(raw, &in); err != nil {
				return nil, nil, fmt.Errorf("invitations page %d: %w", pageNo, err)
			}
			if !strings.EqualFold(in.Status, "pending") {
				continue
			}
			out, raws = append(out, in), append(raws, raw)
		}
		if len(page.Invites) == 0 || seen >= page.PaginationMeta.TotalCount {
			break
		}
	}
	c.invitesMu.Lock()
	c.invites, c.invitesAt = out, c.now()
	c.invitesMu.Unlock()
	return out, raws, nil
}

// invitePrograms returns the programs the user is invited to, as listing
// entries.
func (c *Client) invitePrograms(ctx context.Context) ([]platform.Program, error) {
	invites, raws, err := c.pendingInvites(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]platform.Program, 0, len(invites))
	for i, in := range invites {
		if p, ok := c.program(in, raws[i]); ok {
			out = append(out, p)
		}
	}
	return out, nil
}

// addInvited adds the invited programs a listing lacks. An error means
// they weren't read: the listing can't be taken as whole.
func (c *Client) addInvited(ctx context.Context, programs []platform.Program) ([]platform.Program, error) {
	invited, err := c.invitePrograms(ctx)
	if err != nil {
		return programs, fmt.Errorf("bugcrowd: %w", err)
	}
	listed := make(map[string]bool, len(programs))
	for _, p := range programs {
		listed[p.Handle] = true
	}
	for _, p := range invited {
		if !listed[p.Handle] {
			programs = append(programs, p)
		}
	}
	return programs, nil
}

// invited is the pending invitation to the program, from the last listing.
func (c *Client) invited(handle string) (inviteJSON, bool) {
	c.invitesMu.Lock()
	defer c.invitesMu.Unlock()
	for _, in := range c.invites {
		if h, ok := in.handle(); ok && h == handle {
			return in, true
		}
	}
	return inviteJSON{}, false
}

// ListInvites implements platform.InviteLister with the login, reusing
// what the listing just read.
func (c *Client) ListInvites(ctx context.Context) ([]platform.Invite, error) {
	c.invitesMu.Lock()
	invites, at := c.invites, c.invitesAt
	c.invitesMu.Unlock()
	if at.IsZero() || c.now().Sub(at) > invitesFresh {
		var err error
		if invites, _, err = c.pendingInvites(ctx); err != nil {
			if errors.Is(err, platform.ErrNoWebLogin) {
				return nil, err
			}
			return nil, fmt.Errorf("bugcrowd: %w", err)
		}
	}
	out := make([]platform.Invite, 0, len(invites))
	for _, in := range invites {
		if h, ok := in.handle(); ok {
			out = append(out, platform.Invite{Handle: h}) // the page doesn't say when it was sent
		}
	}
	return out, nil
}

// AcceptInvite implements platform.InviteAccepter as the Invites page's
// "Accept" does: the page's CSRF token, then POST
// /engagements/{invite id}/accept.json.
func (c *Client) AcceptInvite(ctx context.Context, handle string) error {
	if !c.authenticated() {
		return platform.ErrNoWebLogin
	}
	if err := c.ensureSession(ctx); err != nil {
		return fmt.Errorf("bugcrowd: accept invite: %w", err)
	}
	if _, _, err := c.pendingInvites(ctx); err != nil {
		return fmt.Errorf("bugcrowd: accept invite: %w", err)
	}
	in, ok := c.invited(handle)
	if !ok {
		return fmt.Errorf("bugcrowd: accept invite: no pending invitation to %s", handle)
	}
	if err := c.acceptByID(ctx, in.ID); err != nil {
		return fmt.Errorf("bugcrowd: accept invite: %w", err)
	}
	c.invitesMu.Lock()
	c.invites = deleteInvite(c.invites, in.ID)
	c.invitesMu.Unlock()
	return nil
}

// acceptByID posts the acceptance of invitation id with the Invites
// page's CSRF token.
func (c *Client) acceptByID(ctx context.Context, id string) error {
	page, err := c.get(ctx, c.base.String()+"/engagements/invites", "text/html")
	switch {
	case err != nil:
		return fmt.Errorf("invites page: %w", err)
	case isRedirect(page.StatusCode):
		return ErrSessionExpired
	case page.StatusCode != http.StatusOK:
		return fmt.Errorf("invites page: HTTP %d", page.StatusCode)
	}
	token := metaContent(page.Body, "csrf-token")
	if token == "" {
		return errors.New("the invites page has no CSRF token (layout changed?)")
	}
	u := c.base.String() + "/engagements/" + url.PathEscape(id) + "/accept.json"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u, http.NoBody)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("X-CSRF-TOKEN", token)
	req.Header.Set("Origin", c.base.String())
	req.Header.Set("Referer", c.base.String()+"/engagements/invites")
	res, err := c.login.Do(ctx, req) // never retried: it has an effect
	switch {
	case err != nil:
		return err
	case res.StatusCode >= 200 && res.StatusCode < 300:
	case isRedirect(res.StatusCode):
		return ErrSessionExpired
	default:
		return fmt.Errorf("HTTP %d", res.StatusCode)
	}
	return nil
}

func deleteInvite(invites []inviteJSON, id string) []inviteJSON {
	out := make([]inviteJSON, 0, len(invites))
	for _, in := range invites {
		if in.ID != id {
			out = append(out, in)
		}
	}
	return out
}

// metaContent is the content of the page's <meta name=name>.
func metaContent(body []byte, name string) string {
	z := html.NewTokenizer(bytes.NewReader(body))
	for {
		switch z.Next() {
		case html.ErrorToken:
			return ""
		case html.StartTagToken, html.SelfClosingTagToken:
			tag, hasAttr := z.TagName()
			if string(tag) != "meta" || !hasAttr {
				continue
			}
			var isName bool
			var content string
			for {
				k, v, more := z.TagAttr()
				switch string(k) {
				case "name":
					isName = string(v) == name
				case "content":
					content = string(v)
				}
				if !more {
					break
				}
			}
			if isName {
				return content
			}
		}
	}
}
