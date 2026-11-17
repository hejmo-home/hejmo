-- Offline mode and the connections to the outside (ticket 201, Q141 to Q146).
-- The state is the box's, not a household's: it is about what the machine
-- connects to, and it must be there before the first household exists (the
-- setup wizard asks "Should the box stay fully offline?").
CREATE TABLE outbound_box (
    id                   INTEGER PRIMARY KEY CHECK (id = 1),
    offline              INTEGER NOT NULL DEFAULT 0 CHECK (offline IN (0, 1)),
    offline_since        TEXT,                              -- when offline mode was switched on
    asked                INTEGER NOT NULL DEFAULT 0 CHECK (asked IN (0, 1)), -- the wizard question is answered
    update_reminder_days INTEGER NOT NULL DEFAULT 90 CHECK (update_reminder_days BETWEEN 7 AND 365),
    updated_at           TEXT NOT NULL                      -- install or last update, for the reminder
);
INSERT INTO outbound_box (id, updated_at) VALUES (1, strftime('%Y-%m-%dT%H:%M:%SZ', 'now'));

-- One row per connection that has a switch; no row means on.
-- Offline mode does not touch these: the switches are as they were when it is
-- switched off again.
CREATE TABLE outbound_service (
    service      TEXT PRIMARY KEY CHECK (service IN ('weather', 'update', 'calendars', 'ics', 'recipes', 'images')),
    enabled      INTEGER NOT NULL DEFAULT 1 CHECK (enabled IN (0, 1))
);

-- The last contact of every connection: the proof of what went out, and when.
CREATE TABLE outbound_contact (
    service      TEXT PRIMARY KEY,
    last_contact TEXT NOT NULL,
    last_target  TEXT NOT NULL
);

-- What the gate refused, newest last; the box keeps the latest few hundred.
CREATE TABLE outbound_refused (
    id      INTEGER PRIMARY KEY AUTOINCREMENT,
    at      TEXT NOT NULL,
    service TEXT NOT NULL,
    target  TEXT NOT NULL
);
