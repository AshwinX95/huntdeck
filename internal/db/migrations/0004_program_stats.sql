-- Public program statistics (bounties paid, reports resolved, response
-- efficiency, bounty table), one snapshot per program per UTC day so trends
-- can be shown later. NULL means the platform doesn't show that number.
-- Amounts are in the snapshot's currency. rewards is the bounty table as JSON:
-- [{"scope": "...", "low": {"min": 0, "max": 0}, "medium": ..., ...}].

CREATE TABLE program_public_stats (
    program_id          INTEGER NOT NULL REFERENCES programs (id) ON DELETE CASCADE,
    captured_at         TEXT NOT NULL,
    currency            TEXT NOT NULL DEFAULT '',
    resolved_reports    INTEGER,
    reports_90d         INTEGER,
    bounties_paid       REAL,
    bounties_paid_90d   REAL,
    avg_bounty_low      REAL,
    avg_bounty_high     REAL,
    top_bounty_low      REAL,
    top_bounty_high     REAL,
    response_efficiency INTEGER,
    last_resolved_at    TEXT,
    hackers_thanked     INTEGER,
    rewards             TEXT NOT NULL DEFAULT '[]',
    PRIMARY KEY (program_id, captured_at)
) STRICT;

-- stats_at points at the program's latest snapshot, so lists can join it
-- directly and polls can tell which programs are due.
ALTER TABLE programs ADD COLUMN stats_at TEXT;

ALTER TABLE poll_runs ADD COLUMN stats_fetched INTEGER NOT NULL DEFAULT 0;
ALTER TABLE poll_runs ADD COLUMN stats_failed INTEGER NOT NULL DEFAULT 0;
