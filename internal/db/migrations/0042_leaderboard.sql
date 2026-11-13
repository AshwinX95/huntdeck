-- A program's 90-day and all-time leaderboards in sums (Intigriti's web
-- app). points_90d and points_all are the reputation points researchers
-- earned on the program in the last 90 days and all time: NULL unless both
-- boards were read whole (they list at most 100 researchers, and some
-- programs hide the points). Their ratio estimates the 90-day figures
-- (syncer.count90d) until 90 days of totals are saved. hunters_90d is how
-- many researchers earned points in the last 90 days; 100 means 100 or
-- more.
ALTER TABLE program_public_stats ADD COLUMN points_90d INTEGER;
ALTER TABLE program_public_stats ADD COLUMN points_all INTEGER;
ALTER TABLE program_public_stats ADD COLUMN hunters_90d INTEGER;
