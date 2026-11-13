-- The same seek for target lists by kind, which keep one row per program and
-- value (marketplace.atlassian.com is a domain of 704 programs).
CREATE INDEX scope_targets_program_value ON scope_targets (program_id, kind, value);
