package intigriti

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"time"

	"github.com/mdpsec/huntdeck/internal/platform"
)

// inviteJSON is one entry of GET /api/core/researcher/invites, the web
// app's "Invites and applications" page: the invitations not yet accepted
// (verified live 2026-11-08). id is the program's ID, its handle in the
// listing; createdAt is when the invitation was sent (Unix seconds).
type inviteJSON struct {
	ID        string `json:"id"`
	Type      int    `json:"type"` // 1 in every invitation seen
	CreatedAt int64  `json:"createdAt"`
}

// ListInvites implements platform.InviteLister with the web login: one
// request with the saved session, which is renewed (with Chrome if need be)
// as for the browser sync.
func (c *Client) ListInvites(ctx context.Context) ([]platform.Invite, error) {
	if !c.loggedIn() {
		return nil, ErrNoSession
	}
	var list []inviteJSON
	if err := c.webJSON(ctx, "GET", "/invites", nil, &list); err != nil {
		return nil, fmt.Errorf("intigriti: invites: %w", err)
	}
	out := make([]platform.Invite, 0, len(list))
	for _, in := range list {
		if in.ID == "" {
			continue
		}
		inv := platform.Invite{Handle: in.ID}
		if in.CreatedAt > 0 {
			inv.InvitedAt = time.Unix(in.CreatedAt, 0).UTC()
		}
		out = append(out, inv)
	}
	return out, nil
}

// KeepAlive implements platform.KeepAliver: with the web login it checks
// the website session with one small request (the pending invitations),
// which renews it with the sign-on cookie, or the browser login, when it
// has lapsed, so the browser sync and polls always find it live.
func (c *Client) KeepAlive(ctx context.Context) error {
	if !c.loggedIn() {
		return nil
	}
	var list []inviteJSON
	return c.webJSON(ctx, "GET", "/invites", nil, &list)
}

// AcceptInvite implements platform.InviteAccepter as the web app's "Accept
// invite" does: POST /api/core/researcher/bug-bounty-programs/{id}/accept-invite
// with an empty JSON body (verified 2026-11-08). It answers without a body.
func (c *Client) AcceptInvite(ctx context.Context, handle string) error {
	if !c.loggedIn() {
		return ErrNoSession
	}
	u := c.web.app.String() + webAPI + "/bug-bounty-programs/" + url.PathEscape(handle) + "/accept-invite"
	for attempt := 0; ; attempt++ {
		if err := c.ensureSession(ctx, attempt > 0); err != nil {
			return fmt.Errorf("intigriti: accept invite: %w", err)
		}
		res, err := c.webDo(ctx, http.MethodPost, u, nil, "{}")
		if err != nil {
			return fmt.Errorf("intigriti: accept invite: %w", err)
		}
		switch {
		case res.StatusCode == http.StatusUnauthorized && attempt == 0:
			continue // the app dropped the session: renew it once
		case res.StatusCode >= 200 && res.StatusCode < 300:
			return nil
		default:
			return fmt.Errorf("intigriti: accept invite: HTTP %d", res.StatusCode)
		}
	}
}
