-- Subgroups such as "school children" or "nursery child".
CREATE TABLE groups (
    id           TEXT PRIMARY KEY,
    household_id TEXT NOT NULL REFERENCES households(id) ON DELETE CASCADE,
    name         TEXT NOT NULL,
    created_at   TEXT NOT NULL
);

CREATE TABLE group_members (
    group_id  TEXT NOT NULL REFERENCES groups(id) ON DELETE CASCADE,
    person_id TEXT NOT NULL REFERENCES people(id) ON DELETE CASCADE,
    PRIMARY KEY (group_id, person_id)
);

-- A calendar is a source of events: local, or synced (package 4).
CREATE TABLE calendars (
    id           TEXT PRIMARY KEY,
    household_id TEXT NOT NULL REFERENCES households(id) ON DELETE CASCADE,
    name         TEXT NOT NULL,
    kind         TEXT NOT NULL CHECK (kind IN ('local', 'ics', 'caldav', 'google')),
    color        TEXT NOT NULL DEFAULT '',
    created_at   TEXT NOT NULL,
    updated_at   TEXT NOT NULL
);

-- Who an event or a whole calendar is for. No rows = the whole family.
-- Exactly one of person_id / group_id is set per row.
CREATE TABLE calendar_audience (
    calendar_id TEXT NOT NULL REFERENCES calendars(id) ON DELETE CASCADE,
    person_id   TEXT REFERENCES people(id) ON DELETE CASCADE,
    group_id    TEXT REFERENCES groups(id) ON DELETE CASCADE,
    CHECK ((person_id IS NULL) <> (group_id IS NULL))
);
CREATE UNIQUE INDEX calendar_audience_unique ON calendar_audience (calendar_id, COALESCE(person_id, ''), COALESCE(group_id, ''));

-- Times are local wall-clock times plus a time zone (like iCalendar), so a
-- weekly 08:00 event stays at 08:00 across daylight-saving changes.
-- Timed: 'YYYY-MM-DDTHH:MM:SS'; all-day: 'YYYY-MM-DD', end exclusive.
CREATE TABLE events (
    id            TEXT PRIMARY KEY,
    household_id  TEXT NOT NULL REFERENCES households(id) ON DELETE CASCADE,
    calendar_id   TEXT NOT NULL REFERENCES calendars(id) ON DELETE CASCADE,
    title         TEXT NOT NULL,
    description   TEXT NOT NULL DEFAULT '',
    location      TEXT NOT NULL DEFAULT '',
    all_day       INTEGER NOT NULL DEFAULT 0,
    start_local   TEXT NOT NULL,
    end_local     TEXT NOT NULL,
    tzid          TEXT NOT NULL,
    rrule         TEXT NOT NULL DEFAULT '',   -- RFC 5545 RRULE value, '' = single event
    series_id     TEXT REFERENCES events(id) ON DELETE CASCADE, -- set on a changed occurrence
    recurrence_id TEXT,                       -- original start of that occurrence
    extra         TEXT NOT NULL DEFAULT '{}', -- JSON: event type and fields, X-properties (V2)
    created_at    TEXT NOT NULL,
    updated_at    TEXT NOT NULL
);
CREATE INDEX events_calendar ON events (calendar_id);
CREATE INDEX events_series ON events (series_id);

-- Cancelled occurrences of a series (EXDATE).
CREATE TABLE event_exdates (
    event_id   TEXT NOT NULL REFERENCES events(id) ON DELETE CASCADE,
    occurrence TEXT NOT NULL,
    PRIMARY KEY (event_id, occurrence)
);

CREATE TABLE event_audience (
    event_id  TEXT NOT NULL REFERENCES events(id) ON DELETE CASCADE,
    person_id TEXT REFERENCES people(id) ON DELETE CASCADE,
    group_id  TEXT REFERENCES groups(id) ON DELETE CASCADE,
    CHECK ((person_id IS NULL) <> (group_id IS NULL))
);
CREATE UNIQUE INDEX event_audience_unique ON event_audience (event_id, COALESCE(person_id, ''), COALESCE(group_id, ''));

-- Full-text search (FTS5), kept in sync by triggers.
CREATE VIRTUAL TABLE events_fts USING fts5(
    title, description, location,
    content = 'events', content_rowid = 'rowid', tokenize = 'unicode61 remove_diacritics 2'
);
CREATE TRIGGER events_fts_insert AFTER INSERT ON events BEGIN
    INSERT INTO events_fts (rowid, title, description, location) VALUES (new.rowid, new.title, new.description, new.location);
END;
CREATE TRIGGER events_fts_delete AFTER DELETE ON events BEGIN
    INSERT INTO events_fts (events_fts, rowid, title, description, location) VALUES ('delete', old.rowid, old.title, old.description, old.location);
END;
CREATE TRIGGER events_fts_update AFTER UPDATE ON events BEGIN
    INSERT INTO events_fts (events_fts, rowid, title, description, location) VALUES ('delete', old.rowid, old.title, old.description, old.location);
    INSERT INTO events_fts (rowid, title, description, location) VALUES (new.rowid, new.title, new.description, new.location);
END;
