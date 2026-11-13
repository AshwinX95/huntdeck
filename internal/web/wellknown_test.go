package web

import (
	"net/http"
	"strings"
	"testing"
)

// Browsers find the icons where they look by default.
func TestRootIcons(t *testing.T) {
	e := newTestEnv(t)
	for path, typ := range map[string]string{"/favicon.ico": "image/", "/apple-touch-icon.png": "image/png", "/apple-touch-icon-precomposed.png": "image/png"} {
		res, body := e.do(e.client(), http.MethodGet, path, nil, nil)
		if res.StatusCode != http.StatusOK || !strings.HasPrefix(res.Header.Get("Content-Type"), typ) || len(body) < 1000 {
			t.Errorf("%s = %d %s, %d bytes", path, res.StatusCode, res.Header.Get("Content-Type"), len(body))
		}
	}
	if _, body := e.do(e.client(), http.MethodGet, "/programs", nil, nil); !strings.Contains(body, `<link rel="apple-touch-icon" href="/static/apple-touch-icon.png?v=`) {
		t.Error("pages don't link the touch icon")
	}
}

// Only a published copy with a feedback address has a security.txt, and
// it names that address.
func TestSecurityTxt(t *testing.T) {
	for _, tt := range []struct {
		name            string
		readOnly        bool
		publicURL, mail string
		found           bool
	}{
		{"full install", false, "https://demo.example.com", "hello@example.com", false},
		{"published copy without an address", true, "https://demo.example.com", "", false},
		{"unpublished copy", true, "", "hello@example.com", false},
		{"published copy", true, "https://demo.example.com", "hello@example.com", true},
	} {
		e := newTestEnv(t, func(d *Deps) { d.ReadOnly, d.PublicURL, d.FeedbackEmail = tt.readOnly, tt.publicURL, tt.mail })
		res, body := e.do(e.client(), http.MethodGet, "/.well-known/security.txt", nil, nil)
		if !tt.found {
			if res.StatusCode != http.StatusNotFound {
				t.Errorf("%s: security.txt = %d", tt.name, res.StatusCode)
			}
			continue
		}
		for _, want := range []string{"Contact: mailto:hello@example.com\n", "Expires: 20", "Canonical: https://demo.example.com/.well-known/security.txt\n"} {
			if res.StatusCode != http.StatusOK || !strings.Contains(body, want) {
				t.Errorf("%s: security.txt %d lacks %q:\n%s", tt.name, res.StatusCode, want, body)
			}
		}
	}
}

// The hunt board and the status API say when each platform was last
// checked.
func TestPlatformsChecked(t *testing.T) {
	e := newTestEnv(t)
	if _, body := e.do(e.client(), http.MethodGet, "/", nil, nil); !strings.Contains(body, `<p class="board-status">Platforms last checked <span class="sep">·</span> <span title="`) ||
		!strings.Contains(body, `">Fake `) {
		t.Error("the board lacks its status line")
	}
	if _, body := e.do(e.client(), http.MethodGet, "/api/v1/status", nil, nil); !strings.Contains(body, `"checked_at": "20`) {
		t.Error("status lacks checked_at")
	}
}
