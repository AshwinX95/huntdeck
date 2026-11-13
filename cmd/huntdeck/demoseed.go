package main

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/mdpsec/huntdeck/internal/db"
	"github.com/mdpsec/huntdeck/internal/store"
	"github.com/mdpsec/huntdeck/internal/web"
)

// A demo seed is a signed-out seed (store.SeedOptions.SignedOut) written
// to the data directory, for a read-only copy of HuntDeck to load
// (HUNTDECK_READ_ONLY): exported from Settings, or every 12 hours when that's turned on.
const (
	demoSeedName    = "seed-demo.db.gz"
	demoSeedPartial = "seed-demo.new.db.gz"
	// DemoSeedDaily is the settings key turning the daily export on ("1").
	demoSeedDaily = "seed.demo.daily"
	demoSeedEvery = 12 * time.Hour // a demo's data is at most this old, plus its refresh
)

// demoSeeds exports demo seeds; one at a time.
type demoSeeds struct {
	st  *store.Store
	dir string
	log *slog.Logger
	mu  sync.Mutex
}

// Export writes a new demo seed, replacing the last one only once it's
// whole.
func (d *demoSeeds) Export(ctx context.Context) (web.SeedFile, error) {
	if !d.mu.TryLock() {
		return web.SeedFile{}, errors.New("a demo seed is being exported already")
	}
	defer d.mu.Unlock()
	migs, err := db.Migrations()
	if err != nil {
		return web.SeedFile{}, err
	}
	tmp := filepath.Join(d.dir, demoSeedPartial)
	_ = os.Remove(tmp) // left by an export that was cut short
	n, err := d.st.ExportSeed(ctx, tmp, migs, store.SeedOptions{SignedOut: true})
	if err != nil {
		return web.SeedFile{}, err
	}
	if err := os.Rename(tmp, filepath.Join(d.dir, demoSeedName)); err != nil {
		_ = os.Remove(tmp)
		return web.SeedFile{}, err
	}
	f, _ := d.Latest()
	d.log.Info("demo seed exported", "path", filepath.Join(d.dir, demoSeedName), "programs", n["programs"], "bytes", f.Size)
	return f, nil
}

// Latest is the last demo seed exported, if there is one.
func (d *demoSeeds) Latest() (web.SeedFile, bool) {
	fi, err := os.Stat(filepath.Join(d.dir, demoSeedName))
	if err != nil {
		return web.SeedFile{}, false
	}
	return web.SeedFile{Name: demoSeedName, At: fi.ModTime().UTC(), Size: fi.Size()}, true
}

// Daily reports whether the regular export (every 12 hours) is on.
func (d *demoSeeds) Daily(ctx context.Context) bool {
	set, err := d.st.Settings(ctx, demoSeedDaily)
	return err == nil && set[demoSeedDaily] == "1"
}

// SetDaily turns the daily export on or off.
func (d *demoSeeds) SetDaily(ctx context.Context, on bool) error {
	return d.st.Write(ctx, func(tx *store.Tx) error {
		if on {
			return tx.PutSetting(ctx, demoSeedDaily, "1")
		}
		return tx.DeleteSetting(ctx, demoSeedDaily)
	})
}

// Run exports a demo seed whenever the regular export is on and the last one
// is a day old, checking hourly, until ctx ends.
func (d *demoSeeds) Run(ctx context.Context) {
	t := time.NewTicker(time.Hour)
	defer t.Stop()
	for {
		if d.Daily(ctx) {
			if f, ok := d.Latest(); !ok || time.Since(f.At) >= demoSeedEvery-time.Hour {
				if _, err := d.Export(ctx); err != nil && ctx.Err() == nil {
					d.log.Warn("demo seed not exported", "err", err)
				}
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}
