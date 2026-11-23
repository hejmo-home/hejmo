-- Quantities and the article library on lists (ADR 0019, ticket 165).
-- An entry may come from an article (article_id: the key of a shipped article,
-- the id of an own one; '' for free text) and may carry a quantity: qty_base is
-- exact, in pieces, grams or millilitres, qty_unit the unit it is shown in
-- ('' without quantity). The words of an entry stay in text.
ALTER TABLE list_entries ADD COLUMN article_id TEXT NOT NULL DEFAULT '';
ALTER TABLE list_entries ADD COLUMN qty_base REAL NOT NULL DEFAULT 0;
ALTER TABLE list_entries ADD COLUMN qty_unit TEXT NOT NULL DEFAULT '';

-- A list offers suggestions and quantities, or only the plain add row.
ALTER TABLE lists ADD COLUMN suggestions INTEGER NOT NULL DEFAULT 1 CHECK (suggestions IN (0, 1));

-- The units a parent switched on. A household without a row follows the
-- country of its home (internal/unit.Preset).
CREATE TABLE household_units (
    household_id TEXT PRIMARY KEY REFERENCES households(id) ON DELETE CASCADE,
    active       TEXT NOT NULL -- unit keys, comma-separated
);

-- The unit last used for an article: the next add preselects it.
CREATE TABLE article_last_unit (
    household_id TEXT NOT NULL REFERENCES households(id) ON DELETE CASCADE,
    article_id   TEXT NOT NULL,
    unit         TEXT NOT NULL,
    PRIMARY KEY (household_id, article_id)
);
