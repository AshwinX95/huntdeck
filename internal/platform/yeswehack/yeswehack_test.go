package yeswehack

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mdpsec/huntdeck/internal/httpclient"
	"github.com/mdpsec/huntdeck/internal/platform"
	"github.com/mdpsec/huntdeck/internal/scope"
	"github.com/mdpsec/huntdeck/internal/totp"
)

const (
	testEmail    = "hunter@example.com"
	testPassword = "p#ss$word!"
	testSecret   = "JBSWY3DPEHPK3PXP"
)

var t0 = time.Date(2026, 10, 26, 12, 0, 5, 0, time.UTC)

func jwt(exp time.Time) string {
	enc := base64.RawURLEncoding.EncodeToString
	return enc([]byte(`{"alg":"none"}`)) + "." + enc([]byte(fmt.Sprintf(`{"exp":%d}`, exp.Unix()))) + ".sig"
}

type fakeYWH struct {
	t   *testing.T
	srv *httptest.Server

	mu         sync.Mutex
	now        time.Time
	valid      map[string]bool // accepted bearer tokens
	logins     int
	badLogins  int
	hits       map[string]int
	tokenLife  time.Duration
	withTotal  int // overrides nb_results when > 0
	forbidden  bool
	requireTok bool // listing needs a token (to test 401 handling)
}

func newFake(t *testing.T) *fakeYWH {
	t.Helper()
	f := &fakeYWH{t: t, now: t0, valid: map[string]bool{}, hits: map[string]int{}, tokenLife: 4 * time.Hour}
	f.srv = httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeYWH) client(t *testing.T, o Options) *Client {
	t.Helper()
	o.BaseURL = f.srv.URL
	o.HTTP = httpclient.New(httpclient.Options{RatePerSecond: 1000, Burst: 100, MaxAttempts: 1})
	o.Now = func() time.Time { f.mu.Lock(); defer f.mu.Unlock(); return f.now }
	o.Sleep = func(context.Context, time.Duration) error { f.advance(30 * time.Second); return nil }
	c, err := New(o)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func (f *fakeYWH) advance(d time.Duration) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.now = f.now.Add(d)
}

func (f *fakeYWH) fixture(name string) []byte {
	b, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		f.t.Fatal(err)
	}
	return b
}

func (f *fakeYWH) serve(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	f.hits[r.URL.Path]++
	auth := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	authed := f.valid[auth]
	if auth != "" && !authed {
		f.mu.Unlock()
		http.Error(w, `{"code":401,"message":"Invalid JWT Token"}`, http.StatusUnauthorized)
		return
	}
	needTok, forbidden, total := f.requireTok, f.forbidden, f.withTotal
	f.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")

	var body map[string]string
	if r.Method == http.MethodPost {
		_ = json.NewDecoder(r.Body).Decode(&body)
	}
	switch {
	case r.URL.Path == "/login":
		if body["email"] != testEmail || body["password"] != testPassword {
			f.mu.Lock()
			f.badLogins++
			f.mu.Unlock()
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"code":400,"message":"Bad credentials"}`))
			return
		}
		_, _ = w.Write(f.fixture("login_totp.json"))
	case r.URL.Path == "/account/totp":
		f.mu.Lock()
		now := f.now
		f.mu.Unlock()
		want, _ := totp.Code(testSecret, now)
		if body["token"] != "totp-tmp-123" || body["code"] != want {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		f.mu.Lock()
		tok := jwt(now.Add(f.tokenLife))
		f.valid[tok] = true
		f.logins++
		f.mu.Unlock()
		_ = json.NewEncoder(w).Encode(map[string]string{"token": tok})
	case needTok && !authed:
		w.WriteHeader(http.StatusUnauthorized)
	case r.URL.Path == "/programs":
		b := f.fixture("programs_page" + r.URL.Query().Get("page") + ".json")
		if total > 0 {
			b = []byte(strings.Replace(string(b), `"nb_results":4`, fmt.Sprintf(`"nb_results":%d`, total), 1))
		}
		_, _ = w.Write(b)
	case r.URL.Path == "/programs/acme-bug-bounty/ranking":
		if r.URL.Query().Get("resultsPerPage") != "1" {
			t := f.t
			t.Errorf("ranking read with %q", r.URL.RawQuery)
		}
		_, _ = w.Write([]byte(`{"pagination":{"page":1,"nb_pages":379,"results_per_page":1,"nb_results":379},"items":[{"username":"researcher1","points":1429,"rank":1}]}`))
	case r.URL.Path == "/programs/acme-bug-bounty/versions":
		_, _ = w.Write(f.fixture("versions_acme.json"))
	case r.URL.Path == "/programs/acme-bug-bounty":
		if forbidden {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		_, _ = w.Write(f.fixture("program_acme.json"))
	default:
		http.NotFound(w, r)
	}
}

func TestListPublic(t *testing.T) {
	f := newFake(t)
	l, err := f.client(t, Options{}).ListPrograms(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !l.Complete || len(l.Programs) != 3 {
		t.Fatalf("complete=%v programs=%d, want 3 (demo skipped)", l.Complete, len(l.Programs))
	}
	want := []string{
		"acme-bug-bounty public open bounty=true EUR 50..5000 https://yeswehack.com/programs/acme-bug-bounty verified=false",
		"globex-private private open bounty=true EUR -..- https://yeswehack.com/programs/globex-private verified=true",
		"initech-vdp public closed bounty=false  -..- https://yeswehack.com/programs/initech-vdp verified=false",
	}
	for i, p := range l.Programs {
		vis := map[bool]string{true: "private", false: "public"}[p.Private]
		got := fmt.Sprintf("%s %s %s bounty=%v %s %s %s verified=%v", p.Handle, vis, p.State, p.OffersBounty, p.Currency, bountyRange(p), p.URL,
			p.Requirements != nil && p.Requirements.IDVerified)
		if got != want[i] {
			t.Errorf("program %d:\n got %s\nwant %s", i, got, want[i])
		}
	}
	// The listing's report cost, collaboration and country; absent ones
	// say nothing.
	acme, globex := l.Programs[0], l.Programs[1]
	if acme.ReportCost == nil || *acme.ReportCost != 3 || acme.Collaboration == nil || !*acme.Collaboration || acme.Country != "FR" {
		t.Errorf("acme: cost %v, collaboration %v, country %q", acme.ReportCost, acme.Collaboration, acme.Country)
	}
	if acme.OffersSwag == nil || !*acme.OffersSwag || globex.OffersSwag != nil {
		t.Errorf("swag: acme %v, globex %v; want true and not said", acme.OffersSwag, globex.OffersSwag)
	}
	if globex.ReportCost != nil || globex.Collaboration != nil || globex.Country != "" {
		t.Errorf("globex: cost %v, collaboration %v, country %q", globex.ReportCost, globex.Collaboration, globex.Country)
	}
}

func TestListingShortOfTotalIsIncomplete(t *testing.T) {
	f := newFake(t)
	f.withTotal = 9
	l, err := f.client(t, Options{}).ListPrograms(context.Background())
	if err == nil || l.Complete {
		t.Fatalf("complete=%v err=%v", l.Complete, err)
	}
}

func TestLoginWithTOTPAndRelogin(t *testing.T) {
	f := newFake(t)
	f.requireTok = true
	c := f.client(t, Options{Email: testEmail, Password: testPassword, TOTPSecret: testSecret})
	if _, err := c.ListPrograms(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := c.ListPrograms(context.Background()); err != nil {
		t.Fatal(err)
	}
	if f.logins != 1 {
		t.Fatalf("logins = %d, want 1 (token reused)", f.logins)
	}

	// Close to expiry: log in again before the server refuses the token.
	f.advance(4*time.Hour - 30*time.Second)
	if _, err := c.ListPrograms(context.Background()); err != nil {
		t.Fatal(err)
	}
	if f.logins != 2 {
		t.Fatalf("logins = %d, want 2", f.logins)
	}

	// The server revokes the token early: one 401, one fresh login, retry.
	f.mu.Lock()
	f.valid = map[string]bool{}
	f.mu.Unlock()
	f.advance(time.Minute)
	if _, err := c.ListPrograms(context.Background()); err != nil {
		t.Fatal(err)
	}
	if f.logins != 3 {
		t.Fatalf("logins = %d, want 3", f.logins)
	}
}

// With a login, KeepAlive signs in when there's no token and again shortly
// before the one held expires, and reports when it expires.
func TestKeepAliveRenewsToken(t *testing.T) {
	f := newFake(t)
	f.requireTok = true
	c := f.client(t, Options{Email: testEmail, Password: testPassword, TOTPSecret: testSecret})
	ctx := context.Background()
	if err := c.KeepAlive(ctx); err != nil {
		t.Fatal(err)
	}
	if err := c.KeepAlive(ctx); err != nil {
		t.Fatal(err)
	}
	if f.logins != 1 {
		t.Fatalf("logins = %d, want 1", f.logins)
	}
	if cr := c.Credentials(); len(cr) != 1 || cr[0].SignedIn == nil || cr[0].Expires == nil {
		t.Errorf("credentials = %+v", cr)
	}
	f.advance(4*time.Hour - renewBefore + time.Minute) // within renewBefore of expiry
	if err := c.KeepAlive(ctx); err != nil {
		t.Fatal(err)
	}
	if f.logins != 2 {
		t.Errorf("logins = %d, want 2 (renewed before expiry)", f.logins)
	}
	// A pasted token is left alone.
	p := f.client(t, Options{Token: "pasted"})
	if err := p.KeepAlive(ctx); err != nil || f.logins != 2 {
		t.Errorf("pasted token: %v, logins %d", err, f.logins)
	}
}

func TestWrongPasswordIsNotRetried(t *testing.T) {
	f := newFake(t)
	f.requireTok = true
	c := f.client(t, Options{Email: testEmail, Password: "wrong", TOTPSecret: testSecret})
	for range 3 {
		_, err := c.ListPrograms(context.Background())
		var le *loginError
		if !errors.As(err, &le) || strings.Contains(err.Error(), "wrong") {
			t.Fatalf("err = %v", err)
		}
	}
	if f.badLogins != 1 {
		t.Fatalf("bad logins = %d, want 1", f.badLogins)
	}
	if cr := c.Credentials(); len(cr) != 1 || cr[0].Name != "Login" || !cr[0].Stuck || cr[0].Failure == "" || strings.Contains(cr[0].Failure, "wrong") {
		t.Errorf("credentials after a wrong password = %+v", cr)
	}
}

func TestWrongTOTPSecret(t *testing.T) {
	f := newFake(t)
	f.requireTok = true
	c := f.client(t, Options{Email: testEmail, Password: testPassword, TOTPSecret: "GEZDGNBVGY3TQOJQ"})
	_, err := c.ListPrograms(context.Background())
	if err == nil || !strings.Contains(err.Error(), "authenticator code not accepted") {
		t.Fatalf("err = %v", err)
	}
	if f.hits["/account/totp"] != 2 {
		t.Errorf("totp tries = %d, want 2 (one retry for clock skew)", f.hits["/account/totp"])
	}
}

func TestPastedTokenRejected(t *testing.T) {
	f := newFake(t)
	c := f.client(t, Options{Token: "stale"})
	if _, err := c.ListPrograms(context.Background()); !errors.Is(err, ErrAuth) {
		t.Fatalf("err = %v", err)
	}
	if cr := c.Credentials(); len(cr) != 1 || cr[0].Name != "Token" || cr[0].Failure == "" {
		t.Errorf("credentials after a refused token = %+v", cr)
	}
	expired := f.client(t, Options{Token: jwt(t0.Add(-time.Hour))})
	if _, err := expired.ListPrograms(context.Background()); !errors.Is(err, ErrAuth) {
		t.Fatalf("expired token: err = %v", err)
	}
	if cr := expired.Credentials(); len(cr) != 1 || !cr[0].Stuck {
		t.Errorf("credentials after an expired token = %+v", cr)
	}
	if cr := f.client(t, Options{}).Credentials(); cr != nil {
		t.Errorf("public mode credentials = %+v, want none", cr)
	}
}

func TestFetchScopeAndCache(t *testing.T) {
	f := newFake(t)
	c := f.client(t, Options{})
	l, err := c.ListPrograms(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	acme := l.Programs[0]
	es, err := c.FetchScope(context.Background(), acme)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, e := range es {
		got = append(got, fmt.Sprintf("%v|%s|%s|%s|%v|%s", e.InScope, e.AssetType, e.PlatformAssetType, e.Identifier, e.BountyEligible, e.Instruction))
	}
	want := []string{
		"true|url|web-application|https://app.acme.example.com|true|Asset value: critical",
		"true|wildcard|wildcard|*.acme.example.com|true|Asset value: very low",
		"true|android|mobile-application-android|https://play.google.com/store/apps/details?id=com.example.acme|true|Asset value: high",
		"true|other|other|Acme desktop client|true|",
		"false|domain|out_of_scope|blog.acme.example.com|false|",
		"false|other|out_of_scope|All third parties are out of scope|false|",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("entries:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}

	if acme.ScopeChangedAt == nil || !acme.ScopeChangedAt.Equal(time.Date(2026, 10, 23, 12, 12, 18, 0, time.UTC)) {
		t.Errorf("ScopeChangedAt = %v, want last_update_at in UTC", acme.ScopeChangedAt)
	}
}

func TestFetchScopeDetail(t *testing.T) {
	f := newFake(t)
	c := f.client(t, Options{})
	es, d, err := c.FetchScopeDetail(context.Background(), platform.Program{Handle: "acme-bug-bounty"})
	if err != nil || len(es) != 6 {
		t.Fatalf("FetchScopeDetail = %d entries, %v", len(es), err)
	}
	wantPolicy := "## Rules\n\nBe nice.\n\n## Qualifying vulnerabilities\n\n* SQL Injection (SQLi)\n* Cross-Site Scripting (XSS)" +
		"\n\n## Non-qualifying vulnerabilities\n\n* Tabnabbing"
	if d.Policy != wantPolicy {
		t.Errorf("policy = %q\nwant %q", d.Policy, wantPolicy)
	}
	// Rows for the asset values in use with a grid: very low has none.
	want := []platform.RewardRow{
		{Scope: "Asset value: high", Low: platform.RewardRange{Max: 150}, Medium: platform.RewardRange{Max: 600},
			High: platform.RewardRange{Max: 2000}, Critical: platform.RewardRange{Max: 5000}},
		{Scope: "Asset value: critical", Low: platform.RewardRange{Max: 300}, Medium: platform.RewardRange{Max: 1000},
			High: platform.RewardRange{Max: 4000}, Critical: platform.RewardRange{Max: 10000}},
	}
	if fmt.Sprint(d.Rewards) != fmt.Sprint(want) {
		t.Errorf("rewards = %v\nwant %v", d.Rewards, want)
	}
}

func TestRewardGridFallbacks(t *testing.T) {
	critical := 1000.0
	grid := &rewardGrid{Critical: &critical}
	tests := []struct {
		name string
		d    programDetail
		want string
	}{
		{"no bounty", programDetail{GridDefault: grid}, "[]"},
		{"no asset values: default grid", programDetail{Bounty: true, GridDefault: grid}, "[{ {0 0} {0 0} {0 0} {0 1000}}]"},
		{"bounty without any grid", programDetail{Bounty: true}, "[]"},
	}
	for _, tt := range tests {
		if got := tt.d.rewards(); got == nil || fmt.Sprint(got) != tt.want {
			t.Errorf("%s: rewards = %v, want %s", tt.name, got, tt.want)
		}
	}
}

func TestForbiddenScopeIsUnavailable(t *testing.T) {
	f := newFake(t)
	f.forbidden = true
	c := f.client(t, Options{})
	_, err := c.FetchScope(context.Background(), platform.Program{Handle: "acme-bug-bounty"})
	if !errors.Is(err, platform.ErrScopeUnavailable) {
		t.Fatalf("err = %v", err)
	}
}

func TestAssetType(t *testing.T) {
	tests := []struct{ typ, ident, want string }{
		{"web-application", "www.example.com", scope.AssetDomain},
		{"web-application", "https://www.example.com", scope.AssetURL},
		{"application", "*.example.com", scope.AssetWildcard},
		{"application", "Any app under https://example.com", scope.AssetOther},
		{"wildcard", "*.example.com", scope.AssetWildcard},
		{"api", "https://api.example.com", scope.AssetAPI},
		{"mobile-application-ios", "https://apps.apple.com/x", scope.AssetIOS},
		{"mobile-application", "app.example.com", scope.AssetOther},
		{"ai-model", "https://chat.example.com", scope.AssetAIModel},
		{"challenge", "Crown Jewel", scope.AssetOther},
	}
	for _, tt := range tests {
		if got := assetType(tt.typ, tt.ident); got != tt.want {
			t.Errorf("assetType(%q, %q) = %q, want %q", tt.typ, tt.ident, got, tt.want)
		}
	}
}

func TestTokenExpiry(t *testing.T) {
	if got := tokenExpiry(jwt(t0)); !got.Equal(t0.Truncate(time.Second)) {
		t.Errorf("exp = %v", got)
	}
	for _, s := range []string{"", "opaque", "a.b.c", "a." + base64.RawURLEncoding.EncodeToString([]byte(`{}`)) + ".c"} {
		if !tokenExpiry(s).IsZero() {
			t.Errorf("tokenExpiry(%q) should be zero", s)
		}
	}
}

func TestFetchProgramStats(t *testing.T) {
	f := newFake(t)
	c := f.client(t, Options{})
	got := map[string]platform.ProgramStats{}
	err := c.FetchProgramStats(context.Background(), []string{"acme-bug-bounty", "gone"},
		func(covered []string, stats map[string]platform.ProgramStats) error {
			maps.Copy(got, stats)
			return nil
		})
	if err != nil {
		t.Fatal(err)
	}
	s, ok := got["acme-bug-bounty"]
	if !ok || len(got) != 1 {
		t.Fatalf("stats = %v", got)
	}
	if s.Currency != "EUR" || *s.TotalReports != 175 || *s.ReportsLast7d != 4 || *s.ReportsThisMonth != 9 || *s.FirstResponseHours != 48 ||
		*s.AvgBountyLow != 412.5 || *s.TopBountyHigh != 3000 || s.Hunters == nil || *s.Hunters != 379 {
		t.Errorf("stats = %+v", s)
	}
}

func TestScopeHistory(t *testing.T) {
	f := newFake(t)
	c := f.client(t, Options{})
	got := map[string][]platform.ScopeVersion{}
	err := c.ScopeHistory(context.Background(), []platform.Program{{Handle: "acme-bug-bounty"}, {Handle: "gone"}},
		func(handle string, v []platform.ScopeVersion) error {
			got[handle] = v
			return nil
		})
	if err != nil {
		t.Fatal(err)
	}
	v := got["acme-bug-bounty"]
	if len(got) != 1 || len(v) != 2 {
		t.Fatalf("history = %+v; want 2 scope versions for acme-bug-bounty (rules-only and pending versions skipped)", got)
	}
	idents := func(sv platform.ScopeVersion) string {
		var out []string
		for _, e := range sv.Entries {
			side := "out:"
			if e.InScope {
				side = "in:"
			}
			out = append(out, side+e.Identifier)
		}
		return strings.Join(out, " ")
	}
	if !v[0].At.Equal(time.Date(2021, 4, 12, 8, 25, 19, 0, time.UTC)) || idents(v[0]) != "in:*.acme.example.com in:legacy.acme.example.com out:blog.acme.example.com" {
		t.Errorf("oldest version = %v %q", v[0].At, idents(v[0]))
	}
	// The newest replaces scopes but keeps the out-of-scope list it didn't change.
	if !v[1].At.Equal(time.Date(2025, 5, 16, 11, 9, 27, 0, time.UTC)) || idents(v[1]) != "in:*.acme.example.com in:api.acme.example.com out:blog.acme.example.com" {
		t.Errorf("newest version = %v %q", v[1].At, idents(v[1]))
	}
}

// bountyRange formats a program's bounty range for comparisons.
func bountyRange(p platform.Program) string {
	v := func(f *float64) string {
		if f == nil {
			return "-"
		}
		return strconv.FormatFloat(*f, 'f', -1, 64)
	}
	return v(p.BountyMin) + ".." + v(p.BountyMax)
}
