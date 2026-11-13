-- The targets page caches its totals until the rows they count change.
-- Every poll touches programs and scope entries (sync times, unchanged
-- values), so these triggers bump the version only when a column the counts
-- filter on actually changes, or a row is added or deleted. A poll that
-- changes nothing, which is most of them, keeps the cache.
-- A new cached count that reads other columns must add them here.
CREATE TABLE count_version (
    id INTEGER PRIMARY KEY CHECK (id = 1),
    n  INTEGER NOT NULL
) STRICT;
INSERT INTO count_version (id, n) VALUES (1, 0);

CREATE TRIGGER programs_count_insert AFTER INSERT ON programs
BEGIN UPDATE count_version SET n = n + 1; END;
CREATE TRIGGER programs_count_delete AFTER DELETE ON programs
BEGIN UPDATE count_version SET n = n + 1; END;
CREATE TRIGGER programs_count_update AFTER UPDATE OF platform, private, state, offers_bounty, removed_at ON programs
WHEN OLD.platform IS NOT NEW.platform OR OLD.private IS NOT NEW.private OR OLD.state IS NOT NEW.state
    OR OLD.offers_bounty IS NOT NEW.offers_bounty OR OLD.removed_at IS NOT NEW.removed_at
BEGIN UPDATE count_version SET n = n + 1; END;

CREATE TRIGGER scope_entries_count_insert AFTER INSERT ON scope_entries
BEGIN UPDATE count_version SET n = n + 1; END;
CREATE TRIGGER scope_entries_count_delete AFTER DELETE ON scope_entries
BEGIN UPDATE count_version SET n = n + 1; END;
CREATE TRIGGER scope_entries_count_update
AFTER UPDATE OF program_id, in_scope, bounty_eligible, removed_at, added_at, added_bound ON scope_entries
WHEN OLD.program_id IS NOT NEW.program_id OR OLD.in_scope IS NOT NEW.in_scope
    OR OLD.bounty_eligible IS NOT NEW.bounty_eligible OR OLD.removed_at IS NOT NEW.removed_at
    OR OLD.added_at IS NOT NEW.added_at OR OLD.added_bound IS NOT NEW.added_bound
BEGIN UPDATE count_version SET n = n + 1; END;

CREATE TRIGGER scope_targets_count_insert AFTER INSERT ON scope_targets
BEGIN UPDATE count_version SET n = n + 1; END;
CREATE TRIGGER scope_targets_count_delete AFTER DELETE ON scope_targets
BEGIN UPDATE count_version SET n = n + 1; END;
CREATE TRIGGER scope_targets_count_update AFTER UPDATE ON scope_targets
BEGIN UPDATE count_version SET n = n + 1; END;
