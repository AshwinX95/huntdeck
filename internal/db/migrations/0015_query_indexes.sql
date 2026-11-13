-- Root domains (the targets page and API, kind=root) are ordered by apex.
-- This index holds only the targets that have one, in that order, so a page
-- walks it and stops after its rows instead of sorting every target. The
-- query names it with INDEXED BY: without statistics SQLite prefers
-- scope_targets_kind. Its WHERE must match the query's terms exactly.
CREATE INDEX scope_targets_root ON scope_targets (apex)
    WHERE kind IN ('wildcard', 'domain', 'url') AND apex <> '';

-- Resolved in the last 90 days (stats page) reads only recent rows, and
-- resolved reports per submission month (stats page bars) don't read the
-- table.
CREATE INDEX resolved_reports_recent ON resolved_reports (resolved_at, program_id);
CREATE INDEX resolved_reports_submitted ON resolved_reports (program_id, submitted_at);
