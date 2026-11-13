-- The program's current bounty table as JSON (platform.RewardRow list), so a
-- new one can be compared with it: NULL until a platform has reported one
-- (an empty list means it reported none). HackerOne's comes with its stats,
-- so it starts from the latest snapshot; other platforms' stats don't carry
-- the table, so theirs stay unknown and their first one is a baseline.
ALTER TABLE programs ADD COLUMN rewards TEXT;
UPDATE programs SET rewards = (
    SELECT s.rewards FROM program_public_stats s WHERE s.program_id = programs.id AND s.captured_at = programs.stats_at)
WHERE platform = 'hackerone' AND stats_at IS NOT NULL;
