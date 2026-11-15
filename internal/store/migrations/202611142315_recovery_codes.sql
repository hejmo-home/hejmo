-- Recovery codes replace the one-time code at sign-in (ADR 0017). Only
-- Argon2id hashes are stored.
CREATE TABLE recovery_codes (
    id         TEXT PRIMARY KEY,
    person_id  TEXT NOT NULL REFERENCES people(id) ON DELETE CASCADE,
    code_hash  TEXT NOT NULL,
    created_at TEXT NOT NULL,
    used_at    TEXT
);
CREATE INDEX recovery_codes_person ON recovery_codes (person_id);

-- Set when a parent's one-time codes were reset: the account stays a
-- configured account while it has no confirmed secret.
ALTER TABLE accounts ADD COLUMN totp_reset_at TEXT;
