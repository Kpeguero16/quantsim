-- Restores the per-column UNIQUE constraints dropped by the up migration.
--
-- Unlike 004, THIS ROLLBACK IS COMPLETE. No data is transformed in either
-- direction, so the constraints are recreated exactly as migration 001 left
-- them. The contrast with 004.down.sql -- which cannot restore the email
-- capitalisation its up migration destroyed -- is worth noticing: a
-- migration that only moves constraints around is reversible, one that
-- rewrites rows generally is not.
--
-- This direction cannot fail on existing data. Adding UNIQUE (email)
-- validates the table, but any collision it could find would already have
-- violated 004's UNIQUE (lower(email)), which is strictly stronger. Verified
-- against real rows on a throwaway database rather than reasoned about.
--
-- Note that adding a UNIQUE constraint builds an index under an
-- ACCESS EXCLUSIVE lock, blocking writes for its duration -- the same trade
-- recorded for 004 in docs/deferred-tuning.md. Irrelevant at this size.

ALTER TABLE users ADD CONSTRAINT users_email_key UNIQUE (email);
ALTER TABLE users ADD CONSTRAINT users_username_key UNIQUE (username);
