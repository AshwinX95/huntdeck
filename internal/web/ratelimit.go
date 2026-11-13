package web

import (
	"math"
	"net"
	"net/http"
	"net/netip"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/time/rate"
)

// A read-only copy is shown to anyone, so each visitor (by address) gets a
// budget of requests: pages, API calls and bulk scope checks apiece. Normal
// browsing never meets it; a script copying the site does, and gets 429
// with how long to wait. Static files and the health check are free.

// visitorBudget is one kind of request's rate per visitor: how many a
// minute on average, and how many at once.
type visitorBudget struct {
	perMinute float64
	burst     int
}

var visitorBudgets = [...]visitorBudget{
	budgetPages: {120, 60},
	budgetAPI:   {60, 30},
	budgetBulk:  {5, 3},
}

const (
	budgetPages = iota
	budgetAPI
	budgetBulk
)

const (
	maxVisitors = 100_000          // remembered at once; idle ones go first
	visitorIdle = 10 * time.Minute // forgotten after this long without a request
)

// visitorLimiter keeps each visitor's budgets.
type visitorLimiter struct {
	header string // the trusted header naming the client's address; "" for the connection's
	now    func() time.Time

	mu       sync.Mutex
	visitors map[string]*visitor
}

type visitor struct {
	budgets [len(visitorBudgets)]*rate.Limiter
	seen    time.Time
}

func newVisitorLimiter(header string, now func() time.Time) *visitorLimiter {
	if now == nil {
		now = time.Now
	}
	return &visitorLimiter{header: header, now: now, visitors: map[string]*visitor{}}
}

// budgetFor is the budget r draws on, or -1 for none.
func budgetFor(r *http.Request) int {
	switch p := r.URL.Path; {
	case p == "/healthz" || strings.HasPrefix(p, "/static/"):
		return -1
	case r.Method == http.MethodPost && p == "/api/v1/match":
		return budgetBulk
	case strings.HasPrefix(p, "/api/"):
		return budgetAPI
	}
	return budgetPages
}

// clientKey is who r comes from: the trusted header's address if one is
// configured and sent, else the connection's; an IPv6 address by its /64,
// which one machine usually has whole.
func (l *visitorLimiter) clientKey(r *http.Request) string {
	raw := ""
	if l.header != "" {
		raw = strings.TrimSpace(r.Header.Get(l.header))
	}
	if raw == "" {
		raw = r.RemoteAddr
		if host, _, err := net.SplitHostPort(raw); err == nil {
			raw = host
		}
	}
	addr, err := netip.ParseAddr(raw)
	if err != nil {
		return raw
	}
	addr = addr.Unmap()
	if addr.Is6() {
		p, _ := addr.Prefix(64)
		return p.String()
	}
	return addr.String()
}

// allow takes one request from r's budget; when it's spent, it says how
// long until the next one is allowed.
func (l *visitorLimiter) allow(r *http.Request) (bool, time.Duration) {
	kind := budgetFor(r)
	if kind < 0 {
		return true, 0
	}
	now := l.now()
	key := l.clientKey(r)
	l.mu.Lock()
	defer l.mu.Unlock()
	v := l.visitors[key]
	if v == nil {
		if len(l.visitors) >= maxVisitors {
			l.forgetIdle(now)
		}
		v = &visitor{}
		for i, b := range visitorBudgets {
			v.budgets[i] = rate.NewLimiter(rate.Limit(b.perMinute/60), b.burst)
		}
		l.visitors[key] = v
	}
	v.seen = now
	lim := v.budgets[kind]
	if lim.AllowN(now, 1) {
		return true, 0
	}
	wait := time.Duration(math.Ceil((1 - lim.TokensAt(now)) / float64(lim.Limit()) * float64(time.Second)))
	return false, max(wait, time.Second)
}

// forgetIdle drops visitors not seen for visitorIdle, or, if that frees
// nothing, the longest idle half, so the map stays bounded.
func (l *visitorLimiter) forgetIdle(now time.Time) {
	for k, v := range l.visitors {
		if now.Sub(v.seen) > visitorIdle {
			delete(l.visitors, k)
		}
	}
	if len(l.visitors) < maxVisitors {
		return
	}
	cut := now.Add(-visitorIdle / 2)
	for k, v := range l.visitors {
		if v.seen.Before(cut) || len(l.visitors) >= maxVisitors {
			delete(l.visitors, k)
		}
	}
}

// tooMany answers a request over its budget.
func (s *server) tooMany(w http.ResponseWriter, r *http.Request, wait time.Duration) {
	secs := int(math.Ceil(wait.Seconds()))
	w.Header().Set("Retry-After", strconv.Itoa(secs))
	msg := "Too many requests from you just now: try again in " + strconv.Itoa(secs) + " seconds."
	if strings.HasPrefix(r.URL.Path, "/api/") {
		writeJSONError(w, http.StatusTooManyRequests, msg)
		return
	}
	s.renderError(w, r, http.StatusTooManyRequests, msg)
}

// demoCache is how long a read-only copy's pages may be kept: a minute in
// browsers, five at a shared cache (Cloudflare) in front, so a flood of the
// same pages is answered there. Its data changes daily.
const demoCache = "public, max-age=60, s-maxage=300"

// cacheWriter marks a read-only copy's successful responses cacheable
// (demoCache), replacing the no-store pages set for a full install.
type cacheWriter struct {
	http.ResponseWriter
	wrote bool
}

func (c *cacheWriter) WriteHeader(code int) {
	if !c.wrote {
		c.wrote = true
		if code == http.StatusOK {
			c.Header().Set("Cache-Control", demoCache)
		}
	}
	c.ResponseWriter.WriteHeader(code)
}

func (c *cacheWriter) Write(b []byte) (int, error) {
	if !c.wrote {
		c.WriteHeader(http.StatusOK)
	}
	return c.ResponseWriter.Write(b)
}

// Unwrap lets http.ResponseController reach the writer underneath.
func (c *cacheWriter) Unwrap() http.ResponseWriter { return c.ResponseWriter }
