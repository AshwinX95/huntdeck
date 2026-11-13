-- When each scope entry was added to its program, as far as anyone knows:
-- the platform's date where it gives one (HackerOne), else the date of the
-- scope version it first appeared in (Bugcrowd, Intigriti history), else when
-- HuntDeck saw it appear after the program's first scope sync. NULL means it
-- was already in scope when tracking began and nothing dates it.
-- added_bound = 1 means added_at is only an upper bound: the entry was in
-- the oldest version the platform's history goes back to.
-- Entries a program removed before HuntDeck saw them (HackerOne's archived
-- scope, Bugcrowd and Intigriti history) are stored as removed entries.
-- programs.scope_history_at is when a program's scope history was read.

ALTER TABLE scope_entries ADD COLUMN added_at TEXT;
ALTER TABLE scope_entries ADD COLUMN added_bound INTEGER NOT NULL DEFAULT 0;
ALTER TABLE programs ADD COLUMN scope_history_at TEXT;

UPDATE scope_entries SET added_at = platform_created_at WHERE platform_created_at IS NOT NULL;

-- Elsewhere, the latest time HuntDeck recorded the entry being added.
UPDATE scope_entries SET added_at = (
    SELECT MAX(c.occurred_at) FROM changes c
    WHERE c.scope_entry_id = scope_entries.id AND c.kind = 'scope_added')
WHERE added_at IS NULL;

CREATE INDEX scope_entries_added ON scope_entries (added_at);
