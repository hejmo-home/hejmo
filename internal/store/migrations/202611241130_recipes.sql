-- The household's own recipes (ticket 279, ADR 0021). A recipe has a name that
-- is unique in the household ignoring case, its ingredients and its steps as
-- ordered lists of their own (steps are never part of the tips), and free tags.
CREATE TABLE recipes (
    id           TEXT PRIMARY KEY,
    household_id TEXT NOT NULL REFERENCES households(id) ON DELETE CASCADE,
    name         TEXT NOT NULL,
    name_key     TEXT NOT NULL,
    category     TEXT NOT NULL CHECK (category IN ('main', 'side', 'dessert', 'baking', 'breakfast', 'snack')),
    portions     INTEGER NOT NULL CHECK (portions BETWEEN 1 AND 99),
    minutes      INTEGER NOT NULL DEFAULT 0 CHECK (minutes BETWEEN 0 AND 9999), -- 0: not known
    mealtimes    TEXT NOT NULL DEFAULT '',  -- comma-separated, in the order of the day; empty: every mealtime
    tips         TEXT NOT NULL DEFAULT '',
    source       TEXT NOT NULL DEFAULT '',  -- text or a web address
    created_by   TEXT REFERENCES people(id) ON DELETE SET NULL,
    created_at   TEXT NOT NULL,
    updated_by   TEXT REFERENCES people(id) ON DELETE SET NULL,
    updated_at   TEXT NOT NULL
);
CREATE UNIQUE INDEX recipes_name ON recipes (household_id, name_key);

-- An ingredient is an article of the library (article_id: the key of a shipped
-- article or the id of an own one, with an optional variety) or free text.
-- amount 0 means no quantity; unit is a unit of internal/unit or ''.
CREATE TABLE recipe_ingredients (
    recipe_id  TEXT NOT NULL REFERENCES recipes(id) ON DELETE CASCADE,
    position   INTEGER NOT NULL,
    amount     REAL NOT NULL DEFAULT 0 CHECK (amount >= 0),
    unit       TEXT NOT NULL DEFAULT '',
    text       TEXT NOT NULL,
    article_id TEXT NOT NULL DEFAULT '',
    variety    TEXT NOT NULL DEFAULT '',
    PRIMARY KEY (recipe_id, position)
);

CREATE TABLE recipe_steps (
    recipe_id TEXT NOT NULL REFERENCES recipes(id) ON DELETE CASCADE,
    position  INTEGER NOT NULL,
    text      TEXT NOT NULL,
    PRIMARY KEY (recipe_id, position)
);

CREATE TABLE recipe_tags (
    recipe_id TEXT NOT NULL REFERENCES recipes(id) ON DELETE CASCADE,
    position  INTEGER NOT NULL,
    tag       TEXT NOT NULL,
    PRIMARY KEY (recipe_id, position)
);
