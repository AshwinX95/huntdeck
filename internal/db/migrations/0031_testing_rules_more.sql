-- Two more testing rules: automated tools not allowed at all (Intigriti's
-- automatedTooling of 0; null there means no rule), and test accounts that
-- must use the platform's email alias (Intigriti's @intigriti.me).
ALTER TABLE programs ADD COLUMN test_no_automation INTEGER NOT NULL DEFAULT 0;
ALTER TABLE programs ADD COLUMN test_platform_email INTEGER NOT NULL DEFAULT 0;
