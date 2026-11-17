-- Photo screensaver (ADR 0020, ticket 178). A photo lives in exactly one album.
-- An album is uploaded (folder = '') or comes from a subfolder of the box's photo
-- folder (folder = its name); the default album takes the first uploads and the
-- photos lying directly in the folder. The box keeps only a copy shrunk to
-- display size without location data; bytes is the size of that copy.
CREATE TABLE albums (
    id           TEXT PRIMARY KEY,
    household_id TEXT NOT NULL REFERENCES households(id) ON DELETE CASCADE,
    name         TEXT NOT NULL,
    folder       TEXT NOT NULL DEFAULT '',
    is_default   INTEGER NOT NULL DEFAULT 0,
    created_at   TEXT NOT NULL
);
CREATE UNIQUE INDEX albums_name ON albums(household_id, name COLLATE NOCASE);
CREATE UNIQUE INDEX albums_folder ON albums(household_id, folder) WHERE folder != '';
CREATE UNIQUE INDEX albums_default ON albums(household_id) WHERE is_default = 1;

CREATE TABLE photos (
    id           TEXT PRIMARY KEY,
    household_id TEXT NOT NULL REFERENCES households(id) ON DELETE CASCADE,
    album_id     TEXT NOT NULL REFERENCES albums(id) ON DELETE CASCADE,
    name         TEXT NOT NULL,
    hidden       INTEGER NOT NULL DEFAULT 0,
    bytes        INTEGER NOT NULL,
    width        INTEGER NOT NULL DEFAULT 0,
    height       INTEGER NOT NULL DEFAULT 0,
    source_path  TEXT NOT NULL DEFAULT '',   -- a folder photo: its path in the folder
    source_stamp TEXT NOT NULL DEFAULT '',   -- size and time of that file when it was copied
    created_at   TEXT NOT NULL
);
CREATE INDEX photos_album ON photos(album_id, created_at);
CREATE UNIQUE INDEX photos_source ON photos(household_id, source_path) WHERE source_path != '';

-- The albums a display shows. No row means all albums, so a deleted album that
-- was a display's only choice sends it back to all albums.
CREATE TABLE display_albums (
    display_id TEXT NOT NULL REFERENCES displays(id) ON DELETE CASCADE,
    album_id   TEXT NOT NULL REFERENCES albums(id) ON DELETE CASCADE,
    PRIMARY KEY (display_id, album_id)
);

ALTER TABLE displays ADD COLUMN saver_enabled INTEGER NOT NULL DEFAULT 1;
ALTER TABLE displays ADD COLUMN saver_idle_minutes INTEGER NOT NULL DEFAULT 10 CHECK (saver_idle_minutes BETWEEN 1 AND 240);
ALTER TABLE displays ADD COLUMN saver_interval_minutes INTEGER NOT NULL DEFAULT 1 CHECK (saver_interval_minutes BETWEEN 1 AND 240);
-- A new photo flashes an E-Ink screen: every 15 minutes there.
UPDATE displays SET saver_interval_minutes = 15 WHERE type IN ('eink_bw', 'eink_color');

ALTER TABLE households ADD COLUMN photo_limit_gb INTEGER NOT NULL DEFAULT 2 CHECK (photo_limit_gb BETWEEN 1 AND 100000);
