package bugcrowd

import (
	"context"
	"fmt"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/mdpsec/huntdeck/internal/platform"
)

// Scope history: an engagement's changelog.json lists every published
// version of its brief, newest first, with publishedAt and tags ("targets",
// "brief", "migration", ...). The version documents hold the scope as it was.
// Target IDs stay the same across versions. Only the oldest version, the
// latest one and those tagged "targets" are read, at most historyVersions
// per program: about a third of all versions (verified live 2026-10-26).
const historyVersions = 60

// ScopeHistory implements platform.ScopeHistorian.
func (c *Client) ScopeHistory(ctx context.Context, programs []platform.Program, save platform.ScopeHistorySaver) error {
	for _, p := range programs {
		u, err := url.Parse(p.URL)
		if err != nil || !strings.HasPrefix(u.Path, "/engagements/") {
			continue // legacy briefs have no version history
		}
		versions, err := c.history(ctx, u.Path)
		switch {
		case err != nil && fatal(err):
			return fmt.Errorf("bugcrowd: program %s: scope history: %w", p.Handle, err)
		case err != nil:
			continue
		}
		if err := save(p.Handle, versions); err != nil {
			return err
		}
	}
	return nil
}

// history reads the scope versions of one brief, oldest first.
func (c *Client) history(ctx context.Context, brief string) ([]platform.ScopeVersion, error) {
	var cl struct {
		Changelogs []struct {
			State       *string `json:"changelogState"`
			ShowURL     string  `json:"changelogShowUrl"`
			PublishedAt string  `json:"publishedAt"`
			Tags        string  `json:"tags"`
		} `json:"changelogs"`
	}
	if err := c.getJSON(ctx, c.base.String()+brief+"/changelog.json", &cl); err != nil {
		return nil, err
	}
	type pick struct {
		doc string
		at  time.Time
	}
	var picks []pick
	for i, ch := range cl.Changelogs {
		at, err := time.Parse(time.RFC3339, ch.PublishedAt)
		if err != nil || !validDocPath(brief, ch.ShowURL) {
			continue
		}
		latest := i == 0 || (ch.State != nil && *ch.State == "Latest")
		oldest := i == len(cl.Changelogs)-1
		if latest || oldest || slices.Contains(strings.Split(ch.Tags, ","), "targets") {
			picks = append(picks, pick{ch.ShowURL, at.UTC()})
		}
	}
	slices.Reverse(picks) // oldest first
	if len(picks) > historyVersions {
		picks = append(picks[:1], picks[len(picks)-historyVersions+1:]...)
	}
	out := make([]platform.ScopeVersion, 0, len(picks))
	for _, p := range picks {
		var vd versionDoc
		if err := c.getJSON(ctx, c.base.String()+p.doc+".json", &vd); err != nil {
			return nil, fmt.Errorf("version %s: %w", p.at.Format(time.DateOnly), err)
		}
		if c.authenticated() && (vd.IsLoggedIn == nil || !*vd.IsLoggedIn) {
			return nil, ErrSessionExpired
		}
		out = append(out, platform.ScopeVersion{At: p.at, Entries: vd.entries()})
	}
	slices.SortStableFunc(out, func(a, b platform.ScopeVersion) int { return a.At.Compare(b.At) })
	return out, nil
}
