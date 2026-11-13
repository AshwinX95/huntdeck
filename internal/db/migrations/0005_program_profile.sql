-- Program text and more stats from program pages.
-- programs.policy: the program's rules and guidelines (Markdown), from the
-- platform listing. programs.profile: the latest descriptive text from the
-- stats fetch (introduction, campaign notes, exclusions) as JSON; it isn't
-- kept per day like the numbers.
-- program_public_stats gains average response times in hours, the signal
-- requirement and per-severity average bounty and resolved report counts
-- (JSON: {"low": {"avg_bounty": 200, "reports": 6}, ...}).

ALTER TABLE programs ADD COLUMN policy TEXT NOT NULL DEFAULT '';
ALTER TABLE programs ADD COLUMN profile TEXT NOT NULL DEFAULT '{}';

ALTER TABLE program_public_stats ADD COLUMN first_response_hours REAL;
ALTER TABLE program_public_stats ADD COLUMN triage_hours REAL;
ALTER TABLE program_public_stats ADD COLUMN bounty_hours REAL;
ALTER TABLE program_public_stats ADD COLUMN resolution_hours REAL;
ALTER TABLE program_public_stats ADD COLUMN signal_requirement REAL;
ALTER TABLE program_public_stats ADD COLUMN by_severity TEXT NOT NULL DEFAULT '{}';

-- HackerOne listings already carried the policy in raw.
UPDATE programs SET policy = COALESCE(TRIM(json_extract(raw, '$.attributes.policy')), '')
WHERE platform = 'hackerone' AND json_valid(raw);
