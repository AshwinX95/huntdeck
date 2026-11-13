package db

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
)

var discard = slog.New(slog.DiscardHandler)

func openTemp(t *testing.T) (*DB, string) {
	t.Helper()
	dir := t.TempDir()
	d, err := Open(context.Background(), filepath.Join(dir, "test db.db")) // space exercises DSN escaping
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = d.Close() })
	return d, dir
}

func TestOpenPragmas(t *testing.T) {
	d, _ := openTemp(t)
	ctx := context.Background()

	var mode string
	if err := d.R.QueryRowContext(ctx, "PRAGMA journal_mode").Scan(&mode); err != nil || mode != "wal" {
		t.Errorf("journal_mode = %q, %v; want wal", mode, err)
	}
	var fk int
	if err := d.W.QueryRowContext(ctx, "PRAGMA foreign_keys").Scan(&fk); err != nil || fk != 1 {
		t.Errorf("foreign_keys = %d, %v; want 1", fk, err)
	}
	if _, err := d.R.ExecContext(ctx, "CREATE TABLE t (x INTEGER)"); err == nil {
		t.Error("reader pool accepted a write; want query_only")
	}
	if err := d.Ping(ctx); err != nil {
		t.Errorf("Ping: %v", err)
	}
}

func TestEmbeddedMigrationsLoad(t *testing.T) {
	migs, err := Migrations()
	if err != nil {
		t.Fatalf("Migrations: %v", err)
	}
	if len(migs) == 0 || migs[0].Version != 1 {
		t.Fatalf("unexpected migrations: %+v", migs)
	}
}

func TestLoadMigrationsValidation(t *testing.T) {
	tests := []struct {
		name string
		fs   fstest.MapFS
		want string
	}{
		{"gap", fstest.MapFS{"0001_a.sql": {}, "0003_c.sql": {}}, "expected version 0002"},
		{"duplicate", fstest.MapFS{"0001_a.sql": {}, "0001_b.sql": {}}, "expected version 0002"},
		{"starts at 2", fstest.MapFS{"0002_b.sql": {}}, "expected version 0001"},
		{"bad name", fstest.MapFS{"init.sql": {}}, "name must look like"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := LoadMigrations(tt.fs)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Errorf("err = %v, want containing %q", err, tt.want)
			}
		})
	}
}

func migrationsFS(t *testing.T, files map[string]string) []Migration {
	t.Helper()
	m := fstest.MapFS{}
	for name, body := range files {
		m[name] = &fstest.MapFile{Data: []byte(body)}
	}
	migs, err := LoadMigrations(m)
	if err != nil {
		t.Fatalf("LoadMigrations: %v", err)
	}
	return migs
}

func TestMigrateFreshAndIdempotent(t *testing.T) {
	d, dir := openTemp(t)
	ctx := context.Background()
	backups := filepath.Join(dir, "backups")
	migs := migrationsFS(t, map[string]string{
		"0001_a.sql": "CREATE TABLE a (id INTEGER PRIMARY KEY); CREATE TABLE b (id INTEGER PRIMARY KEY);",
		"0002_c.sql": "INSERT INTO a (id) VALUES (1);",
	})

	n, err := d.Migrate(ctx, migs, backups, discard)
	if err != nil || n != 2 {
		t.Fatalf("first Migrate = %d, %v; want 2, nil", n, err)
	}
	var count int
	if err := d.R.QueryRowContext(ctx, "SELECT COUNT(*) FROM a").Scan(&count); err != nil || count != 1 {
		t.Errorf("multi-statement migration not fully applied: %d, %v", count, err)
	}
	if _, err := os.Stat(backups); !os.IsNotExist(err) {
		t.Errorf("fresh database should not be backed up (stat err: %v)", err)
	}

	n, err = d.Migrate(ctx, migs, backups, discard)
	if err != nil || n != 0 {
		t.Errorf("second Migrate = %d, %v; want 0, nil", n, err)
	}
}

func TestMigrateBacksUpExistingSchema(t *testing.T) {
	d, dir := openTemp(t)
	ctx := context.Background()
	backups := filepath.Join(dir, "backups")
	v1 := map[string]string{"0001_a.sql": "CREATE TABLE a (id INTEGER PRIMARY KEY);"}
	if _, err := d.Migrate(ctx, migrationsFS(t, v1), backups, discard); err != nil {
		t.Fatal(err)
	}

	v2 := map[string]string{"0001_a.sql": v1["0001_a.sql"], "0002_b.sql": "CREATE TABLE b (id INTEGER PRIMARY KEY);"}
	if n, err := d.Migrate(ctx, migrationsFS(t, v2), backups, discard); err != nil || n != 1 {
		t.Fatalf("Migrate = %d, %v; want 1, nil", n, err)
	}

	files, _ := filepath.Glob(filepath.Join(backups, "huntdeck-*-schema-v0001.db"))
	if len(files) != 1 {
		t.Fatalf("expected one v1 backup, got %v", files)
	}
	bk, err := Open(ctx, files[0])
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = bk.Close() })
	var tables int
	if err := bk.R.QueryRowContext(ctx, "SELECT COUNT(*) FROM sqlite_master WHERE name IN ('a','b')").Scan(&tables); err != nil || tables != 1 {
		t.Errorf("backup should hold the pre-migration schema (table a only): %d, %v", tables, err)
	}
}

func TestMigrateFailureRollsBack(t *testing.T) {
	d, dir := openTemp(t)
	ctx := context.Background()
	migs := migrationsFS(t, map[string]string{
		"0001_a.sql":   "CREATE TABLE a (id INTEGER PRIMARY KEY);",
		"0002_bad.sql": "CREATE TABLE b (id INTEGER PRIMARY KEY); NOT VALID SQL;",
	})
	n, err := d.Migrate(ctx, migs, filepath.Join(dir, "backups"), discard)
	if err == nil || !strings.Contains(err.Error(), "0002_bad") || n != 1 {
		t.Fatalf("Migrate = %d, %v; want 1 applied and an error naming 0002_bad", n, err)
	}
	var tables int
	if err := d.R.QueryRowContext(ctx, "SELECT COUNT(*) FROM sqlite_master WHERE name = 'b'").Scan(&tables); err != nil || tables != 0 {
		t.Errorf("failed migration left table b behind: %d, %v", tables, err)
	}
	var version int
	if err := d.R.QueryRowContext(ctx, "SELECT MAX(version) FROM schema_migrations").Scan(&version); err != nil || version != 1 {
		t.Errorf("schema version = %d, %v; want 1", version, err)
	}
}

func TestMigrateRefusesNewerSchema(t *testing.T) {
	d, dir := openTemp(t)
	ctx := context.Background()
	backups := filepath.Join(dir, "backups")
	two := migrationsFS(t, map[string]string{"0001_a.sql": "SELECT 1;", "0002_b.sql": "SELECT 1;"})
	if _, err := d.Migrate(ctx, two, backups, discard); err != nil {
		t.Fatal(err)
	}
	_, err := d.Migrate(ctx, two[:1], backups, discard)
	if err == nil || !strings.Contains(err.Error(), "upgrade HuntDeck") {
		t.Errorf("err = %v; want refusal to run against a newer schema", err)
	}
}

// The database holds private programs' data: Open leaves it its owner's
// alone, also one an older version left readable to others.
func TestOpenMakesTheDatabasePrivate(t *testing.T) {
	path := filepath.Join(t.TempDir(), "huntdeck.db")
	for _, f := range []string{path, path + "-wal"} {
		if err := os.WriteFile(f, nil, 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(f, 0o644); err != nil { // whatever the test's umask
			t.Fatal(err)
		}
	}
	d, err := Open(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = d.Close() }()
	for _, f := range []string{path, path + "-wal", path + "-shm"} {
		if fi, err := os.Stat(f); err != nil || fi.Mode().Perm() != 0o600 {
			t.Errorf("%s: %v, %v; want 0600", filepath.Base(f), fi.Mode().Perm(), err)
		}
	}
}
