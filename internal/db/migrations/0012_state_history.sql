-- When a program's past state changes (paused, resumed, closed) were read
-- from its platform's feed (Intigriti). They are stored as state_changed
-- changes dated when they happened, only for before HuntDeck saw the
-- program: HuntDeck's own polls record the rest.

ALTER TABLE programs ADD COLUMN state_history_at TEXT;
