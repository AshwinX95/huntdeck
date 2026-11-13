-- HuntScore: how worth hunting a program is right now (internal/huntscore).
-- The current score and its breakdown (store.HuntDetail as JSON) live on
-- the program for lists to sort by; NULL until the first scoring.
ALTER TABLE programs ADD COLUMN hunt_score REAL;
ALTER TABLE programs ADD COLUMN hunt_detail TEXT;

-- One row per program per UTC day (the day's latest scoring), for history
-- charts and "rising" programs.
CREATE TABLE hunt_scores (
    program_id  INTEGER NOT NULL REFERENCES programs (id) ON DELETE CASCADE,
    day         TEXT NOT NULL, -- YYYY-MM-DD
    score       REAL NOT NULL,
    fresh       REAL NOT NULL,
    competition REAL NOT NULL,
    payout      REAL NOT NULL,
    response    REAL NOT NULL,
    PRIMARY KEY (program_id, day)
) STRICT;
