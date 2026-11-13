package custom

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mdpsec/huntdeck/internal/httpclient"
	"github.com/mdpsec/huntdeck/internal/platform"
	"github.com/mdpsec/huntdeck/internal/scope"
)

func fixture(t *testing.T) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", "source.json"))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// A document read from a file lists its programs, complete, and their
// scope with types worked out where it gives none.
func TestFileSource(t *testing.T) {
	path := filepath.Join(t.TempDir(), "source.json")
	if err := os.WriteFile(path, fixture(t), 0o600); err != nil {
		t.Fatal(err)
	}
	s, err := New(Options{Name: "inspectiv", Location: path})
	if err != nil {
		t.Fatal(err)
	}
	l, err := s.ListPrograms(context.Background())
	if err != nil || !l.Complete || len(l.Programs) != 2 {
		t.Fatalf("listing = %+v, %v", l, err)
	}
	acme, globex := l.Programs[0], l.Programs[1]
	if acme.Name != "Acme Corp" || acme.State != platform.StateOpen || acme.Currency != "USD" || *acme.BountyMax != 5000 ||
		acme.StartedAt == nil || acme.Policy != "No **DoS**." {
		t.Errorf("acme = %+v", acme)
	}
	if globex.State != platform.StatePaused || !globex.Private || globex.Currency != "" {
		t.Errorf("globex = %+v", globex)
	}
	entries, err := s.FetchScope(context.Background(), acme)
	if err != nil || len(entries) != 4 {
		t.Fatalf("scope = %+v, %v", entries, err)
	}
	want := []platform.ScopeEntry{
		{PlatformID: "s-1", InScope: true, AssetType: scope.AssetWildcard, PlatformAssetType: "wildcard", Identifier: "*.acme.example.com", BountyEligible: true, MaxSeverity: "critical"},
		{InScope: true, AssetType: scope.AssetURL, PlatformAssetType: scope.AssetURL, Identifier: "https://api.acme.example.com/v2", BountyEligible: true, Instruction: "Use the X-Test header."},
		{InScope: true, AssetType: scope.AssetOther, PlatformAssetType: "Mobile App", Identifier: "Acme Android app", BountyEligible: true},
		{InScope: false, AssetType: scope.AssetDomain, PlatformAssetType: scope.AssetDomain, Identifier: "admin.acme.example.com"},
	}
	for i := range want {
		if entries[i] != want[i] {
			t.Errorf("entry %d = %+v\nwant %+v", i, entries[i], want[i])
		}
	}
	if g, _ := s.FetchScope(context.Background(), globex); len(g) != 1 || g[0].BountyEligible || g[0].AssetType != scope.AssetCIDR {
		t.Errorf("globex scope = %+v", g)
	}
	if _, err := s.FetchScope(context.Background(), platform.Program{Handle: "nope"}); err == nil {
		t.Error("a program the document lacks has a scope")
	}
}

// Over HTTP: the document, an incomplete one, server errors and a
// missing file all come back as such.
func TestSourceOverHTTP(t *testing.T) {
	body, status := string(fixture(t)), http.StatusOK
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	defer srv.Close()
	client := httpclient.New(httpclient.Options{RatePerSecond: 100, MaxAttempts: 2, BaseBackoff: time.Millisecond})
	s, err := New(Options{Name: "inspectiv", Location: srv.URL + "/feed.json", HTTP: client})
	if err != nil {
		t.Fatal(err)
	}
	if l, err := s.ListPrograms(context.Background()); err != nil || !l.Complete || len(l.Programs) != 2 {
		t.Fatalf("listing = %+v, %v", l, err)
	}
	body = strings.Replace(body, `"version": 1,`, `"version": 1, "complete": false,`, 1)
	if l, err := s.ListPrograms(context.Background()); err != nil || l.Complete {
		t.Errorf("a document saying it's incomplete: %+v, %v", l.Complete, err)
	}
	status = http.StatusBadGateway
	if _, err := s.ListPrograms(context.Background()); err == nil {
		t.Error("a 502 lists programs")
	}
	if requests, _ := s.Stats(); requests < 3 {
		t.Errorf("requests = %d", requests)
	}
	f, _ := New(Options{Name: "x1", Location: filepath.Join(t.TempDir(), "missing.json")})
	if _, err := f.ListPrograms(context.Background()); err == nil {
		t.Error("a missing file lists programs")
	}
}

// A document with a mistake is refused whole, saying where.
func TestParseErrors(t *testing.T) {
	for _, tt := range []struct{ doc, err string }{
		{`{`, "not JSON"},
		{`{"version": 2, "programs": []}`, "version 2"},
		{`{"version": 1}`, `no "programs"`},
		{`{"version": 1, "programs": [{"handle": "a b", "name": "A", "url": "https://x.example.com/"}]}`, "program 1"},
		{`{"version": 1, "programs": [{"handle": "a", "name": "A", "url": "https://x.example.com/"}, {"handle": "a", "name": "B", "url": "https://x.example.com/"}]}`, "listed twice"},
		{`{"version": 1, "programs": [{"handle": "a", "name": "A", "url": "javascript:alert(1)"}]}`, "url"},
		{`{"version": 1, "programs": [{"handle": "a", "name": "A", "url": "https://x.example.com/", "state": "gone"}]}`, "state"},
		{`{"version": 1, "programs": [{"handle": "a", "name": "A", "url": "https://x.example.com/", "bounty_min": 9, "bounty_max": 1}]}`, "bounty_min"},
		{`{"version": 1, "programs": [{"handle": "a", "name": "A", "url": "https://x.example.com/", "scope": [{"identifier": " "}]}]}`, "scope entry 1"},
	} {
		if _, err := Parse([]byte(tt.doc)); err == nil || !strings.Contains(err.Error(), tt.err) {
			t.Errorf("Parse(%s) = %v, want %q", tt.doc, err, tt.err)
		}
	}
}
