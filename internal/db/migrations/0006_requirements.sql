-- What a hacker needs before a program accepts reports (platform.Requirements),
-- kept on programs so lists can show and filter on it. req_known is 0 until a
-- listing or stats fetch has said; HackerOne's come with the program stats.

ALTER TABLE programs ADD COLUMN req_known INTEGER NOT NULL DEFAULT 0;
ALTER TABLE programs ADD COLUMN req_min_signal REAL;
ALTER TABLE programs ADD COLUMN req_id_verified INTEGER NOT NULL DEFAULT 0;
ALTER TABLE programs ADD COLUMN req_cleared INTEGER NOT NULL DEFAULT 0;
ALTER TABLE programs ADD COLUMN req_application INTEGER NOT NULL DEFAULT 0;

-- Intigriti: confidentiality level 2 ("Application") means researchers apply first.
UPDATE programs SET req_known = 1,
    req_application = COALESCE(json_extract(raw, '$.confidentialityLevel.id') = 2, 0)
WHERE platform = 'intigriti' AND json_valid(raw);

-- YesWeHack: hunter_audience "verified" means identity-verified hunters only.
UPDATE programs SET req_known = 1,
    req_id_verified = COALESCE(json_extract(raw, '$.hunter_audience') = 'verified', 0)
WHERE platform = 'yeswehack' AND json_valid(raw);

-- HackerOne: the signal requirement is already in the latest stats; ID and
-- clearance arrive with the next stats fetch, which sets req_known.
UPDATE programs SET req_min_signal = (
    SELECT s.signal_requirement FROM program_public_stats s
    WHERE s.program_id = programs.id AND s.captured_at = programs.stats_at AND s.signal_requirement > -10)
WHERE platform = 'hackerone' AND stats_at IS NOT NULL;
