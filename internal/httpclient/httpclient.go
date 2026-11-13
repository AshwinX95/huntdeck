// Package httpclient is the HTTP client every platform integration uses.
//
// It adds what platform APIs need and plain net/http lacks: a token-bucket
// rate limit, bounded retries with exponential backoff and jitter, 429
// handling that honours Retry-After, a response size cap, and error messages
// that never include credentials.
package httpclient

import (
	"context"
	"errors"
	"fmt"
	"io"
	"math/rand/v2"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"golang.org/x/time/rate"

	"github.com/mdpsec/huntdeck/internal/logging"
	"github.com/mdpsec/huntdeck/internal/version"
)

// Options configures a Client. Zero values get sensible defaults.
type Options struct {
	RatePerSecond float64       // sustained request rate; default 2
	Burst         int           // default 1
	MaxAttempts   int           // total tries per request; default 5
	BaseBackoff   time.Duration // first retry delay for errors/5xx; default 1s
	RateLimitWait time.Duration // first delay for a 429 without Retry-After; default 15s
	MaxBackoff    time.Duration // cap on any single delay; default 2m
	Timeout       time.Duration // per attempt; default 30s
	MaxBodyBytes  int64         // default 32 MiB
	Proxy         *url.URL      // optional HTTP(S) proxy, e.g. Burp
	UserAgent     string        // default "HuntDeck/<version>"

	// Jar, if set, stores cookies across requests (for platforms with a
	// login session instead of an API token).
	Jar http.CookieJar
	// RedirectHosts lists other hosts redirects may lead to, such as a
	// platform's login domains. By default redirects stay on the original
	// host, so credentials in headers never travel elsewhere.
	RedirectHosts []string

	// Sleep waits for d or until ctx is done. Tests replace it.
	Sleep func(ctx context.Context, d time.Duration) error
}

// Client is safe for concurrent use.
type Client struct {
	hc      *http.Client
	limiter *rate.Limiter
	opts    Options

	requests    atomic.Int64
	rateLimited atomic.Int64
}

// Response is a fully read HTTP response.
type Response struct {
	StatusCode int
	Header     http.Header
	Body       []byte
}

// New builds a Client.
func New(o Options) *Client {
	if o.RatePerSecond <= 0 {
		o.RatePerSecond = 2
	}
	if o.Burst <= 0 {
		o.Burst = 1
	}
	if o.MaxAttempts <= 0 {
		o.MaxAttempts = 5
	}
	if o.BaseBackoff <= 0 {
		o.BaseBackoff = time.Second
	}
	if o.RateLimitWait <= 0 {
		o.RateLimitWait = 15 * time.Second
	}
	if o.MaxBackoff <= 0 {
		o.MaxBackoff = 2 * time.Minute
	}
	if o.Timeout <= 0 {
		o.Timeout = 30 * time.Second
	}
	if o.MaxBodyBytes <= 0 {
		o.MaxBodyBytes = 32 << 20
	}
	if o.UserAgent == "" {
		o.UserAgent = "HuntDeck/" + version.Version + " (+https://github.com/mdpsec/huntdeck)"
	}
	if o.Sleep == nil {
		o.Sleep = sleepCtx
	}

	tr := http.DefaultTransport.(*http.Transport).Clone()
	if o.Proxy != nil {
		tr.Proxy = http.ProxyURL(o.Proxy)
	}
	allowed := map[string]bool{}
	for _, h := range o.RedirectHosts {
		allowed[strings.ToLower(h)] = true
	}
	return &Client{
		hc: &http.Client{
			Transport: tr,
			Timeout:   o.Timeout,
			Jar:       o.Jar,
			// Never follow a redirect to an unexpected host. Go drops the
			// Authorization header on cross-host redirects anyway, and the
			// jar only sends each host its own cookies.
			CheckRedirect: func(req *http.Request, via []*http.Request) error {
				if len(via) >= 10 {
					return errors.New("stopped after 10 redirects")
				}
				if req.URL.Host != via[0].URL.Host && !allowed[strings.ToLower(req.URL.Host)] {
					return http.ErrUseLastResponse
				}
				if req.URL.Scheme != "https" && via[0].URL.Scheme == "https" {
					return http.ErrUseLastResponse
				}
				return nil
			},
		},
		limiter: rate.NewLimiter(rate.Limit(o.RatePerSecond), o.Burst),
		opts:    o,
	}
}

// Stats reports totals since the client was created.
func (c *Client) Stats() (requests, rateLimited int64) {
	return c.requests.Load(), c.rateLimited.Load()
}

// Do sends req, retrying network errors, 429 and 5xx responses. Any other
// status is returned as-is for the caller to interpret. The request body, if
// any, must be replayable (http.NewRequest sets GetBody for common types).
func (c *Client) Do(ctx context.Context, req *http.Request) (*Response, error) {
	if req.Header.Get("User-Agent") == "" {
		req.Header.Set("User-Agent", c.opts.UserAgent)
	}
	target := RedactURL(req.URL)

	var lastErr error
	for attempt := 1; ; attempt++ {
		if err := c.limiter.Wait(ctx); err != nil {
			return nil, fmt.Errorf("%s %s: %w", req.Method, target, err)
		}
		res, err := c.once(ctx, req)
		c.requests.Add(1)

		var wait time.Duration
		switch {
		case err != nil:
			if ctx.Err() != nil {
				return nil, fmt.Errorf("%s %s: %w", req.Method, target, ctx.Err())
			}
			lastErr = fmt.Errorf("%s %s: %w", req.Method, target, err)
			wait = c.backoff(c.opts.BaseBackoff, attempt)
		case res.StatusCode == http.StatusTooManyRequests:
			c.rateLimited.Add(1)
			lastErr = fmt.Errorf("%s %s: rate limited (HTTP 429)", req.Method, target)
			wait = retryAfter(res.Header, time.Now())
			if wait <= 0 {
				wait = c.backoff(c.opts.RateLimitWait, attempt)
			}
		case res.StatusCode >= 500:
			lastErr = fmt.Errorf("%s %s: HTTP %d", req.Method, target, res.StatusCode)
			wait = c.backoff(c.opts.BaseBackoff, attempt)
		default:
			return res, nil
		}

		if attempt >= c.opts.MaxAttempts {
			return nil, fmt.Errorf("giving up after %d attempts: %w", attempt, lastErr)
		}
		if err := c.opts.Sleep(ctx, min(wait, c.opts.MaxBackoff)); err != nil {
			return nil, fmt.Errorf("%s %s: %w", req.Method, target, err)
		}
	}
}

func (c *Client) once(ctx context.Context, req *http.Request) (*Response, error) {
	r := req.Clone(ctx)
	if req.GetBody != nil {
		body, err := req.GetBody()
		if err != nil {
			return nil, err
		}
		r.Body = body
	}
	res, err := c.hc.Do(r)
	if err != nil {
		return nil, scrubURLError(err)
	}
	defer func() { _ = res.Body.Close() }()

	body, err := io.ReadAll(io.LimitReader(res.Body, c.opts.MaxBodyBytes+1))
	if err != nil {
		return nil, fmt.Errorf("read body: %w", err)
	}
	if int64(len(body)) > c.opts.MaxBodyBytes {
		return nil, fmt.Errorf("response body exceeds %d bytes", c.opts.MaxBodyBytes)
	}
	return &Response{StatusCode: res.StatusCode, Header: res.Header, Body: body}, nil
}

// backoff returns base * 2^(attempt-1) with ±20% jitter.
func (c *Client) backoff(base time.Duration, attempt int) time.Duration {
	d := base << min(attempt-1, 10)
	jitter := 0.8 + 0.4*rand.Float64() //nolint:gosec // jitter needs no crypto randomness
	return time.Duration(float64(d) * jitter)
}

// retryAfter parses a Retry-After header (seconds or HTTP date).
func retryAfter(h http.Header, now time.Time) time.Duration {
	v := strings.TrimSpace(h.Get("Retry-After"))
	if v == "" {
		return 0
	}
	if secs, err := strconv.Atoi(v); err == nil && secs >= 0 {
		return time.Duration(secs) * time.Second
	}
	if t, err := http.ParseTime(v); err == nil {
		return t.Sub(now)
	}
	return 0
}

// scrubURLError replaces the URL inside a *url.Error with its redacted form,
// so transport errors can't leak query-string credentials.
func scrubURLError(err error) error {
	var ue *url.Error
	if errors.As(err, &ue) {
		if u, perr := url.Parse(ue.URL); perr == nil {
			return &url.Error{Op: ue.Op, URL: RedactURL(u), Err: ue.Err}
		}
		return &url.Error{Op: ue.Op, URL: "[unparseable URL]", Err: ue.Err}
	}
	return err
}

// RedactURL renders u without user info and with sensitive query values
// masked, for logs and error messages.
func RedactURL(u *url.URL) string {
	c := *u
	c.User = nil
	q := c.Query()
	changed := false
	for k := range q {
		if logging.IsSensitiveKey(k) {
			q.Set(k, logging.Redacted)
			changed = true
		}
	}
	if changed {
		c.RawQuery = q.Encode()
	}
	return c.String()
}

func sleepCtx(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return nil
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}
