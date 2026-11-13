-- YesWeHack and Intigriti publish running totals (every report submitted,
-- and on Intigriti bounties paid and reports accepted) but no 90-day
-- figures. HuntDeck works those out from the totals it saves each day;
-- counted_days is how many days of saved totals a snapshot's reports_90d
-- and bounties_paid_90d were worked out from (NULL when the platform
-- published them; under 90 means they are estimates).
ALTER TABLE program_public_stats ADD COLUMN accepted_reports INTEGER;
ALTER TABLE program_public_stats ADD COLUMN counted_days INTEGER;
