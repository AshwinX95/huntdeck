package yeswehack

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/mdpsec/huntdeck/internal/httpclient"
)

// TestLive checks the real API still looks the way this package expects.
// It runs only with HUNTDECK_LIVE_TESTS=1 and logs in if YWH_EMAIL/
// YWH_PASSWORD (and YWH_TOTP_SECRET) or YWH_TOKEN are set.
func TestLive(t *testing.T) {
	if os.Getenv("HUNTDECK_LIVE_TESTS") != "1" {
		t.Skip("set HUNTDECK_LIVE_TESTS=1 to run against api.yeswehack.com")
	}
	c, err := New(Options{
		Email: os.Getenv("YWH_EMAIL"), Password: os.Getenv("YWH_PASSWORD"),
		TOTPSecret: os.Getenv("YWH_TOTP_SECRET"), Token: os.Getenv("YWH_TOKEN"),
		HTTP: httpclient.New(httpclient.Options{RatePerSecond: 1}),
	})
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
	private := 0
	for _, p := range l.Programs {
		if p.Private {
			private++
		}
	}
	var life time.Duration
	if !c.expires.IsZero() {
		life = time.Until(c.expires).Round(time.Minute)
	}
	t.Logf("logged in=%v token life=%v programs=%d private=%d", c.token != "", life, len(l.Programs), private)

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
