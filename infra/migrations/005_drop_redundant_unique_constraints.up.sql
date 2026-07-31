-- Drop the per-column UNIQUE constraints from migration 001. Since 004 they
-- are redundant: two rows with identical emails necessarily share
-- lower(email), so UNIQUE (lower(email)) already rejects them. Same for
-- username. Four unique index maintenances per insert become two.
--
-- REQUIRES MIGRATION 004. These constraints are only safe to drop because
-- 004's unique indexes on lower(email) and lower(username) exist to take
-- over. Dropping them without those indexes would leave the users table with
-- NO uniqueness on email or username at all. golang-migrate applies in
-- order, so this cannot happen through `make migrate-up` -- the warning is
-- for anyone running this file by hand.
--
-- REQUIRES THE lower(email) LOOKUP (Step 10 SPEC.md 2.1). users_email_key is
-- what an exact-match `WHERE email = $1` query scans. Dropping it while such
-- a query is live leaves the lookup correct but sequential. The code change
-- ships first; on a 15-row table the difference is unobservable, which is
-- exactly why the ordering is written down rather than remembered.
--
-- Dropped as CONSTRAINTs, not indexes: these were created by
-- CREATE TABLE ... UNIQUE, so DROP INDEX would refuse them.

ALTER TABLE users DROP CONSTRAINT users_email_key;
ALTER TABLE users DROP CONSTRAINT users_username_key;
