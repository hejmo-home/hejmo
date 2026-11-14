-- Box as CalDAV server (ADR 0012): which calendars phones may subscribe to.
-- The family calendar (kind 'local') is always served and ignores this flag.
-- On by default for ICS calendars, off for CalDAV and Google calendars.
ALTER TABLE calendars ADD COLUMN shared INTEGER NOT NULL DEFAULT 0;
UPDATE calendars SET shared = 1 WHERE kind = 'ics';
