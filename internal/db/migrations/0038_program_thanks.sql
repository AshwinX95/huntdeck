-- A program's hunters with how many of their reports it accepted, as the
-- platform's public list of them shows (YesWeHack's Thanks). Kept so a
-- daily read can stop at the first page it already knows; their sum is the
-- program's valid reports, saved with its stats as accepted_reports.
-- thanks_read_at is when the whole list was last read.
CREATE TABLE program_thanks (
    program_id    INTEGER NOT NULL REFERENCES programs (id) ON DELETE CASCADE,
    hunter        TEXT NOT NULL,
    valid_reports INTEGER NOT NULL,
    PRIMARY KEY (program_id, hunter)
) STRICT;

ALTER TABLE programs ADD COLUMN thanks_read_at TEXT;
