-- Bugcrowd files plenty of plain wildcards, hosts, URLs, IPs and ranges under
-- "other" (and moves entries there). Polls now give such an entry its real
-- type (bugcrowd.labelOther); this does the same for entries already stored,
-- so the next poll doesn't record each one as an asset type change.
--
-- The rule, as in labelOther: the entry's targets (parsed from it as "other")
-- all share one host, and one of them is the whole identifier, ignoring case,
-- surrounding blanks, an http(s):// prefix and a trailing "/" or "/*". That
-- target's kind is the type; an exact match beats a prefix-less one.
-- Removed entries whose targets are gone keep "other": one that comes back
-- is relabelled by that poll and recorded only as re-added.
WITH cand AS (
    SELECT e.id, lower(trim(e.identifier, ' ' || char(9) || char(10) || char(13))) AS s
    FROM scope_entries e JOIN programs p ON p.id = e.program_id
    WHERE p.platform = 'bugcrowd' AND e.asset_type = 'other'
), noscheme AS (
    SELECT id, s, CASE WHEN s LIKE 'https://%' THEN substr(s, 9) WHEN s LIKE 'http://%' THEN substr(s, 8) ELSE s END AS b
    FROM cand
), nostar AS (
    SELECT id, s, CASE WHEN b LIKE '%/*' THEN substr(b, 1, length(b) - 2) ELSE b END AS b FROM noscheme
), bare AS (
    SELECT id, s, CASE WHEN b LIKE '%/' THEN substr(b, 1, length(b) - 1) ELSE b END AS b FROM nostar
), onehost AS (
    SELECT c.id FROM bare c JOIN scope_targets t ON t.scope_entry_id = c.id
    GROUP BY c.id HAVING COUNT(DISTINCT t.host) = 1
), typed AS (
    SELECT c.id, COALESCE(
        (SELECT t.kind FROM scope_targets t WHERE t.scope_entry_id = c.id AND lower(t.value) = c.s LIMIT 1),
        (SELECT t.kind FROM scope_targets t WHERE t.scope_entry_id = c.id AND lower(t.value) = c.b LIMIT 1)) AS kind
    FROM bare c JOIN onehost USING (id)
)
UPDATE scope_entries SET asset_type = typed.kind
FROM typed WHERE scope_entries.id = typed.id AND typed.kind IS NOT NULL;
