-- CalDAV write-back (ADR 0012): changes on the box are pushed to the server.

-- Two-way is the default for CalDAV calendars; a parent can switch one to
-- read-only. Calendars connected before write-back existed had no switch and
-- were read-only only because nothing could be written.
UPDATE calendars SET read_only = 0 WHERE kind = 'caldav';

-- Box changes not yet written to the server, per remote object. pending counts
-- the changes (0: none), so a change made while a push is on its way is not
-- lost when the push finishes. A new object has an empty ETag and raw text
-- until its first push. changed_at is when the box last changed the object,
-- which decides a sync conflict against the server's LAST-MODIFIED.
ALTER TABLE remote_objects ADD COLUMN pending    INTEGER NOT NULL DEFAULT 0;
ALTER TABLE remote_objects ADD COLUMN changed_at TEXT    NOT NULL DEFAULT '';

-- The version that lost a sync conflict, kept with the log entry for the 30
-- days the log is kept.
ALTER TABLE sync_log ADD COLUMN detail TEXT NOT NULL DEFAULT '';
