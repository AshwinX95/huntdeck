package yeswehack

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"time"

	"github.com/mdpsec/huntdeck/internal/weblogin"
)

// who starts the website login's errors.
const who = "yeswehack: website login"

// Web app addresses: where the login browser signs in, and the page its
// YesWeHack tab opens at. The app keeps its token in its origin's
// localStorage, as "access_token".
const (
	webOrigin = "https://yeswehack.com"
	WebHome   = webOrigin + "/programs"
	tokenKey  = "access_token"
)

// browserLogin signs in with Chrome, in the shared login browser, as a
// person would: the email and password, then the authenticator app's code
// if 2FA is on. The web app then holds its token in localStorage, which
// HuntDeck takes for its API calls (and renews, see refreshToken).
func (c *Client) browserLogin(ctx context.Context) error {
	var token string
	f := weblogin.Flow{
		Who:   who,
		Start: webOrigin + "/auth/login",
		// "login" (email and password), "2fa"; "in" once the app holds a
		// token and has left /auth; "error:" and the form's message.
		Step: `(() => {
			if (location.protocol === 'chrome-error:') return 'neterror';
			if (location.origin !== ` + jsString(webOrigin) + `) return '';
			const vis = e => !!e && e.getClientRects().length > 0;
			const auth = location.pathname.startsWith('/auth');
			if (!auth && localStorage.getItem(` + jsString(tokenKey) + `)) return 'in';
			const err = [...document.querySelectorAll('form .alert, form [role=alert], form .invalid-feedback, form .error-message, ywh-alert')]
				.filter(vis).map(e => e.innerText.trim()).filter(Boolean)[0];
			let step = '';
			if (vis(document.querySelector('#password-input'))) step = 'login';
			else if (location.pathname.startsWith('/auth/totp') && [...document.querySelectorAll('input')].some(vis)) step = '2fa';
			return step && err ? 'error:' + err : step;
		})()`,
		StepWait: 90 * time.Second,
		Timeout:  4 * time.Minute,
		Refused: func(msg, after string) error {
			if after == "" {
				return fmt.Errorf("%s: the sign-in page shows %q", who, msg)
			}
			return &weblogin.Rejection{Who: who, Msg: msg + " (after " + after + ")"}
		},
		Acts: map[string]weblogin.Act{
			"login": {Sends: "the password", Do: func(ctx context.Context, p weblogin.Page) error {
				if err := p.Settle(ctx, 2*time.Second); err != nil {
					return err
				}
				if err := p.Fill(ctx, "#email-input", c.web.Email); err != nil {
					return err
				}
				if err := p.Fill(ctx, "#password-input", c.web.Password); err != nil {
					return err
				}
				p.Remember(ctx)
				return p.Press(ctx)
			}},
			"2fa": {Sends: "the 2FA code", Do: func(ctx context.Context, p weblogin.Page) error {
				if c.web.TOTPSecret == "" {
					return &weblogin.Rejection{Who: who, Msg: "2FA is on; set YWH_TOTP_SECRET"}
				}
				if err := p.Settle(ctx, time.Second); err != nil {
					return err
				}
				// The page's code field: its first visible input.
				if err := p.Eval(ctx, `(() => { const i = [...document.querySelectorAll('input')].find(e => e.getClientRects().length > 0);
					i && i.setAttribute('data-huntdeck', 'code') })()`, nil); err != nil {
					return err
				}
				code, err := c.web.Code(ctx)
				if err != nil {
					return err
				}
				if err := p.Fill(ctx, "[data-huntdeck=code]", code); err != nil {
					return err
				}
				p.Remember(ctx)
				return p.Press(ctx)
			}},
		},
		Take: func(ctx context.Context, p weblogin.Page) error {
			if err := p.Eval(ctx, `localStorage.getItem(`+jsString(tokenKey)+`) || ''`, &token); err != nil {
				return err
			}
			if token == "" {
				return errors.New("signed in, but the web app holds no token")
			}
			return nil
		},
	}
	if err := f.Run(ctx, c.host, Name, nil); err != nil {
		return err
	}
	c.setToken(token)
	return nil
}

// refreshToken trades the token for a new one, as the web app does
// before its token expires: POST /token/refresh, answered with
// {"token", "ttl"}. A refused one (it expired) returns errRefreshRefused.
func (c *Client) refreshToken(ctx context.Context, tok string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.base.String()+"/token/refresh", nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Authorization", "Bearer "+tok)
	res, err := c.http.Do(ctx, req)
	if err != nil {
		return "", fmt.Errorf("yeswehack: renew token: %w", err)
	}
	switch res.StatusCode {
	case http.StatusOK:
	case http.StatusUnauthorized, http.StatusForbidden:
		return "", errRefreshRefused
	default:
		return "", fmt.Errorf("yeswehack: renew token: HTTP %d", res.StatusCode)
	}
	var r struct {
		Token string `json:"token"`
	}
	if err := json.Unmarshal(res.Body, &r); err != nil || r.Token == "" {
		return "", errors.New("yeswehack: renew token: no token in the reply")
	}
	return r.Token, nil
}

var errRefreshRefused = errors.New("yeswehack: the token can't be renewed")

// browserToken returns a usable token with the login browser: the one
// held while it's fresh, renewed while it's still valid, else a browser
// login. The caller holds authMu.
func (c *Client) browserToken(ctx context.Context, renewWithin time.Duration) (string, error) {
	now := c.now()
	if c.token != "" && (c.expires.IsZero() || now.Before(c.expires.Add(-renewWithin))) {
		return c.token, nil
	}
	if c.token != "" && now.Before(c.expires) {
		tok, err := c.refreshToken(ctx, c.token)
		switch {
		case err == nil:
			c.setToken(tok)
			c.logins.Renewed()
			return tok, nil
		case !errors.Is(err, errRefreshRefused):
			return "", err
		}
	}
	if err := c.logins.Login(ctx, c.browserLogin); err != nil {
		if !errors.Is(err, weblogin.ErrWaiting) {
			c.cred.Refused(err.Error(), weblogin.IsRejection(err))
		}
		return "", err
	}
	c.cred.SignedIn(&c.expires)
	return c.token, nil
}

// setToken holds a new token and saves it, owner-only, so a restart
// carries on with it. The caller holds authMu.
func (c *Client) setToken(tok string) {
	c.token, c.expires = tok, tokenExpiry(tok)
	if c.tokenFile == "" {
		return
	}
	if err := os.WriteFile(c.tokenFile, []byte(tok), 0o600); err != nil {
		c.saveErr = err
	}
}

// loadToken reads the saved token, if it's still valid.
func (c *Client) loadToken() {
	if c.tokenFile == "" {
		return
	}
	b, err := os.ReadFile(c.tokenFile)
	if err != nil || len(b) == 0 {
		return
	}
	tok := string(b)
	if exp := tokenExpiry(tok); !exp.IsZero() && c.now().Before(exp) {
		c.token, c.expires = tok, exp
	}
}

// webSession is what the login browser's tab shows: the web app's token.
type webSession struct{ c *Client }

// Hosts implements weblogin.Session: no cookies, the token is in
// localStorage.
func (webSession) Hosts() []string { return nil }

// Saved implements weblogin.Session.
func (webSession) Saved(string) []*http.Cookie { return nil }

// LocalStorage implements weblogin.StorageSession.
func (s webSession) LocalStorage() (string, map[string]string) {
	s.c.authMu.Lock()
	defer s.c.authMu.Unlock()
	if s.c.token == "" || !s.c.now().Before(s.c.expires) {
		return "", nil
	}
	return webOrigin, map[string]string{tokenKey: s.c.token}
}

// BrowserSession is the session for the login browser's YesWeHack tab.
func (c *Client) BrowserSession() weblogin.Session { return webSession{c} }

// jsString quotes s for a script.
func jsString(s string) string { return fmt.Sprintf("%q", s) }
