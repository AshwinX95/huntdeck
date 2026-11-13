package store

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/mdpsec/huntdeck/internal/db"
)

// Exporting doesn't hold the live database's write lock, so a running
// server's polls can still write while a seed is made.
func TestExportSeedLeavesLiveDatabaseWritable(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	live := filepath.Join(dir, "live.db")
	migs, err := db.Migrations()
	if err != nil {
		t.Fatal(err)
	}
	if err := migrateFile(ctx, live, migs, dir); err != nil {
		t.Fatal(err)
	}
	d1, err := db.Open(ctx, live)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = d1.Close() }()
	for i := range 3000 {
		if _, err := d1.W.ExecContext(ctx, `INSERT INTO programs (platform, handle, name, url, private, state, offers_bounty, first_seen_at, last_seen_at)
			VALUES ('hackerone', ?, 'P', 'https://example.com', 0, 'open', 0, '2026-01-01T00:00:00.000Z', '2026-01-01T00:00:00.000Z')`, i); err != nil {
			t.Fatal(err)
		}
	}
	// The server: a second process's connection, writing while the export runs.
	d2, err := db.Open(ctx, live)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = d2.Close() }()

	// Stop the export inside its transaction, as copySeed opens it, and
	// write from the server meanwhile.
	conn, err := d1.W.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	seed := filepath.Join(dir, "seed.db")
	if err := newSeedFile(ctx, seed, migs); err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{`ATTACH DATABASE '` + seed + `' AS seed`, seedBegin, seedTables[0].copySQL("main", "seed", SeedOptions{})} {
		if _, err := conn.ExecContext(ctx, q); err != nil {
			t.Fatal(q, err)
		}
	}
	wctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	if _, err := d2.W.ExecContext(wctx, `UPDATE programs SET name = 'Q' WHERE id = 1`); err != nil {
		t.Errorf("the live database couldn't be written during an export: %v", err)
	}
	for _, q := range []string{`COMMIT`, `DETACH DATABASE seed`} {
		if _, err := conn.ExecContext(ctx, q); err != nil {
			t.Fatal(q, err)
		}
	}
	_ = conn.Close()
}
