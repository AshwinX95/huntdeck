package hackerone

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mdpsec/huntdeck/internal/httpclient"
	"github.com/mdpsec/huntdeck/internal/platform"
)

// fakeSite is hackerone.com's website: pages carry a CSRF token for the
// session, and GraphQL answers `me`, the invitations and acceptInvitation
// for a signed-in session with its token.
type fakeSite struct {
	t   *testing.T
	srv *httptest.Server

	mu       sync.Mutex
	sessions map[string]bool // live session cookie values
	invites  []string        // open invitations, by team handle
	accepted []string
	fail     bool // GraphQL answers 500
	queries  []string
	authSent bool // an Authorization header reached the website
}

func newFakeSite(t *testing.T) *fakeSite {
	f := &fakeSite{t: t, sessions: map[string]bool{}}
	f.srv = httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeSite) session(r *http.Request) string {
	if ck, err := r.Cookie(sessionCookie); err == nil && f.sessions[ck.Value] {
		return ck.Value
	}
	return ""
}

func (f *fakeSite) serve(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if r.Header.Get("Authorization") != "" {
		f.authSent = true
	}
	sess := f.session(r)
	switch r.URL.Path {
	case csrfPage:
		_, _ = w.Write([]byte(`<html><head><meta name="csrf-token" content="tok-` + sess + `"></head></html>`))
	case "/graphql":
		if r.Header.Get("X-CSRF-Token") != "tok-"+sess {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		if f.fail {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		var body struct {
			Query     string         `json:"query"`
			Variables map[string]any `json:"variables"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		f.queries = append(f.queries, strings.Fields(body.Query)[1])
		var me any
		if sess != "" {
			me = map[string]any{"id": "Z2lkOi8v"}
		}
		switch {
		case strings.HasPrefix(body.Query, "mutation AcceptSoftLaunchInvitation"):
			tok, _ := body.Variables["token"].(string)
			for i, h := range f.invites {
				if "inv-"+h == tok {
					f.accepted = append(f.accepted, h)
					f.invites = append(f.invites[:i], f.invites[i+1:]...)
					_, _ = w.Write([]byte(`{"data":{"acceptInvitation":{"was_successful":true}}}`))
					return
				}
			}
			_, _ = w.Write([]byte(`{"data":{"acceptInvitation":{"was_successful":false}}}`))
		case strings.HasPrefix(body.Query, "query PendingInvitations"), strings.HasPrefix(body.Query, "query Invitation"):
			want, _ := body.Variables["h"].(string)
			var edges []map[string]any
			for _, h := range f.invites {
				if want == "" || want == h {
					edges = append(edges, map[string]any{"node": map[string]any{"token": "inv-" + h, "expires_at": "2026-11-21T01:07:00Z",
						"team": map[string]any{"handle": h, "name": strings.ToUpper(h), "currency": "usd", "offers_bounties": true,
							"started_accepting_at": "2021-12-01T00:00:00.000Z"}}})
				}
			}
			if me != nil {
				me = map[string]any{"id": "Z2lkOi8v", "soft_launch_invitations": map[string]any{"pageInfo": map[string]any{"hasNextPage": false}, "edges": edges}}
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{"me": me}})
		case strings.Contains(body.Query, "team(handle"):
			// Stats: private programs only for a signed-in session.
			h, _ := body.Variables["h0"].(string)
			var team any
			if sess != "" || !strings.HasPrefix(h, "private") {
				team = map[string]any{"handle": h, "currency": "usd", "resolved_report_count": 7}
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{"t0": team}})
		default:
			_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{"me": me}})
		}
	default:
		w.WriteHeader(http.StatusNotFound)
	}
}

// webClient is a client of the fake API (if api isn't nil) and the fake
// website, whose login (counted in logins) starts session "s<n>" unless
// refuse says otherwise.
func webClient(t *testing.T, api *fakeAPI, site *fakeSite, logins *int, refuse error) *Client {
	t.Helper()
	jar, _ := cookiejar.New(nil)
	opts := httpclient.Options{RatePerSecond: 1000, Burst: 100, MaxAttempts: 1, Sleep: func(context.Context, time.Duration) error { return nil }}
	webOpts := opts
	webOpts.Jar = jar
	base := "http://127.0.0.1:1"
	if api != nil {
		base = api.srv.URL
	}
	c, err := New(Options{Username: "user", Token: "token", BaseURL: base, ScopesPerMinute: 1e6, StatsPerSecond: 1e6,
		HTTP: httpclient.New(opts), GraphQLURL: site.srv.URL + "/graphql",
		Email: "hunter@example.com", Password: "pw", WebHTTP: httpclient.New(webOpts), Jar: jar, WebURL: site.srv.URL,
		Login: func(context.Context) error {
			*logins++
			if refuse != nil {
				return refuse
			}
			v := "s" + string(rune('0'+*logins))
			site.mu.Lock()
			site.sessions[v] = true
			site.mu.Unlock()
			u, _ := url.Parse(site.srv.URL)
			jar.SetCookies(u, []*http.Cookie{{Name: sessionCookie, Value: v, Path: "/"}})
			return nil
		}})
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestListProgramsAddsInvitedPrograms(t *testing.T) {
	api, site := newFakeAPI(t), newFakeSite(t)
	site.invites = []string{"umbrella", "acme"} // acme is listed by the API already
	logins := 0
	c := webClient(t, api, site, &logins, nil)
	l, err := c.ListPrograms(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	var handles []string
	for _, p := range l.Programs {
		handles = append(handles, p.Handle)
	}
	if got := strings.Join(handles, ","); got != "acme,globex-private,initech-vdp,umbrella" || !l.Complete {
		t.Fatalf("listing %s (complete %v), want the API's three and the invited umbrella, complete", got, l.Complete)
	}
	sh := l.Programs[3]
	if !sh.Private || !sh.OffersBounty || sh.Currency != "USD" || sh.URL != "https://hackerone.com/umbrella" || sh.StartedAt == nil {
		t.Errorf("invited program %+v", sh)
	}
	if logins != 1 || site.authSent {
		t.Errorf("logins %d, API token sent to the website %v; want one login, never the token", logins, site.authSent)
	}

	// ListInvites reuses what the listing read.
	before := len(site.queries)
	inv, err := c.ListInvites(context.Background())
	if err != nil || len(inv) != 2 || inv[0].Handle != "umbrella" || len(site.queries) != before {
		t.Errorf("ListInvites = %v, %v after %d more queries; want both, none asked", inv, err, len(site.queries)-before)
	}
	if cr := c.Credentials(); len(cr) != 2 || cr[1].Name != "Website login" || cr[1].LastOK == nil {
		t.Errorf("credentials %+v, want the website login accepted", cr)
	}
}

func TestListingWithoutInvitationsIsPartial(t *testing.T) {
	api, site := newFakeAPI(t), newFakeSite(t)
	logins := 0
	c := webClient(t, api, site, &logins, nil)
	if _, err := c.ListPrograms(context.Background()); err != nil { // signs in
		t.Fatal(err)
	}
	site.fail = true
	l, err := c.ListPrograms(context.Background())
	if err != nil || l.Complete || len(l.Programs) != 3 {
		t.Errorf("with the invitations failing: %d programs, complete %v, err %v; want the API's three, not complete", len(l.Programs), l.Complete, err)
	}
}

func TestAcceptInvite(t *testing.T) {
	site := newFakeSite(t)
	site.invites = []string{"hooli", "massive-dynamic"}
	logins := 0
	c := webClient(t, nil, site, &logins, nil)
	if err := c.AcceptInvite(context.Background(), "hooli"); err != nil {
		t.Fatal(err)
	}
	if strings.Join(site.accepted, ",") != "hooli" || strings.Join(site.invites, ",") != "massive-dynamic" {
		t.Errorf("accepted %v, open %v", site.accepted, site.invites)
	}
	if err := c.AcceptInvite(context.Background(), "hooli"); err == nil || !strings.Contains(err.Error(), "no open invitation") {
		t.Errorf("accepting again: %v, want no open invitation", err)
	}
}

func TestLapsedSessionSignsInAgain(t *testing.T) {
	site := newFakeSite(t)
	site.invites = []string{"umbrella"}
	logins := 0
	c := webClient(t, nil, site, &logins, nil)
	if _, err := c.ListInvites(context.Background()); err != nil {
		t.Fatal(err)
	}
	site.mu.Lock()
	site.sessions = map[string]bool{} // HackerOne ended it
	site.mu.Unlock()
	c.web.verifiedAt = time.Time{}
	c.invitesAt = time.Time{}
	inv, err := c.ListInvites(context.Background())
	if err != nil || len(inv) != 1 || logins != 2 {
		t.Errorf("after the session lapsed: %v, %v, %d logins; want the invitation after a second login", inv, err, logins)
	}
}

func TestRejectedLoginSticks(t *testing.T) {
	site := newFakeSite(t)
	logins := 0
	c := webClient(t, nil, site, &logins, rejected("Invalid email or password"))
	for range 2 {
		if _, err := c.ListInvites(context.Background()); err == nil {
			t.Fatal("ListInvites worked without a session")
		}
	}
	if logins != 1 {
		t.Errorf("%d logins after a rejection, want 1: a wrong password isn't retried", logins)
	}
	if cr := c.Credentials(); cr[1].Failure == "" || !cr[1].Stuck {
		t.Errorf("website login %+v, want refused for good", cr[1])
	}
}

func TestStatsUseTheSession(t *testing.T) {
	site := newFakeSite(t)
	logins := 0
	c := webClient(t, nil, site, &logins, nil)
	got := map[string]platform.ProgramStats{}
	err := c.FetchProgramStats(context.Background(), []string{"private-wayne"}, func(_ []string, s map[string]platform.ProgramStats) error {
		for k, v := range s {
			got[k] = v
		}
		return nil
	})
	if err != nil || got["private-wayne"].ResolvedReports == nil {
		t.Errorf("private program's stats: %v, %+v; want them, from the session", err, got)
	}

	// Without a website login, the same query is anonymous: nothing for
	// a private program, and no error for it.
	plain := newFakeSite(t)
	jarless, err := New(Options{Username: "user", Token: "token", StatsPerSecond: 1e6, GraphQLURL: plain.srv.URL + "/graphql",
		HTTP: httpclient.New(httpclient.Options{RatePerSecond: 1000, Burst: 100, MaxAttempts: 1})})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := jarless.ListInvites(context.Background()); !errors.Is(err, platform.ErrNoWebLogin) {
		t.Errorf("ListInvites without a website login: %v, want ErrNoWebLogin", err)
	}
}
