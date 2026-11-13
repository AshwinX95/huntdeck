-- Two leases per platform instead of one: its polls (scheduled, manual
-- and browser syncs), and refreshes of single programs (trigger
-- 'program', such as one whose invitation was just accepted). A refresh
-- runs beside a poll rather than waiting for it, which on Bugcrowd (a full
-- poll at 1 request a second) could take most of an hour; two refreshes
-- of a platform still take turns.
DROP INDEX poll_runs_one_running;
CREATE UNIQUE INDEX poll_runs_one_running ON poll_runs (platform) WHERE status = 'running' AND trigger <> 'program';
CREATE UNIQUE INDEX poll_runs_one_refresh ON poll_runs (platform) WHERE status = 'running' AND trigger = 'program';
