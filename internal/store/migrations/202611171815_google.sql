-- Google Calendar connection (ADR 0013, ticket 18).

-- The family's own OAuth client ("app registration" in the interface): one per
-- household. The client secret is sealed with the box's key like every credential.
CREATE TABLE google_app (
    household_id  TEXT PRIMARY KEY REFERENCES households(id) ON DELETE CASCADE,
    client_id     TEXT NOT NULL,
    client_secret TEXT NOT NULL,           -- sealed, base64
    created_by    TEXT,                    -- the parent who entered it
    created_at    TEXT NOT NULL
);

-- A started sign-in at Google. The state sent to Google carries a signed
-- token; its nonce is the key here, so a state works once. The PKCE verifier
-- is sealed. Rows are pruned a day after they expire.
CREATE TABLE google_login (
    nonce         TEXT PRIMARY KEY,
    household_id  TEXT NOT NULL REFERENCES households(id) ON DELETE CASCADE,
    person_id     TEXT,
    verifier      TEXT NOT NULL,           -- sealed, base64
    redirect_uri  TEXT NOT NULL,
    connection_id TEXT,                    -- set when an existing connection signs in again
    created_at    TEXT NOT NULL,
    expires_at    TEXT NOT NULL,
    used_at       TEXT
);

-- A Google connection keeps its refresh token in `credential` (sealed) and the
-- Google account in `username`. `signed_in_at` is when Google issued that token
-- (an app left on "Testing" loses it after 7 days); `token_renewed_at` the last
-- time it got a new access token; `signin_needed_at` when Google first refused
-- it (invalid_grant), until the parent signs in again.
ALTER TABLE calendar_connections ADD COLUMN signed_in_at TEXT;
ALTER TABLE calendar_connections ADD COLUMN token_renewed_at TEXT;
ALTER TABLE calendar_connections ADD COLUMN signin_needed_at TEXT;

-- Google says a calendar is only shared for viewing: it can never be two-way.
ALTER TABLE calendars ADD COLUMN source_read_only INTEGER NOT NULL DEFAULT 0;
