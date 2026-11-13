-- More program facts the platforms already send. NULL or '' means the
-- platform hasn't said:
--   bounty_splitting        a bounty can be split with collaborators
--                           (HackerOne's listing)
--   offers_swag             the program gives swag (HackerOne's stats,
--                           YesWeHack's listing)
--   safe_harbor_level       the safe harbor the brief offers, "full" or
--                           "partial" (Bugcrowd)
--   coordinated_disclosure  the program allows coordinated disclosure
--                           (Bugcrowd)
-- and, per scope entry, the CVSS environmental requirements the program
-- sets for the asset: how much its confidentiality, integrity and
-- availability matter (HackerOne: none, low, medium, high, or not_defined).
ALTER TABLE programs ADD COLUMN bounty_splitting INTEGER;
ALTER TABLE programs ADD COLUMN offers_swag INTEGER;
ALTER TABLE programs ADD COLUMN safe_harbor_level TEXT NOT NULL DEFAULT '';
ALTER TABLE programs ADD COLUMN coordinated_disclosure INTEGER;
ALTER TABLE scope_entries ADD COLUMN cvss_confidentiality TEXT NOT NULL DEFAULT '';
ALTER TABLE scope_entries ADD COLUMN cvss_integrity TEXT NOT NULL DEFAULT '';
ALTER TABLE scope_entries ADD COLUMN cvss_availability TEXT NOT NULL DEFAULT '';

-- The listings already carried two of them in raw.
UPDATE programs SET bounty_splitting = json_extract(raw, '$.attributes.allows_bounty_splitting')
WHERE platform = 'hackerone' AND json_valid(raw)
    AND json_type(raw, '$.attributes.allows_bounty_splitting') IN ('true', 'false');
UPDATE programs SET offers_swag = json_extract(raw, '$.gift')
WHERE platform = 'yeswehack' AND json_valid(raw) AND json_type(raw, '$.gift') IN ('true', 'false');
