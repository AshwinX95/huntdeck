package hackerone

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"net/http"
	"net/url"
	"regexp"
	"sync"
	"time"

	"github.com/mdpsec/huntdeck/internal/httpclient"
	"github.com/mdpsec/huntdeck/internal/platform"
	"github.com/mdpsec/huntdeck/internal/weblogin"
)

// The website (hackerone.com) has what the Hacker API doesn't: stats for
// private programs and the invitations waiting to be accepted. Both come
// from its GraphQL endpoint with a signed-in session (verified live
// 2026-11-09):
//
//   - /users/sign_in sits behind Cloudflare's browser check, so logging in
//     runs in Chrome (package browser), windowed on a virtual display: the
//     email and password with "Remember me" ticked, then a TOTP code.
//   - The session is one cookie, __Host-session, good for two weeks. With
//     it a plain HTTP client gets any page, whose <meta name="csrf-token">
//     goes with GraphQL requests as X-CSRF-Token (without it: 403).
//   - `me` is null in GraphQL answers once the session has lapsed.

const (
	defaultWebURL = "https://hackerone.com"
	// WebHome is the page the login browser's HackerOne tab opens at.
	WebHome       = defaultWebURL + csrfPage
	sessionCookie = "__Host-session"
	// csrfPage is a page read for its CSRF token.
	csrfPage = "/opportunities/all"
	// verifyEvery is how long a checked session is taken as live before
	// it's checked again (one page and one small query).
	verifyEvery = time.Hour
	// who starts the website login's errors.
	who = "hackerone: website login"
)

// ErrNoSession means logged-in data was asked for without H1_EMAIL and
// H1_PASSWORD.
var ErrNoSession = fmt.Errorf("hackerone: %w (H1_EMAIL, H1_PASSWORD)", platform.ErrNoWebLogin)

// webSession holds the website login. mu serialises logins and checks.
type webSession struct {
	mu      sync.Mutex
	account *weblogin.Account
	host    *weblogin.Host // the shared login browser
	history *weblogin.History
	jar     http.CookieJar
	http    *httpclient.Client // with jar; the website only
	base    *url.URL
	login   func(ctx context.Context) error
	now     func() time.Time

	csrf       string    // the session's CSRF token, "" until read
	verifiedAt time.Time // when the session was last seen signed in
	cred       platform.CredentialState
}

// rejected is HackerOne refusing the login; it sticks until a restart.
func rejected(msg string) error { return &weblogin.Rejection{Who: who, Msg: msg} }

func (c *Client) loggedIn() bool { return c.web != nil }

// hasCookie reports whether the jar holds a session cookie.
func (s *webSession) hasCookie() bool {
	for _, ck := range s.jar.Cookies(s.base) {
		if ck.Name == sessionCookie && ck.Value != "" {
			return true
		}
	}
	return false
}

// ensureSession makes sure there is a live website session with its CSRF
// token: the one checked within verifyEvery, else the jar's if it's still
// signed in, else a browser login. force checks again even so, after the
// website refused a request.
func (c *Client) ensureSession(ctx context.Context, force bool) error {
	s := c.web
	if s == nil {
		return ErrNoSession
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.history.Stuck(); err != nil {
		return err
	}
	if !force && s.csrf != "" && s.now().Sub(s.verifiedAt) < verifyEvery {
		return nil
	}
	s.csrf = ""
	if s.hasCookie() {
		ok, err := c.checkSession(ctx)
		if err != nil {
			return err
		}
		if ok {
			return nil
		}
	}
	if err := s.history.Login(ctx, s.login); err != nil {
		if !errors.Is(err, weblogin.ErrWaiting) {
			s.cred.Refused(err.Error(), weblogin.IsRejection(err))
		}
		return err
	}
	ok, err := c.checkSession(ctx)
	if err == nil && !ok {
		err = errors.New(who + ": Chrome signed in, but the session it left isn't")
	}
	return err
}

var csrfMeta = regexp.MustCompile(`<meta name="csrf-token" content="([^"]+)"`)

// checkSession reads a page for the CSRF token and asks who is signed in;
// ok is false if no one is (the session lapsed). The caller holds mu.
func (c *Client) checkSession(ctx context.Context) (ok bool, err error) {
	s := c.web
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.base.String()+csrfPage, nil)
	if err != nil {
		return false, err
	}
	req.Header.Set("Accept", "text/html")
	res, err := s.http.Do(ctx, req)
	if err != nil {
		return false, fmt.Errorf("hackerone: website session: %w", err)
	}
	m := csrfMeta.FindSubmatch(res.Body)
	if res.StatusCode != http.StatusOK || m == nil {
		return false, fmt.Errorf("hackerone: website session: HTTP %d, no CSRF token on the page", res.StatusCode)
	}
	s.csrf = html.UnescapeString(string(m[1]))
	var me struct {
		Me *struct {
			ID string `json:"id"`
		} `json:"me"`
	}
	status, err := c.gql(ctx, true, `query { me { id } }`, nil, 0, &me)
	if err != nil && status != http.StatusForbidden {
		return false, fmt.Errorf("hackerone: website session: %w", err)
	}
	if me.Me == nil {
		s.csrf = ""
		return false, nil
	}
	s.verifiedAt = s.now()
	s.cred.Accepted()
	return true, nil
}

// webGraphQL runs a query with the website session, renewing it once if
// the website refuses it (403) or answers as if no one were signed in
// (signedIn reports which, for queries that ask for `me`).
func (c *Client) webGraphQL(ctx context.Context, query string, vars map[string]any, into any, signedIn func() bool) error {
	for attempt := 0; ; attempt++ {
		if err := c.ensureSession(ctx, attempt > 0); err != nil {
			return err
		}
		status, err := c.gql(ctx, true, query, vars, 0, into)
		if attempt == 0 && (status == http.StatusForbidden || (err == nil && signedIn != nil && !signedIn())) {
			continue
		}
		return err
	}
}

// gql posts one query, paced by statsLimiter: with the website session
// and its CSRF token if web, else anonymously. Errors about one of the
// aliases t0..t(aliases-1) only mean that program is missing; any other
// error fails the query. It returns the HTTP status.
func (c *Client) gql(ctx context.Context, web bool, query string, vars map[string]any, aliases int, into any) (int, error) {
	if err := c.statsLimiter.Wait(ctx); err != nil {
		return 0, err
	}
	body, err := json.Marshal(map[string]any{"query": query, "variables": vars})
	if err != nil {
		return 0, err
	}
	u, client := c.graphQL, c.http
	if web {
		u, client = c.web.base.String()+"/graphql", c.web.http
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u, bytes.NewReader(body))
	if err != nil {
		return 0, err
	}
	// Never the API token: it isn't valid here.
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	if web {
		req.Header.Set("X-CSRF-Token", c.web.csrf)
	}
	res, err := client.Do(ctx, req)
	if err != nil {
		return 0, err
	}
	if res.StatusCode != http.StatusOK {
		return res.StatusCode, fmt.Errorf("HTTP %d", res.StatusCode)
	}
	var resp struct {
		Data   json.RawMessage `json:"data"`
		Errors []graphQLError  `json:"errors"`
	}
	if err := json.Unmarshal(res.Body, &resp); err != nil {
		return res.StatusCode, fmt.Errorf("decode response: %w", err)
	}
	for _, e := range resp.Errors {
		if len(e.Path) == 0 || !isAlias(e.Path[0], aliases) {
			return res.StatusCode, fmt.Errorf("graphql: %s", e.Message)
		}
	}
	if len(resp.Data) == 0 || string(resp.Data) == "null" {
		return res.StatusCode, errors.New("graphql: response has no data")
	}
	if err := json.Unmarshal(resp.Data, into); err != nil {
		return res.StatusCode, fmt.Errorf("decode data: %w", err)
	}
	return res.StatusCode, nil
}

// browserLogin signs in with Chrome, windowed, as a person would: past
// Cloudflare's check, the email and password with "Remember me" ticked,
// then a TOTP code.
func (c *Client) browserLogin(ctx context.Context) error {
	s := c.web
	f := weblogin.Flow{
		Who:   who,
		Start: s.base.String() + "/users/sign_in",
		// "check" is Cloudflare's, waited out; "login" the email and
		// password; "error:" and the form's message.
		Step: `(() => {
			if (location.protocol === 'chrome-error:') return 'neterror';
			if (document.title.includes('Just a moment')) return 'check';
			const vis = s => { const e = document.querySelector(s); return e && e.offsetParent !== null };
			if (!location.pathname.startsWith('/users/')) return 'in';
			const err = [...document.querySelectorAll('form .error, form [role=alert], .flash-messages, .alert-danger, .flash-error')]
				.map(e => e.innerText.trim()).filter(Boolean)[0];
			if (vis('#sign_in_totp_code')) return err ? 'error:' + err : '2fa';
			if (vis('#sign_in_password')) return err ? 'error:' + err : 'login';
			return '';
		})()`,
		Pass:     []string{"check"},
		StepWait: 90 * time.Second,
		Timeout:  4 * time.Minute,
		Sites:    []*url.URL{s.base},
		Refused: func(msg, after string) error {
			if after == "" { // nothing sent yet: the page itself is unwell
				return fmt.Errorf("%s: the sign-in page shows %q", who, msg)
			}
			return rejected(msg + " (after " + after + ")")
		},
		Acts: map[string]weblogin.Act{
			"login": {Sends: "the password", Do: func(ctx context.Context, p weblogin.Page) error {
				if err := p.Settle(ctx, 2*time.Second); err != nil {
					return err
				}
				if err := p.Fill(ctx, "#sign_in_email", s.account.Email); err != nil {
					return err
				}
				if err := p.Fill(ctx, "#sign_in_password", s.account.Password); err != nil {
					return err
				}
				p.Tick(ctx, "#user_remember_me")
				p.Remember(ctx)

				return p.Press(ctx)
			}},
			"2fa": {Sends: "the 2FA code", Do: func(ctx context.Context, p weblogin.Page) error {
				code, err := s.account.Code(ctx)
				if err != nil {
					return err
				}
				if err := p.Fill(ctx, "#sign_in_totp_code", code); err != nil {
					return err
				}
				p.Remember(ctx)

				return p.Press(ctx)
			}},
		},
	}
	return f.Run(ctx, s.host, Name, s.jar)
}
