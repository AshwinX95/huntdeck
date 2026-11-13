-- Programs added by hand (platform 'independent': ones companies run
-- themselves) stay out of seeds unless the user shares them: what they
-- typed may be notes of their own. in_seeds is that choice; polls never
-- touch it.
ALTER TABLE programs ADD COLUMN in_seeds INTEGER NOT NULL DEFAULT 0 CHECK (in_seeds IN (0, 1));
