package web

import (
	"context"
	"net/http"
	"strconv"
	"time"
)

// Settings > Polling's Demo seed: a signed-out seed in the data directory
// for a read-only copy of HuntDeck to load (HUNTDECK_READ_ONLY), exported on
// request or every 12 hours.

// SeedFile is an exported seed file.
type SeedFile struct {
	Name string
	At   time.Time
	Size int64
}

// MB is its size in megabytes, to one decimal.
func (f SeedFile) MB() string { return strconv.FormatFloat(float64(f.Size)/1e6, 'f', 1, 64) + " MB" }

// DemoSeeder exports demo seeds (cmd/huntdeck's demoSeeds).
type DemoSeeder interface {
	Export(ctx context.Context) (SeedFile, error)
	Latest() (SeedFile, bool)
	Daily(ctx context.Context) bool
	SetDaily(ctx context.Context, on bool) error
}

// demoSeedData is what the Polling tab shows of it.
type demoSeedData struct {
	Latest *SeedFile
	Daily  bool
}

func (s *server) demoSeedData(ctx context.Context) *demoSeedData {
	if s.DemoSeed == nil {
		return nil
	}
	d := &demoSeedData{Daily: s.DemoSeed.Daily(ctx)}
	if f, ok := s.DemoSeed.Latest(); ok {
		d.Latest = &f
	}
	return d
}

// exportDemoSeed exports a demo seed now (POST /settings/seed). It takes
// a minute or so; the page waits for it.
func (s *server) exportDemoSeed(w http.ResponseWriter, r *http.Request) {
	if s.DemoSeed == nil {
		http.NotFound(w, r)
		return
	}
	msg := "msg=seed-exported"
	if _, err := s.DemoSeed.Export(r.Context()); err != nil {
		s.Log.Warn("demo seed not exported", "err", err)
		msg = "err=seed-failed"
	}
	http.Redirect(w, r, "/settings?"+msg+"#demo-seed", http.StatusSeeOther)
}

// setDemoSeedDaily turns the daily export on or off (POST /settings/seed/daily).
func (s *server) setDemoSeedDaily(w http.ResponseWriter, r *http.Request) {
	if s.DemoSeed == nil {
		http.NotFound(w, r)
		return
	}
	msg := "msg=seed-daily-off"
	on := r.PostFormValue("daily") == "1"
	if on {
		msg = "msg=seed-daily-on"
	}
	if err := s.DemoSeed.SetDaily(r.Context(), on); err != nil {
		s.Log.Error("demo seed setting not saved", "err", err)
		msg = "err=seed-failed"
	}
	http.Redirect(w, r, "/settings?"+msg+"#demo-seed", http.StatusSeeOther)
}
