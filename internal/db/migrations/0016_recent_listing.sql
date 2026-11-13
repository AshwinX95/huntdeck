-- Quick polls of platforms that can list programs newest first read only
-- that page between complete listings; such runs are marked here.
ALTER TABLE poll_runs ADD COLUMN listing_recent INTEGER NOT NULL DEFAULT 0;
