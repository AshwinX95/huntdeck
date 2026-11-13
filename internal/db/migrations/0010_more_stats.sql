-- Program stats that only some platforms publish:
--   rewarded_reports: reports that earned a bounty (Bugcrowd);
--   total_reports:    every report submitted, valid or not (YesWeHack);
--   reports_7d:       reports submitted in the last 7 days (YesWeHack);
--   validation_hours: time within which most submissions are accepted or
--                     rejected (Bugcrowd's "validation within").

ALTER TABLE program_public_stats ADD COLUMN rewarded_reports INTEGER;
ALTER TABLE program_public_stats ADD COLUMN total_reports INTEGER;
ALTER TABLE program_public_stats ADD COLUMN reports_7d INTEGER;
ALTER TABLE program_public_stats ADD COLUMN validation_hours REAL;
