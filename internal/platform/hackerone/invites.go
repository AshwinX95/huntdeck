package hackerone

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/mdpsec/huntdeck/internal/platform"
)

// Pending invitations are only on the website: the "Pending invitations"
// page reads me.soft_launch_invitations, and its "Accept invite" button
// runs acceptInvitation with the invitation's token (verified live
// 2026-11-09). The Hacker API lists only programs the user has joined, so
// the listing adds the invited ones from here; their scope then comes from
// the API like any other's (it answers for invited programs too).

const (
	// invitesFresh is how long the invitations a listing read are reused
	// by ListInvites, which runs right after it.
	invitesFresh = 10 * time.Minute
	invitePages  = 10 // of 100 invitations
)

// webInvite is one pending invitation and what it says about the program.
type webInvite struct {
	ExpiresAt string `json:"expires_at"`
	Team      struct {
		Handle             string  `json:"handle"`
		Name               string  `json:"name"`
		Currency           string  `json:"currency"`
		OffersBounties     *bool   `json:"offers_bounties"`
		StartedAcceptingAt *string `json:"started_accepting_at"`
	} `json:"team"`
}

const invitesQuery = `query PendingInvitations($cursor: String) { me { id
soft_launch_invitations(first: 100, after: $cursor, state: open) { pageInfo { hasNextPage endCursor }
edges { node { expires_at team { handle name currency offers_bounties started_accepting_at } } } } } }`

// pendingInvites reads every open invitation with the website session.
func (c *Client) pendingInvites(ctx context.Context) ([]webInvite, error) {
	if !c.loggedIn() {
		return nil, ErrNoSession
	}
	var out []webInvite
	cursor := any(nil)
	for range invitePages {
		var data struct {
			Me *struct {
				Invitations struct {
					PageInfo struct {
						HasNextPage bool   `json:"hasNextPage"`
						EndCursor   string `json:"endCursor"`
					} `json:"pageInfo"`
					Edges []struct {
						Node webInvite `json:"node"`
					} `json:"edges"`
				} `json:"soft_launch_invitations"`
			} `json:"me"`
		}
		if err := c.webGraphQL(ctx, invitesQuery, map[string]any{"cursor": cursor}, &data, func() bool { return data.Me != nil }); err != nil {
			return nil, fmt.Errorf("hackerone: invitations: %w", err)
		}
		if data.Me == nil {
			return nil, errors.New("hackerone: invitations: the website session isn't signed in")
		}
		for _, e := range data.Me.Invitations.Edges {
			if e.Node.Team.Handle != "" {
				out = append(out, e.Node)
			}
		}
		if !data.Me.Invitations.PageInfo.HasNextPage || data.Me.Invitations.PageInfo.EndCursor == "" {
			break
		}
		cursor = data.Me.Invitations.PageInfo.EndCursor
	}
	c.invitesMu.Lock()
	c.invites, c.invitesAt = out, time.Now()
	c.invitesMu.Unlock()
	return out, nil
}

// invitePrograms returns the programs the user is invited to and hasn't
// joined, as listing entries.
func (c *Client) invitePrograms(ctx context.Context) ([]platform.Program, error) {
	invites, err := c.pendingInvites(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]platform.Program, 0, len(invites))
	for _, in := range invites {
		t := in.Team
		p := platform.Program{Handle: t.Handle, Name: t.Name, URL: "https://hackerone.com/" + t.Handle, Private: true,
			State: platform.StateOpen, Currency: strings.ToUpper(t.Currency), StartedAt: parseTime(t.StartedAcceptingAt)}
		if t.OffersBounties != nil {
			p.OffersBounty = *t.OffersBounties
		}
		out = append(out, p)
	}
	return out, nil
}

// ListInvites implements platform.InviteLister with the website login,
// reusing what the listing just read.
func (c *Client) ListInvites(ctx context.Context) ([]platform.Invite, error) {
	c.invitesMu.Lock()
	invites, at := c.invites, c.invitesAt
	c.invitesMu.Unlock()
	if at.IsZero() || time.Since(at) > invitesFresh {
		var err error
		if invites, err = c.pendingInvites(ctx); err != nil {
			return nil, err
		}
	}
	out := make([]platform.Invite, 0, len(invites))
	for _, in := range invites {
		out = append(out, platform.Invite{Handle: in.Team.Handle}) // the website doesn't say when it was sent
	}
	return out, nil
}

// AcceptInvite implements platform.InviteAccepter as the program page's
// "Accept invite" does: the invitation's token, then acceptInvitation.
func (c *Client) AcceptInvite(ctx context.Context, handle string) error {
	if !c.loggedIn() {
		return ErrNoSession
	}
	const find = `query Invitation($h: String!) { me { id soft_launch_invitations(team_handle: $h, state: open, first: 1) {
edges { node { token } } } } }`
	var data struct {
		Me *struct {
			Invitations struct {
				Edges []struct {
					Node struct {
						Token string `json:"token"`
					} `json:"node"`
				} `json:"edges"`
			} `json:"soft_launch_invitations"`
		} `json:"me"`
	}
	if err := c.webGraphQL(ctx, find, map[string]any{"h": handle}, &data, func() bool { return data.Me != nil }); err != nil {
		return fmt.Errorf("hackerone: accept invite: %w", err)
	}
	if data.Me == nil || len(data.Me.Invitations.Edges) == 0 || data.Me.Invitations.Edges[0].Node.Token == "" {
		return fmt.Errorf("hackerone: accept invite: no open invitation to %s", handle)
	}
	const accept = `mutation AcceptSoftLaunchInvitation($token: String!) { acceptInvitation(input: {token: $token}) { was_successful } }`
	var res struct {
		Accept *struct {
			OK bool `json:"was_successful"`
		} `json:"acceptInvitation"`
	}
	if err := c.webGraphQL(ctx, accept, map[string]any{"token": data.Me.Invitations.Edges[0].Node.Token}, &res, nil); err != nil {
		return fmt.Errorf("hackerone: accept invite: %w", err)
	}
	if res.Accept == nil || !res.Accept.OK {
		return errors.New("hackerone: accept invite: HackerOne didn't accept it")
	}
	return nil
}

// KeepAlive implements platform.KeepAliver: with the website login it
// checks the session now and then (verifyEvery), signing in again if it
// has lapsed, so invitations and private stats always find it live.
func (c *Client) KeepAlive(ctx context.Context) error {
	if !c.loggedIn() {
		return nil
	}
	return c.ensureSession(ctx, false)
}
