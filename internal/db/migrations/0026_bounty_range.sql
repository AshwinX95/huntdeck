-- The bounty range a platform's listing gives a program, in its currency;
-- NULL where it doesn't say. Polls keep it current; this fills it from the
-- listings already stored, so it shows before the next poll.
ALTER TABLE programs ADD COLUMN bounty_min REAL;
ALTER TABLE programs ADD COLUMN bounty_max REAL;

UPDATE programs SET
    bounty_max = NULLIF(json_extract(raw, '$.maxBounty.value'), 0),
    bounty_min = NULLIF(json_extract(raw, '$.minBounty.value'), 0)
WHERE platform = 'intigriti' AND offers_bounty = 1 AND json_valid(raw);

UPDATE programs SET
    bounty_max = NULLIF(json_extract(raw, '$.bounty_reward_max'), 0),
    bounty_min = CASE WHEN json_extract(raw, '$.bounty_reward_max') > 0 THEN NULLIF(json_extract(raw, '$.bounty_reward_min'), 0) END
WHERE platform = 'yeswehack' AND offers_bounty = 1 AND json_valid(raw);

-- Bugcrowd gives "$15,000", "€500", or "Points" (which reads as 0, so NULL).
UPDATE programs SET
    bounty_max = NULLIF(CAST(REPLACE(REPLACE(REPLACE(REPLACE(
        json_extract(raw, '$.rewardSummary.maxReward'), '$', ''), '€', ''), '£', ''), ',', '') AS REAL), 0),
    bounty_min = NULLIF(CAST(REPLACE(REPLACE(REPLACE(REPLACE(
        json_extract(raw, '$.rewardSummary.minReward'), '$', ''), '€', ''), '£', ''), ',', '') AS REAL), 0)
WHERE platform = 'bugcrowd' AND offers_bounty = 1 AND json_valid(raw);
