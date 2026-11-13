package sessionjar

import (
	"os"
	"path/filepath"
	"testing"
)

func TestClaim(t *testing.T) {
	dir := t.TempDir()
	marker := filepath.Join(dir, "sessions", "site-account")
	jar := filepath.Join(dir, "sessions", "site.json")
	profile := filepath.Join(dir, "chrome", "site")
	write := func() {
		t.Helper()
		for _, f := range []string{jar, filepath.Join(profile, "Cookies")} {
			if err := os.MkdirAll(filepath.Dir(f), 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(f, []byte("session"), 0o600); err != nil {
				t.Fatal(err)
			}
		}
	}
	exists := func(p string) bool { _, err := os.Stat(p); return err == nil }

	steps := []struct {
		name    string
		account string
		saved   bool // a session is saved before the claim
		reset   bool
	}{
		{"no marker yet: whose the session is is unknown", "a@example.com", true, true},
		{"same account", "a@example.com", true, false},
		{"same account, differently written", " A@Example.com ", true, false},
		{"another account", "b@example.com", true, true},
		{"nothing saved yet", "c@example.com", false, false},
	}
	for _, s := range steps {
		if s.saved {
			write()
		}
		reset, err := Claim(marker, s.account, jar, profile)
		if err != nil {
			t.Fatalf("%s: %v", s.name, err)
		}
		if reset != s.reset {
			t.Errorf("%s: reset = %v, want %v", s.name, reset, s.reset)
		}
		if gone := !exists(jar) && !exists(profile); gone != (s.reset || !s.saved) {
			t.Errorf("%s: session deleted = %v", s.name, gone)
		}
	}
	raw, err := os.ReadFile(marker)
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) == "c@example.com\n" {
		t.Error("the marker holds the account name itself")
	}
}
