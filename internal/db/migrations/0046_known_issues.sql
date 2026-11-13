-- Bugcrowd's known issues per program (engagement_known_issues.json):
-- all of them and the distinct ones, saved with each day's stats.
ALTER TABLE program_public_stats ADD COLUMN known_issues INTEGER;
ALTER TABLE program_public_stats ADD COLUMN known_issues_unique INTEGER;
