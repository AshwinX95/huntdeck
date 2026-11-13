-- The hacker profile (Signal, ID checks, clearance, residence) and its
-- "ones I can submit to" filter and HuntScore gate were dropped.
DELETE FROM settings WHERE key = 'hacker.profile';
