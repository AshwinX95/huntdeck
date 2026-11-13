package intigriti

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"slices"
	"time"

	"github.com/mdpsec/huntdeck/internal/platform"
)

// Scope history: every published scope ("domains") version can be read at
// GET /v1/programs/{id}/domains/{versionId} → {domains: {id, createdAt,
// content}}. Version IDs come from the activity feed's "new domains version"
// events (fromVersionId, toVersionId), so one ScopeHistory call pages the whole
// feed (about 18 pages in 2026-10) and then reads each program's versions.
// Domain IDs stay the same across versions. A program with no events in the
// feed has one version: the current one. Verified live 2026-10-26.
const (
	feedMaxPages    = 100
	historyVersions = 100 // per program
)

type versionJSON struct {
	Domains *struct {
		ID        string       `json:"id"`
		CreatedAt *int64       `json:"createdAt"` // Unix seconds
		Content   []domainJSON `json:"content"`
	} `json:"domains"`
}

// ScopeHistory implements platform.ScopeHistorian.
func (c *Client) ScopeHistory(ctx context.Context, programs []platform.Program, save platform.ScopeHistorySaver) error {
	versions, err := c.domainVersions(ctx)
	if err != nil {
		return fmt.Errorf("intigriti: scope history: %w", err)
	}
	for _, p := range programs {
		ids := versions[p.PlatformID]
		var out []platform.ScopeVersion
		var err error
		if len(ids) == 0 {
			out, err = c.currentVersion(ctx, p)
		} else {
			out, err = c.readVersions(ctx, p, ids)
		}
		switch {
		case errors.Is(err, ErrAuth), errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
			return fmt.Errorf("intigriti: program %s: scope history: %w", p.Handle, err)
		case err != nil:
			continue
		}
		if err := save(p.Handle, out); err != nil {
			return err
		}
	}
	return nil
}

// activityJSON is one event of the program activity feed.
type activityJSON struct {
	ProgramID string `json:"programId"`
	Type      enum   `json:"type"`
	CreatedAt *int64 `json:"createdAt"` // Unix seconds
	Activity  struct {
		From       string `json:"fromVersionId"`
		To         string `json:"toVersionId"`
		FromStatus *enum  `json:"fromStatus"`
		ToStatus   *enum  `json:"toStatus"`
	} `json:"activity"`
}

// eachActivity pages the whole activity feed, newest first, calling fn
// with each event.
func (c *Client) eachActivity(ctx context.Context, fn func(activityJSON)) error {
	read := 0
	for page := 0; page < feedMaxPages; page++ {
		var feed struct {
			MaxCount *int           `json:"maxCount"`
			Records  []activityJSON `json:"records"`
		}
		if err := c.getJSON(ctx, fmt.Sprintf("%s/v1/programs/activities?limit=%d&offset=%d", c.base, pageSize, read), &feed); err != nil {
			return fmt.Errorf("activities at offset %d: %w", read, err)
		}
		if feed.MaxCount == nil {
			return errors.New("activities: response has no maxCount (format changed?)")
		}
		for _, r := range feed.Records {
			fn(r)
		}
		read += len(feed.Records)
		if len(feed.Records) == 0 || read >= *feed.MaxCount {
			return nil
		}
	}
	return nil
}

// domainVersions pages the whole activity feed and returns the domains
// version IDs seen per program.
func (c *Client) domainVersions(ctx context.Context) (map[string][]string, error) {
	out := map[string][]string{}
	seen := map[string]bool{}
	add := func(program, id string) {
		if id != "" && !seen[program+"/"+id] {
			seen[program+"/"+id] = true
			out[program] = append(out[program], id)
		}
	}
	err := c.eachActivity(ctx, func(r activityJSON) {
		if r.Type.ID == activityDomains {
			add(r.ProgramID, r.Activity.From)
			add(r.ProgramID, r.Activity.To)
		}
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// StateHistory implements platform.StateHistorian: the feed's status
// events, one pass over the whole feed (about 18 pages in 2026-10).
func (c *Client) StateHistory(ctx context.Context, programs []platform.Program, save platform.StateHistorySaver) error {
	byProgram := map[string][]platform.StateChange{}
	err := c.eachActivity(ctx, func(r activityJSON) {
		a := r.Activity
		if r.Type.ID != activityStatus || r.CreatedAt == nil || a.FromStatus == nil || a.ToStatus == nil {
			return
		}
		from, to := state(a.FromStatus.ID), state(a.ToStatus.ID)
		if from != to {
			byProgram[r.ProgramID] = append(byProgram[r.ProgramID],
				platform.StateChange{At: time.Unix(*r.CreatedAt, 0).UTC(), From: from, To: to})
		}
	})
	if err != nil {
		return fmt.Errorf("intigriti: state history: %w", err)
	}
	for _, p := range programs {
		changes := byProgram[p.PlatformID]
		if len(changes) == 0 {
			continue
		}
		slices.SortStableFunc(changes, func(a, b platform.StateChange) int { return a.At.Compare(b.At) })
		if err := save(p.Handle, changes); err != nil {
			return err
		}
	}
	return nil
}

// readVersions reads a program's domains versions, oldest first.
func (c *Client) readVersions(ctx context.Context, p platform.Program, ids []string) ([]platform.ScopeVersion, error) {
	if len(ids) > historyVersions {
		ids = ids[:historyVersions]
	}
	out := make([]platform.ScopeVersion, 0, len(ids))
	for _, id := range ids {
		var v versionJSON
		u := c.base.String() + "/v1/programs/" + url.PathEscape(p.PlatformID) + "/domains/" + url.PathEscape(id)
		if err := c.getJSON(ctx, u, &v); err != nil {
			return nil, fmt.Errorf("version %s: %w", id, err)
		}
		if sv, ok := v.version(); ok {
			out = append(out, sv)
		}
	}
	slices.SortStableFunc(out, func(a, b platform.ScopeVersion) int { return a.At.Compare(b.At) })
	return out, nil
}

// currentVersion reads the program's current domains, the only version of
// a program whose scope never changed while the feed was kept.
func (c *Client) currentVersion(ctx context.Context, p platform.Program) ([]platform.ScopeVersion, error) {
	var v versionJSON
	if err := c.getJSON(ctx, c.base.String()+"/v1/programs/"+url.PathEscape(p.PlatformID), &v); err != nil {
		return nil, err
	}
	if sv, ok := v.version(); ok {
		return []platform.ScopeVersion{sv}, nil
	}
	return nil, nil
}

func (v versionJSON) version() (platform.ScopeVersion, bool) {
	if v.Domains == nil || v.Domains.CreatedAt == nil {
		return platform.ScopeVersion{}, false
	}
	return platform.ScopeVersion{At: time.Unix(*v.Domains.CreatedAt, 0).UTC(), Entries: domainEntries(v.Domains.Content)}, true
}
