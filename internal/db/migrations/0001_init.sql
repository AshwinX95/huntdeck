-- Key/value settings editable from the UI. Platform and scope tables arrive
-- in later migrations.
CREATE TABLE settings (
    key        TEXT PRIMARY KEY,
    value      TEXT NOT NULL,
    updated_at TEXT NOT NULL
) STRICT;
