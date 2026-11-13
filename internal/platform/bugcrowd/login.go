package bugcrowd

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/mdpsec/huntdeck/internal/httpclient"
	"github.com/mdpsec/huntdeck/internal/totp"
)

// Bugcrowd researchers sign in through Okta's Identity Engine ("IDX"):
// identity.bugcrowd.com redirects to Okta's authorize page, which embeds a
// state token. Each IDX response lists the "remediations" Okta will accept
// next (identify, answer a challenge, pick an authenticator). doLogin
// answers them in whatever order Okta asks, until Okta returns a success
// link that redirects back to bugcrowd.com with a session cookie.

const maxLoginSteps = 10

var stateTokenRE = regexp.MustCompile(`"stateToken"\s*:\s*"([^"]+)"`)

type idxResponse struct {
	StateHandle string `json:"stateHandle"`
	Remediation struct {
		Value []remediation `json:"value"`
	} `json:"remediation"`
	CurrentAuthenticatorEnrollment *struct {
		Value authenticator `json:"value"`
	} `json:"currentAuthenticatorEnrollment"`
	CurrentAuthenticator *struct {
		Value authenticator `json:"value"`
	} `json:"currentAuthenticator"`
	Authenticators *struct {
		Value []authenticator `json:"value"`
	} `json:"authenticators"`
	Messages *struct {
		Value []struct {
			Message string `json:"message"`
			Class   string `json:"class"`
		} `json:"value"`
	} `json:"messages"`
	Success *struct {
		Href string `json:"href"`
	} `json:"success"`
}

type authenticator struct {
	ID          string `json:"id"`
	Type        string `json:"type"`
	Key         string `json:"key"`
	DisplayName string `json:"displayName"`
	Methods     []struct {
		Type string `json:"type"`
	} `json:"methods"`
}

type remediation struct {
	Name  string  `json:"name"`
	Href  string  `json:"href"`
	Value []field `json:"value"`
}

type field struct {
	Name    string          `json:"name"`
	Value   json.RawMessage `json:"value"`
	Options []struct {
		Label     string          `json:"label"`
		Value     json.RawMessage `json:"value"`
		RelatesTo json.RawMessage `json:"relatesTo"`
	} `json:"options"`
}

// loginError is a rejection of the user's credentials. It sticks, so a wrong
// password isn't retried every poll until Okta locks the account.
type loginError struct{ msg string }

func (e *loginError) Error() string {
	return "bugcrowd: login rejected: " + e.msg + " (fix the BC_ settings and restart HuntDeck)"
}

func (c *Client) doLogin(ctx context.Context) error {
	if c.loginErr != nil {
		return c.loginErr
	}
	err := c.runLogin(ctx)
	var le *loginError
	if errors.As(err, &le) {
		c.loginErr = err
	}
	return err
}

func (c *Client) runLogin(ctx context.Context) error {
	start := c.identity.String() + "/login/hacker/oauth2/authorization/hacker?returnTo=" + url.QueryEscape(c.base.String()+"/dashboard")
	res, err := c.loginRequest(ctx, http.MethodGet, start, nil)
	if err != nil {
		return fmt.Errorf("bugcrowd: login: open sign-in page: %w", err)
	}
	if res.StatusCode != http.StatusOK {
		return fmt.Errorf("bugcrowd: login: sign-in page: HTTP %d", res.StatusCode)
	}
	m := stateTokenRE.FindSubmatch(res.Body)
	if m == nil {
		return errors.New("bugcrowd: login: no state token on the sign-in page (login flow changed?)")
	}
	state, err := c.idx(ctx, c.okta.String()+"/idp/idx/introspect", map[string]any{"stateToken": unescapeJS(string(m[1]))})
	if err != nil {
		return err
	}

	passwordSent := false
	for range maxLoginSteps {
		if state.Success != nil && state.Success.Href != "" {
			return c.finishLogin(ctx, state.Success.Href)
		}
		body, href, err := c.answer(ctx, state, &passwordSent)
		if err != nil {
			return err
		}
		if state, err = c.idx(ctx, href, body); err != nil {
			return err
		}
	}
	return fmt.Errorf("bugcrowd: login: no success after %d steps", maxLoginSteps)
}

// answer builds the reply to the most useful remediation Okta offers.
func (c *Client) answer(ctx context.Context, s *idxResponse, passwordSent *bool) (map[string]any, string, error) {
	byName := map[string]remediation{}
	var names []string
	for _, r := range s.Remediation.Value {
		byName[r.Name] = r
		names = append(names, r.Name)
	}
	body := map[string]any{"stateHandle": s.StateHandle}

	if r, ok := byName["challenge-authenticator"]; ok {
		a := s.current()
		switch {
		case a.Type == "password" || a.Key == "okta_password":
			body["credentials"] = map[string]any{"passcode": c.password}
			*passwordSent = true
		case isOTP(a):
			code, err := c.totpCode(ctx)
			if err != nil {
				return nil, "", err
			}
			body["credentials"] = map[string]any{"passcode": code}
		default:
			return nil, "", &loginError{fmt.Sprintf("Bugcrowd asked for %q, but HuntDeck can only answer a password or an authenticator-app code", a.label())}
		}
		return body, r.Href, nil
	}
	if r, ok := byName["identify"]; ok {
		body["identifier"] = c.email
		if r.has("credentials") {
			body["credentials"] = map[string]any{"passcode": c.password}
			*passwordSent = true
		}
		return body, r.Href, nil
	}
	if r, ok := byName["select-authenticator-authenticate"]; ok {
		want := "otp"
		if !*passwordSent {
			want = "password"
		}
		choice, ok := r.pick(s, want)
		if !ok && want == "password" {
			choice, ok = r.pick(s, "otp")
		}
		if !ok {
			return nil, "", &loginError{"Bugcrowd offered no password or authenticator-app option"}
		}
		body["authenticator"] = choice
		return body, r.Href, nil
	}
	return nil, "", &loginError{fmt.Sprintf("Bugcrowd asked for a step HuntDeck can't complete (%s); log in once in a browser to finish any account setup", strings.Join(names, ", "))}
}

func (s *idxResponse) current() authenticator {
	if s.CurrentAuthenticatorEnrollment != nil {
		return s.CurrentAuthenticatorEnrollment.Value
	}
	if s.CurrentAuthenticator != nil {
		return s.CurrentAuthenticator.Value
	}
	return authenticator{}
}

// isOTP reports whether a takes an authenticator-app code: Google
// Authenticator, or any authenticator whose method is a one-time code, such
// as the "Bugcrowd Legacy OTP" (custom_otp) that accounts from before Okta
// still have.
func isOTP(a authenticator) bool {
	if a.Key == "google_otp" || a.Key == "custom_otp" || (a.Type == "app" && a.Key != "okta_verify") || a.Type == "otp" {
		return true
	}
	for _, m := range a.Methods {
		if m.Type == "otp" || m.Type == "totp" {
			return true
		}
	}
	return false
}

func (a authenticator) label() string {
	return firstNonEmpty(a.DisplayName, a.Key, a.Type, "unknown")
}

func (r remediation) has(name string) bool {
	for _, f := range r.Value {
		if f.Name == name {
			return true
		}
	}
	return false
}

// pick returns the form values for the authenticator option of the wanted
// kind ("password" or "otp").
func (r remediation) pick(s *idxResponse, want string) (map[string]any, bool) {
	for _, f := range r.Value {
		if f.Name != "authenticator" {
			continue
		}
		for _, o := range f.Options {
			var v struct {
				Form struct {
					Value []struct {
						Name  string `json:"name"`
						Value any    `json:"value"`
					} `json:"value"`
				} `json:"form"`
			}
			if err := json.Unmarshal(o.Value, &v); err != nil {
				continue
			}
			form := map[string]any{}
			for _, fv := range v.Form.Value {
				if fv.Value != nil {
					form[fv.Name] = fv.Value
				}
			}
			a := s.resolve(o.RelatesTo)
			method, _ := form["methodType"].(string)
			var kind string
			switch {
			case a.Type == "password" || a.Key == "okta_password" || method == "password":
				kind = "password"
			case isOTP(a) || method == "otp" || method == "totp":
				kind = "otp"
			}
			if kind == want && form["id"] != nil {
				return form, true
			}
		}
	}
	return nil, false
}

// resolve follows a relatesTo reference, which is either an inline object or
// a JSON path like "$.authenticators.value[1]".
func (s *idxResponse) resolve(raw json.RawMessage) authenticator {
	var a authenticator
	if json.Unmarshal(raw, &a) == nil && (a.Key != "" || a.Type != "") {
		return a
	}
	var ref string
	if json.Unmarshal(raw, &ref) != nil || s.Authenticators == nil {
		return authenticator{}
	}
	idx, ok := strings.CutPrefix(ref, "$.authenticators.value[")
	if !ok {
		return authenticator{}
	}
	n, err := strconv.Atoi(strings.TrimSuffix(idx, "]"))
	if err != nil || n < 0 || n >= len(s.Authenticators.Value) {
		return authenticator{}
	}
	return s.Authenticators.Value[n]
}

// totpCode returns a fresh code, waiting for the next 30-second step if the
// current one was already used (Okta accepts each code once).
func (c *Client) totpCode(ctx context.Context) (string, error) {
	if c.totpSecret == "" {
		return "", &loginError{"Bugcrowd asked for an authenticator-app code; set BC_TOTP_SECRET"}
	}
	now := c.now()
	if totp.Step(now) == c.lastTOTPStep {
		if err := c.sleep(ctx, totp.NextStep(now).Sub(now)+time.Second); err != nil {
			return "", err
		}
		now = c.now()
	}
	code, err := totp.Code(c.totpSecret, now)
	if err != nil {
		return "", &loginError{"BC_TOTP_SECRET: " + err.Error()}
	}
	c.lastTOTPStep = totp.Step(now)
	return code, nil
}

// idx posts one IDX step and decodes the reply. Okta reports wrong
// credentials as error messages, often with HTTP 400/401.
func (c *Client) idx(ctx context.Context, href string, body map[string]any) (*idxResponse, error) {
	u, err := url.Parse(href)
	if err != nil || u.Scheme != c.okta.Scheme || u.Host != c.okta.Host {
		return nil, fmt.Errorf("bugcrowd: login: unexpected step URL host %q", hostOf(href))
	}
	step := strings.TrimPrefix(u.Path, "/idp/idx/")
	b, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	res, err := c.loginRequest(ctx, http.MethodPost, u.String(), b)
	if err != nil {
		return nil, fmt.Errorf("bugcrowd: login step %s: %w", step, err)
	}
	var s idxResponse
	if err := json.Unmarshal(res.Body, &s); err != nil {
		return nil, fmt.Errorf("bugcrowd: login step %s: HTTP %d, unreadable reply", step, res.StatusCode)
	}
	if s.Messages != nil {
		for _, m := range s.Messages.Value {
			if strings.EqualFold(m.Class, "ERROR") {
				return nil, &loginError{fmt.Sprintf("%s (at step %s)", m.Message, step)}
			}
		}
	}
	if res.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("bugcrowd: login step %s: HTTP %d", step, res.StatusCode)
	}
	return &s, nil
}

// finishLogin follows Okta's success link, which logs in to
// identity.bugcrowd.com, then opens the dashboard: bugcrowd.com bounces
// through identity and /auth/set-session, which sets its own session cookie.
func (c *Client) finishLogin(ctx context.Context, href string) error {
	u, err := url.Parse(href)
	if err != nil || u.Scheme != c.okta.Scheme || u.Host != c.okta.Host {
		return fmt.Errorf("bugcrowd: login: unexpected success URL host %q", hostOf(href))
	}
	for _, step := range []struct{ name, url string }{
		{"identity", u.String()},
		{"dashboard", c.base.String() + "/dashboard"},
	} {
		res, err := c.loginRequest(ctx, http.MethodGet, step.url, nil)
		if err != nil {
			return fmt.Errorf("bugcrowd: login: %s: %w", step.name, err)
		}
		if res.StatusCode >= 400 {
			return fmt.Errorf("bugcrowd: login: %s: HTTP %d", step.name, res.StatusCode)
		}
	}
	return nil
}

func (c *Client) loginRequest(ctx context.Context, method, u string, body []byte) (*httpclient.Response, error) {
	req, err := http.NewRequestWithContext(ctx, method, u, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "application/json; okta-version=1.0.0")
	} else {
		req.Body, req.GetBody = http.NoBody, nil
		req.Header.Set("Accept", "text/html,application/xhtml+xml")
	}
	return c.login.Do(ctx, req)
}

func hostOf(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return "?"
	}
	return u.Host
}

// unescapeJS decodes the \xHH and \uHHHH escapes Okta uses for the state
// token inside its inline script.
func unescapeJS(s string) string {
	if !strings.Contains(s, `\`) {
		return s
	}
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] == '\\' && i+3 < len(s) && s[i+1] == 'x' {
			if n, err := strconv.ParseUint(s[i+2:i+4], 16, 8); err == nil {
				b.WriteByte(byte(n))
				i += 3
				continue
			}
		}
		if s[i] == '\\' && i+5 < len(s) && s[i+1] == 'u' {
			if n, err := strconv.ParseUint(s[i+2:i+6], 16, 16); err == nil {
				b.WriteRune(rune(n))
				i += 5
				continue
			}
		}
		b.WriteByte(s[i])
	}
	return b.String()
}
