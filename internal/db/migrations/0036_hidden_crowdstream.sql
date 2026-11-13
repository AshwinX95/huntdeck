-- A Bugcrowd program whose CrowdStream has never listed a report keeps it
-- to itself (Canva shows "no accepted or disclosed submissions" while
-- publishing 315 rewarded vulnerabilities); it isn't a program without
-- reports. accepted_since now means the feed has listed at least one, so
-- such programs' accepted and resolved counts read as not shared instead of
-- 0, and HuntScore no longer takes their empty feed for no competition.
UPDATE programs SET accepted_since = NULL
WHERE accepted_since IS NOT NULL
  AND NOT EXISTS (SELECT 1 FROM accepted_reports a WHERE a.program_id = programs.id);
