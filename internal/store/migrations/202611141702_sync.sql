-- Calendar sync (ADR 0012): connections, per-calendar sync state, sync log.

-- The link from the box to an outside calendar address. ICS subscriptions
-- need only the address; credentials follow with CalDAV and Google.
CREATE TABLE calendar_connections (
    id           TEXT PRIMARY KEY,
    household_id TEXT NOT NULL REFERENCES households(id) ON DELETE CASCADE,
    kind         TEXT NOT NULL CHECK (kind IN ('ics', 'caldav', 'google')),
    url          TEXT NOT NULL,
    created_at   TEXT NOT NULL
);

-- Removing a connection removes its synced calendars and their events.
ALTER TABLE calendars ADD COLUMN connection_id TEXT REFERENCES calendar_connections(id) ON DELETE CASCADE;
ALTER TABLE calendars ADD COLUMN read_only INTEGER NOT NULL DEFAULT 0;

-- Identity of a synced event in its feed (UID, plus the original start for a
-- changed occurrence), so ids stay stable across syncs.
ALTER TABLE events ADD COLUMN remote_key TEXT;
CREATE UNIQUE INDEX events_remote_key ON events (calendar_id, remote_key) WHERE remote_key IS NOT NULL;

-- Sync state of one synced calendar. Times are RFC 3339 UTC.
CREATE TABLE calendar_sync (
    calendar_id      TEXT PRIMARY KEY REFERENCES calendars(id) ON DELETE CASCADE,
    interval_minutes INTEGER NOT NULL,
    started_at       TEXT NOT NULL,              -- staleness counts from here until the first success
    last_attempt_at  TEXT,
    last_success_at  TEXT,
    next_attempt_at  TEXT NOT NULL,
    failures         INTEGER NOT NULL DEFAULT 0, -- consecutive failures, drives backoff
    last_error_key    TEXT NOT NULL DEFAULT '', -- i18n message key, empty after a success
    last_error_params TEXT NOT NULL DEFAULT '{}' -- JSON parameters of the message
);

-- Failures, conflicts and remarks of the synchronisation, readable by parents.
CREATE TABLE sync_log (
    id           TEXT PRIMARY KEY,
    household_id TEXT NOT NULL REFERENCES households(id) ON DELETE CASCADE,
    calendar_id  TEXT NOT NULL REFERENCES calendars(id) ON DELETE CASCADE,
    at           TEXT NOT NULL,
    kind         TEXT NOT NULL CHECK (kind IN ('failure', 'conflict', 'note')),
    message_key  TEXT NOT NULL,                -- i18n key, translated when shown
    params       TEXT NOT NULL DEFAULT '{}'    -- JSON parameters of the message
);
CREATE INDEX sync_log_household ON sync_log (household_id, at);
