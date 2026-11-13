// Package intigriti implements platform.Platform with Intigriti's official
// researcher API (https://api.intigriti.com/external/researcher/swagger),
// using a personal access token.
//
// Requests per poll: one per 500 programs, one for the newest 500 events of
// the program activity feed, plus one per program whose scope is fetched
// (scope comes with the program details). The feed says which programs got a
// new scope ("domains") version, so polls fetch only those, and every
// program at least daily. Full polls also read program stats about daily
// from the public program pages (see stats.go): one request for the list of
// them plus one per public program.
package intigriti

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/mdpsec/huntdeck/internal/httpclient"
	"github.com/mdpsec/huntdeck/internal/platform"
	"github.com/mdpsec/huntdeck/internal/scope"
	"github.com/mdpsec/huntdeck/internal/weblogin"
)

// Name is the platform identifier stored in the database.
const Name = "intigriti"

const (
	defaultBaseURL = "https://api.intigriti.com/external/researcher"
	webBase        = "https://app.intigriti.com"
	// WebHome is the page the login browser's Intigriti tab opens at.
	WebHome   = webBase + "/researcher/dashboard"
	publicAPI = webBase + "/api/core/public"
	pageSize  = 500 // the API maximum
	maxPages  = 100
)

// Status, confidentiality and tier IDs used by the API (seen live 2026-10).
const (
	statusOpen      = 3
	statusSuspended = 4 // paused, e.g. close to budget
	tierOutOfScope  = 5 // others: 1 No Bounty, 2 Tier 3, 3 Tier 2, 4 Tier 1

	levelApplication = 2 // anyone can apply
	levelRegistered  = 3 // any logged-in researcher
	levelPublic      = 4

	activityDomains = 1 // a new domains (scope) version
	activityStatus  = 3 // a program status change; 2 is a new rules version
)

// ErrAuth means Intigriti rejected the token.
var ErrAuth = errors.New("intigriti: token rejected; create a new one at app.intigriti.com (personal access tokens) and set IT_TOKEN")

// Options configures a Client.
type Options struct {
	Token     string
	HTTP      *httpclient.Client
	BaseURL   string // tests only
	PublicURL string // tests only: the public program pages' JSON

	// Email and Password (and TOTPSecret, with 2FA) sign in to the web app
	// for the stats the API lacks (see session.go). Jar must be the cookie
	// jar HTTP was built with, and HTTP must allow redirects to
	// login.intigriti.com. Chrome is the browser for the first login (default:
	// one found on PATH).
	Email, Password, TOTPSecret string
	Browser                     *weblogin.Host // the login browser, shared with other platforms
	// LoginState, if set, is a file recording when a browser login last
	// failed, so the wait before the next one survives a restart.
	LoginState string
	Jar        http.CookieJar
	AppURL     string // tests only: the web app
	// Login replaces the browser login, in tests.
	Login func(ctx context.Context) error
}

// Client is an Intigriti researcher API client.
type Client struct {
	http   *httpclient.Client
	base   *url.URL
	public *url.URL
	token  string
	web    *webSession // nil without a web login
	tokenS platform.CredentialState

	metaMu sync.Mutex
	meta   map[string]webMeta // by program ID, from the logged-in program list
}

// New returns a Client.
func New(o Options) (*Client, error) {
	if o.Token == "" {
		return nil, errors.New("intigriti: token is required")
	}
	if o.BaseURL == "" {
		o.BaseURL = defaultBaseURL
	}
	if o.PublicURL == "" {
		o.PublicURL = publicAPI
	}
	base, err := url.Parse(strings.TrimSuffix(o.BaseURL, "/"))
	if err != nil {
		return nil, fmt.Errorf("intigriti: base URL: %w", err)
	}
	public, err := url.Parse(strings.TrimSuffix(o.PublicURL, "/"))
	if err != nil {
		return nil, fmt.Errorf("intigriti: public URL: %w", err)
	}
	c := &Client{http: o.HTTP, base: base, public: public, token: o.Token}
	if o.Email != "" || o.Password != "" {
		if o.Email == "" || o.Password == "" || o.Jar == nil {
			return nil, errors.New("intigriti: a web login needs the email, the password and a cookie jar")
		}
		if o.AppURL == "" {
			o.AppURL = webBase
		}
		app, err := url.Parse(strings.TrimSuffix(o.AppURL, "/"))
		if err != nil {
			return nil, fmt.Errorf("intigriti: app URL: %w", err)
		}
		c.web = &webSession{account: &weblogin.Account{Who: who, Email: o.Email, Password: o.Password, TOTPSecret: o.TOTPSecret, TOTPEnv: "IT_TOTP_SECRET"},
			host: o.Browser, history: weblogin.NewHistory(o.LoginState), jar: o.Jar, app: app, login: o.Login}
		if c.web.login == nil {
			if o.Browser == nil {
				return nil, errors.New("intigriti: a web login needs the login browser")
			}
			c.web.login = c.browserLogin
		}
	}
	return c, nil
}

// Account implements platform.AccountNamer: the web login's email, which
// lists private programs, else the API token (each is one account's).
func (c *Client) Account() string {
	if c.web != nil {
		return "web:" + strings.ToLower(c.web.account.Email)
	}
	return "token:" + c.token
}

// Name implements platform.Platform.
func (c *Client) Name() string { return Name }

// Stats implements platform.Platform.
func (c *Client) Stats() (int64, int64) { return c.http.Stats() }

type enum struct {
	ID    int    `json:"id"`
	Value string `json:"value"`
}

type money struct {
	Value    float64 `json:"value"`
	Currency string  `json:"currency"`
}

type programJSON struct {
	ID                   string `json:"id"`
	Handle               string `json:"handle"`
	Name                 string `json:"name"`
	MinBounty            *money `json:"minBounty"`
	MaxBounty            *money `json:"maxBounty"`
	ConfidentialityLevel enum   `json:"confidentialityLevel"`
	Status               enum   `json:"status"`
	WebLinks             struct {
		Detail string `json:"detail"`
	} `json:"webLinks"`
}

// ListPrograms implements platform.Platform.
func (c *Client) ListPrograms(ctx context.Context) (platform.Listing, error) {
	var out platform.Listing
	seen := map[string]bool{}
	listed, total := 0, -1
	for page := 0; total < 0 || listed < total; page++ {
		if page >= maxPages {
			return out, fmt.Errorf("intigriti: list programs: more than %d pages", maxPages)
		}
		var p struct {
			MaxCount *int              `json:"maxCount"`
			Records  []json.RawMessage `json:"records"`
		}
		u := fmt.Sprintf("%s/v1/programs?limit=%d&offset=%d", c.base, pageSize, listed)
		if err := c.getJSON(ctx, u, &p); err != nil {
			return out, fmt.Errorf("intigriti: list programs at offset %d: %w", listed, err)
		}
		if p.MaxCount == nil {
			return out, errors.New("intigriti: list programs: response has no maxCount (format changed?)")
		}
		total = *p.MaxCount
		if len(p.Records) == 0 {
			break
		}
		for _, raw := range p.Records {
			listed++
			var pj programJSON
			if err := json.Unmarshal(raw, &pj); err != nil {
				return out, fmt.Errorf("intigriti: decode program: %w", err)
			}
			if pj.ID == "" || strings.ContainsAny(pj.ID, "/?#") {
				return out, fmt.Errorf("intigriti: program %q has unexpected id %q", pj.Name, pj.ID)
			}
			prog := convertProgram(pj, raw)
			c.enrich(&prog, pj.ConfidentialityLevel.ID)
			if seen[prog.Handle] {
				continue
			}
			seen[prog.Handle] = true
			out.Programs = append(out.Programs, prog)
		}
	}
	if listed < total {
		return out, fmt.Errorf("intigriti: listing returned %d of %d programs; it changed while paging", listed, total)
	}
	out.Complete = true
	c.addScopeChanges(ctx, &out)
	return out, nil
}

// addScopeChanges reads the newest page of the program activity feed and
// sets ScopeChangedAt for programs with a new domains version in it. The
// page covers every event since its oldest one, which becomes ChangesSince.
// A failure leaves the listing as it is, with ChangesErr set.
func (c *Client) addScopeChanges(ctx context.Context, l *platform.Listing) {
	var feed struct {
		MaxCount *int `json:"maxCount"`
		Records  []struct {
			ProgramID string `json:"programId"`
			Type      enum   `json:"type"`
			CreatedAt *int64 `json:"createdAt"` // Unix seconds
		} `json:"records"`
	}
	if err := c.getJSON(ctx, fmt.Sprintf("%s/v1/programs/activities?limit=%d&offset=0", c.base, pageSize), &feed); err != nil {
		l.ChangesErr = fmt.Errorf("intigriti: program activities: %w", err)
		return
	}
	if feed.MaxCount == nil {
		l.ChangesErr = errors.New("intigriti: program activities: response has no maxCount (format changed?)")
		return
	}
	since := time.Unix(0, 0).UTC() // the page holds the whole feed
	partial := len(feed.Records) < *feed.MaxCount
	latest := map[string]time.Time{}
	for i, r := range feed.Records {
		if r.CreatedAt == nil || r.ProgramID == "" {
			l.ChangesErr = errors.New("intigriti: program activities: event without program or time (format changed?)")
			return
		}
		at := time.Unix(*r.CreatedAt, 0).UTC()
		if partial && (i == 0 || at.Before(since)) {
			since = at
		}
		if r.Type.ID == activityDomains && at.After(latest[r.ProgramID]) {
			latest[r.ProgramID] = at
		}
	}
	for i := range l.Programs {
		if at, ok := latest[l.Programs[i].PlatformID]; ok {
			l.Programs[i].ScopeChangedAt = &at
		}
	}
	l.ChangesSince = &since
}

func convertProgram(pj programJSON, raw json.RawMessage) platform.Program {
	p := platform.Program{
		Handle:     pj.ID, // handles are only unique per company; the ID is stable
		PlatformID: pj.ID,
		Name:       strings.TrimSpace(pj.Name),
		URL:        webURL(pj.WebLinks.Detail),
		Private:    private(pj.ConfidentialityLevel.ID),
		State:      state(pj.Status.ID),
		// The listing says when researchers must apply to a program.
		Requirements: &platform.Requirements{Application: pj.ConfidentialityLevel.ID == levelApplication},
		Raw:          raw,
	}
	if pj.MaxBounty != nil && pj.MaxBounty.Value > 0 {
		p.OffersBounty = true
		p.Currency = strings.ToUpper(pj.MaxBounty.Currency)
		p.BountyMax = &pj.MaxBounty.Value
		if pj.MinBounty != nil && pj.MinBounty.Value > 0 {
			p.BountyMin = &pj.MinBounty.Value
		}
	}
	return p
}

// state maps a program status to a state: anything but open or suspended
// (draft, closed, archived) counts as closed.
func state(status int) string {
	switch status {
	case statusOpen:
		return platform.StateOpen
	case statusSuspended:
		return platform.StatePaused
	}
	return platform.StateClosed
}

// private reports whether a confidentiality level means an invitation
// (InviteOnly); unknown levels count as private to be safe.
func private(level int) bool {
	switch level {
	case levelApplication, levelRegistered, levelPublic:
		return false
	}
	return true
}

// webURL turns the API's detail link into a link that opens the program
// for the signed-in researcher. Links point at app.intigriti.com, sometimes
// through a ?redirect= parameter.
//
// The app's session cookie is SameSite=Strict, so a link followed from
// another site (HuntDeck) arrives without it: /programs/{company}/{handle}
// /detail, and /researcher/programs/… too, then show the public page,
// Forbidden for a private program. The app's own search links
// /researcher/program-redirect/{company}/{handle} instead, which signs in
// first (through login.intigriti.com, whose cookie is sent) and opens the
// program (verified clicking from another site, 2026-11-09).
func webURL(detail string) string {
	u, err := url.Parse(detail)
	if err != nil || detail == "" {
		return webBase + "/researcher/programs"
	}
	if r := u.Query().Get("redirect"); strings.HasPrefix(r, "/") && !strings.HasPrefix(r, "//") {
		if l, ok := ProgramLink(webBase + r); ok {
			return l
		}
		return webBase + "/researcher" + r
	}
	if l, ok := ProgramLink(detail); ok {
		return l
	}
	if u.Scheme == "https" && strings.HasSuffix(u.Host, "intigriti.com") {
		return u.String()
	}
	return webBase + "/researcher/programs"
}

type domainJSON struct {
	ID          string `json:"id"`
	Type        enum   `json:"type"`
	Endpoint    string `json:"endpoint"`
	Tier        enum   `json:"tier"`
	Description string `json:"description"`
}

// FetchScope implements platform.Platform.
func (c *Client) FetchScope(ctx context.Context, p platform.Program) ([]platform.ScopeEntry, error) {
	entries, _, err := c.FetchScopeDetail(ctx, p)
	return entries, err
}

// FetchScopeDetail implements platform.ScopeDetailFetcher: the program
// holds its rules of engagement (Markdown). The API has no bounty table.
func (c *Client) FetchScopeDetail(ctx context.Context, p platform.Program) ([]platform.ScopeEntry, platform.ScopeDetail, error) {
	var d struct {
		Domains *struct {
			Content []domainJSON `json:"content"`
		} `json:"domains"`
		Rules *struct {
			Content *struct {
				Description string       `json:"description"`
				Testing     *testingJSON `json:"testingRequirements"`
			} `json:"content"`
		} `json:"rulesOfEngagement"`
	}
	if err := c.getJSON(ctx, c.base.String()+"/v1/programs/"+url.PathEscape(p.PlatformID), &d); err != nil {
		return nil, platform.ScopeDetail{}, fmt.Errorf("intigriti: program %s: %w", p.Handle, err)
	}
	if d.Domains == nil {
		return nil, platform.ScopeDetail{}, fmt.Errorf("intigriti: program %s: %w (no published domains)", p.Handle, platform.ErrScopeUnavailable)
	}
	var detail platform.ScopeDetail
	if d.Rules != nil && d.Rules.Content != nil {
		detail.Policy = strings.TrimSpace(d.Rules.Content.Description)
		if t := d.Rules.Content.Testing; t != nil {
			detail.Testing = t.rules()
		}
	}
	return domainEntries(d.Domains.Content), detail, nil
}

// testingJSON is a program's testing requirements. The site shows
// automatedTooling as "max. N requests/sec", 0 as "Not allowed" and null as
// "Not applicable", and intigritiMe as whether test accounts need your
// @intigriti.me address (read from its app 2026-10-28).
type testingJSON struct {
	IntigritiMe      bool     `json:"intigritiMe"`
	AutomatedTooling *float64 `json:"automatedTooling"`
	UserAgent        string   `json:"userAgent"`
	RequestHeader    string   `json:"requestHeader"`
}

func (t testingJSON) rules() *platform.TestingRules {
	r := &platform.TestingRules{UserAgent: strings.TrimSpace(t.UserAgent), Header: strings.TrimSpace(t.RequestHeader), PlatformEmail: t.IntigritiMe}
	if a := t.AutomatedTooling; a != nil {
		r.RateLimit, r.NoAutomation = max(*a, 0), *a <= 0
	}
	return r
}

func domainEntries(content []domainJSON) []platform.ScopeEntry {
	out := make([]platform.ScopeEntry, 0, len(content))
	for _, dom := range content {
		in := dom.Tier.ID != tierOutOfScope
		ident := strings.TrimSpace(dom.Endpoint)
		e := platform.ScopeEntry{
			PlatformID:        dom.ID,
			InScope:           in,
			AssetType:         assetType(dom.Type, ident),
			PlatformAssetType: dom.Type.Value,
			Identifier:        ident,
			BountyEligible:    in && !strings.EqualFold(dom.Tier.Value, "No Bounty"),
			Instruction:       strings.TrimSpace(dom.Description),
		}
		if in && dom.Tier.Value != "" {
			e.MaxSeverity = dom.Tier.Value // "Tier 1" .. "Tier 3" or "No Bounty"
		}
		out = append(out, e)
	}
	return out
}

// assetType maps Intigriti domain types (by ID, with the name as a fallback)
// to normalised asset types.
func assetType(t enum, ident string) string {
	switch {
	case t.ID == 1 || strings.EqualFold(t.Value, "url"):
		return scope.WebType(ident)
	case t.ID == 2 || strings.EqualFold(t.Value, "android"):
		return scope.AssetAndroid
	case t.ID == 3 || strings.EqualFold(t.Value, "ios"):
		return scope.AssetIOS
	case t.ID == 4 || strings.EqualFold(t.Value, "iprange"):
		if strings.Contains(ident, "/") {
			return scope.AssetCIDR
		}
		return scope.AssetIP
	case t.ID == 5 || strings.EqualFold(t.Value, "device"):
		return scope.AssetHardware
	case t.ID == 7 || strings.EqualFold(t.Value, "wildcard"):
		return scope.AssetWildcard
	default:
		return scope.AssetOther
	}
}

func (c *Client) getJSON(ctx context.Context, u string, into any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Accept", "application/json")
	res, err := c.http.Do(ctx, req)
	if err != nil {
		return err
	}
	switch res.StatusCode {
	case http.StatusOK:
		c.tokenS.Accepted()
	case http.StatusUnauthorized:
		c.tokenS.Refused("Intigriti refused it (401): create a new personal access token and set IT_TOKEN", false)
		return ErrAuth
	case http.StatusForbidden:
		c.tokenS.Accepted() // the token is fine; this program's scope isn't shown to it
		return fmt.Errorf("HTTP 403: %w", platform.ErrScopeUnavailable)
	default:
		return fmt.Errorf("HTTP %d", res.StatusCode)
	}
	if err := json.Unmarshal(res.Body, into); err != nil {
		return fmt.Errorf("decode response: %w", err)
	}
	return nil
}

// Credentials reports the API token's state; the web login reports its own
// (WebSession). See platform.CredentialReporter.
func (c *Client) Credentials() []platform.Credential {
	return []platform.Credential{c.tokenS.Report("API token")}
}

// programPage matches a link to a program's page in the web app, public
// (/programs/…) or researcher (/researcher/programs/…).
var programPage = regexp.MustCompile(`(?i)^https://app\.intigriti\.com(?:/researcher)?/programs/([^/?#]+)/([^/?#]+)(?:[/?#].*)?$`)

// ProgramLink turns a link to an Intigriti program's page into one that
// opens it from another site, signed in (see webURL); ok is false for a
// link that isn't to a program's page.
func ProgramLink(link string) (string, bool) {
	m := programPage.FindStringSubmatch(link)
	if m == nil {
		return "", false
	}
	return webBase + "/researcher/program-redirect/" + m[1] + "/" + m[2], true
}
