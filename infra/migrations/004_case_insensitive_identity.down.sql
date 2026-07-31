-- Drops the case-insensitive uniqueness constraints.
--
-- THIS DOES NOT UNDO THE UP MIGRATION COMPLETELY, and cannot.
--
-- The up migration rewrote existing emails to lowercase. That original
-- capitalisation is gone -- no backup column preserves it, deliberately
-- (SPEC.md 7): it is real complexity carried forever to protect data whose
-- only distinguishing feature is capitalisation that nobody wants and that
-- no mail provider honours.
--
-- So rolling back restores the ability to create case-duplicate accounts,
-- but not the exact strings that were there before. That is accepted, and
-- recorded here so the next person reading this file is not surprised by it.
--
-- The original per-column UNIQUE constraints on email and username from
-- migration 001 are untouched by both directions of this migration, so
-- exact-match uniqueness survives a rollback.

DROP INDEX IF EXISTS idx_users_username_lower;
DROP INDEX IF EXISTS idx_users_email_lower;
