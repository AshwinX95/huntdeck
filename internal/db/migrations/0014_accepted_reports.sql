-- Accepted reports a platform lists without their details (Bugcrowd's
-- CrowdStream): when each was submitted, accepted (valid) and resolved,
-- which shows how many valid reports a program gets and how fast it fixes
-- them. accepted_at is a day (00:00 UTC) when the platform gives only that.
-- A report seen again later takes its newest resolution state.

CREATE TABLE accepted_reports (
    id            INTEGER PRIMARY KEY,
    program_id    INTEGER NOT NULL REFERENCES programs (id) ON DELETE CASCADE,
    report_id     TEXT NOT NULL,
    submitted_at  TEXT,
    accepted_at   TEXT NOT NULL,
    resolved_at   TEXT,
    priority      INTEGER,
    bounty        REAL,
    reporter      TEXT NOT NULL DEFAULT '',
    first_seen_at TEXT NOT NULL,
    updated_at    TEXT NOT NULL,
    UNIQUE (program_id, report_id)
) STRICT;
CREATE INDEX accepted_reports_program ON accepted_reports (program_id, accepted_at);
CREATE INDEX accepted_reports_resolved ON accepted_reports (program_id, resolved_at);

-- accepted_synced_at: when the program's feed was last read in full (it's
-- read again weekly to catch resolutions). accepted_since: the start of
-- the first full read's window, so every report accepted since then is
-- kept; months before it are incomplete.
ALTER TABLE programs ADD COLUMN accepted_synced_at TEXT;
ALTER TABLE programs ADD COLUMN accepted_since TEXT;
ALTER TABLE poll_runs ADD COLUMN accepted_new INTEGER NOT NULL DEFAULT 0;

-- Reports submitted this calendar month so far (YesWeHack).
ALTER TABLE program_public_stats ADD COLUMN reports_month INTEGER;
