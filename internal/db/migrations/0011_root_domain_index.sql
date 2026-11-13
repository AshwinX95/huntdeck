-- Root-domain lists (target kind "root") group targets by apex.
CREATE INDEX scope_targets_apex ON scope_targets (apex, kind);
