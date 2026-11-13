-- A program with accepted reports saved lists them: it doesn't keep
-- CrowdStream to itself (migration 0036). Programs whose reports came only
-- from the all-programs feed, their own feed giving no window (13 public
-- Bugcrowd programs without bounties), kept accepted_since NULL, so their
-- own rows showed a dash while the platform's months counted their
-- reports. They now list them from the day of the earliest saved, as
-- store.MarkAcceptedListed marks them after each read of the feed.
UPDATE programs
SET accepted_since = (SELECT MIN(a.accepted_at) FROM accepted_reports a WHERE a.program_id = programs.id)
WHERE accepted_since IS NULL
  AND EXISTS (SELECT 1 FROM accepted_reports a WHERE a.program_id = programs.id);
