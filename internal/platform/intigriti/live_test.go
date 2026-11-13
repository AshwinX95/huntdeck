package intigriti

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/mdpsec/huntdeck/internal/httpclient"
)

// TestLive checks the real API still looks the way this package expects.
// It runs only with HUNTDECK_LIVE_TESTS=1 and IT_TOKEN set, and logs counts
// of the enumeration values it sees (no program names).
func TestLive(t *testing.T) {
	if os.Getenv("HUNTDECK_LIVE_TESTS") != "1" || os.Getenv("IT_TOKEN") == "" {
		t.Skip("set HUNTDECK_LIVE_TESTS=1 and IT_TOKEN to run against api.intigriti.com")
	}
	c, err := New(Options{Token: os.Getenv("IT_TOKEN"), HTTP: httpclient.New(httpclient.Options{RatePerSecond: 2})})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	l, err := c.ListPrograms(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !l.Complete || len(l.Programs) < 20 {
		t.Fatalf("listing: complete=%v programs=%d", l.Complete, len(l.Programs))
	}
	enums := map[string]int{}
	for _, p := range l.Programs {
		var pj programJSON
		_ = json.Unmarshal(p.Raw, &pj)
		enums[fmt.Sprintf("status %d=%s", pj.Status.ID, pj.Status.Value)]++
		enums[fmt.Sprintf("confidentiality %d=%s", pj.ConfidentialityLevel.ID, pj.ConfidentialityLevel.Value)]++
	}
	t.Logf("programs=%d enums=%v", len(l.Programs), enums)

	domains := map[string]int{}
	fetched := 0
	for _, p := range l.Programs {
		if fetched == 5 {
			break
		}
		var d struct {
			Domains *struct {
				Content []domainJSON `json:"content"`
			} `json:"domains"`
		}
		if err := c.getJSON(ctx, c.base.String()+"/v1/programs/"+p.PlatformID, &d); err != nil {
			t.Logf("program detail: %v", err)
			continue
		}
		fetched++
		if d.Domains == nil {
			continue
		}
		for _, dom := range d.Domains.Content {
			domains[fmt.Sprintf("type %d=%s", dom.Type.ID, dom.Type.Value)]++
			domains[fmt.Sprintf("tier %d=%s", dom.Tier.ID, dom.Tier.Value)]++
		}
	}
	t.Logf("details=%d domain enums=%v", fetched, domains)
	if fetched == 0 {
		t.Fatal("no program details could be fetched")
	}
}
