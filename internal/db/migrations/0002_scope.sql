-- Programs, their scope, derived targets, poll runs and the change log.
-- Timestamps are UTC strings in store.TimeFormat, so they sort as text.

CREATE TABLE programs (
    id              INTEGER PRIMARY KEY,
    platform        TEXT NOT NULL,
    handle          TEXT NOT NULL,
    platform_id     TEXT NOT NULL DEFAULT '',
    name            TEXT NOT NULL,
    url             TEXT NOT NULL,
    private         INTEGER NOT NULL CHECK (private IN (0, 1)),
    state           TEXT NOT NULL CHECK (state IN ('open', 'paused', 'closed')),
    offers_bounty   INTEGER NOT NULL CHECK (offers_bounty IN (0, 1)),
    currency        TEXT NOT NULL DEFAULT '',
    fast_payments   INTEGER NOT NULL DEFAULT 0,
    safe_harbor     INTEGER NOT NULL DEFAULT 0,
    triage_active   INTEGER NOT NULL DEFAULT 0,
    open_scope      INTEGER NOT NULL DEFAULT 0,
    started_at      TEXT,
    joined_at       TEXT,
    raw             TEXT NOT NULL DEFAULT '{}',
    first_seen_at   TEXT NOT NULL,
    last_seen_at    TEXT NOT NULL,
    removed_at      TEXT,
    scope_hash      TEXT NOT NULL DEFAULT '',
    scope_synced_at TEXT,
    UNIQUE (platform, handle)
) STRICT;

-- Exactly what the platform lists. Never deleted; removed_at marks removal.
CREATE TABLE scope_entries (
    id                  INTEGER PRIMARY KEY,
    program_id          INTEGER NOT NULL REFERENCES programs (id) ON DELETE CASCADE,
    entry_key           TEXT NOT NULL, -- platform's scope ID, or a content hash if it has none
    in_scope            INTEGER NOT NULL CHECK (in_scope IN (0, 1)),
    asset_type          TEXT NOT NULL,
    platform_asset_type TEXT NOT NULL,
    identifier          TEXT NOT NULL,
    bounty_eligible     INTEGER NOT NULL CHECK (bounty_eligible IN (0, 1)),
    max_severity        TEXT NOT NULL DEFAULT '',
    instruction         TEXT NOT NULL DEFAULT '',
    platform_created_at TEXT,
    platform_updated_at TEXT,
    content_hash        TEXT NOT NULL,
    first_seen_at       TEXT NOT NULL,
    updated_at          TEXT NOT NULL,
    removed_at          TEXT,
    UNIQUE (program_id, entry_key)
) STRICT;

-- Tool-ready values parsed from scope_entries.identifier. Rebuilt whenever
-- the entry changes.
CREATE TABLE scope_targets (
    id             INTEGER PRIMARY KEY,
    scope_entry_id INTEGER NOT NULL REFERENCES scope_entries (id) ON DELETE CASCADE,
    kind           TEXT NOT NULL CHECK (kind IN ('wildcard', 'domain', 'url', 'ip', 'cidr')),
    value          TEXT NOT NULL,
    host           TEXT NOT NULL DEFAULT '',
    apex           TEXT NOT NULL DEFAULT ''
) STRICT;
CREATE INDEX scope_targets_entry ON scope_targets (scope_entry_id);
CREATE INDEX scope_targets_kind ON scope_targets (kind, value);

CREATE TABLE poll_runs (
    id               INTEGER PRIMARY KEY,
    platform         TEXT NOT NULL,
    mode             TEXT NOT NULL CHECK (mode IN ('full', 'quick')),
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
    error            TEXT NOT NULL DEFAULT ''
) STRICT;
CREATE INDEX poll_runs_platform ON poll_runs (platform, mode, started_at);
-- The lease: at most one running poll per platform, across processes.
CREATE UNIQUE INDEX poll_runs_one_running ON poll_runs (platform) WHERE status = 'running';

-- Append-only event log for the UI feed and notifications.
CREATE TABLE changes (
    id             INTEGER PRIMARY KEY,
    occurred_at    TEXT NOT NULL,
    poll_run_id    INTEGER REFERENCES poll_runs (id) ON DELETE SET NULL,
    program_id     INTEGER NOT NULL REFERENCES programs (id) ON DELETE CASCADE,
    scope_entry_id INTEGER REFERENCES scope_entries (id) ON DELETE SET NULL,
    kind           TEXT NOT NULL,
    field          TEXT NOT NULL DEFAULT '',
    old_value      TEXT NOT NULL DEFAULT '',
    new_value      TEXT NOT NULL DEFAULT '',
    notified_at    TEXT
) STRICT;
CREATE INDEX changes_occurred ON changes (occurred_at);
CREATE INDEX changes_program ON changes (program_id, occurred_at);

-- Snapshots of the user's own numbers, written only when they change.
CREATE TABLE program_user_stats (
    program_id    INTEGER NOT NULL REFERENCES programs (id) ON DELETE CASCADE,
    captured_at   TEXT NOT NULL,
    reports       INTEGER NOT NULL,
    valid_reports INTEGER NOT NULL,
    bounty_earned REAL NOT NULL,
    PRIMARY KEY (program_id, captured_at)
) STRICT;
