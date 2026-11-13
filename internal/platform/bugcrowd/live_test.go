package bugcrowd

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/mdpsec/huntdeck/internal/httpclient"
)

// TestLive checks the real site still looks the way this package expects.
// It runs only with HUNTDECK_LIVE_TESTS=1, logs in if BC_EMAIL/BC_PASSWORD/
// BC_TOTP_SECRET or BC_SESSION are set, and makes about 30 requests at 1/s.
func TestLive(t *testing.T) {
	if os.Getenv("HUNTDECK_LIVE_TESTS") != "1" {
		t.Skip("set HUNTDECK_LIVE_TESTS=1 to run against bugcrowd.com")
	}
	c, err := New(Options{
		Session: os.Getenv("BC_SESSION"), Email: os.Getenv("BC_EMAIL"),
		Password: os.Getenv("BC_PASSWORD"), TOTPSecret: os.Getenv("BC_TOTP_SECRET"),
		HTTP: httpclient.Options{RatePerSecond: 1},
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	l, err := c.ListPrograms(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !l.Complete || len(l.Programs) < 100 {
		t.Fatalf("listing: complete=%v programs=%d", l.Complete, len(l.Programs))
	}
	private := 0
	for _, p := range l.Programs {
		if p.Private {
			private++
		}
	}
	t.Logf("logged in=%v programs=%d private=%d", c.authenticated(), len(l.Programs), private)

	for _, p := range l.Programs[:2] {
		es, err := c.FetchScope(ctx, p)
		if err != nil {
			t.Fatalf("scope of %s: %v", p.Handle, err)
		}
		if len(es) == 0 {
			t.Errorf("scope of %s is empty", p.Handle)
		}
	}
}
