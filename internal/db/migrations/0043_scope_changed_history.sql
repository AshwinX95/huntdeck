-- Scope changed falls back to the dates scope history gives: an entry
-- added on a known date (not just bounded), or removed. Matches
-- store.scopeChangedAt.
UPDATE programs SET scope_changed_at = (SELECT MAX(at) FROM (
	SELECT MAX(c.occurred_at) AS at FROM changes c
		WHERE c.program_id = programs.id AND c.kind IN ('scope_added', 'scope_removed', 'scope_updated')
	UNION ALL SELECT MAX(e.added_at) FROM scope_entries e WHERE e.program_id = programs.id AND e.added_bound = 0
	UNION ALL SELECT MAX(e.removed_at) FROM scope_entries e WHERE e.program_id = programs.id));
