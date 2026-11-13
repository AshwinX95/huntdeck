-- Re-deriving targets after a scope parser change (store.Retarget) didn't
-- refresh the programs' cached target counts, so some went stale (a
-- program counting 3 wildcards whose scope lists 16) and the hunt board's
-- totals disagreed with the Targets page it links to. Retarget refreshes
-- them now; this brings every program's up to date once, as
-- store.RefreshScopeCounts counts them.
UPDATE programs SET
    n_in_scope = (SELECT COUNT(*) FROM scope_entries e WHERE e.program_id = programs.id AND e.removed_at IS NULL AND e.in_scope = 1),
    n_out_scope = (SELECT COUNT(*) FROM scope_entries e WHERE e.program_id = programs.id AND e.removed_at IS NULL AND e.in_scope = 0),
    (n_wildcards, n_domains, n_urls, n_ips, n_cidrs) = (
        SELECT COUNT(DISTINCT CASE WHEN t.kind = 'wildcard' THEN t.value END),
            COUNT(DISTINCT CASE WHEN t.kind = 'domain' THEN t.value END),
            COUNT(DISTINCT CASE WHEN t.kind = 'url' THEN t.value END),
            COUNT(DISTINCT CASE WHEN t.kind = 'ip' THEN t.value END),
            COUNT(DISTINCT CASE WHEN t.kind = 'cidr' THEN t.value END)
        FROM scope_targets t JOIN scope_entries e ON e.id = t.scope_entry_id
        WHERE e.program_id = programs.id AND e.removed_at IS NULL AND e.in_scope = 1);
