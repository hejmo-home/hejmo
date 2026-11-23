-- An update file a parent uploaded in offline mode (ticket 221, Q154): it waits
-- for confirmation (ready), then installs (installing) until the host script
-- reports the result. The box's, not a household's: at most one at a time.
CREATE TABLE update_upload (
    id           INTEGER PRIMARY KEY CHECK (id = 1),
    state        TEXT NOT NULL CHECK (state IN ('ready', 'installing')),
    version      TEXT NOT NULL,   -- what the file holds, x.y.z
    from_version TEXT NOT NULL,   -- what ran when it was uploaded; empty if unknown
    arch         TEXT NOT NULL,
    file_name    TEXT NOT NULL,   -- as the browser sent it, for the page
    size         INTEGER NOT NULL,
    sha256       TEXT NOT NULL,   -- of the file
    released     TEXT NOT NULL,   -- YYYY-MM-DD, from the signed manifest
    signer       TEXT NOT NULL,   -- the identity the signature names
    uploaded_at  TEXT NOT NULL,
    started_at   TEXT,
    started_by   TEXT             -- the name of the parent who said yes
);

-- What a finished installation of an uploaded file leaves for the page: the
-- release date of the version that runs now, which only the signed manifest knows.
ALTER TABLE outbound_box ADD COLUMN installed_version  TEXT NOT NULL DEFAULT '';
ALTER TABLE outbound_box ADD COLUMN installed_released TEXT NOT NULL DEFAULT '';
