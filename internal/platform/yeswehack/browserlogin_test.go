package yeswehack

import (
	"context"
	"encoding/base64"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/mdpsec/huntdeck/internal/httpclient"
	"github.com/mdpsec/huntdeck/internal/weblogin"
)

// numberedJWT is an unsigned token expiring at exp, told apart by n.
func numberedJWT(n int, exp time.Time) string {
	payload := base64.RawURLEncoding.EncodeToString([]byte(fmt.Sprintf(`{"exp":%d,"n":%d}`, exp.Unix(), n)))
	return "e30." + payload + ".sig"
}

// With the login browser, a saved token carries on after a restart and
// is renewed through /token/refresh before it expires, as the web app
// does, rather than signed in again; the tab gets it in localStorage.
func TestBrowserTokenRenewsAndCarriesOn(t *testing.T) {
	now := time.Date(2026, 11, 10, 12, 0, 0, 0, time.UTC)
	refreshes := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/token/refresh" || r.Method != http.MethodPost || r.Header.Get("Authorization") != "Bearer "+numberedJWT(1, now.Add(10*time.Minute)) {
			http.Error(w, "no", http.StatusUnauthorized)
			return
		}
		refreshes++
		fmt.Fprintf(w, `{"token":%q,"ttl":14400}`, numberedJWT(2, now.Add(4*time.Hour)))
	}))
	defer srv.Close()
	dir := t.TempDir()
	tokenFile := filepath.Join(dir, "yeswehack-token")
	if err := os.WriteFile(tokenFile, []byte(numberedJWT(1, now.Add(10*time.Minute))), 0o600); err != nil {
		t.Fatal(err)
	}
	c, err := New(Options{Email: "hunter@example.com", Password: "pw", BaseURL: srv.URL, Now: func() time.Time { return now },
		HTTP: httpclient.New(httpclient.Options{RatePerSecond: 100, Burst: 10, MaxAttempts: 1}), Browser: weblogin.NewHost(weblogin.Chrome{}),
		TokenFile: tokenFile, LoginState: filepath.Join(dir, "login.json")})
	if err != nil {
		t.Fatal(err)
	}
	if got := c.Credentials(); len(got) != 1 || got[0].Name != "Website login" {
		t.Errorf("credentials %+v, want the website login", got)
	}

	// A poll uses the saved token while it has over a minute left.
	tok, err := c.currentToken(context.Background())
	if err != nil || tok != numberedJWT(1, now.Add(10*time.Minute)) || refreshes != 0 {
		t.Fatalf("currentToken = %v, %v after %d refreshes; want the saved one", tok == numberedJWT(1, now.Add(10*time.Minute)), err, refreshes)
	}
	// KeepAlive renews it within 15 minutes of expiring, and saves the new one.
	if err := c.KeepAlive(context.Background()); err != nil {
		t.Fatal(err)
	}
	want := numberedJWT(2, now.Add(4*time.Hour))
	if refreshes != 1 || c.token != want {
		t.Fatalf("after KeepAlive: %d refreshes, renewed %v", refreshes, c.token == want)
	}
	b, err := os.ReadFile(tokenFile)
	fi, _ := os.Stat(tokenFile)
	if err != nil || string(b) != want || fi.Mode().Perm() != 0o600 {
		t.Errorf("saved token renewed %v (%v), mode %v", string(b) == want, err, fi.Mode().Perm())
	}
	origin, items := c.BrowserSession().(weblogin.StorageSession).LocalStorage()
	if origin != webOrigin || items[tokenKey] != want {
		t.Errorf("the tab's storage: %s, has token %v", origin, items[tokenKey] == want)
	}

	// An expired saved token isn't loaded.
	if err := os.WriteFile(tokenFile, []byte(numberedJWT(3, now.Add(-time.Minute))), 0o600); err != nil {
		t.Fatal(err)
	}
	c2, _ := New(Options{Email: "hunter@example.com", Password: "pw", BaseURL: srv.URL, Now: func() time.Time { return now },
		HTTP: httpclient.New(httpclient.Options{}), Browser: weblogin.NewHost(weblogin.Chrome{}), TokenFile: tokenFile})
	if c2.token != "" {
		t.Error("an expired saved token was loaded")
	}
}
