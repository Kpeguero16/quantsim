-- Make the database enforce the case-insensitive identity the service layer
-- now assumes (SPEC.md 2.7, 2.8, 2.10).
--
-- Without these indexes, app-level normalisation is one forgotten
-- strings.ToLower away from letting the same real mailbox hold two accounts.
--
-- ORDER MATTERS. Existing emails are lowercased FIRST, so the data and the
-- constraint agree by the time the index exists. That update is safe
-- precisely because the index created immediately after would reject any
-- collision the update could have produced -- and because the whole file
-- runs as one implicit transaction, a failure at the index leaves the
-- lowercasing rolled back rather than half-applied.
--
-- THIS MIGRATION FAILS, BY DESIGN, on any database holding two accounts
-- whose emails differ only in capitalisation. It does not resolve them
-- itself: a migration in this project never deletes a user row. Find them
-- and decide by hand first:
--
--   SELECT lower(email) AS normalized, count(*), array_agg(email)
--     FROM users GROUP BY 1 HAVING count(*) > 1;
--
-- Usernames are deliberately NOT rewritten. The service lowercases new ones
-- at registration, but an existing "Khalil" keeps the capitalisation its
-- owner chose -- the unique index below is what prevents a second "khalil",
-- and nothing looks a user up by username. Emails are different: they ARE a
-- lookup key, so their stored form has to match what Login normalises to.
--
-- updated_at is left alone on purpose. Canonicalising the stored form of an
-- address is not a change the account's owner made, and updated_at is
-- exposed through /auth/me.

UPDATE users SET email = lower(email) WHERE email <> lower(email);

CREATE UNIQUE INDEX idx_users_email_lower ON users (lower(email));
CREATE UNIQUE INDEX idx_users_username_lower ON users (lower(username));
