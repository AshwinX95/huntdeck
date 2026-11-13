package web

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestBudgetFor(t *testing.T) {
	for _, tt := range []struct {
		method, path string
		want         int
	}{
		{http.MethodGet, "/", budgetPages},
		{http.MethodGet, "/programs/hackerone/x", budgetPages},
		{http.MethodGet, "/api/v1/programs", budgetAPI},
		{http.MethodGet, "/api/v1/match", budgetAPI},
		{http.MethodPost, "/api/v1/match", budgetBulk},
		{http.MethodGet, "/static/app.js", -1},
		{http.MethodGet, "/healthz", -1},
	} {
		if got := budgetFor(httptest.NewRequest(tt.method, tt.path, nil)); got != tt.want {
			t.Errorf("%s %s: budget %d, want %d", tt.method, tt.path, got, tt.want)
		}
	}
}

func TestClientKey(t *testing.T) {
	req := func(remote, cf string) *http.Request {
		r := httptest.NewRequest(http.MethodGet, "/", nil)
		r.RemoteAddr = remote
		if cf != "" {
			r.Header.Set("CF-Connecting-IP", cf)
		}
		return r
	}
	trusting, plain := newVisitorLimiter("CF-Connecting-IP", nil), newVisitorLimiter("", nil)
	for _, tt := range []struct {
		l    *visitorLimiter
		r    *http.Request
		want string
	}{
		{trusting, req("127.0.0.1:5000", "203.0.113.7"), "203.0.113.7"},
		{trusting, req("127.0.0.1:5000", ""), "127.0.0.1"},
		{plain, req("198.51.100.2:5000", "203.0.113.7"), "198.51.100.2"}, // the header isn't trusted
		{trusting, req("127.0.0.1:5000", "2001:db8:1:2:3:4:5:6"), "2001:db8:1:2::/64"},
		{trusting, req("127.0.0.1:5000", "::ffff:203.0.113.7"), "203.0.113.7"},
	} {
		if got := tt.l.clientKey(tt.r); got != tt.want {
			t.Errorf("clientKey = %q, want %q", got, tt.want)
		}
	}
}

// A visitor's burst is allowed, then they wait; another visitor isn't
// held up, and the budget comes back with time.
func TestVisitorLimiter(t *testing.T) {
	now := time.Date(2026, 11, 10, 12, 0, 0, 0, time.UTC)
	l := newVisitorLimiter("CF-Connecting-IP", func() time.Time { return now })
	from := func(ip, method, path string) *http.Request {
		r := httptest.NewRequest(method, path, nil)
		r.Header.Set("CF-Connecting-IP", ip)
		return r
	}
	for i := range visitorBudgets[budgetBulk].burst {
		if ok, _ := l.allow(from("203.0.113.1", http.MethodPost, "/api/v1/match")); !ok {
			t.Fatalf("bulk check %d refused within the burst", i+1)
		}
	}
	ok, wait := l.allow(from("203.0.113.1", http.MethodPost, "/api/v1/match"))
	if ok || wait < time.Second || wait > 13*time.Second {
		t.Errorf("past the burst: allowed %v, wait %v", ok, wait)
	}
	if ok, _ := l.allow(from("203.0.113.1", http.MethodGet, "/programs")); !ok {
		t.Error("pages share the bulk budget")
	}
	if ok, _ := l.allow(from("203.0.113.2", http.MethodPost, "/api/v1/match")); !ok {
		t.Error("another visitor was held up")
	}
	now = now.Add(wait)
	if ok, _ := l.allow(from("203.0.113.1", http.MethodPost, "/api/v1/match")); !ok {
		t.Errorf("still refused after waiting %v", wait)
	}
}

// Only a read-only copy limits visitors and lets its pages be cached.
func TestDemoLimitsAndCaching(t *testing.T) {
	demo := newTestEnv(t, func(d *Deps) { d.ReadOnly, d.ClientIPHeader = true, "CF-Connecting-IP" })
	full := newTestEnv(t)
	get := func(e *testEnv, ip, path string) *http.Response {
		req, _ := http.NewRequestWithContext(t.Context(), http.MethodGet, e.srv.URL+path, nil)
		req.Header.Set("CF-Connecting-IP", ip)
		res, err := e.client().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		_ = res.Body.Close()
		return res
	}
	if cc := get(demo, "203.0.113.9", "/stats").Header.Get("Cache-Control"); cc != demoCache {
		t.Errorf("demo page Cache-Control %q", cc)
	}
	if cc := get(demo, "203.0.113.9", "/no-such-page").Header.Get("Cache-Control"); cc == demoCache {
		t.Error("a demo's 404 is cacheable")
	}
	if cc := get(full, "203.0.113.9", "/stats").Header.Get("Cache-Control"); cc == demoCache {
		t.Error("a full install's page is cacheable")
	}
	var last *http.Response
	for range visitorBudgets[budgetAPI].burst + 1 {
		last = get(demo, "203.0.113.10", "/api/v1/status")
	}
	if last.StatusCode != http.StatusTooManyRequests || last.Header.Get("Retry-After") == "" {
		t.Errorf("demo API past its burst: %d, Retry-After %q", last.StatusCode, last.Header.Get("Retry-After"))
	}
	if res := get(demo, "203.0.113.11", "/api/v1/status"); res.StatusCode != http.StatusOK {
		t.Errorf("another visitor got %d", res.StatusCode)
	}
	for range visitorBudgets[budgetAPI].burst + 1 {
		last = get(full, "203.0.113.10", "/api/v1/status")
	}
	if last.StatusCode != http.StatusOK {
		t.Errorf("a full install limited its user: %d", last.StatusCode)
	}
}
