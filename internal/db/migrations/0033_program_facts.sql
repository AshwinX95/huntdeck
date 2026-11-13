-- More facts from the listings and logged-in pages. NULL or '' means the
-- platform hasn't said:
--   collaboration   reports can be shared with other hackers (Intigriti,
--                   YesWeHack)
--   industry        the company's sector (Intigriti, Bugcrowd)
--   country         where the company is (YesWeHack)
--   triage_service  the platform's triage service level (Bugcrowd)
--   report_cost     credits a report costs to submit (YesWeHack)
--   req_terms       the program's own terms must be accepted first (Intigriti)
-- and, with the stats, when a report was last accepted (Intigriti's recent
-- activity).
ALTER TABLE programs ADD COLUMN collaboration INTEGER;
ALTER TABLE programs ADD COLUMN industry TEXT NOT NULL DEFAULT '';
ALTER TABLE programs ADD COLUMN country TEXT NOT NULL DEFAULT '';
ALTER TABLE programs ADD COLUMN triage_service TEXT NOT NULL DEFAULT '';
ALTER TABLE programs ADD COLUMN report_cost INTEGER;
ALTER TABLE programs ADD COLUMN req_terms INTEGER NOT NULL DEFAULT 0;
ALTER TABLE program_public_stats ADD COLUMN last_accepted_at TEXT;
