-- A household is one box's family. Designed so a person can later belong to
-- several households (two-home model, V3): roles live on the membership.
CREATE TABLE households (
    id          TEXT PRIMARY KEY,
    name        TEXT NOT NULL,
    home_name   TEXT NOT NULL DEFAULT '',
    latitude    REAL,
    longitude   REAL,
    region      TEXT NOT NULL DEFAULT '',   -- ISO 3166-2, e.g. DE-BY, for holidays
    timezone    TEXT NOT NULL DEFAULT 'Europe/Berlin',
    display_pin_hash TEXT NOT NULL DEFAULT '',
    created_at  TEXT NOT NULL,
    updated_at  TEXT NOT NULL
);

CREATE TABLE people (
    id          TEXT PRIMARY KEY,
    name        TEXT NOT NULL,
    color       TEXT NOT NULL DEFAULT '',   -- hex, colour mode
    shape       TEXT NOT NULL DEFAULT '',   -- black-and-white marker
    symbol      TEXT NOT NULL DEFAULT '',
    birthday    TEXT,                       -- YYYY-MM-DD
    created_at  TEXT NOT NULL,
    updated_at  TEXT NOT NULL
);

CREATE TABLE household_members (
    household_id TEXT NOT NULL REFERENCES households(id) ON DELETE CASCADE,
    person_id    TEXT NOT NULL REFERENCES people(id) ON DELETE CASCADE,
    role         TEXT NOT NULL CHECK (role IN ('parent', 'child')),
    sort_order   INTEGER NOT NULL DEFAULT 0,
    PRIMARY KEY (household_id, person_id)
);

CREATE TABLE accounts (
    person_id         TEXT PRIMARY KEY REFERENCES people(id) ON DELETE CASCADE,
    username          TEXT NOT NULL UNIQUE COLLATE NOCASE,
    password_hash     TEXT NOT NULL,
    totp_secret       TEXT NOT NULL,
    totp_confirmed_at TEXT,             -- setup is complete once a parent confirmed TOTP
    created_at        TEXT NOT NULL
);

CREATE TABLE sessions (
    token_hash  TEXT PRIMARY KEY,
    person_id   TEXT NOT NULL REFERENCES people(id) ON DELETE CASCADE,
    created_at  TEXT NOT NULL,
    expires_at  TEXT NOT NULL
);

CREATE TABLE api_tokens (
    id           TEXT PRIMARY KEY,
    name         TEXT NOT NULL,
    token_hash   TEXT NOT NULL UNIQUE,
    person_id    TEXT NOT NULL REFERENCES people(id) ON DELETE CASCADE,
    scopes       TEXT NOT NULL,              -- space-separated
    created_at   TEXT NOT NULL,
    last_used_at TEXT,
    revoked_at   TEXT
);
