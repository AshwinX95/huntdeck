package bugcrowd

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/cookiejar"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mdpsec/huntdeck/internal/weblogin"
)

func TestLoginThenListWithSessionChecks(t *testing.T) {
	f := newFake(t)
	c := f.client(t, Options{Email: testEmail, Password: testPassword, TOTPSecret: testSecret})
	l, err := c.ListPrograms(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !l.Complete || len(l.Programs) != 5 { // 4 listed, 1 invited
		t.Fatalf("complete=%v programs=%d", l.Complete, len(l.Programs))
	}
	// Before login, after login, after listing.
	if n := f.hit("/auth/session"); n != 3 {
		t.Errorf("session checks = %d, want 3", n)
	}
	if cr := c.Credentials(); len(cr) != 1 || cr[0].Name != "Login" || cr[0].LastOK == nil || cr[0].Failure != "" || cr[0].SignedIn == nil {
		t.Errorf("credentials after logging in = %+v", cr)
	}

	// A second listing reuses the session.
	if _, err := c.ListPrograms(context.Background()); err != nil {
		t.Fatal(err)
	}
	if n := f.hit("/auth/set-session"); n != 1 {
		t.Errorf("logged in %d times, want 1", n)
	}
}

func TestWrongPasswordIsNotRetried(t *testing.T) {
	f := newFake(t)
	c := f.client(t, Options{Email: testEmail, Password: "wrong", TOTPSecret: testSecret})
	for range 3 {
		_, err := c.ListPrograms(context.Background())
		var le *loginError
		if !errors.As(err, &le) || !strings.Contains(err.Error(), "Password is incorrect") {
			t.Fatalf("err = %v", err)
		}
		if strings.Contains(err.Error(), "wrong") {
			t.Fatal("error leaks the password")
		}
	}
	if f.passwordsTried != 1 {
		t.Errorf("password tried %d times, want 1 (account lockout)", f.passwordsTried)
	}
	if cr := c.Credentials(); len(cr) != 1 || !cr[0].Stuck || !strings.Contains(cr[0].Failure, "Password is incorrect") || strings.Contains(cr[0].Failure, "wrong") {
		t.Errorf("credentials after a wrong password = %+v", cr)
	}
}

// Accounts from before Bugcrowd moved to Okta answer a "Bugcrowd Legacy
// OTP" (custom_otp) with the same authenticator-app code.
func TestLegacyOTPTakesTheAppCode(t *testing.T) {
	f := newFake(t)
	f.otpStep = "okta_otp_legacy.json"
	c := f.client(t, Options{Email: testEmail, Password: testPassword, TOTPSecret: testSecret})
	if _, err := c.ListPrograms(context.Background()); err != nil {
		t.Fatal(err)
	}
	if n := f.hit("/auth/set-session"); n != 1 {
		t.Errorf("logged in %d times, want 1", n)
	}
}

func TestOTPRequiredWithoutSecret(t *testing.T) {
	f := newFake(t)
	c := f.client(t, Options{Email: testEmail, Password: testPassword})
	_, err := c.ListPrograms(context.Background())
	if err == nil || !strings.Contains(err.Error(), "BC_TOTP_SECRET") {
		t.Fatalf("err = %v", err)
	}
}

func TestSessionCookie(t *testing.T) {
	f := newFake(t)
	for _, s := range []string{goodSession, "_bugcrowd_session=" + goodSession, "Cookie: other=1; _bugcrowd_session=" + goodSession} {
		c := f.client(t, Options{Session: s})
		l, err := c.ListPrograms(context.Background())
		if err != nil || !l.Complete {
			t.Errorf("session %q: complete=%v err=%v", s, l.Complete, err)
		}
	}

	c := f.client(t, Options{Session: "stale"})
	l, err := c.ListPrograms(context.Background())
	if !errors.Is(err, ErrSessionExpired) || len(l.Programs) != 0 || l.Complete {
		t.Fatalf("expired session: programs=%d complete=%v err=%v", len(l.Programs), l.Complete, err)
	}
	if f.hit("/engagements.json") != 3*3 {
		t.Errorf("an expired session must not list (public results would look like removals)")
	}
	if err := c.KeepAlive(context.Background()); !errors.Is(err, ErrSessionExpired) {
		t.Errorf("KeepAlive = %v", err)
	}
}

func TestPickAuthenticator(t *testing.T) {
	f := newFake(t)
	var s idxResponse
	if err := json.Unmarshal(f.fixture("okta_password.json"), &s); err != nil {
		t.Fatal(err)
	}
	var sel remediation
	for _, r := range s.Remediation.Value {
		if r.Name == "select-authenticator-authenticate" {
			sel = r
		}
	}
	otp, ok := sel.pick(&s, "otp")
	if !ok || otp["id"] != "aut-otp" || otp["methodType"] != "otp" {
		t.Errorf("otp option = %v, %v", otp, ok)
	}
	pw, ok := sel.pick(&s, "password")
	if !ok || pw["id"] != "aut-pw" {
		t.Errorf("password option = %v, %v", pw, ok)
	}
}

func TestUnescapeJS(t *testing.T) {
	if got := unescapeJS(`st\x2D123\u002Dabc`); got != "st-123-abc" {
		t.Errorf("got %q", got)
	}
}

func TestTOTPCodeIsNotReused(t *testing.T) {
	var slept time.Duration
	now := testNow
	c := &Client{totpSecret: testSecret, now: func() time.Time { return now },
		sleep: func(_ context.Context, d time.Duration) error { slept += d; now = now.Add(d); return nil }}
	a, err := c.totpCode(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	b, err := c.totpCode(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if slept == 0 || a == b {
		t.Errorf("second code in the same step should wait for the next one (slept %v, %s/%s)", slept, a, b)
	}
}

// With a login, KeepAlive signs in as soon as there's no live session,
// not at the next poll, and leaves a live one alone.
func TestKeepAliveSignsInAgain(t *testing.T) {
	f := newFake(t)
	c := f.client(t, Options{Email: testEmail, Password: testPassword, TOTPSecret: testSecret})
	if err := c.KeepAlive(context.Background()); err != nil {
		t.Fatal(err)
	}
	// Signed in, and expiring as the check after the login says (14 days),
	// straight away: not only after the next check.
	cr := c.Credentials()
	if len(cr) != 1 || cr[0].SignedIn == nil || cr[0].Failure != "" || cr[0].Expires == nil ||
		cr[0].Expires.Sub(*cr[0].SignedIn).Round(time.Hour) != 14*24*time.Hour {
		t.Errorf("credentials after signing in = %+v", cr)
	}
	if err := c.KeepAlive(context.Background()); err != nil {
		t.Fatal(err)
	}
	if n := f.hit("/auth/set-session"); n != 1 {
		t.Errorf("logged in %d times, want 1", n)
	}
}

// With the login browser, the login is the website's, signed in in
// Chrome, and its session needs a jar to be saved in.
func TestBrowserLoginOptions(t *testing.T) {
	host := weblogin.NewHost(weblogin.Chrome{})
	if _, err := New(Options{Email: testEmail, Password: testPassword, Browser: host}); err == nil {
		t.Error("a browser login without a jar was accepted")
	}
	jar, _ := cookiejar.New(nil)
	c, err := New(Options{Email: testEmail, Password: testPassword, Browser: host, Jar: jar, LoginState: filepath.Join(t.TempDir(), "login.json")})
	if err != nil {
		t.Fatal(err)
	}
	if cr := c.Credentials(); len(cr) != 1 || cr[0].Name != "Website login" {
		t.Errorf("credentials %+v, want the website login", cr)
	}
	if c.jar != http.CookieJar(jar) {
		t.Error("the client doesn't use the given jar")
	}
}
