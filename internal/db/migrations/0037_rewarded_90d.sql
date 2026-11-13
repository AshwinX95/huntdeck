-- Reports rewarded in the last 90 days, worked out by the syncer from the
-- saved count of rewarded vulnerabilities (Bugcrowd programs that keep
-- CrowdStream to themselves): a count, scaled up like counted_days says
-- until there are 90 days of saves. HuntScore uses it as report volume.
ALTER TABLE program_public_stats ADD COLUMN rewarded_90d REAL;
