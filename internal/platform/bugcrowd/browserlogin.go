package bugcrowd

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"time"

	"github.com/mdpsec/huntdeck/internal/weblogin"
)

// who starts the website login's errors.
const who = "bugcrowd: website login"

// WebHome is the page the login browser's Bugcrowd tab opens at.
const WebHome = defaultBaseURL + "/dashboard"

// browserLogin signs in with Chrome, in the shared login browser, as a
// person would on Bugcrowd's Okta page: the email with "Remember me"
// ticked, the password, then the authenticator app's code (Bugcrowd may
// ask which authenticator first). The browser's profile remembers the
// device, so later logins may skip steps; its cookies (bugcrowd.com, the
// identity service and Okta) are kept in the jar, saved across restarts.
func (c *Client) browserLogin(ctx context.Context) error {
	f := weblogin.Flow{
		Who:   who,
		Start: c.base.String() + "/user/sign_in",
		// "email", "emailpw" (email and password on one form), "password",
		// "choose" (pick the authenticator), "code"; "error:" and Okta's
		// message.
		Step: `(() => {
			if (location.protocol === 'chrome-error:') return 'neterror';
			const shown = e => !!e && e.getClientRects().length > 0;
			const vis = s => shown(document.querySelector(s));
			if (location.hostname === ` + jsString(c.base.Hostname()) + ` && !location.pathname.startsWith('/user/sign_in')) return 'in';
			if (location.hostname !== ` + jsString(c.okta.Hostname()) + `) return '';
			const err = [...document.querySelectorAll('.o-form-error-container .okta-form-infobox-error, .o-form-error-container [role=alert], .o-form-input-error')]
				.filter(shown).map(e => e.innerText.trim()).filter(Boolean)[0];
			const step = (() => {
				if (vis('input[name=identifier]')) return vis('input[name="credentials.passcode"]') ? 'emailpw' : 'email';
				if (vis('input[name="credentials.passcode"][type=password]')) return 'password';
				if (vis('input[name="credentials.passcode"]')) return 'code';
				if ([...document.querySelectorAll('.authenticator-row a, .authenticator-row button, [data-se=authenticator-button]')].some(shown)) return 'choose';
				return '';
			})();
			return step && err ? 'error:' + err : step;
		})()`,
		StepWait: 90 * time.Second,
		Timeout:  4 * time.Minute,
		Sites:    []*url.URL{c.base, c.identity, c.okta},
		Refused: func(msg, after string) error {
			if after == "" {
				return fmt.Errorf("%s: the sign-in page shows %q", who, msg)
			}
			return &weblogin.Rejection{Who: who, Msg: msg + " (after " + after + ")"}
		},
		Acts: map[string]weblogin.Act{
			"email": {Sends: "the email", Repeatable: true, Do: func(ctx context.Context, p weblogin.Page) error {
				if err := c.fillEmail(ctx, p); err != nil {
					return err
				}
				return p.Press(ctx)
			}},
			"emailpw": {Sends: "the password", Do: func(ctx context.Context, p weblogin.Page) error {
				if err := c.fillEmail(ctx, p); err != nil {
					return err
				}
				if err := p.Fill(ctx, `input[name="credentials.passcode"]`, c.account.Password); err != nil {
					return err
				}
				p.Remember(ctx)
				return p.Press(ctx)
			}},
			"password": {Sends: "the password", Do: func(ctx context.Context, p weblogin.Page) error {
				if err := p.Settle(ctx, time.Second); err != nil {
					return err
				}
				if err := p.Fill(ctx, `input[name="credentials.passcode"]`, c.account.Password); err != nil {
					return err
				}
				p.Remember(ctx)
				return p.Press(ctx)
			}},
			"choose": {Sends: "the choice of authenticator", Repeatable: true, Do: func(ctx context.Context, p weblogin.Page) error {
				// The authenticator app, by its row's name: Google
				// Authenticator, or "Bugcrowd Legacy OTP" from before Okta.
				var ok bool
				err := p.Eval(ctx, `(() => {
					const rows = [...document.querySelectorAll('.authenticator-row, [data-se=authenticator-row]')];
					const row = rows.find(r => /otp|authenticator|google/i.test(r.innerText) && !/okta verify|email|phone|sms|voice|security key|biometric/i.test(r.innerText));
					const b = row && [...row.querySelectorAll('[data-se=authenticator-button], a, button')].find(e => e.getClientRects().length > 0);
					if (!b) return false;
					b.setAttribute('data-huntdeck', 'pick');
					return true;
				})()`, &ok)
				if err != nil {
					return err
				}
				if !ok {
					return &weblogin.Rejection{Who: who, Msg: "Bugcrowd offered no authenticator-app option"}
				}
				return p.Click(ctx, `[data-huntdeck=pick]`)
			}},
			"code": {Sends: "the 2FA code", Do: func(ctx context.Context, p weblogin.Page) error {
				if c.account.TOTPSecret == "" {
					return &weblogin.Rejection{Who: who, Msg: "Bugcrowd asked for an authenticator-app code; set BC_TOTP_SECRET"}
				}
				code, err := c.account.Code(ctx)
				if err != nil {
					return err
				}
				if err := p.Fill(ctx, `input[name="credentials.passcode"]`, code); err != nil {
					return err
				}
				p.Remember(ctx)
				return p.Press(ctx)
			}},
		},
	}
	return f.Run(ctx, c.host, Name, c.jar)
}

// fillEmail types the email and ticks "Remember me", so Okta knows the
// device next time.
func (c *Client) fillEmail(ctx context.Context, p weblogin.Page) error {
	if err := p.Settle(ctx, 2*time.Second); err != nil {
		return err
	}
	if err := p.Fill(ctx, `input[name=identifier]`, c.account.Email); err != nil {
		return err
	}
	p.Tick(ctx, `input[name=rememberMe]`)
	p.Remember(ctx)
	return nil
}

// jsString quotes s for a script.
func jsString(s string) string { return fmt.Sprintf("%q", s) }

// browserSignIn runs the browser login, unless a refusal sticks or the
// last one failed too recently (weblogin.History), and records how it went.
func (c *Client) browserSignIn(ctx context.Context) error {
	err := c.logins.Login(ctx, c.browserLogin)
	if err != nil && !errors.Is(err, weblogin.ErrWaiting) {
		c.cred.Refused(err.Error(), weblogin.IsRejection(err))
	}
	return err
}
