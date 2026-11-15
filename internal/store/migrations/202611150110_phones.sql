-- Phones (ADR 0015): a parent shows a QR code, the phone opens it, the parent
-- chooses the person the phone is for, and the phone gets a long-lived,
-- revocable device session.
CREATE TABLE phones (
    id           TEXT PRIMARY KEY,
    household_id TEXT NOT NULL REFERENCES households(id) ON DELETE CASCADE,
    person_id    TEXT NOT NULL REFERENCES people(id) ON DELETE CASCADE,
    name         TEXT NOT NULL,
    created_at   TEXT NOT NULL,
    last_seen_at TEXT
);

-- A device session is a session that belongs to a phone: revoking the phone
-- deletes it.
ALTER TABLE sessions ADD COLUMN phone_id TEXT REFERENCES phones(id) ON DELETE CASCADE;

-- A pairing code, valid once for ten minutes. Only hashes are stored: the code
-- (in the QR code) and the claim (the phone's own secret).
CREATE TABLE phone_pairings (
    id           TEXT PRIMARY KEY,
    household_id TEXT NOT NULL REFERENCES households(id) ON DELETE CASCADE,
    code_hash    TEXT NOT NULL UNIQUE,
    claim_hash   TEXT,                        -- set when a phone opened the code
    device       TEXT NOT NULL DEFAULT '',    -- what the phone said about itself
    person_id    TEXT REFERENCES people(id) ON DELETE CASCADE, -- set when a parent confirmed
    name         TEXT NOT NULL DEFAULT '',
    phone_id     TEXT,                        -- the id the phone will have, set with person_id
    created_at   TEXT NOT NULL,
    expires_at   TEXT NOT NULL
);
