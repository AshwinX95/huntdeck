package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"

	"github.com/mdpsec/huntdeck/internal/db"
	"github.com/mdpsec/huntdeck/internal/platform"
	"github.com/mdpsec/huntdeck/internal/store"
)

func testStore(t *testing.T, dir string) *store.Store {
	t.Helper()
	ctx := context.Background()
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
	return store.New(d, nil)
}

func addProgram(t *testing.T, st *store.Store, handle string) {
	t.Helper()
	err := st.Write(context.Background(), func(tx *store.Tx) error {
		_, err := tx.UpsertProgram(context.Background(), "fake", platform.Program{Handle: handle, Name: handle,
			URL: "https://example.com/" + handle, State: platform.StateOpen})
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
}

// A new install downloads the starter seed once, into its empty database;
// one with programs, or with the seed turned off, asks for nothing; one
// that can't be downloaded or loaded starts empty rather than failing.
func TestAutoSeedDownload(t *testing.T) {
	ctx := context.Background()
	src := testStore(t, t.TempDir())
	addProgram(t, src, "acme")
	seedPath := filepath.Join(t.TempDir(), "seed.db.gz")
	migs, _ := db.Migrations()
	if _, err := src.ExportSeed(ctx, seedPath, migs, store.SeedOptions{}); err != nil {
		t.Fatal(err)
	}
	seed, err := os.ReadFile(seedPath)
	if err != nil {
		t.Fatal(err)
	}
	h := sha256.Sum256(seed)
	good := hex.EncodeToString(h[:])

	tests := []struct {
		name     string
		off      bool
		existing bool   // the database holds a program already
		body     []byte // what the seed URL serves
		digest   string
		want     []string // programs after
		wantAsks int32
	}{
		{"new install", false, false, seed, good, []string{"acme"}, 2},
		{"seed turned off", true, false, seed, good, nil, 0},
		{"database has programs", false, true, seed, good, []string{"mine"}, 0},
		{"checksum doesn't match", false, false, seed, hex.EncodeToString(make([]byte, 32)), nil, 3},
		{"not a seed", false, false, []byte("garbage"), func() string { s := sha256.Sum256([]byte("garbage")); return hex.EncodeToString(s[:]) }(), nil, 2},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var asks atomic.Int32
			srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				asks.Add(1)
				switch r.URL.Path {
				case "/seed/seed.db.gz":
					_, _ = w.Write(tt.body)
				case "/seed/seed.db.gz.sha256":
					_, _ = w.Write([]byte(tt.digest + "  seed.db.gz\n"))
				default:
					http.NotFound(w, r)
				}
			}))
			defer srv.Close()
			dir := t.TempDir()
			st := testStore(t, dir)
			if tt.existing {
				addProgram(t, st, "mine")
			}
			url := srv.URL + "/seed/seed.db.gz"
			if tt.off {
				url = ""
			}
			if err := autoSeed(ctx, st, dir, url, srv.Client(), slog.New(slog.DiscardHandler)); err != nil {
				t.Fatalf("autoSeed: %v (a starter seed must never stop HuntDeck starting)", err)
			}
			items, _, err := st.Programs(ctx, store.ProgramFilter{State: "all"})
			if err != nil {
				t.Fatal(err)
			}
			var got []string
			for _, p := range items {
				got = append(got, p.Handle)
			}
			if len(got) != len(tt.want) || (len(got) > 0 && got[0] != tt.want[0]) {
				t.Errorf("programs %v, want %v", got, tt.want)
			}
			if n := asks.Load(); n != tt.wantAsks {
				t.Errorf("%d requests, want %d", n, tt.wantAsks)
			}
			// Loaded once, then never again: its file is renamed, nothing else is left.
			left, _ := filepath.Glob(filepath.Join(dir, "seed*"))
			wantLeft := 0
			if len(tt.want) > 0 && !tt.existing {
				wantLeft = 1
				if _, err := os.Stat(filepath.Join(dir, "seed.db.gz.loaded")); err != nil {
					t.Errorf("seed not marked loaded: %v", err)
				}
			}
			if len(left) != wantLeft {
				t.Errorf("files left: %v", left)
			}
			// A second start asks for nothing.
			before := asks.Load()
			if err := autoSeed(ctx, st, dir, url, srv.Client(), slog.New(slog.DiscardHandler)); err != nil {
				t.Fatal(err)
			}
			if len(tt.want) > 0 && asks.Load() != before {
				t.Errorf("asked again on a second start")
			}
		})
	}
}
