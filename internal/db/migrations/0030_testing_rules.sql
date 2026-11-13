-- Two more requirements (Intigriti's listing shows both): residents of the
-- European Economic Area only, and two-factor auth on the hacker's account.
-- And a program's rules for test traffic: the request rate allowed (per
-- second) and a User-Agent and header to send. testing_known is set once a
-- platform has said, even that there are none.
ALTER TABLE programs ADD COLUMN req_eea INTEGER NOT NULL DEFAULT 0;
ALTER TABLE programs ADD COLUMN req_two_factor INTEGER NOT NULL DEFAULT 0;
ALTER TABLE programs ADD COLUMN testing_known INTEGER NOT NULL DEFAULT 0;
ALTER TABLE programs ADD COLUMN test_rate_limit REAL;
ALTER TABLE programs ADD COLUMN test_user_agent TEXT NOT NULL DEFAULT '';
ALTER TABLE programs ADD COLUMN test_header TEXT NOT NULL DEFAULT '';
