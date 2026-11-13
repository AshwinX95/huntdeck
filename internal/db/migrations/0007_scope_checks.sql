-- Polls skip fetching a program's scope when the platform says it hasn't
-- changed since the last fetch. programs.scope_synced_at stays the time of
-- the last real fetch; scope_checked_at is the last time the scope was known
-- to be current, by fetching it or by the platform's word.
-- poll_runs.scopes_unchanged counts programs skipped that way.

ALTER TABLE programs ADD COLUMN scope_checked_at TEXT;
UPDATE programs SET scope_checked_at = scope_synced_at;

ALTER TABLE poll_runs ADD COLUMN scopes_unchanged INTEGER NOT NULL DEFAULT 0;
