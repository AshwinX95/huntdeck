package sessionjar

import (
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func names(cs []*http.Cookie) map[string]string {
	out := map[string]string{}
	for _, c := range cs {
		out[c.Name] = c.Value
	}
	return out
}

// Cookies set for the named hosts survive a reopen; others, expired and
// deleted ones don't.
func TestReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sessions", "site.json")
	now := time.Now().UTC().Truncate(time.Second)
	app := &url.URL{Scheme: "https", Host: "app.example.com", Path: "/auth/callback"}
	login := &url.URL{Scheme: "https", Host: "login.example.com", Path: "/"}
	other := &url.URL{Scheme: "https", Host: "other.example.net", Path: "/"}

	j, err := Open(path, "app.example.com", "login.example.com")
	if err != nil {
		t.Fatal(err)
	}
	j.now = func() time.Time { return now }
	j.SetCookies(app, []*http.Cookie{
		{Name: "__Host-session", Value: "s1", Path: "/", Secure: true, HttpOnly: true}, // session cookie
		{Name: "csrf", Value: "c1", Path: "/", MaxAge: 3600},
		{Name: "old", Value: "o", Path: "/", Expires: now.Add(-time.Hour)},
		{Name: "gone", Value: "g", Path: "/"},
	})
	j.SetCookies(login, []*http.Cookie{{Name: "sso", Value: "x", Path: "/", Expires: now.Add(30 * 24 * time.Hour)}})
	j.SetCookies(other, []*http.Cookie{{Name: "tracker", Value: "t", Path: "/"}})
	j.SetCookies(app, []*http.Cookie{{Name: "gone", Path: "/", MaxAge: -1}})

	if fi, err := os.Stat(path); err != nil || fi.Mode().Perm() != 0o600 {
		t.Fatalf("saved file: %v %v", fi, err)
	}

	j2, err := Open(path, "app.example.com", "login.example.com")
	if err != nil {
		t.Fatal(err)
	}
	got := names(j2.Cookies(&url.URL{Scheme: "https", Host: "app.example.com", Path: "/api"}))
	if len(got) != 2 || got["__Host-session"] != "s1" || got["csrf"] != "c1" {
		t.Errorf("app cookies after reopen = %v", got)
	}
	if got := names(j2.Cookies(login)); got["sso"] != "x" {
		t.Errorf("login cookies after reopen = %v", got)
	}
	if got := j2.Cookies(other); len(got) != 0 {
		t.Errorf("an unlisted host's cookies were saved: %v", got)
	}
}

// Open drops cookies that expired while HuntDeck wasn't running.
func TestOpenSkipsExpired(t *testing.T) {
	path := filepath.Join(t.TempDir(), "s.json")
	past := time.Now().Add(-time.Minute).UTC().Format(time.RFC3339)
	future := time.Now().Add(time.Hour).UTC().Format(time.RFC3339)
	body := `{"cookies":[
		{"host":"app.example.com","path":"/","name":"expired","value":"e","expires":"` + past + `"},
		{"host":"app.example.com","path":"/","name":"live","value":"l","expires":"` + future + `"},
		{"host":"evil.example.net","path":"/","name":"foreign","value":"f"}]}`
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	j, err := Open(path, "app.example.com")
	if err != nil {
		t.Fatal(err)
	}
	got := names(j.Cookies(&url.URL{Scheme: "https", Host: "app.example.com", Path: "/"}))
	if len(got) != 1 || got["live"] != "l" {
		t.Errorf("cookies = %v", got)
	}
	if got := j.Cookies(&url.URL{Scheme: "https", Host: "evil.example.net", Path: "/"}); len(got) != 0 {
		t.Errorf("an unlisted host's saved cookie was loaded: %v", got)
	}
}

// A file that can't be parsed is an empty jar, replaced on the next save.
func TestOpenCorrupt(t *testing.T) {
	path := filepath.Join(t.TempDir(), "s.json")
	if err := os.WriteFile(path, []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	j, err := Open(path, "app.example.com")
	if err != nil {
		t.Fatal(err)
	}
	u := &url.URL{Scheme: "https", Host: "app.example.com", Path: "/"}
	j.SetCookies(u, []*http.Cookie{{Name: "a", Value: "1", Path: "/"}})
	j2, err := Open(path, "app.example.com")
	if err != nil {
		t.Fatal(err)
	}
	if got := names(j2.Cookies(u)); got["a"] != "1" {
		t.Errorf("cookies = %v", got)
	}
}

func TestDefaultPath(t *testing.T) {
	for in, want := range map[string]string{"": "/", "/": "/", "/a": "/", "/a/b": "/a", "/a/b/": "/a/b"} {
		if got := defaultPath(in); got != want {
			t.Errorf("defaultPath(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestSaved: the kept cookies come back with their attributes, for the
// login browser to take up the session.
func TestSaved(t *testing.T) {
	j, err := Open(filepath.Join(t.TempDir(), "jar.json"), "app.example.com", "login.example.com")
	if err != nil {
		t.Fatal(err)
	}
	u, _ := url.Parse("https://app.example.com/dashboard")
	exp := time.Now().Add(time.Hour).Truncate(time.Second)
	j.SetCookies(u, []*http.Cookie{
		{Name: "__Host-session", Value: "s1", Path: "/", Secure: true, HttpOnly: true},
		{Name: "csrf", Value: "c1", Domain: "example.com", Path: "/", Expires: exp, Secure: true},
		{Name: "gone", Value: "x", Path: "/", MaxAge: -1},
	})
	got := map[string]*http.Cookie{}
	for _, c := range j.Saved("APP.example.com") {
		got[c.Name] = c
	}
	if s := got["__Host-session"]; len(got) != 2 || s == nil || s.Domain != "" || !s.Secure || !s.HttpOnly || s.Value != "s1" {
		t.Errorf("host-only session %+v (of %d)", s, len(got))
	}
	if c := got["csrf"]; c == nil || c.Domain != "example.com" || !c.Expires.Equal(exp) || c.HttpOnly {
		t.Errorf("domain cookie %+v", c)
	}
	if len(j.Saved("login.example.com")) != 0 || strings.Join(j.Hosts(), ",") != "app.example.com,login.example.com" {
		t.Errorf("another host's cookies %v, hosts %v", j.Saved("login.example.com"), j.Hosts())
	}
}
