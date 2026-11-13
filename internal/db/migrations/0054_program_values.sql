-- The numbers the API serves for each program (its bounty range, scope
-- counts and the Programs page's figures), as last seen, and when each last
-- changed: the API's per-field updated_at (store.TrackValues, run after
-- every poll). changed_at is NULL where that's unknown: a value unchanged
-- since tracking began that history couldn't date.
CREATE TABLE program_values (
    program_id INTEGER NOT NULL REFERENCES programs (id) ON DELETE CASCADE,
    field      TEXT NOT NULL,
    value      TEXT NOT NULL, -- JSON
    changed_at TEXT,
    PRIMARY KEY (program_id, field)
) STRICT, WITHOUT ROWID;

CREATE INDEX program_values_changed ON program_values (changed_at, program_id);
