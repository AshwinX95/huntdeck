package httpclient

import (
	"context"
	"errors"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// recordSleeps returns a Sleep func that records delays instead of waiting.
func recordSleeps(into *[]time.Duration) func(context.Context, time.Duration) error {
	return func(ctx context.Context, d time.Duration) error {
		*into = append(*into, d)
		return ctx.Err()
	}
}

func newClient(sleeps *[]time.Duration) *Client {
	return New(Options{RatePerSecond: 1000, Burst: 100, MaxAttempts: 4, Sleep: recordSleeps(sleeps)})
}

func get(t *testing.T, c *Client, u string) (*Response, error) {
	t.Helper()
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, u, nil)
	if err != nil {
		t.Fatal(err)
	}
	return c.Do(context.Background(), req)
}

// sequence serves the given status codes in order, then 200s.
func sequence(t *testing.T, codes []int, header http.Header) (*httptest.Server, *atomic.Int32) {
	t.Helper()
	var n atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		i := int(n.Add(1)) - 1
		for k, v := range header {
			w.Header()[k] = v
		}
		if i < len(codes) {
			w.WriteHeader(codes[i])
			return
		}
		_, _ = w.Write([]byte("ok"))
	}))
	t.Cleanup(srv.Close)
	return srv, &n
}

func TestRetriesServerErrorsThenSucceeds(t *testing.T) {
	var sleeps []time.Duration
	srv, n := sequence(t, []int{500, 503}, nil)
	c := newClient(&sleeps)

	res, err := get(t, c, srv.URL)
	if err != nil || res.StatusCode != 200 || string(res.Body) != "ok" {
		t.Fatalf("got %+v, %v", res, err)
	}
	if n.Load() != 3 || len(sleeps) != 2 {
		t.Errorf("requests=%d sleeps=%v; want 3 requests, 2 sleeps", n.Load(), sleeps)
	}
	if sleeps[1] <= sleeps[0]/2 {
		t.Errorf("backoff should grow: %v", sleeps)
	}
	if reqs, _ := c.Stats(); reqs != 3 {
		t.Errorf("Stats requests = %d, want 3", reqs)
	}
}

func TestHonoursRetryAfter(t *testing.T) {
	var sleeps []time.Duration
	srv, _ := sequence(t, []int{429}, http.Header{"Retry-After": {"7"}})
	c := newClient(&sleeps)

	if _, err := get(t, c, srv.URL); err != nil {
		t.Fatal(err)
	}
	if len(sleeps) != 1 || sleeps[0] != 7*time.Second {
		t.Errorf("sleeps = %v, want [7s]", sleeps)
	}
	if _, limited := c.Stats(); limited != 1 {
		t.Errorf("rate limited count = %d, want 1", limited)
	}
}

func TestRateLimitWithoutRetryAfterUsesLongBackoff(t *testing.T) {
	var sleeps []time.Duration
	srv, _ := sequence(t, []int{429}, nil)
	c := newClient(&sleeps)
	if _, err := get(t, c, srv.URL); err != nil {
		t.Fatal(err)
	}
	if len(sleeps) != 1 || sleeps[0] < 10*time.Second {
		t.Errorf("sleeps = %v, want one delay of at least ~15s minus jitter", sleeps)
	}
}

func TestGivesUpAfterMaxAttempts(t *testing.T) {
	var sleeps []time.Duration
	srv, n := sequence(t, []int{500, 500, 500, 500, 500}, nil)
	c := newClient(&sleeps)
	_, err := get(t, c, srv.URL)
	if err == nil || !strings.Contains(err.Error(), "giving up after 4 attempts") {
		t.Fatalf("err = %v", err)
	}
	if n.Load() != 4 {
		t.Errorf("requests = %d, want 4", n.Load())
	}
}

func TestClientErrorsAreNotRetried(t *testing.T) {
	var sleeps []time.Duration
	srv, n := sequence(t, []int{404}, nil)
	res, err := get(t, newClient(&sleeps), srv.URL)
	if err != nil || res.StatusCode != 404 || n.Load() != 1 {
		t.Errorf("res=%+v err=%v requests=%d; want one 404 and no retry", res, err, n.Load())
	}
}

func TestContextCancelStopsRetrying(t *testing.T) {
	srv, _ := sequence(t, []int{500, 500, 500}, nil)
	ctx, cancel := context.WithCancel(context.Background())
	c := New(Options{RatePerSecond: 1000, Sleep: func(context.Context, time.Duration) error {
		cancel()
		return context.Canceled
	}})
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL, nil)
	if _, err := c.Do(ctx, req); !errors.Is(err, context.Canceled) {
		t.Errorf("err = %v, want context.Canceled", err)
	}
}

func TestBodyLimit(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(strings.Repeat("x", 100)))
	}))
	defer srv.Close()
	var sleeps []time.Duration
	c := New(Options{RatePerSecond: 1000, MaxAttempts: 1, MaxBodyBytes: 10, Sleep: recordSleeps(&sleeps)})
	if _, err := get(t, c, srv.URL); err == nil || !strings.Contains(err.Error(), "exceeds") {
		t.Errorf("err = %v, want body size error", err)
	}
}

func TestErrorsDoNotLeakCredentials(t *testing.T) {
	var sleeps []time.Duration
	c := New(Options{RatePerSecond: 1000, MaxAttempts: 1, Sleep: recordSleeps(&sleeps)})
	// Port 1 on loopback refuses connections.
	_, err := get(t, c, "http://user:hunter2@127.0.0.1:1/x?api_token=s3cret&page=2")
	if err == nil {
		t.Fatal("expected connection error")
	}
	for _, leak := range []string{"hunter2", "s3cret"} {
		if strings.Contains(err.Error(), leak) {
			t.Errorf("error leaks %q: %v", leak, err)
		}
	}
	if !strings.Contains(err.Error(), "page=2") {
		t.Errorf("non-sensitive query lost: %v", err)
	}
}

func TestDoesNotFollowCrossHostRedirect(t *testing.T) {
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("credentials forwarded to another host: %v", r.Header.Get("Authorization"))
	}))
	defer other.Close()
	srv := httptest.NewServer(http.RedirectHandler(other.URL, http.StatusFound))
	defer srv.Close()

	var sleeps []time.Duration
	req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, srv.URL, nil)
	req.Header.Set("Authorization", "Basic x")
	res, err := newClient(&sleeps).Do(context.Background(), req)
	if err != nil || res.StatusCode != http.StatusFound {
		t.Errorf("res=%+v err=%v; want the 302 returned, not followed", res, err)
	}
}

func TestRetryAfterParsing(t *testing.T) {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	tests := []struct {
		v    string
		want time.Duration
	}{
		{"", 0},
		{"5", 5 * time.Second},
		{"Thu, 01 Jan 2026 00:00:30 GMT", 30 * time.Second},
		{"soon", 0},
	}
	for _, tt := range tests {
		if got := retryAfter(http.Header{"Retry-After": {tt.v}}, now); got != tt.want {
			t.Errorf("retryAfter(%q) = %v, want %v", tt.v, got, tt.want)
		}
	}
}

func TestRedactURL(t *testing.T) {
	u, _ := url.Parse("https://u:p@api.example.com/v1?token=abc&page=3")
	got := RedactURL(u)
	if strings.Contains(got, "abc") || strings.Contains(got, "u:p") || !strings.Contains(got, "page=3") {
		t.Errorf("RedactURL = %q", got)
	}
}

func TestRedirectsStayOnHostUnlessAllowed(t *testing.T) {
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("other"))
	}))
	t.Cleanup(other.Close)
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, other.URL+"/landing", http.StatusFound)
	}))
	t.Cleanup(origin.Close)

	res, err := get(t, New(Options{RatePerSecond: 1000}), origin.URL)
	if err != nil {
		t.Fatal(err)
	}
	if res.StatusCode != http.StatusFound {
		t.Fatalf("unlisted host: status %d, want the 302 itself", res.StatusCode)
	}

	otherHost := strings.TrimPrefix(other.URL, "http://")
	res, err = get(t, New(Options{RatePerSecond: 1000, RedirectHosts: []string{otherHost}}), origin.URL)
	if err != nil {
		t.Fatal(err)
	}
	if res.StatusCode != http.StatusOK || string(res.Body) != "other" {
		t.Fatalf("allowed host: status %d body %q", res.StatusCode, res.Body)
	}
}

func TestJarKeepsCookies(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/set" {
			http.SetCookie(w, &http.Cookie{Name: "sid", Value: "abc", Path: "/"})
			http.Redirect(w, r, "/check", http.StatusFound)
			return
		}
		c, err := r.Cookie("sid")
		if err != nil {
			http.Error(w, "no cookie", http.StatusUnauthorized)
			return
		}
		_, _ = w.Write([]byte(c.Value))
	}))
	t.Cleanup(srv.Close)
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	res, err := get(t, New(Options{RatePerSecond: 1000, Jar: jar}), srv.URL+"/set")
	if err != nil {
		t.Fatal(err)
	}
	if res.StatusCode != http.StatusOK || string(res.Body) != "abc" {
		t.Fatalf("status %d body %q", res.StatusCode, res.Body)
	}
}
