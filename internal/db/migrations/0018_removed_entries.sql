-- The scope check looks for the input's root domain in removed entries'
-- identifiers. That is a substring match, so it reads every removed entry;
-- this index holds just those, with every column the lookup reads, so it
-- skips live entries and never touches the table's wide rows.
CREATE INDEX scope_entries_removed ON scope_entries (removed_at, identifier, asset_type, in_scope, program_id)
    WHERE removed_at IS NOT NULL;
