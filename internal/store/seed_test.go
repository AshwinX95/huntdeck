package store

import (
	"context"
	"errors"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/mdpsec/huntdeck/internal/db"
)

// Every table and column is either copied into seeds or left out on
// purpose, so a migration adding one has to say which.
func TestSeedSpecCoversSchema(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t, nil)
	tables := map[string][]string{}
	rows, err := s.db.R.QueryContext(ctx, `SELECT m.name, p.name FROM sqlite_master m, pragma_table_info(m.name) p
		WHERE m.type = 'table' AND m.name NOT LIKE 'sqlite_%'`)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var tbl, col string
		if err := rows.Scan(&tbl, &col); err != nil {
			t.Fatal(err)
		}
		tables[tbl] = append(tables[tbl], col)
	}
	if err := rows.Close(); err != nil {
		t.Fatal(err)
	}
	spec := map[string]seedTable{}
	for _, st := range seedTables {
		spec[st.Name] = st
		if _, ok := tables[st.Name]; !ok {
			t.Errorf("seedTables lists %s, which the schema doesn't have", st.Name)
		}
	}
	for tbl, cols := range tables {
		st, copied := spec[tbl]
		_, left := seedLeftOut[tbl]
		switch {
		case copied && left:
			t.Errorf("%s is both in seedTables and seedLeftOut", tbl)
		case !copied && !left:
			t.Errorf("table %s is new: add it to seedTables (internal/store/seedspec.go) if it's public platform data, else to seedLeftOut", tbl)
		case copied:
			var named []string
			for _, c := range st.Cols {
				name, _, _ := strings.Cut(c, "=")
				named = append(named, name)
			}
			named = append(named, st.Omit...)
			for _, c := range cols {
				if n := countOf(named, c); n != 1 {
					t.Errorf("column %s.%s is listed %d times in seedTables: list it once, in Cols if it's public platform data, else in Omit", tbl, c, n)
				}
			}
			for _, c := range named {
				if !slices.Contains(cols, c) {
					t.Errorf("seedTables lists %s.%s, which the schema doesn't have", tbl, c)
				}
			}
		}
	}
	for tbl := range seedLeftOut {
		if _, ok := tables[tbl]; !ok {
			t.Errorf("seedLeftOut lists %s, which the schema doesn't have", tbl)
		}
	}
}

func countOf(list []string, s string) int {
	n := 0
	for _, v := range list {
		if v == s {
			n++
		}
	}
	return n
}

// A seed holds the public programs' platform data and nothing of the
// user's: no programs that are or were private, preferences, tags,
// notifications, polls, own stats, scores or hunters' handles.
func TestSeedExportImport(t *testing.T) {
	ctx := context.Background()
	src := newTestStore(t, nil)
	exec(t, src, `
		INSERT INTO programs (id, platform, handle, name, url, private, state, offers_bounty, raw, first_seen_at, last_seen_at,
			joined_at, ignored, hunt_score, thanks_read_at, policy)
		VALUES
			(1, 'hackerone', 'acme', 'Acme', 'https://example.com/acme', 0, 'open', 1,
				'{"id":"7","rank_for_user":12,"attributes":{"handle":"acme","bookmarked":true,"number_of_reports_for_user":3,"offers_bounties":true,"new_field_for_user":"x"}}',
				'2026-01-01T00:00:00.000Z', '2026-11-01T00:00:00.000Z', '2026-02-01T00:00:00.000Z', 1, 42.5, '2026-03-01T00:00:00.000Z', 'Be nice'),
			(2, 'bugcrowd', 'secret', 'Secret', 'https://example.com/secret', 1, 'open', 1, '{"isPrivate":true}',
				'2026-01-01T00:00:00.000Z', '2026-11-01T00:00:00.000Z', NULL, 0, NULL, NULL, ''),
			(3, 'intigriti', 'was-private', 'Was private', 'https://example.com/wp', 0, 'open', 1, '{"following":true}',
				'2026-01-01T00:00:00.000Z', '2026-11-01T00:00:00.000Z', NULL, 0, NULL, NULL, ''),
			(4, 'newplatform', 'other', 'Other', 'https://example.com/o', 0, 'open', 0, '{"mine":1}',
				'2026-01-01T00:00:00.000Z', '2026-11-01T00:00:00.000Z', NULL, 0, NULL, NULL, '');
		INSERT INTO scope_entries (id, program_id, entry_key, in_scope, asset_type, platform_asset_type, identifier, bounty_eligible,
			content_hash, first_seen_at, updated_at)
		VALUES (10, 1, 'a', 1, 'wildcard', 'WILDCARD', '*.example.com', 1, 'h', '2026-01-01T00:00:00.000Z', '2026-01-01T00:00:00.000Z'),
			(20, 2, 'b', 1, 'domain', 'URL', 'secret.example.net', 1, 'h', '2026-01-01T00:00:00.000Z', '2026-01-01T00:00:00.000Z');
		INSERT INTO scope_targets (id, scope_entry_id, kind, value, host, apex, program_id)
		VALUES (100, 10, 'wildcard', '*.example.com', 'example.com', 'example.com', 1),
			(200, 20, 'domain', 'secret.example.net', 'secret.example.net', 'example.net', 2);
		INSERT INTO poll_runs (id, platform, mode, trigger, status, started_at) VALUES (7, 'hackerone', 'full', 'schedule', 'ok', '2026-01-01T00:00:00.000Z');
		INSERT INTO changes (occurred_at, poll_run_id, program_id, scope_entry_id, kind, field, old_value, new_value, notified_at)
		VALUES ('2026-01-01T00:00:00.000Z', 7, 1, NULL, 'program_added', 'visibility', '', 'public', '2026-01-01T09:30:00.000Z'),
			('2026-01-02T00:00:00.000Z', 7, 1, 10, 'scope_added', '', '', '*.example.com', NULL),
			('2026-01-01T00:00:00.000Z', 7, 2, NULL, 'program_added', 'visibility', '', 'private', NULL),
			('2026-01-01T00:00:00.000Z', 7, 3, NULL, 'program_added', 'visibility', '', 'private', NULL),
			('2026-05-01T00:00:00.000Z', 7, 3, NULL, 'visibility_changed', 'visibility', 'private', 'public', NULL);
		INSERT INTO program_public_stats (program_id, captured_at, currency, resolved_reports) VALUES
			(1, '2026-10-01T00:00:00.000Z', 'usd', 12), (2, '2026-10-01T00:00:00.000Z', 'usd', 5);
		INSERT INTO disclosed_reports (program_id, report_id, title, url, reporter, raw, disclosed_at, first_seen_at, updated_at)
		VALUES (1, 'r1', 'XSS', 'https://example.com/r1', 'alice',
			'{"id":1,"relationships":{"reporter":{"data":{"attributes":{"username":"alice"}}},"program":{"data":{"type":"program"}}}}',
			'2026-01-01T00:00:00.000Z',
			'2026-01-01T00:00:00.000Z', '2026-01-01T00:00:00.000Z');
		INSERT INTO resolved_reports (program_id, report_id, resolved_at, action, reporter, first_seen_at, updated_at)
		VALUES (1, 'r2', '2026-01-01T00:00:00.000Z', 'resolved', 'bob', '2026-01-01T00:00:00.000Z', '2026-01-01T00:00:00.000Z');
		INSERT INTO accepted_reports (program_id, report_id, accepted_at, reporter, first_seen_at, updated_at)
		VALUES (1, 'r3', '2026-01-01T00:00:00.000Z', 'carol', '2026-01-01T00:00:00.000Z', '2026-01-01T00:00:00.000Z');
		INSERT INTO program_thanks (program_id, hunter, valid_reports) VALUES (1, 'dave', 4);
		INSERT INTO program_tags (program_id, tag) VALUES (1, 'focus');
		INSERT INTO program_user_stats (program_id, captured_at, reports, valid_reports, bounty_earned) VALUES (1, '2026-01-01T00:00:00.000Z', 3, 2, 500);
		INSERT INTO hunt_scores (program_id, day, score, fresh, competition, payout, response) VALUES (1, '2026-01-01', 40, 1, 1, 1, 1);
		INSERT INTO settings (key, value, updated_at) VALUES
			('display.timezone', 'Europe/London', '2026-01-01T00:00:00.000Z'),
			('huntscore.preset', 'payout', '2026-01-01T00:00:00.000Z'),
			('disclosures.hackerone.cursor', '2026-10-01T00:00:00.000Z', '2026-01-01T00:00:00.000Z'),
			('scope.version', '1', '2026-01-01T00:00:00.000Z');
		INSERT INTO notify_channels (id, name, kind, config, rules, enabled, instant, created_at, updated_at)
		VALUES (1, 'mine', 'webhook', '{"url":"https://hooks.example.com/secret"}', '{}', 1, 1, '2026-01-01T00:00:00.000Z', '2026-01-01T00:00:00.000Z');`)

	migs, err := db.Migrations()
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"seed.db", "seed.db.gz"} {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), name)
			n, err := src.ExportSeed(ctx, path, migs, SeedOptions{})
			if err != nil {
				t.Fatal(err)
			}
			if n["programs"] != 2 || n["scope_entries"] != 1 || n["changes"] != 2 || n["settings"] != 2 {
				t.Errorf("exported %v", n)
			}
			if _, err := src.ExportSeed(ctx, path, migs, SeedOptions{}); err == nil {
				t.Error("exporting over an existing file worked")
			}

			dst := newTestStore(t, nil)
			exec(t, dst, `INSERT INTO settings (key, value, updated_at) VALUES ('display.timezone', 'Asia/Tokyo', '2026-01-01T00:00:00.000Z')`)
			got, err := dst.ImportSeed(ctx, path, t.TempDir(), migs)
			if err != nil {
				t.Fatal(err)
			}
			if got.String() != n.String() {
				t.Errorf("imported %v, exported %v", got, n)
			}
			for _, c := range []struct{ q, want string }{
				{`SELECT group_concat(handle) FROM (SELECT handle FROM programs ORDER BY id)`, "acme,other"},
				// Only the listed fields (seedRawFields): fields about the account, and
				// ones nobody has listed yet, stay out.
				{`SELECT raw FROM programs WHERE id = 1`, `{"id":"7","attributes":{"handle":"acme","offers_bounties":true}}`},
				{`SELECT raw FROM programs WHERE id = 4`, `{}`},
				{`SELECT policy || '|' || ignored || '|' || coalesce(joined_at, '-') || coalesce(hunt_score, '-') || coalesce(thanks_read_at, '-') FROM programs WHERE id = 1`, "Be nice|0|---"},
				{`SELECT group_concat(identifier) FROM scope_entries`, "*.example.com"},
				{`SELECT group_concat(value) FROM scope_targets`, "*.example.com"},
				{`SELECT count(*) || '|' || count(poll_run_id) || '|' || count(notified_at) FROM changes`, "2|0|2"},
				{`SELECT count(*) FROM changes WHERE notified_at IS NOT occurred_at`, "0"}, // not when the user was notified
				{`SELECT group_concat(resolved_reports) FROM program_public_stats`, "12"},
				{`SELECT reporter || '|' || raw FROM disclosed_reports`, `|{"id":1,"relationships":{"program":{"data":{"type":"program"}}}}`},
				{`SELECT reporter FROM resolved_reports`, ""},
				{`SELECT reporter FROM accepted_reports`, ""},
				{`SELECT group_concat(key || '=' || value) FROM (SELECT * FROM settings ORDER BY key)`,
					"disclosures.hackerone.cursor=2026-10-01T00:00:00.000Z,display.timezone=Asia/Tokyo,scope.version=1"},
				{`SELECT (SELECT count(*) FROM program_thanks) + (SELECT count(*) FROM program_tags) + (SELECT count(*) FROM program_user_stats)
					+ (SELECT count(*) FROM hunt_scores) + (SELECT count(*) FROM poll_runs) + (SELECT count(*) FROM notify_channels)`, "0"},
			} {
				if v := query(t, dst, c.q); v != c.want {
					t.Errorf("%s\n= %q, want %q", c.q, v, c.want)
				}
			}

			if _, err := dst.ImportSeed(ctx, path, t.TempDir(), migs); !errors.Is(err, ErrNotEmpty) {
				t.Errorf("second import: %v, want ErrNotEmpty", err)
			}
		})
	}
}

// A signed-out seed, for a demo, leaves out public programs a platform
// shows only to accounts: Intigriti's Application and Registered ones (and
// any whose level can't be read), any whose scope is withheld until the
// account accepts terms, and Intigriti's announcements.
func TestSignedOutSeed(t *testing.T) {
	ctx := context.Background()
	src := newTestStore(t, nil)
	exec(t, src, `
		INSERT INTO programs (id, platform, handle, name, url, private, state, offers_bounty, raw, first_seen_at, last_seen_at, scope_unavailable_at)
		VALUES
			(1, 'intigriti', 'public', 'P', 'https://example.com/p', 0, 'open', 1, '{"confidentialityLevel":{"id":4,"value":"Public"}}', '2026-01-01T00:00:00.000Z', '2026-01-01T00:00:00.000Z', NULL),
			(2, 'intigriti', 'application', 'A', 'https://example.com/a', 0, 'open', 1, '{"confidentialityLevel":{"id":2,"value":"Application"}}', '2026-01-01T00:00:00.000Z', '2026-01-01T00:00:00.000Z', NULL),
			(3, 'intigriti', 'registered', 'R', 'https://example.com/r', 0, 'open', 1, '{"confidentialityLevel":{"id":3,"value":"Registered"}}', '2026-01-01T00:00:00.000Z', '2026-01-01T00:00:00.000Z', NULL),
			(4, 'bugcrowd', 'terms', 'T', 'https://example.com/t', 0, 'open', 0, 'not json', '2026-01-01T00:00:00.000Z', '2026-01-01T00:00:00.000Z', '2026-01-02T00:00:00.000Z'),
			(5, 'hackerone', 'open', 'O', 'https://example.com/o', 0, 'open', 1, 'not json', '2026-01-01T00:00:00.000Z', '2026-01-01T00:00:00.000Z', NULL),
			(6, 'intigriti', 'unreadable', 'U', 'https://example.com/u', 0, 'open', 1, '{}', '2026-01-01T00:00:00.000Z', '2026-01-01T00:00:00.000Z', NULL);
		INSERT INTO changes (occurred_at, program_id, kind, field, old_value, new_value)
		VALUES ('2026-01-03T00:00:00.000Z', 1, 'announcement', 'Researchers only', '', 'an update'),
			('2026-01-03T00:00:00.000Z', 5, 'announcement', 'Public', '', 'an update'),
			('2026-01-03T00:00:00.000Z', 1, 'scope_added', '', '', '*.example.com');
		INSERT INTO scope_entries (id, program_id, entry_key, in_scope, asset_type, platform_asset_type, identifier, bounty_eligible,
			content_hash, first_seen_at, updated_at)
		VALUES (1, 2, 'k', 1, 'wildcard', 'WILDCARD', '*.example.net', 1, 'h', '2026-01-01T00:00:00.000Z', '2026-01-01T00:00:00.000Z');`)
	migs, err := db.Migrations()
	if err != nil {
		t.Fatal(err)
	}
	for _, tt := range []struct {
		o        SeedOptions
		programs int64
		entries  int64
		changes  int64
	}{{SeedOptions{}, 6, 1, 3}, {SeedOptions{SignedOut: true}, 2, 0, 2}} {
		n, err := src.ExportSeed(ctx, filepath.Join(t.TempDir(), "seed.db"), migs, tt.o)
		if err != nil {
			t.Fatal(err)
		}
		// Signed out: Intigriti's public program only (not one whose level
		// can't be read), and none of its researcher-only announcements.
		if n["programs"] != tt.programs || n["scope_entries"] != tt.entries || n["changes"] != tt.changes {
			t.Errorf("%+v: %d programs, %d scope entries, %d changes; want %d, %d, %d", tt.o, n["programs"], n["scope_entries"], n["changes"],
				tt.programs, tt.entries, tt.changes)
		}
	}
}

// A seed from a newer build is refused rather than half loaded.
func TestImportSeedRefusesNewerSchema(t *testing.T) {
	ctx := context.Background()
	src := newTestStore(t, nil)
	migs, err := db.Migrations()
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "seed.db")
	if _, err := src.ExportSeed(ctx, path, migs, SeedOptions{}); err != nil {
		t.Fatal(err)
	}
	dst := newTestStore(t, nil)
	if _, err := dst.ImportSeed(ctx, path, t.TempDir(), migs[:len(migs)-1]); err == nil || !strings.Contains(err.Error(), "upgrade HuntDeck") {
		t.Errorf("importing a newer seed: %v", err)
	}
}

func exec(t *testing.T, s *Store, q string) {
	t.Helper()
	if _, err := s.db.W.ExecContext(context.Background(), q); err != nil {
		t.Fatal(err)
	}
}

func query(t *testing.T, s *Store, q string) string {
	t.Helper()
	var v *string
	if err := s.db.R.QueryRowContext(context.Background(), q).Scan(&v); err != nil {
		t.Fatal(err)
	}
	if v == nil {
		return ""
	}
	return *v
}

// Programs added by hand stay out of seeds unless shared (in_seeds), and
// shared invite-only ones stay out too.
func TestOwnProgramsInSeeds(t *testing.T) {
	ctx := context.Background()
	src := newTestStore(t, nil)
	exec(t, src, `
		INSERT INTO programs (id, platform, handle, name, url, private, state, offers_bounty, raw, first_seen_at, last_seen_at, in_seeds)
		VALUES
			(1, 'independent', 'kept', 'K', 'https://example.com/k', 0, 'open', 1, '{}', '2026-01-01T00:00:00.000Z', '2026-01-01T00:00:00.000Z', 0),
			(2, 'independent', 'shared', 'S', 'https://example.com/s', 0, 'open', 1, '{}', '2026-01-01T00:00:00.000Z', '2026-01-01T00:00:00.000Z', 1),
			(3, 'independent', 'invite', 'I', 'https://example.com/i', 1, 'open', 1, '{}', '2026-01-01T00:00:00.000Z', '2026-01-01T00:00:00.000Z', 1),
			(4, 'hackerone', 'open', 'O', 'https://example.com/o', 0, 'open', 1, '{}', '2026-01-01T00:00:00.000Z', '2026-01-01T00:00:00.000Z', 0);
		INSERT INTO scope_entries (id, program_id, entry_key, in_scope, asset_type, platform_asset_type, identifier, bounty_eligible,
			content_hash, first_seen_at, updated_at)
		VALUES (1, 1, 'k', 1, 'domain', 'domain', 'kept.example.com', 1, 'h', '2026-01-01T00:00:00.000Z', '2026-01-01T00:00:00.000Z'),
			(2, 2, 'k', 1, 'domain', 'domain', 'shared.example.com', 1, 'h', '2026-01-01T00:00:00.000Z', '2026-01-01T00:00:00.000Z');`)
	migs, err := db.Migrations()
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "seed.db")
	n, err := src.ExportSeed(ctx, path, migs, SeedOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if n["programs"] != 2 || n["scope_entries"] != 1 {
		t.Errorf("%d programs, %d scope entries; want 2 (shared, hackerone), 1", n["programs"], n["scope_entries"])
	}
	// Loading the seed keeps the shared one (imports filter the same way).
	dst := newTestStore(t, nil)
	if n, err = dst.ImportSeed(ctx, path, t.TempDir(), migs); err != nil {
		t.Fatal(err)
	}
	if n["programs"] != 2 || n["scope_entries"] != 1 {
		t.Errorf("imported %d programs, %d scope entries; want 2, 1", n["programs"], n["scope_entries"])
	}
}
