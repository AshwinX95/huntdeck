-- Cached per-program scope counts, so lists and summaries don't aggregate
-- every scope target on each request; kept current by store.Tx.RefreshScopeCounts
-- in the same transaction that writes a program's scope.
-- scope_unavailable_at marks programs whose scope the platform refuses to show
-- (terms not accepted, compliance gate), so quick polls stop retrying them.

ALTER TABLE programs ADD COLUMN scope_unavailable_at TEXT;
ALTER TABLE programs ADD COLUMN scope_changed_at TEXT;
ALTER TABLE programs ADD COLUMN n_in_scope INTEGER NOT NULL DEFAULT 0;
ALTER TABLE programs ADD COLUMN n_out_scope INTEGER NOT NULL DEFAULT 0;
ALTER TABLE programs ADD COLUMN n_wildcards INTEGER NOT NULL DEFAULT 0;
ALTER TABLE programs ADD COLUMN n_domains INTEGER NOT NULL DEFAULT 0;
ALTER TABLE programs ADD COLUMN n_urls INTEGER NOT NULL DEFAULT 0;
ALTER TABLE programs ADD COLUMN n_ips INTEGER NOT NULL DEFAULT 0;
ALTER TABLE programs ADD COLUMN n_cidrs INTEGER NOT NULL DEFAULT 0;

CREATE INDEX scope_entries_program ON scope_entries (program_id, removed_at, in_scope);
CREATE INDEX scope_targets_host ON scope_targets (host) WHERE host <> '';

UPDATE programs SET
    n_in_scope = (SELECT COUNT(*) FROM scope_entries e
        WHERE e.program_id = programs.id AND e.removed_at IS NULL AND e.in_scope = 1),
    n_out_scope = (SELECT COUNT(*) FROM scope_entries e
        WHERE e.program_id = programs.id AND e.removed_at IS NULL AND e.in_scope = 0),
    scope_changed_at = (SELECT MAX(c.occurred_at) FROM changes c
        WHERE c.program_id = programs.id AND c.kind IN ('scope_added', 'scope_removed', 'scope_updated'));

UPDATE programs SET
    n_wildcards = COALESCE(tc.n_wildcards, 0), n_domains = COALESCE(tc.n_domains, 0), n_urls = COALESCE(tc.n_urls, 0),
    n_ips = COALESCE(tc.n_ips, 0), n_cidrs = COALESCE(tc.n_cidrs, 0)
FROM (
    SELECT e.program_id,
        COUNT(DISTINCT CASE WHEN t.kind = 'wildcard' THEN t.value END) AS n_wildcards,
        COUNT(DISTINCT CASE WHEN t.kind = 'domain' THEN t.value END) AS n_domains,
        COUNT(DISTINCT CASE WHEN t.kind = 'url' THEN t.value END) AS n_urls,
        COUNT(DISTINCT CASE WHEN t.kind = 'ip' THEN t.value END) AS n_ips,
        COUNT(DISTINCT CASE WHEN t.kind = 'cidr' THEN t.value END) AS n_cidrs
    FROM scope_targets t JOIN scope_entries e ON e.id = t.scope_entry_id
    WHERE e.removed_at IS NULL AND e.in_scope = 1
    GROUP BY e.program_id
) AS tc
WHERE tc.program_id = programs.id;
