-- A scored target can be new to the user rather than to its program: in
-- scope when they got access to a private program. added_at is then when
-- they did, and joined says so. Scores are rebuilt after every poll.
ALTER TABLE target_scores ADD COLUMN joined INTEGER NOT NULL DEFAULT 0;
