-- An asset a platform without scope IDs listed again with only a trailing
-- slash added or taken off was recorded as removed and added (two entries,
-- two changes). Polls now record it as an edit of one entry
-- (syncer.slashTwins); this does the same for what was saved before: the
-- addition becomes the identifier's edit, the removal goes, and the new
-- entry takes over the old one's history.
CREATE TEMP TABLE slash_pairs AS
SELECT r.id AS removed_change, MIN(a.id) AS added_change, r.scope_entry_id AS old_entry, MIN(a.scope_entry_id) AS new_entry,
       r.old_value AS old_identifier
FROM changes r
JOIN changes a ON a.program_id = r.program_id AND a.poll_run_id = r.poll_run_id AND a.field = r.field
    AND a.kind = 'scope_added' AND r.kind = 'scope_removed' AND a.old_value <> 'removed'
    AND a.new_value <> r.old_value AND rtrim(a.new_value, '/') = rtrim(r.old_value, '/')
JOIN scope_entries eo ON eo.id = r.scope_entry_id
JOIN scope_entries en ON en.id = a.scope_entry_id
WHERE eo.id <> en.id AND eo.asset_type = en.asset_type AND eo.in_scope = en.in_scope
GROUP BY r.id
HAVING COUNT(*) = 1;

UPDATE changes SET kind = 'scope_updated', field = 'identifier',
    old_value = (SELECT old_identifier FROM slash_pairs WHERE added_change = changes.id)
WHERE id IN (SELECT added_change FROM slash_pairs);

DELETE FROM changes WHERE id IN (SELECT removed_change FROM slash_pairs);

UPDATE changes SET scope_entry_id = (SELECT new_entry FROM slash_pairs WHERE old_entry = changes.scope_entry_id)
WHERE scope_entry_id IN (SELECT old_entry FROM slash_pairs);

UPDATE scope_entries SET
    first_seen_at = (SELECT o.first_seen_at FROM slash_pairs p JOIN scope_entries o ON o.id = p.old_entry WHERE p.new_entry = scope_entries.id),
    added_at = (SELECT o.added_at FROM slash_pairs p JOIN scope_entries o ON o.id = p.old_entry WHERE p.new_entry = scope_entries.id),
    added_bound = (SELECT o.added_bound FROM slash_pairs p JOIN scope_entries o ON o.id = p.old_entry WHERE p.new_entry = scope_entries.id)
WHERE id IN (SELECT new_entry FROM slash_pairs);

DELETE FROM scope_targets WHERE scope_entry_id IN (SELECT old_entry FROM slash_pairs);
DELETE FROM scope_entries WHERE id IN (SELECT old_entry FROM slash_pairs);

DROP TABLE slash_pairs;
