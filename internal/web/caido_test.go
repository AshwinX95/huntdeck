package web

import (
	"context"
	"encoding/json"
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/mdpsec/huntdeck/internal/platform"
	"github.com/mdpsec/huntdeck/internal/platform/independent"
	"github.com/mdpsec/huntdeck/internal/scope"
)

// A program's scope downloads as a Caido configuration file, and the
// program page's menu lists what it leaves out.
func TestCaidoExport(t *testing.T) {
	e := newTestEnv(t)
	if err := e.syncer.SaveOwn(context.Background(), []independent.Program{{
		Program: platform.Program{Handle: "gadgets", Name: "Gadgets", URL: "https://gadgets.example/", State: platform.StateOpen, OffersBounty: true},
		Scope: []platform.ScopeEntry{
			{InScope: true, AssetType: scope.AssetWildcard, Identifier: "*.gadgets.example"},
			{InScope: true, AssetType: scope.AssetURL, Identifier: "https://api.gadgets.example/v1/users"},
			{InScope: true, AssetType: scope.AssetDomain, Identifier: "api.gadgets.example"}, // the same host
			{InScope: true, AssetType: scope.AssetAndroid, Identifier: "com.gadgets.android"},
			{InScope: true, AssetType: scope.AssetHardware, Identifier: "Gadget Hub v2"},
			{InScope: false, AssetType: scope.AssetDomain, Identifier: "admin.gadgets.example"},
			{InScope: false, AssetType: scope.AssetURL, Identifier: "https://gadgets.example/billing"},
		},
	}}); err != nil {
		t.Fatal(err)
	}
	c := e.client()
	res, body := e.do(c, http.MethodGet, "/api/v1/programs/independent/gadgets/scope.caido.json", nil, nil)
	if res.StatusCode != http.StatusOK || res.Header.Get("Content-Disposition") != `attachment; filename="huntdeck-independent-gadgets-caido.json"` ||
		res.Header.Get("X-HuntDeck-Skipped") != "3" {
		t.Fatalf("= %d, %q, skipped %q", res.StatusCode, res.Header.Get("Content-Disposition"), res.Header.Get("X-HuntDeck-Skipped"))
	}
	var doc struct {
		Config struct {
			Project struct {
				Scopes []struct {
					Name                string
					Allowlist, Denylist []string
				}
			}
		}
	}
	if err := json.Unmarshal([]byte(body), &doc); err != nil || len(doc.Config.Project.Scopes) != 1 {
		t.Fatalf("%v: %s", err, body)
	}
	sc := doc.Config.Project.Scopes[0]
	if !slices.Equal(sc.Allowlist, []string{"*.gadgets.example", "api.gadgets.example"}) || !slices.Equal(sc.Denylist, []string{"admin.gadgets.example"}) ||
		sc.Name != "Gadgets (Independent)" {
		t.Errorf("scope = %+v", sc)
	}
	_, page := e.do(c, http.MethodGet, "/programs/independent/gadgets", nil, nil)
	for _, want := range []string{`href="/api/v1/programs/independent/gadgets/scope.caido.json"`, "Left out of Caido’s file",
		"com.gadgets.android", "Gadget Hub v2", "out of scope: https://gadgets.example/billing"} {
		if !strings.Contains(page, want) {
			t.Errorf("program page lacks %s", want)
		}
	}
	if res, _ := e.do(c, http.MethodGet, "/api/v1/programs/fake/nope/scope.caido.json", nil, nil); res.StatusCode != http.StatusNotFound {
		t.Errorf("missing program = %d", res.StatusCode)
	}
}
