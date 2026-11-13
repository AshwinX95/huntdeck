// Package bugcrowd implements platform.Platform for Bugcrowd. Bugcrowd has
// no researcher API, so this reads the JSON the bugcrowd.com web app uses.
// It is undocumented and can change without notice.
//
// Requests per poll, at BC_RATE (default 1/s; Bugcrowd's WAF blocks faster
// clients for hours):
//   - listing: one per 24 engagements (22 for the 520 public ones in
//     2026-10), plus two session checks when logged in. Quick polls read
//     only the newest page (ListRecent: one request plus one session
//     check) between complete listings every two hours;
//   - scope: one changelog.json (about 3 KB) per program, whose newest
//     entry says when the brief last changed (ScopeChangedAt). Full polls
//     then fetch the brief's version document only when it changed since
//     the last download, or that was over a week ago: one more request,
//     plus the changelog again;
//   - pending invitations, when logged in: one request per listing (see
//     invites.go);
//   - stats: one /statistics per program in full polls, about daily;
//   - accepted reports (CrowdStream): a page or two of the all-programs
//     feed every poll; in full polls one request per 20 reports in a
//     program's six-month window, for every program once and then weekly
//     (see crowdstream.go).
//
// Three modes: public only (no credentials); a session cookie copied from a
// browser; or email + password (+ TOTP secret) through Bugcrowd's Okta
// login, which also re-logs in when the session ends. When logged in, the
// session is checked before and after listing, because an expired session
// still gets HTTP 200 with the public listing, which would make every
// private program look removed.
package bugcrowd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/mdpsec/huntdeck/internal/httpclient"
	"github.com/mdpsec/huntdeck/internal/platform"
	"github.com/mdpsec/huntdeck/internal/weblogin"
)

// Name is the platform identifier stored in the database.
const Name = "bugcrowd"

const (
	defaultBaseURL     = "https://bugcrowd.com"
	defaultIdentityURL = "https://identity.bugcrowd.com"
	defaultOktaURL     = "https://login.hackers.bugcrowd.com"
	sessionCookie      = "_bugcrowd_session"
	maxPages           = 200
)

var (
	// ErrSessionExpired means Bugcrowd no longer accepts the session and
	// HuntDeck has no password to log in again with.
	ErrSessionExpired = errors.New("bugcrowd: session expired or invalid; copy a fresh BC_SESSION from your browser, or set BC_EMAIL/BC_PASSWORD/BC_TOTP_SECRET")
	// ErrBlocked means Bugcrowd's WAF refused the request.
	ErrBlocked = errors.New("bugcrowd: request blocked (HTTP 403/406 from the WAF); wait a few hours or lower BC_RATE")
)

// Options configures a Client.
type Options struct {
	Session    string // a Cookie header, or a bare _bugcrowd_session value
	Email      string
	Password   string
	TOTPSecret string // base32 key of the authenticator app

	// HTTP sets rate, proxy and so on. The cookie jar and login redirect
	// hosts are added by New.
	HTTP httpclient.Options

	// Browser, if set, is the shared login browser: email and password
	// logins run there (browserlogin.go) rather than over plain HTTP, and
	// the session lives in Jar (saved across restarts, a sessionjar for
	// bugcrowd.com, the identity service and Okta). LoginState is where
	// the login history is kept (weblogin.History).
	Browser    *weblogin.Host
	Jar        http.CookieJar
	LoginState string

	// Tests only.
	BaseURL, IdentityURL, OktaURL string
	Now                           func() time.Time
}

// Client is a Bugcrowd client. It is safe for concurrent use.
type Client struct {
	http, login          *httpclient.Client
	jar                  http.CookieJar
	base, identity, okta *url.URL

	email, password, totpSecret string
	hasSession                  bool
	now                         func() time.Time
	sleep                       func(context.Context, time.Duration) error

	// With the login browser: the shared Chrome, the account it signs in
	// with, and how its logins went.
	host    *weblogin.Host
	account *weblogin.Account
	logins  *weblogin.History

	authMu       sync.Mutex
	loginErr     error // credentials were rejected: don't retry (account lockout)
	lastTOTPStep int64
	cred         platform.CredentialState // the login, or the pasted session

	// ranks are the scope ranks from the last complete listing (0: none),
	// so the stats don't need a request each for them.
	// unpriced are its bug bounties showing no rewards, for the stats to
	// say whether they pay.
	rankMu   sync.Mutex
	ranks    map[string]int
	unpriced map[string]bool
	ranksAt  time.Time

	// invites are the pending invitations last read (invites.go).
	invitesMu sync.Mutex
	invites   []inviteJSON
	invitesAt time.Time

	// counts are each program's slow-moving counts, read about weekly
	// (see slowCountsAge).
	countsMu sync.Mutex
	counts   map[string]slowCounts
}

// New returns a Client. With no credentials it works in public-only mode.
func New(o Options) (*Client, error) {
	if (o.Email == "") != (o.Password == "") {
		return nil, errors.New("bugcrowd: email and password must be set together")
	}
	c := &Client{
		email: o.Email, password: o.Password, totpSecret: o.TOTPSecret,
		now: o.Now, sleep: o.HTTP.Sleep,
	}
	if c.now == nil {
		c.now = time.Now
	}
	if c.sleep == nil {
		c.sleep = sleepCtx
	}
	var err error
	for _, u := range []struct {
		dst  **url.URL
		val  string
		def  string
		name string
	}{{&c.base, o.BaseURL, defaultBaseURL, "base"}, {&c.identity, o.IdentityURL, defaultIdentityURL, "identity"}, {&c.okta, o.OktaURL, defaultOktaURL, "okta"}} {
		v := u.val
		if v == "" {
			v = u.def
		}
		if *u.dst, err = url.Parse(strings.TrimSuffix(v, "/")); err != nil {
			return nil, fmt.Errorf("bugcrowd: %s URL: %w", u.name, err)
		}
	}
	if o.Browser != nil && o.Email != "" {
		if o.Jar == nil {
			return nil, errors.New("bugcrowd: a browser login needs a cookie jar")
		}
		c.host, c.jar, c.logins = o.Browser, o.Jar, weblogin.NewHistory(o.LoginState)
		c.logins.Now = c.now
		c.account = &weblogin.Account{Who: who, Email: o.Email, Password: o.Password, TOTPSecret: o.TOTPSecret,
			TOTPEnv: "BC_TOTP_SECRET", Now: c.now}
	} else if c.jar, err = cookiejar.New(nil); err != nil {
		return nil, err
	}
	if o.Session != "" {
		cookies := parseSession(o.Session)
		if len(cookies) == 0 {
			return nil, errors.New("bugcrowd: BC_SESSION is not a cookie value")
		}
		c.jar.SetCookies(c.base, cookies)
		c.hasSession = true
	}

	ho := o.HTTP
	ho.Jar = c.jar
	ho.RedirectHosts = []string{c.base.Host, c.identity.Host, c.okta.Host}
	c.http = httpclient.New(ho)
	// Login steps have side effects (a one-time code can't be sent twice),
	// so they are never retried.
	lo := ho
	lo.MaxAttempts = 1
	c.login = httpclient.New(lo)
	return c, nil
}

// Account implements platform.AccountNamer: the login's email, "" with
// only a pasted session.
func (c *Client) Account() string { return strings.ToLower(c.email) }

// Name implements platform.Platform.
func (c *Client) Name() string { return Name }

// Stats implements platform.Platform.
func (c *Client) Stats() (int64, int64) {
	r1, l1 := c.http.Stats()
	r2, l2 := c.login.Stats()
	return r1 + r2, l1 + l2
}

func (c *Client) authenticated() bool { return c.hasSession || c.email != "" }

// parseSession accepts "a=1; b=2" or a bare session value.
func parseSession(s string) []*http.Cookie {
	s = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(s), "Cookie:"))
	if !strings.Contains(s, "=") {
		return []*http.Cookie{{Name: sessionCookie, Value: s, Path: "/"}} //nolint:gosec // seeds our client-side jar; not a cookie we serve
	}
	cs, err := http.ParseCookie(s)
	if err != nil {
		return nil
	}
	for _, ck := range cs {
		ck.Path = "/"
	}
	return cs
}

// fatal reports whether err means later requests would fail too, so
// there's no point in falling back to another endpoint.
func fatal(err error) bool {
	return errors.Is(err, ErrBlocked) || errors.Is(err, ErrSessionExpired) ||
		errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded)
}

// KeepAlive implements platform.KeepAliver. It touches the session so it
// doesn't expire between polls. Public-only clients do nothing.
func (c *Client) KeepAlive(ctx context.Context) error {
	if !c.authenticated() {
		return nil
	}
	if c.email != "" {
		// With a login, an ended session is signed in again now rather
		// than at the next poll, so the session is always live.
		return c.ensureSession(ctx)
	}
	ok, err := c.sessionValid(ctx)
	if err != nil {
		return err
	}
	if !ok {
		if c.email != "" {
			return nil // the next poll logs in again
		}
		c.cred.Refused("Bugcrowd no longer accepts the session: paste a fresh BC_SESSION and restart", true)
		return ErrSessionExpired
	}
	return nil
}

// Credentials reports the login's (or the pasted session's) state; none in
// public-only mode. See platform.CredentialReporter.
func (c *Client) Credentials() []platform.Credential {
	switch {
	case c.email != "" && c.host != nil:
		return []platform.Credential{c.cred.Report("Website login")}
	case c.email != "":
		return []platform.Credential{c.cred.Report("Login")}
	case c.hasSession:
		return []platform.Credential{c.cred.Report("Session cookie")}
	}
	return nil
}

// ensureSession makes sure the session is live, logging in if needed.
func (c *Client) ensureSession(ctx context.Context) error {
	c.authMu.Lock()
	defer c.authMu.Unlock()
	ok, err := c.sessionValid(ctx)
	if err != nil || ok {
		return err
	}
	if c.email == "" {
		c.cred.Refused("Bugcrowd no longer accepts the session: paste a fresh BC_SESSION and restart", true)
		return ErrSessionExpired
	}
	if c.host != nil {
		if err := c.browserSignIn(ctx); err != nil {
			return err
		}
	} else if err := c.doLogin(ctx); err != nil {
		if le := (*loginError)(nil); errors.As(err, &le) { // refused, not just unreachable
			c.cred.Refused(le.msg, true)
		}
		return err
	}
	// Before the check, which records when the new session expires.
	c.cred.SignedIn(nil)
	if ok, err = c.sessionValid(ctx); err != nil {
		return err
	}
	if !ok {
		return errors.New("bugcrowd: login finished but Bugcrowd doesn't accept the session")
	}
	return nil
}

// sessionValid asks Bugcrowd whether the session cookie is logged in.
func (c *Client) sessionValid(ctx context.Context) (bool, error) {
	res, err := c.get(ctx, c.base.String()+"/auth/session?update_activity=false", "application/json")
	if err != nil {
		return false, err
	}
	switch {
	case res.StatusCode == http.StatusOK:
		if c.authenticated() {
			c.cred.Accepted()
			var s struct {
				SecondsRemaining float64 `json:"secondsRemaining"`
			}
			if json.Unmarshal(res.Body, &s) == nil && s.SecondsRemaining > 0 {
				c.cred.ExpiresAt(time.Now().Add(time.Duration(s.SecondsRemaining) * time.Second))
			}
		}
		return true, nil
	case res.StatusCode == http.StatusUnauthorized, res.StatusCode == http.StatusForbidden && isJSON(res):
		return false, nil // logged out: 403 {"message":"Forbidden"}
	case res.StatusCode == http.StatusForbidden || res.StatusCode == http.StatusNotAcceptable:
		return false, ErrBlocked
	case isRedirect(res.StatusCode):
		return false, nil
	default:
		return false, fmt.Errorf("session check: HTTP %d", res.StatusCode)
	}
}

func (c *Client) get(ctx context.Context, u, accept string) (*httpclient.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", accept)
	return c.http.Do(ctx, req)
}

func (c *Client) getJSON(ctx context.Context, u string, into any) error {
	res, err := c.get(ctx, u, "application/json")
	if err != nil {
		return err
	}
	switch {
	case res.StatusCode == http.StatusOK:
	case (res.StatusCode == http.StatusForbidden || res.StatusCode == http.StatusNotAcceptable) && !isJSON(res):
		return ErrBlocked
	case isRedirect(res.StatusCode):
		if c.authenticated() {
			return ErrSessionExpired
		}
		return fmt.Errorf("redirected to %s", redactLocation(res))
	default:
		return fmt.Errorf("HTTP %d", res.StatusCode)
	}
	if err := json.Unmarshal(res.Body, into); err != nil {
		return fmt.Errorf("decode response: %w", err)
	}
	return nil
}

func isJSON(res *httpclient.Response) bool {
	return strings.Contains(res.Header.Get("Content-Type"), "json")
}

func isRedirect(code int) bool { return code >= 300 && code < 400 }

func redactLocation(res *httpclient.Response) string {
	u, err := url.Parse(res.Header.Get("Location"))
	if err != nil {
		return "[bad Location]"
	}
	u.RawQuery = ""
	return httpclient.RedactURL(u)
}

func sleepCtx(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}
