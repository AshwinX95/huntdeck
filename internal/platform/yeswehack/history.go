package yeswehack

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"slices"
	"time"

	"github.com/mdpsec/huntdeck/internal/platform"
)

// Scope history: GET /programs/{slug}/versions lists every accepted version
// of a program, newest first, in one response (no paging; 24 to 90 items in
// 2026-10). Each version's data holds only the fields it changed; the
// oldest holds them all. "scopes" and "out_of_scope" are always whole lists,
// so replaying the versions oldest first gives the scope at each one.
// Verified live 2026-10-26.

type versionJSON struct {
	Status     string                     `json:"status"` // "A": accepted
	AcceptedAt string                     `json:"accepted_at"`
	Data       map[string]json.RawMessage `json:"data"`
}

// ScopeHistory implements platform.ScopeHistorian. Programs whose versions
// can't be read are skipped.
func (c *Client) ScopeHistory(ctx context.Context, programs []platform.Program, save platform.ScopeHistorySaver) error {
	for _, p := range programs {
		var res struct {
			Items []versionJSON `json:"items"`
		}
		err := c.getJSON(ctx, c.base.String()+"/programs/"+url.PathEscape(p.Handle)+"/versions", &res)
		switch {
		case errors.Is(err, ErrAuth) || ctx.Err() != nil:
			return fmt.Errorf("yeswehack: program %s: scope history: %w", p.Handle, err)
		case err != nil:
			continue
		}
		versions, err := replay(res.Items)
		if err != nil {
			continue // a format we don't understand: leave the program undated
		}
		if len(versions) == 0 {
			continue
		}
		if err := save(p.Handle, versions); err != nil {
			return err
		}
	}
	return nil
}

// replay rebuilds the scope at each version that changed it, oldest first.
func replay(items []versionJSON) ([]platform.ScopeVersion, error) {
	type dated struct {
		at time.Time
		v  versionJSON
	}
	var accepted []dated
	for _, it := range items {
		at, err := time.Parse(time.RFC3339, it.AcceptedAt)
		if it.Status != "A" || err != nil {
			continue
		}
		accepted = append(accepted, dated{at.UTC(), it})
	}
	slices.SortStableFunc(accepted, func(a, b dated) int { return a.at.Compare(b.at) })

	var cur programDetail
	var out []platform.ScopeVersion
	for _, d := range accepted {
		changed := false
		for field, into := range map[string]any{"scopes": &cur.Scopes, "out_of_scope": &cur.OutOfScope, "bounty": &cur.Bounty} {
			raw, ok := d.v.Data[field]
			if !ok {
				continue
			}
			if err := json.Unmarshal(raw, into); err != nil {
				return nil, fmt.Errorf("version of %s: %s: %w", d.at.Format(time.DateOnly), field, err)
			}
			changed = changed || field != "bounty"
		}
		if changed {
			out = append(out, platform.ScopeVersion{At: d.at, Entries: cur.entries()})
		}
	}
	return out, nil
}
