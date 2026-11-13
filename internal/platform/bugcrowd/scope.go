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

// FetchScope implements platform.Platform.
func (c *Client) FetchScope(ctx context.Context, p platform.Program) ([]platform.ScopeEntry, error) {
	entries, _, err := c.FetchScopeDetail(ctx, p)
	return entries, err
}

// FetchScopeDetail implements platform.ScopeDetailFetcher: an engagement's
// brief document also holds its rules and reward ranges. Legacy briefs
// report neither.
func (c *Client) FetchScopeDetail(ctx context.Context, p platform.Program) ([]platform.ScopeEntry, platform.ScopeDetail, error) {
	if in, ok := c.invited(p.Handle); ok {
		// Not accepted yet: the brief answers 404, the invitation has it.
		vd := in.document()
		return vd.entries(), vd.detail(), nil
	}
	u, err := url.Parse(p.URL)
	if err != nil || u.Path == "" {
		return nil, platform.ScopeDetail{}, fmt.Errorf("bugcrowd: program %s: bad URL %q", p.Handle, p.URL)
	}
	brief := u.Path
	if !strings.HasPrefix(brief, "/engagements/") {
		entries, err := c.legacyScope(ctx, brief)
		if err != nil {
			return nil, platform.ScopeDetail{}, fmt.Errorf("bugcrowd: program %s: %w", p.Handle, err)
		}
		return entries, platform.ScopeDetail{}, nil
	}

	doc, published, err := c.latestDocument(ctx, brief)
	if err != nil {
		return nil, platform.ScopeDetail{}, fmt.Errorf("bugcrowd: program %s: %w", p.Handle, err)
	}
	var vd versionDoc
	if err := c.getJSON(ctx, c.base.String()+doc+".json", &vd); err != nil {
		return nil, platform.ScopeDetail{}, fmt.Errorf("bugcrowd: program %s: brief document: %w", p.Handle, err)
	}
	if c.authenticated() && (vd.IsLoggedIn == nil || !*vd.IsLoggedIn) {
		// Logged-out documents hide some target names; never store those
		// over the real ones.
		return nil, platform.ScopeDetail{}, fmt.Errorf("bugcrowd: program %s: %w", p.Handle, ErrSessionExpired)
	}
	d := vd.detail()
	if d.State != "" && !published.IsZero() {
		d.StateSince = &published // a pause or resume publishes a new version
	}
	return vd.entries(), d, nil
}

// ScopeChangedAt implements platform.ScopeChangeChecker: the publication
// time of the brief's latest version, which holds the scope. Legacy briefs
// and briefs without a usable changelog report the zero time.
func (c *Client) ScopeChangedAt(ctx context.Context, p platform.Program) (time.Time, error) {
	if _, ok := c.invited(p.Handle); ok {
		return time.Time{}, nil // no changelog until accepted
	}
	u, err := url.Parse(p.URL)
	if err != nil || !strings.HasPrefix(u.Path, "/engagements/") {
		return time.Time{}, nil
	}
	latest, err := c.latestChangelog(ctx, u.Path)
	if err != nil {
		if fatal(err) {
			return time.Time{}, fmt.Errorf("bugcrowd: program %s: changelog: %w", p.Handle, err)
		}
		return time.Time{}, nil
	}
	return latest.published, nil
}

// changelogEntry is the brief's latest published version.
type changelogEntry struct {
	doc       string    // e.g. "/engagements/acme/changelog/<uuid>"
	published time.Time // zero if not given
}

// latestChangelog reads the brief's changelog.json. An entry without a valid
// document path is an error.
func (c *Client) latestChangelog(ctx context.Context, brief string) (changelogEntry, error) {
	var cl struct {
		Changelogs []struct {
			State       *string `json:"changelogState"`
			ShowURL     string  `json:"changelogShowUrl"`
			PublishedAt string  `json:"publishedAt"`
		} `json:"changelogs"`
	}
	if err := c.getJSON(ctx, c.base.String()+brief+"/changelog.json", &cl); err != nil {
		return changelogEntry{}, err
	}
	if len(cl.Changelogs) == 0 {
		return changelogEntry{}, errors.New("empty changelog")
	}
	latest := cl.Changelogs[0] // newest first
	for _, ch := range cl.Changelogs {
		if ch.State != nil && *ch.State == "Latest" {
			latest = ch
			break
		}
	}
	if !validDocPath(brief, latest.ShowURL) {
		return changelogEntry{}, fmt.Errorf("unexpected changelog link %q", latest.ShowURL)
	}
	e := changelogEntry{doc: latest.ShowURL}
	if t, err := time.Parse(time.RFC3339, latest.PublishedAt); err == nil {
		e.published = t.UTC()
	}
	return e, nil
}

// latestDocument returns the path of the brief's current version document,
// e.g. "/engagements/acme/changelog/<uuid>"; published is when that version was published, zero if not known.
func (c *Client) latestDocument(ctx context.Context, brief string) (doc string, published time.Time, err error) {
	latest, err := c.latestChangelog(ctx, brief)
	switch {
	case err == nil:
		return latest.doc, latest.published, nil
	case fatal(err):
		return "", time.Time{}, err
	}
	// No usable changelog: read the link from the brief page instead.
	doc, err = c.documentFromBrief(ctx, brief)
	return doc, time.Time{}, err
}

func validDocPath(brief, doc string) bool {
	rest, ok := strings.CutPrefix(doc, brief+"/changelog/")
	return ok && rest != "" && !strings.ContainsAny(rest, "/?#.\\")
}

// documentFromBrief reads the version-document link from the brief page's
// ResearcherEngagementBrief element.
func (c *Client) documentFromBrief(ctx context.Context, brief string) (string, error) {
	res, err := c.get(ctx, c.base.String()+brief, "text/html")
	if err != nil {
		return "", err
	}
	switch {
	case isRedirect(res.StatusCode):
		if c.authenticated() {
			return "", ErrSessionExpired
		}
		return "", fmt.Errorf("brief page: %w (needs login)", platform.ErrScopeUnavailable)
	case res.StatusCode != http.StatusOK:
		return "", fmt.Errorf("brief page: HTTP %d", res.StatusCode)
	}
	endpoints, found := reactProps(res.Body, "ResearcherEngagementBrief", "data-api-endpoints")
	if !found {
		if bytes.Contains(res.Body, []byte("ResearcherEngagementCompliance")) {
			return "", fmt.Errorf("brief page: %w (Bugcrowd wants you to accept terms or enable 2FA first)", platform.ErrScopeUnavailable)
		}
		if props, moved := reactProps(res.Body, "ProfileEngagementShardMigrated", "data-react-props"); moved {
			// Still listed on bugcrowd.com, but now run on another Bugcrowd
			// instance (such as Bugcrowd Gov) that needs its own account.
			var m struct {
				Destination string `json:"destinationShardDisplayName"`
			}
			_ = json.Unmarshal([]byte(props), &m)
			if m.Destination == "" {
				m.Destination = "another Bugcrowd instance"
			}
			return "", fmt.Errorf("brief page: %w (engagement moved to %s)", platform.ErrScopeUnavailable, m.Destination)
		}
		return "", errors.New("brief page: no ResearcherEngagementBrief element (page layout changed?)")
	}
	var ep struct {
		Brief struct {
			Doc string `json:"getBriefVersionDocument"`
		} `json:"engagementBriefApi"`
	}
	if err := json.Unmarshal([]byte(endpoints), &ep); err != nil {
		return "", fmt.Errorf("brief page: decode endpoints: %w", err)
	}
	if !validDocPath(brief, ep.Brief.Doc) {
		return "", fmt.Errorf("brief page: %w (no published brief document)", platform.ErrScopeUnavailable)
	}
	return ep.Brief.Doc, nil
}

// reactProps returns attribute attr of the first element whose
// data-react-class is class.
func reactProps(body []byte, class, attr string) (string, bool) {
	z := html.NewTokenizer(bytes.NewReader(body))
	for {
		switch z.Next() {
		case html.ErrorToken:
			return "", false
		case html.StartTagToken, html.SelfClosingTagToken:
			var isClass bool
			var val string
			var hasVal bool
			for {
				k, v, more := z.TagAttr()
				switch string(k) {
				case "data-react-class":
					isClass = string(v) == class
				case attr:
					val, hasVal = string(v), true
				}
				if !more {
					break
				}
			}
			if isClass && hasVal {
				return val, true
			}
		}
	}
}

type versionDoc struct {
	IsLoggedIn *bool `json:"isLoggedIn"`
	// Participation is "open" for anyone, else how hackers get in (by
	// application, by invitation). ShowIDAlert is set when the engagement
	// wants identity verification the logged-in hacker hasn't done.
	Participation         string `json:"participation"`
	ShowIDAlert           *bool  `json:"showIdVerificationAlert"`
	CollaborationEnabled  *bool  `json:"collaborationEnabled"`
	CoordinatedDisclosure *bool  `json:"coordinatedDisclosure"`
	Data                  struct {
		Brief struct {
			Description           string `json:"description"`     // HTML
			TargetsOverview       string `json:"targetsOverview"` // HTML: rewards, rules and reporting
			AdditionalInformation string `json:"additionalInformation"`
			SafeHarbor            *struct {
				Status string `json:"status"` // "full" or "partial"
			} `json:"safeHarborStatus"`
		} `json:"brief"`
		Scope      []scopeGroup `json:"scope"`
		Engagement struct {
			StartsAt *time.Time `json:"startsAt"` // "2026-10-26T14:40:53Z"
			// State is "in_progress", "in_progress_paused" (verified live
			// 2026-11-05 on a paused engagement), or another for one that
			// ended.
			State string `json:"state"`
		} `json:"engagement"`
	} `json:"data"`
}

// scopeGroup is one group of a brief's targets, with its reward range.
// Pending invitations carry the same groups (invites.go).
type scopeGroup struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	InScope     bool   `json:"inScope"`
	RewardRange *struct {
		P1    *int `json:"p1MaxCents"`
		P2    *int `json:"p2MaxCents"`
		P3    *int `json:"p3MaxCents"`
		P4    *int `json:"p4MaxCents"`
		P5    *int `json:"p5MaxCents"`
		P1Min *int `json:"p1MinCents"`
		P2Min *int `json:"p2MinCents"`
		P3Min *int `json:"p3MinCents"`
		P4Min *int `json:"p4MinCents"`
	} `json:"rewardRange"`
	Targets []targetJSON `json:"targets"`
}

type targetJSON struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	URI         string `json:"uri"`
	Category    string `json:"category"`
	IPAddress   string `json:"ipAddress"`
	Description string `json:"description"`
	GroupTarget *struct {
		ID string `json:"id"`
	} `json:"engagementBriefTargetGroupTarget"`
}

func (vd versionDoc) entries() []platform.ScopeEntry {
	var out []platform.ScopeEntry
	for _, g := range vd.Data.Scope {
		paid := false
		if r := g.RewardRange; r != nil {
			for _, v := range []*int{r.P1, r.P2, r.P3, r.P4, r.P5} {
				paid = paid || (v != nil && *v > 0)
			}
		}
		for _, t := range g.Targets {
			id := g.ID + ":" + t.ID
			if t.GroupTarget != nil && t.GroupTarget.ID != "" {
				id = t.GroupTarget.ID
			}
			out = append(out, t.entry(id, g.InScope, g.InScope && paid))
		}
	}
	return out
}

func (t targetJSON) entry(id string, inScope, bounty bool) platform.ScopeEntry {
	name := strings.TrimSpace(t.Name)
	ident := firstNonEmpty(strings.TrimSpace(t.URI), strings.TrimSpace(t.IPAddress), name)
	var notes []string
	if name != "" && name != ident {
		notes = append(notes, name)
	}
	if d := strings.TrimSpace(t.Description); d != "" {
		notes = append(notes, d)
	}
	return platform.ScopeEntry{
		PlatformID:        id,
		InScope:           inScope,
		AssetType:         assetType(t.Category, ident),
		PlatformAssetType: t.Category,
		Identifier:        ident,
		BountyEligible:    bounty,
		Instruction:       strings.Join(notes, "\n\n"),
	}
}

// legacyScope reads scope from the target-group endpoints older programs
// used. No public program uses them any more (2026-10), but private ones
// might.
func (c *Client) legacyScope(ctx context.Context, brief string) ([]platform.ScopeEntry, error) {
	var groups struct {
		Groups []struct {
			InScope    bool   `json:"in_scope"`
			TargetsURL string `json:"targets_url"`
		} `json:"groups"`
	}
	if err := c.getJSON(ctx, c.base.String()+brief+"/target_groups", &groups); err != nil {
		return nil, fmt.Errorf("target groups: %w", err)
	}
	var out []platform.ScopeEntry
	for i, g := range groups.Groups {
		if !strings.HasPrefix(g.TargetsURL, "/") || strings.HasPrefix(g.TargetsURL, "//") {
			return nil, fmt.Errorf("target group %d: unexpected targets_url %q", i, g.TargetsURL)
		}
		var ts struct {
			Targets []targetJSON `json:"targets"`
		}
		if err := c.getJSON(ctx, c.base.String()+g.TargetsURL, &ts); err != nil {
			return nil, fmt.Errorf("target group %d: %w", i, err)
		}
		for _, t := range ts.Targets {
			id := ""
			if t.ID != "" {
				id = fmt.Sprintf("g%d:%s", i, t.ID)
			}
			out = append(out, t.entry(id, g.InScope, false))
		}
	}
	return out, nil
}
