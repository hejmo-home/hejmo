-- Passkeys of adults (ticket 95, ADR 0018). A passkey replaces password and
-- one-time code at sign-in; it is valid only for the box name it was created
-- under (rp_id). credential holds the library's record (public key, counters,
-- flags) as JSON; it holds no secret, the private key stays on the device.
CREATE TABLE passkeys (
    id            TEXT PRIMARY KEY,
    person_id     TEXT NOT NULL REFERENCES people(id) ON DELETE CASCADE,
    credential_id BLOB NOT NULL UNIQUE,
    credential    TEXT NOT NULL,
    name          TEXT NOT NULL,
    rp_id         TEXT NOT NULL,
    -- device is the kind of device it was created on ("mac", "iphone"), as a key.
    device        TEXT NOT NULL DEFAULT '',
    created_at    TEXT NOT NULL,
    last_used_at  TEXT
);
CREATE INDEX passkeys_person ON passkeys (person_id);

-- The passkey a browser session was made with, so the list can say which one
-- the parent is signed in with. NULL for a sign-in with password.
ALTER TABLE sessions ADD COLUMN passkey_id TEXT;

-- Who reset a parent's one-time codes (the person's id); the reset time is
-- totp_reset_at. Said at the next sign-in, which sets up new codes.
ALTER TABLE accounts ADD COLUMN totp_reset_by TEXT;
