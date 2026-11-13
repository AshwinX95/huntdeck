-- Root-domain lists keep one row per program and root domain, so each row
-- looks for an earlier target of the same program with the same apex.
-- Without the program on the target that walked every program's targets
-- under the apex (atlassian.com: 1,459 targets in 101 programs), quadratic
-- in the apex's size. A target's entry never changes program.
ALTER TABLE scope_targets ADD COLUMN program_id INTEGER NOT NULL DEFAULT 0;
UPDATE scope_targets SET program_id = (SELECT e.program_id FROM scope_entries e WHERE e.id = scope_targets.scope_entry_id);
CREATE INDEX scope_targets_program_root ON scope_targets (program_id, apex)
    WHERE kind IN ('wildcard', 'domain', 'url') AND apex <> '';
