-- Corrects data saved before two fixes (see syncer.count90d and
-- syncer.cleanName):
--
-- 1. 90-day figures worked out from saved totals (counted_days set) could
--    exceed the all-time total they came from: a week's growth scaled to 90
--    days overshoots on a young, busy program. For a program launched less
--    than 90 days before the snapshot, every report and bounty so far falls
--    in the window, so the totals are the exact figures; anywhere else a
--    figure is capped at its total.
UPDATE program_public_stats
SET reports_90d = COALESCE(total_reports, reports_90d),
    bounties_paid_90d = COALESCE(bounties_paid, bounties_paid_90d),
    counted_days = 90
WHERE counted_days IS NOT NULL
  AND program_id IN (SELECT id FROM programs WHERE started_at IS NOT NULL
                     AND started_at > strftime('%Y-%m-%dT%H:%M:%fZ', program_public_stats.captured_at, '-90 days'));

UPDATE program_public_stats SET reports_90d = total_reports
WHERE counted_days IS NOT NULL AND reports_90d > total_reports;

UPDATE program_public_stats SET bounties_paid_90d = bounties_paid
WHERE counted_days IS NOT NULL AND bounties_paid_90d > bounties_paid;

-- 2. Program names keep the spaces some platforms send around them or
--    doubled inside them (" Esteé  Lauder"), which sorts them first and
--    would show as a rename once names are cleaned on the way in.
UPDATE programs SET name = trim(replace(replace(replace(name, '    ', ' '), '  ', ' '), '  ', ' '))
WHERE name != trim(name) OR name LIKE '%  %';
