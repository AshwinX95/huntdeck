-- A response, triage, bounty, resolution or validation time of 0 is a
-- platform's "no figure" (HackerOne gives it for programs with no recent
-- numbers), not an instant answer: it's stored as none from now on
-- (store.measured), and the ones saved so far are cleared.
UPDATE program_public_stats SET first_response_hours = NULL WHERE first_response_hours <= 0;
UPDATE program_public_stats SET triage_hours = NULL WHERE triage_hours <= 0;
UPDATE program_public_stats SET bounty_hours = NULL WHERE bounty_hours <= 0;
UPDATE program_public_stats SET resolution_hours = NULL WHERE resolution_hours <= 0;
UPDATE program_public_stats SET validation_hours = NULL WHERE validation_hours <= 0;
