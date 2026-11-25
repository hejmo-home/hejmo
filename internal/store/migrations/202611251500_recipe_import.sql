-- Importing a recipe from a web page (ticket 289, ADR 0021 and 0024).
--
-- A page that was read waits here as a draft until a parent saves it: only the parent who read it can open it,
-- and after an hour the page's data and its picture are deleted (the row stays a day, so the page can say so).
-- data is the recipe as the page described it (JSON), picture the page's image already made into the stored JPEG.
CREATE TABLE recipe_drafts (
    id           TEXT PRIMARY KEY,
    household_id TEXT NOT NULL REFERENCES households(id) ON DELETE CASCADE,
    read_by      TEXT NOT NULL REFERENCES people(id) ON DELETE CASCADE,
    url          TEXT NOT NULL,
    host         TEXT NOT NULL,
    data         BLOB NOT NULL,
    picture      BLOB,
    expired      INTEGER NOT NULL DEFAULT 0 CHECK (expired IN (0, 1)),
    read_at      TEXT NOT NULL
);

-- Where an imported recipe came from. The link is the recipe's source and never changes (the store ignores a
-- new source for a recipe that has a row here). lang is the language the page was written in, '' if unknown.
CREATE TABLE recipe_imported (
    recipe_id   TEXT PRIMARY KEY REFERENCES recipes(id) ON DELETE CASCADE,
    url         TEXT NOT NULL,
    host        TEXT NOT NULL,
    lang        TEXT NOT NULL DEFAULT '',
    imported_by TEXT REFERENCES people(id) ON DELETE SET NULL,
    imported_at TEXT NOT NULL
);
CREATE INDEX recipe_imported_url ON recipe_imported (url);

-- The picture of the page as the recipe's cover (at most 1600 px, no metadata); ticket 280 builds its gallery on it.
CREATE TABLE recipe_cover (
    recipe_id TEXT PRIMARY KEY REFERENCES recipes(id) ON DELETE CASCADE,
    jpeg      BLOB NOT NULL,
    host      TEXT NOT NULL DEFAULT ''
);

-- The nutrition values the page stated, per portion (NULL: the page did not state it, never 0). They only fit
-- the page's ingredients and portions: when someone changes either, dropped_* says who changed what and when,
-- and the recipe page calculates the values itself (ticket 288). portions and ing_key are the recipe as saved.
CREATE TABLE recipe_page_nutrition (
    recipe_id     TEXT PRIMARY KEY REFERENCES recipes(id) ON DELETE CASCADE,
    kcal          REAL, kj REAL, fat REAL, saturates REAL, carbs REAL, sugars REAL, protein REAL, salt REAL,
    url           TEXT NOT NULL,
    stated_at     TEXT NOT NULL,
    portions      INTEGER NOT NULL,
    ing_key       TEXT NOT NULL,
    dropped_at    TEXT NOT NULL DEFAULT '',
    dropped_by    TEXT REFERENCES people(id) ON DELETE SET NULL,
    dropped_what  TEXT NOT NULL DEFAULT '' CHECK (dropped_what IN ('', 'portions', 'ingredients')),
    dropped_to    INTEGER NOT NULL DEFAULT 0
);
