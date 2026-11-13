package web

import (
	"strings"
	"testing"

	"github.com/mdpsec/huntdeck/internal/scope"
	"github.com/mdpsec/huntdeck/internal/store"
)

// An entry naming several targets says on the Scope tab what it covers.
func TestExpandsTo(t *testing.T) {
	entry := func(id string) store.EntryView {
		return store.EntryView{AssetType: scope.AssetWildcard, Identifier: id, Targets: scope.Targets(scope.AssetWildcard, id)}
	}
	for _, tt := range []struct{ id, want string }{
		{"*.example.com", ""},
		{"*.nymhair.{com,co.uk,uk}", "*.nymhair.com · *.nymhair.co.uk · *.nymhair.uk"},
		{"*.nymhair.co.uk,.com,.uk", "*.nymhair.co.uk · *.nymhair.com · *.nymhair.uk"},
	} {
		got := ""
		if x := expandsTo(entry(tt.id)); x != nil {
			got = x.Text
		}
		if got != tt.want {
			t.Errorf("%s covers %q, want %q", tt.id, got, tt.want)
		}
	}
	var many []string
	for i := range expansionShown + 3 {
		many = append(many, "h"+string(rune('a'+i))+".example.com")
	}
	if x := expandsTo(entry(strings.Join(many, ", "))); x == nil || !strings.HasSuffix(x.Text, " · +3 more") || strings.Count(x.Title, "\n") != len(many)-1 {
		t.Errorf("many targets: %+v", x)
	}
}
