package web

import (
	"context"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"
)

// fakeSeeds is a DemoSeeder that counts exports.
type fakeSeeds struct {
	exports int
	daily   bool
}

func (f *fakeSeeds) Export(context.Context) (SeedFile, error) {
	f.exports++
	return SeedFile{Name: "seed-demo.db.gz", At: time.Now(), Size: 1}, nil
}
func (f *fakeSeeds) Latest() (SeedFile, bool) {
	return SeedFile{Name: "seed-demo.db.gz", At: time.Now(), Size: 1}, f.exports > 0
}
func (f *fakeSeeds) Daily(context.Context) bool { return f.daily }
func (f *fakeSeeds) SetDaily(_ context.Context, on bool) error {
	f.daily = on
	return nil
}

// Settings exports a demo seed and turns the daily export on and off.
func TestDemoSeedSettings(t *testing.T) {
	seeds := &fakeSeeds{}
	e := newTestEnv(t, func(d *Deps) { d.DemoSeed = seeds })
	c := e.client()
	if _, body := e.do(c, http.MethodGet, "/settings", nil, nil); !strings.Contains(body, `id="demo-seed"`) || !strings.Contains(body, "Not exported yet") {
		t.Error("the Polling tab has no Demo seed panel")
	}
	res, _ := e.do(c, http.MethodPost, "/settings/seed", url.Values{}, nil)
	if res.StatusCode != http.StatusSeeOther || !strings.Contains(res.Header.Get("Location"), "seed-exported") || seeds.exports != 1 {
		t.Errorf("export: %d %s, %d exports", res.StatusCode, res.Header.Get("Location"), seeds.exports)
	}
	if e.do(c, http.MethodPost, "/settings/seed/daily", url.Values{"daily": {"1"}}, nil); !seeds.daily {
		t.Error("daily export not turned on")
	}
	if e.do(c, http.MethodPost, "/settings/seed/daily", url.Values{}, nil); seeds.daily {
		t.Error("daily export not turned off")
	}
}
