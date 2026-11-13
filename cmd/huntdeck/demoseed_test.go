package main

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"github.com/mdpsec/huntdeck/internal/db"
	"github.com/mdpsec/huntdeck/internal/store"
)

// A demo seed is written whole to the data directory, owner-only, and the
// daily export follows its setting.
func TestDemoSeedExport(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	d, err := db.Open(ctx, filepath.Join(dir, "huntdeck.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = d.Close() })
	migs, err := db.Migrations()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := d.Migrate(ctx, migs, filepath.Join(dir, "backups"), slog.New(slog.DiscardHandler)); err != nil {
		t.Fatal(err)
	}
	seeds := &demoSeeds{st: store.New(d, nil), dir: dir, log: slog.New(slog.DiscardHandler)}
	if _, ok := seeds.Latest(); ok {
		t.Fatal("a seed before any export")
	}
	// A partial file left by an export cut short doesn't stop the next.
	if err := os.WriteFile(filepath.Join(dir, demoSeedPartial), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	for range 2 { // the second replaces the first
		if _, err := seeds.Export(ctx); err != nil {
			t.Fatal(err)
		}
	}
	f, ok := seeds.Latest()
	fi, err := os.Stat(filepath.Join(dir, demoSeedName))
	if !ok || err != nil || f.Size == 0 || fi.Mode().Perm() != 0o600 {
		t.Errorf("latest %+v %v, %v, mode %v", f, ok, err, fi.Mode().Perm())
	}
	if _, err := os.Stat(filepath.Join(dir, demoSeedPartial)); !os.IsNotExist(err) {
		t.Errorf("partial file left: %v", err)
	}
	if seeds.Daily(ctx) {
		t.Error("daily on by default")
	}
	if err := seeds.SetDaily(ctx, true); err != nil || !seeds.Daily(ctx) {
		t.Errorf("daily not turned on: %v", err)
	}
}
