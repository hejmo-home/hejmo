-- The article library (ADR 0019, ticket 164). The articles Hejmo ships are
-- code and a data file, not rows; here live a household's own articles, which
-- shipped articles a parent hid by hand, and the setting that hides articles
-- with an allergen.
CREATE TABLE articles (
    id             TEXT PRIMARY KEY,
    household_id   TEXT NOT NULL REFERENCES households(id) ON DELETE CASCADE,
    name           TEXT NOT NULL, -- one name for every language
    dept           TEXT NOT NULL,
    kind           TEXT NOT NULL CHECK (kind IN ('meat', 'fish', 'veg', 'vegan', 'nonfood')),
    allergens      TEXT NOT NULL DEFAULT '', -- keys of the 14 EU allergens, comma-separated
    unit1          TEXT NOT NULL,            -- the first default unit, preselected when adding
    unit2          TEXT NOT NULL DEFAULT '', -- the optional second one
    -- What the article shows: nothing (the symbol of the department), a chosen
    -- symbol (an emoji), an uploaded photo or an image fetched from the web.
    pic_source     TEXT NOT NULL DEFAULT '' CHECK (pic_source IN ('', 'symbol', 'upload', 'web')),
    symbol         TEXT NOT NULL DEFAULT '',
    picture        BLOB,                     -- the stored JPEG, at most 512 px, no metadata
    picture_host   TEXT NOT NULL DEFAULT '', -- the site a web image came from
    picture_from_w INTEGER NOT NULL DEFAULT 0,
    picture_from_h INTEGER NOT NULL DEFAULT 0,
    picture_from_b INTEGER NOT NULL DEFAULT 0,
    picture_at     TEXT NOT NULL DEFAULT '',
    created_by     TEXT REFERENCES people(id) ON DELETE SET NULL,
    created_at     TEXT NOT NULL,
    updated_at     TEXT NOT NULL
);
CREATE INDEX articles_household ON articles (household_id, created_at);

-- A shipped article a parent hid by hand (key of the data file).
CREATE TABLE article_hidden (
    household_id TEXT NOT NULL REFERENCES households(id) ON DELETE CASCADE,
    article_key  TEXT NOT NULL,
    PRIMARY KEY (household_id, article_key)
);

-- The experimental setting that hides articles with an allergen. Off by
-- default; switching it on needs the confirmed notice (who, when).
CREATE TABLE library_settings (
    household_id  TEXT PRIMARY KEY REFERENCES households(id) ON DELETE CASCADE,
    hide_allergen INTEGER NOT NULL DEFAULT 0,
    everyone      INTEGER NOT NULL DEFAULT 1, -- every person with allergies; else only hide_people
    hide_people   TEXT NOT NULL DEFAULT '',   -- person ids, comma-separated
    confirmed_by  TEXT REFERENCES people(id) ON DELETE SET NULL,
    confirmed_at  TEXT NOT NULL DEFAULT ''
);
