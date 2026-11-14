-- Displays (ADR 0005, 0015). A display that opens the box's display address
-- gets a row at once, holding only the hash of its own token and a pairing
-- code. A parent confirming the code (paired_at set) names the display; the
-- token then identifies it. Revoking sets revoked_at and the token is dead.
CREATE TABLE displays (
    id TEXT PRIMARY KEY,
    household_id TEXT NOT NULL REFERENCES households(id) ON DELETE CASCADE,
    token_hash TEXT NOT NULL UNIQUE,
    name TEXT NOT NULL DEFAULT '',
    room TEXT NOT NULL DEFAULT '',
    type TEXT NOT NULL DEFAULT '',
    pairing_code TEXT,
    code_expires_at TEXT,
    created_at TEXT NOT NULL,
    paired_at TEXT,
    last_seen_at TEXT,
    revoked_at TEXT
);
-- A code identifies one waiting display.
CREATE UNIQUE INDEX displays_pairing_code ON displays(pairing_code) WHERE pairing_code IS NOT NULL;
