-- Asset scores: how worth hunting each recently added in-scope target is
-- (internal/huntscore), for the Targets page's "best new targets". One row
-- per program and target; the whole table is rewritten with every scoring,
-- so it only ever holds targets of open, huntable programs added within the
-- scoring window.
CREATE TABLE target_scores (
    program_id INTEGER NOT NULL REFERENCES programs (id) ON DELETE CASCADE,
    kind       TEXT NOT NULL,
    value      TEXT NOT NULL,
    apex       TEXT NOT NULL,     -- root domain, '' if none
    bounty     INTEGER NOT NULL,  -- bounty-eligible on a program that pays
    added_at   TEXT NOT NULL,
    score      REAL NOT NULL,
    reasons    TEXT NOT NULL,     -- JSON array of strings
    PRIMARY KEY (program_id, kind, value)
) STRICT;

CREATE INDEX target_scores_best ON target_scores (kind, score DESC);
