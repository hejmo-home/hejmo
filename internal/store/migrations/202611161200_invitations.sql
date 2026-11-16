-- Invitations (ticket 145): a parent invites an adult person without an account.
-- Only a hash of the link is stored. A row is open until it is accepted,
-- withdrawn (also when a newer link replaces it) or past expires_at.
CREATE TABLE invitations (
    id           TEXT PRIMARY KEY,
    household_id TEXT NOT NULL REFERENCES households(id) ON DELETE CASCADE,
    person_id    TEXT NOT NULL REFERENCES people(id) ON DELETE CASCADE,
    token_hash   TEXT NOT NULL UNIQUE,
    created_by   TEXT REFERENCES people(id) ON DELETE SET NULL,
    created_at   TEXT NOT NULL,
    expires_at   TEXT NOT NULL,
    accepted_at  TEXT,
    withdrawn_at TEXT
);
CREATE INDEX invitations_person ON invitations (person_id, created_at);
