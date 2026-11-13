-- More of what the platforms' program pages show: how many researchers are
-- on a program (Bugcrowd: joined it; YesWeHack: ranked on it; Intigriti:
-- contributed to it), when anyone last reported to it (Intigriti), and
-- Bugcrowd's own scope rank. programs.announcements_at is when a program's
-- announcements were first read: those already there are history, not news.
ALTER TABLE program_public_stats ADD COLUMN hunters INTEGER;
ALTER TABLE program_public_stats ADD COLUMN last_submission_at TEXT;
ALTER TABLE program_public_stats ADD COLUMN scope_rank INTEGER;
ALTER TABLE programs ADD COLUMN announcements_at TEXT;
