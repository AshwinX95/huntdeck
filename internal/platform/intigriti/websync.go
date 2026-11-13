package intigriti

import (
	"context"
	"errors"
	"fmt"

	"github.com/mdpsec/huntdeck/internal/platform"
)

// WebSyncConfigured implements platform.WebSyncer: true with IT_EMAIL and
// IT_PASSWORD set.
func (c *Client) WebSyncConfigured() bool { return c.loggedIn() }

// WebSync implements platform.WebSyncer: the logged-in stats (webstats.go).
// If the login fails it falls back to the public pages and still returns
// the login's error, so the run reports it.
func (c *Client) WebSync(ctx context.Context, handles []string, save platform.StatsSaver) error {
	if !c.loggedIn() {
		return ErrNoSession
	}
	err := c.fetchWebStats(ctx, handles, save)
	if err == nil || ctx.Err() != nil {
		return err
	}
	if perr := c.fetchPublicStats(ctx, handles, save); perr != nil {
		return errors.Join(err, perr)
	}
	return fmt.Errorf("%w (used the public pages instead)", err)
}

// WebSyncPulls implements platform.WebSyncer.
func (c *Client) WebSyncPulls() []string {
	return []string{
		"Reports submitted and accepted, total and average paid",
		"Researchers who contributed, and when the last report came in",
		"Average time to first response, triage and decision (last 90 days)",
		"The bounty table by tier and severity",
		"When a report was last accepted (from the program's recent activity)",
		"Program updates to researchers",
		"The brief: in-scope notes, out of scope, severity assessment and FAQ (changes are tracked)",
		"Whether it hands out test credentials, and if any are left",
		"Requirements: ID check, EEA residency, two-factor, accepting the program's terms, apply first",
		"Whether collaboration is allowed, and the company's industry",
	}
}

// WebSession implements platform.WebSyncer.
func (c *Client) WebSession() platform.WebSession {
	if c.web == nil {
		return platform.WebSession{}
	}
	return c.web.history.Report(c.web.appCookie(appSessionCookie) != "")
}
