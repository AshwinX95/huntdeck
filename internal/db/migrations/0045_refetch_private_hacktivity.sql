-- HackerOne's Hacktivity holds only the first 10 items of each page past the
-- first 50 for a private program, which paging took for the end: those
-- programs' report histories stopped at 60. Fetch them again, whole, at the
-- next full poll.
UPDATE programs SET resolutions_synced_at = NULL, disclosures_synced_at = NULL
WHERE platform = 'hackerone' AND private = 1;
