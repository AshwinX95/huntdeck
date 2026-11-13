package web

import (
	"net/http"
	"regexp"
	"strings"
	"testing"
)

// Images opened on their own get a policy that lets the browser's own
// viewer style them, and pages keep theirs.
func TestImagePolicy(t *testing.T) {
	e := newTestEnv(t)
	for path, image := range map[string]bool{
		"/programs/fake/acme/og.png": true, "/static/og.png": true, "/favicon.ico": true, "/apple-touch-icon.png": true,
		"/static/favicon.svg": false, "/programs/fake/acme": false,
	} {
		res, _ := e.do(e.client(), http.MethodGet, path, nil, nil)
		if csp := res.Header.Get("Content-Security-Policy"); res.StatusCode != http.StatusOK || (csp == imagePolicy) != image {
			t.Errorf("%s = %d, CSP %q", path, res.StatusCode, csp)
		}
	}
}

// Each answer's policy carries a fresh nonce (for Cloudflare's injected
// bot detection) and still refuses any other inline script.
func TestCSPNonce(t *testing.T) {
	e := newTestEnv(t)
	nonce := regexp.MustCompile(`script-src 'self' 'nonce-([A-Za-z0-9+/]{22}==)';`)
	seen := map[string]bool{}
	for _, path := range []string{"/", "/", "/programs"} {
		res, body := e.do(e.client(), http.MethodGet, path, nil, nil)
		csp := res.Header.Get("Content-Security-Policy")
		m := nonce.FindStringSubmatch(csp)
		if m == nil || strings.Contains(csp, "unsafe-inline") {
			t.Fatalf("%s: CSP %q", path, csp)
		}
		if seen[m[1]] {
			t.Errorf("nonce %q used twice", m[1])
		}
		seen[m[1]] = true
		if strings.Contains(body, m[1]) {
			t.Errorf("%s: the page itself uses the nonce", path)
		}
	}
}
