-- The user's own labels on programs. An ignored program is left out of
-- lists, target exports, HuntScore picks and notifications unless asked
-- for; tags are free-form labels to filter by. Polls never touch either.
ALTER TABLE programs ADD COLUMN ignored INTEGER NOT NULL DEFAULT 0;

CREATE TABLE program_tags (
    program_id INTEGER NOT NULL REFERENCES programs (id) ON DELETE CASCADE,
    tag        TEXT NOT NULL,
    PRIMARY KEY (program_id, tag)
) STRICT;

CREATE INDEX program_tags_tag ON program_tags (tag, program_id);

-- Target totals filter on both (see 0017_count_version.sql).
CREATE TRIGGER programs_count_ignored AFTER UPDATE OF ignored ON programs
WHEN OLD.ignored IS NOT NEW.ignored
BEGIN UPDATE count_version SET n = n + 1; END;
CREATE TRIGGER program_tags_count_insert AFTER INSERT ON program_tags
BEGIN UPDATE count_version SET n = n + 1; END;
CREATE TRIGGER program_tags_count_delete AFTER DELETE ON program_tags
BEGIN UPDATE count_version SET n = n + 1; END;
