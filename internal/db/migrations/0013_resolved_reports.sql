-- Resolved reports a platform lists without disclosing them (HackerOne's
-- Hacktivity): no title or details, only when each was submitted and
-- resolved, which shows how fast a program resolves and whether that's
-- changing. resolved_at is the earliest resolving activity seen: the
-- resolution itself (action "resolved"), or a bounty or swag awarded after
-- it ("bounty", "swag"), in which case the report was resolved by then.
-- A report seen again later keeps its earliest date. Disclosed reports
-- live in disclosed_reports; their resolve dates aren't published.

CREATE TABLE resolved_reports (
    id            INTEGER PRIMARY KEY,
    program_id    INTEGER NOT NULL REFERENCES programs (id) ON DELETE CASCADE,
    report_id     TEXT NOT NULL,
    submitted_at  TEXT,
    resolved_at   TEXT NOT NULL,
    action        TEXT NOT NULL,
    bounty        REAL,
    reporter      TEXT NOT NULL DEFAULT '',
    first_seen_at TEXT NOT NULL,
    updated_at    TEXT NOT NULL,
    UNIQUE (program_id, report_id)
) STRICT;
CREATE INDEX resolved_reports_program ON resolved_reports (program_id, resolved_at);

-- When a program's resolution history was fetched in full.
ALTER TABLE programs ADD COLUMN resolutions_synced_at TEXT;
ALTER TABLE poll_runs ADD COLUMN resolutions_new INTEGER NOT NULL DEFAULT 0;
