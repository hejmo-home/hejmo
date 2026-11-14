-- CalDAV and iCloud connections (ADR 0012): credentials, one remote address per
-- synced calendar, and what the box knows of each remote object.

-- The password is sealed with the box's key (AES-256-GCM) before it is stored.
ALTER TABLE calendar_connections ADD COLUMN username TEXT NOT NULL DEFAULT '';
ALTER TABLE calendar_connections ADD COLUMN credential TEXT NOT NULL DEFAULT '';
-- 1: the address is that of one calendar, added by hand, not of a server to discover.
ALTER TABLE calendar_connections ADD COLUMN single_calendar INTEGER NOT NULL DEFAULT 0;

-- Address of a synced calendar on the server. ICS calendars use the
-- connection's address and leave this empty.
ALTER TABLE calendars ADD COLUMN remote_url TEXT;
CREATE UNIQUE INDEX calendars_remote_url ON calendars (connection_id, remote_url) WHERE remote_url IS NOT NULL;

-- The object (.ics resource) an event came from.
ALTER TABLE events ADD COLUMN remote_url TEXT;
CREATE INDEX events_remote_url ON events (calendar_id, remote_url);

-- One row per object on the server: its ETag for delta sync and its raw
-- iCalendar text, kept so write-back can later change only what the box models.
CREATE TABLE remote_objects (
    calendar_id TEXT NOT NULL REFERENCES calendars(id) ON DELETE CASCADE,
    url         TEXT NOT NULL,
    etag        TEXT NOT NULL,
    raw         TEXT NOT NULL,
    PRIMARY KEY (calendar_id, url)
);
