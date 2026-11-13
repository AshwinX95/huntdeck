-- A program's leaderboard for one past calendar month (Intigriti), in
-- sums: the points researchers earned on it that month (null where the
-- program hides them), how many earned any, and whether the board was cut
-- short at its 100 entries. Never who. Read once per finished month, about
-- twelve back, to estimate accepted reports per month from before
-- HuntDeck saved the program's totals.
CREATE TABLE leaderboard_months (
    program_id INTEGER NOT NULL REFERENCES programs (id) ON DELETE CASCADE,
    month      TEXT    NOT NULL, -- YYYY-MM
    points     INTEGER,
    hunters    INTEGER NOT NULL,
    capped     INTEGER NOT NULL DEFAULT 0,
    fetched_at TEXT    NOT NULL,
    PRIMARY KEY (program_id, month)
) WITHOUT ROWID;
