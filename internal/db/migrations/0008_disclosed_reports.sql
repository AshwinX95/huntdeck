-- Publicly disclosed reports per program (HackerOne Hacktivity): metadata
-- and a link to the report, never the write-up. Kept against the program so
-- patterns (severities, weakness types, bounties) can be spotted.
-- programs.n_disclosed caches the count for lists; disclosures_synced_at is
-- when the program's disclosure history was last fetched in full (NULL until
-- the first backfill). poll_runs.disclosures_new counts reports first seen.

CREATE TABLE disclosed_reports (
    id            INTEGER PRIMARY KEY,
    program_id    INTEGER NOT NULL REFERENCES programs (id) ON DELETE CASCADE,
    report_id     TEXT    NOT NULL,
    title         TEXT    NOT NULL,
    severity      TEXT    NOT NULL DEFAULT '', -- none, low, medium, high, critical; '' if unrated
    weakness      TEXT    NOT NULL DEFAULT '', -- CWE name
    cve_ids       TEXT    NOT NULL DEFAULT '[]',
    substate      TEXT    NOT NULL DEFAULT '', -- resolved, informative, ...
    bounty        REAL,                        -- total awarded, when public
    submitted_at  TEXT,
    disclosed_at  TEXT    NOT NULL,
    votes         INTEGER NOT NULL DEFAULT 0,
    url           TEXT    NOT NULL,
    reporter      TEXT    NOT NULL DEFAULT '', -- username
    summary       TEXT    NOT NULL DEFAULT '', -- the platform's short summary
    raw           TEXT    NOT NULL DEFAULT '{}',
    first_seen_at TEXT    NOT NULL,
    updated_at    TEXT    NOT NULL,
    UNIQUE (program_id, report_id)
);
CREATE INDEX disclosed_reports_program ON disclosed_reports (program_id, disclosed_at DESC);

ALTER TABLE programs ADD COLUMN n_disclosed INTEGER NOT NULL DEFAULT 0;
ALTER TABLE programs ADD COLUMN disclosures_synced_at TEXT;

ALTER TABLE poll_runs ADD COLUMN disclosures_new INTEGER NOT NULL DEFAULT 0;
