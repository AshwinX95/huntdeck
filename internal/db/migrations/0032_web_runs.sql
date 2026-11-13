-- Browser syncs (logged-in website reads) are runs of their own, mode
-- 'web'. SQLite can't change a CHECK in place, so poll_runs is rebuilt.
-- Dropping the old table sets changes.poll_run_id to NULL (ON DELETE SET
-- NULL), so the links are kept aside and put back.
CREATE TEMP TABLE run_links AS SELECT id, poll_run_id FROM changes WHERE poll_run_id IS NOT NULL;

CREATE TABLE poll_runs_new (
    id               INTEGER PRIMARY KEY,
    platform         TEXT NOT NULL,
    mode             TEXT NOT NULL CHECK (mode IN ('full', 'quick', 'web')),
    trigger          TEXT NOT NULL,
    status           TEXT NOT NULL CHECK (status IN ('running', 'ok', 'partial', 'failed')),
    listing_complete INTEGER NOT NULL DEFAULT 0,
    started_at       TEXT NOT NULL,
    finished_at      TEXT,
    programs_seen    INTEGER NOT NULL DEFAULT 0,
    scopes_fetched   INTEGER NOT NULL DEFAULT 0,
    scopes_failed    INTEGER NOT NULL DEFAULT 0,
    changes          INTEGER NOT NULL DEFAULT 0,
    requests         INTEGER NOT NULL DEFAULT 0,
    rate_limited     INTEGER NOT NULL DEFAULT 0,
    error            TEXT NOT NULL DEFAULT '',
    stats_fetched    INTEGER NOT NULL DEFAULT 0,
    stats_failed     INTEGER NOT NULL DEFAULT 0,
    scopes_unchanged INTEGER NOT NULL DEFAULT 0,
    disclosures_new  INTEGER NOT NULL DEFAULT 0,
    resolutions_new  INTEGER NOT NULL DEFAULT 0,
    accepted_new     INTEGER NOT NULL DEFAULT 0,
    listing_recent   INTEGER NOT NULL DEFAULT 0,
    notified_at      TEXT
) STRICT;
INSERT INTO poll_runs_new (id, platform, mode, trigger, status, listing_complete, started_at, finished_at,
    programs_seen, scopes_fetched, scopes_failed, changes, requests, rate_limited, error, stats_fetched,
    stats_failed, scopes_unchanged, disclosures_new, resolutions_new, accepted_new, listing_recent, notified_at)
SELECT id, platform, mode, trigger, status, listing_complete, started_at, finished_at,
    programs_seen, scopes_fetched, scopes_failed, changes, requests, rate_limited, error, stats_fetched,
    stats_failed, scopes_unchanged, disclosures_new, resolutions_new, accepted_new, listing_recent, notified_at
FROM poll_runs;
DROP TABLE poll_runs;
ALTER TABLE poll_runs_new RENAME TO poll_runs;

CREATE INDEX poll_runs_platform ON poll_runs (platform, mode, started_at);
-- The lease: at most one running poll per platform, across processes.
CREATE UNIQUE INDEX poll_runs_one_running ON poll_runs (platform) WHERE status = 'running';
CREATE INDEX poll_runs_unnotified ON poll_runs (id) WHERE notified_at IS NULL;

UPDATE changes SET poll_run_id = run_links.poll_run_id FROM run_links WHERE changes.id = run_links.id;
DROP TABLE temp.run_links;
