-- hejmo:foreign-keys-off
-- Outlook calendar sync (ADR 0020, #177). SQLite cannot change a CHECK
-- constraint, so the two tables that name the kinds are rebuilt (the marker
-- above runs this migration with foreign keys off, so the drops do not cascade).

-- A connection of kind 'outlook' holds the Microsoft account in username, the
-- sealed refresh token in credential, the family's own app ID (empty: the
-- Hejmo project's registration), when the sign-in was last renewed and, if
-- Microsoft ended it, since when (reauth_since).
CREATE TABLE calendar_connections_new (
    id              TEXT PRIMARY KEY,
    household_id    TEXT NOT NULL REFERENCES households(id) ON DELETE CASCADE,
    kind            TEXT NOT NULL CHECK (kind IN ('ics', 'caldav', 'google', 'outlook')),
    url             TEXT NOT NULL,
    created_at      TEXT NOT NULL,
    username        TEXT NOT NULL DEFAULT '',
    credential      TEXT NOT NULL DEFAULT '',
    single_calendar INTEGER NOT NULL DEFAULT 0,
    client_id       TEXT NOT NULL DEFAULT '',
    renewed_at      TEXT NOT NULL DEFAULT '',
    reauth_since    TEXT NOT NULL DEFAULT ''
);
INSERT INTO calendar_connections_new (rowid, id, household_id, kind, url, created_at, username, credential, single_calendar)
    SELECT rowid, id, household_id, kind, url, created_at, username, credential, single_calendar FROM calendar_connections;
DROP TABLE calendar_connections;
ALTER TABLE calendar_connections_new RENAME TO calendar_connections;

-- source_name: the calendar's name in Outlook. source_locked: Outlook says the calendar is not the account's to change
-- (shared with it, birthdays, holidays); source_owner names who shared it.
CREATE TABLE calendars_new (
    id            TEXT PRIMARY KEY,
    household_id  TEXT NOT NULL REFERENCES households(id) ON DELETE CASCADE,
    name          TEXT NOT NULL,
    kind          TEXT NOT NULL CHECK (kind IN ('local', 'ics', 'caldav', 'google', 'meals', 'outlook')),
    color         TEXT NOT NULL DEFAULT '',
    created_at    TEXT NOT NULL,
    updated_at    TEXT NOT NULL,
    connection_id TEXT REFERENCES calendar_connections(id) ON DELETE CASCADE,
    read_only     INTEGER NOT NULL DEFAULT 0,
    shared        INTEGER NOT NULL DEFAULT 0,
    remote_url    TEXT,
    waste         INTEGER NOT NULL DEFAULT 0,
    source_locked INTEGER NOT NULL DEFAULT 0,
    source_owner  TEXT NOT NULL DEFAULT '',
    source_name   TEXT NOT NULL DEFAULT ''
);
INSERT INTO calendars_new (rowid, id, household_id, name, kind, color, created_at, updated_at, connection_id, read_only, shared, remote_url, waste)
    SELECT rowid, id, household_id, name, kind, color, created_at, updated_at, connection_id, read_only, shared, remote_url, waste FROM calendars;
DROP TABLE calendars;
ALTER TABLE calendars_new RENAME TO calendars;
CREATE UNIQUE INDEX calendars_remote_url ON calendars (connection_id, remote_url) WHERE remote_url IS NOT NULL;

-- An event other people are invited to (Outlook attendees): read-only on the box.
ALTER TABLE events ADD COLUMN invited INTEGER NOT NULL DEFAULT 0;
ALTER TABLE events ADD COLUMN invite_organizer TEXT NOT NULL DEFAULT '';
ALTER TABLE events ADD COLUMN invite_response TEXT NOT NULL DEFAULT '';
ALTER TABLE events ADD COLUMN invite_link TEXT NOT NULL DEFAULT '';
