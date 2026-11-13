package web

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/mdpsec/huntdeck/internal/platform"
	"github.com/mdpsec/huntdeck/internal/platform/independent"
	"github.com/mdpsec/huntdeck/internal/scope"
)

// Every kind of asset lists on Targets and exports, not just addresses:
// apps, hardware and the rest from their scope entries.
func TestTargetsAssetKinds(t *testing.T) {
	e := newTestEnv(t)
	if err := e.syncer.SaveOwn(context.Background(), []independent.Program{{
		Program: platform.Program{Handle: "gadgets", Name: "Gadgets", URL: "https://gadgets.example/", State: platform.StateOpen, OffersBounty: true},
		Scope: []platform.ScopeEntry{
			{InScope: true, AssetType: scope.AssetAndroid, Identifier: "com.gadgets.android", BountyEligible: true},
			{InScope: true, AssetType: scope.AssetIOS, Identifier: "id123456789", BountyEligible: true},
			{InScope: true, AssetType: scope.AssetHardware, Identifier: "Gadget Hub v2"},
			{InScope: true, AssetType: scope.AssetWildcard, Identifier: "*.gadgets.example"},
		},
	}}); err != nil {
		t.Fatal(err)
	}
	c := e.client()
	for _, tt := range []struct {
		kind      string
		want, not []string
		label     string
	}{
		{"app", []string{"com.gadgets.android", "id123456789"}, []string{"Gadget Hub v2", "*.gadgets.example"}, "Apps"},
		{"apps", []string{"com.gadgets.android", "id123456789"}, nil, "Apps"},
		{"ios", []string{"id123456789"}, []string{"com.gadgets.android"}, "iOS apps"},
		{"hardware", []string{"Gadget Hub v2"}, []string{"com.gadgets.android"}, "Hardware"},
	} {
		_, body := e.do(c, http.MethodGet, "/targets?kind="+tt.kind, nil, nil)
		for _, w := range tt.want {
			if !strings.Contains(body, `title="`+w+`">`+w+`</td>`) {
				t.Errorf("kind=%s lacks %s", tt.kind, w)
			}
		}
		for _, n := range tt.not {
			if strings.Contains(body, `>`+n+`</td>`) {
				t.Errorf("kind=%s lists %s", tt.kind, n)
			}
		}
		if !strings.Contains(body, `<th class="c-target">`+tt.label+`</th>`) {
			t.Errorf("kind=%s isn't labelled %s", tt.kind, tt.label)
		}
		_, txt := e.do(c, http.MethodGet, "/api/v1/targets?kind="+tt.kind, nil, nil)
		if got := strings.Fields(txt); len(got) == 0 || !strings.Contains(txt, tt.want[0]) {
			t.Errorf("export kind=%s = %q", tt.kind, txt)
		}
	}
	_, body := e.do(c, http.MethodGet, "/targets", nil, nil)
	if !strings.Contains(body, `<div class="ft">Other assets</div>`) || !strings.Contains(body, `href="/targets?kind=hardware"`) {
		t.Error("rail lacks the other assets")
	}
	if res, _ := e.do(c, http.MethodGet, "/api/v1/targets?kind=nonsense", nil, nil); res.StatusCode != http.StatusBadRequest {
		t.Errorf("unknown kind = %d", res.StatusCode)
	}
}
