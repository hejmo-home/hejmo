-- Ingredients of planned recipes put on a shopping list by hand (ticket 283,
-- ADR 0019 and 0021). One run is one tap of "Zutaten auf die Liste" (a meal or
-- a week). Everything a run did is recorded per list entry and per meal: this
-- is what keeps a meal from landing twice (only missing portions are added),
-- what the list says under an entry ("Für Curry (Di)"), what Undo takes back
-- and the basis for taking quantities back later (ticket 284).

CREATE TABLE meal_shop_runs (
    id           TEXT PRIMARY KEY,
    household_id TEXT NOT NULL REFERENCES households(id) ON DELETE CASCADE,
    list_id      TEXT NOT NULL REFERENCES lists(id) ON DELETE CASCADE,
    list_name    TEXT NOT NULL,
    day          TEXT NOT NULL,                 -- the day it ran, in the household's zone
    created_at   TEXT NOT NULL,
    left_out     TEXT NOT NULL DEFAULT '',      -- pantry articles left out, one per line
    undone_at    TEXT
);
CREATE INDEX meal_shop_runs_household ON meal_shop_runs (household_id, created_at);

-- The meals a run shopped for, with the portions it put on the list.
CREATE TABLE meal_shop_meals (
    run_id      TEXT NOT NULL REFERENCES meal_shop_runs(id) ON DELETE CASCADE,
    day         TEXT NOT NULL,
    mealtime    TEXT NOT NULL,
    recipe_id   TEXT NOT NULL,
    recipe_name TEXT NOT NULL,
    portions    INTEGER NOT NULL,
    PRIMARY KEY (run_id, day, mealtime)
);
CREATE INDEX meal_shop_meals_day ON meal_shop_meals (day, mealtime);

-- What a run did to one list entry, and how it was before and after.
CREATE TABLE meal_shop_steps (
    run_id      TEXT NOT NULL REFERENCES meal_shop_runs(id) ON DELETE CASCADE,
    entry_id    TEXT NOT NULL REFERENCES list_entries(id) ON DELETE CASCADE,
    seq         INTEGER NOT NULL,
    action      TEXT NOT NULL,                  -- new, added or filled
    text        TEXT NOT NULL,
    variety     TEXT NOT NULL DEFAULT '',
    before_base REAL NOT NULL DEFAULT 0,
    before_unit TEXT NOT NULL DEFAULT '',
    after_base  REAL NOT NULL DEFAULT 0,
    after_unit  TEXT NOT NULL DEFAULT '',
    PRIMARY KEY (run_id, entry_id)
);
CREATE INDEX meal_shop_steps_entry ON meal_shop_steps (entry_id);

-- What one meal brought to one entry: its base quantity (pieces, grams or
-- millilitres; none for a kitchen measure or free text).
CREATE TABLE meal_shop_parts (
    run_id      TEXT NOT NULL REFERENCES meal_shop_runs(id) ON DELETE CASCADE,
    entry_id    TEXT NOT NULL REFERENCES list_entries(id) ON DELETE CASCADE,
    day         TEXT NOT NULL,
    mealtime    TEXT NOT NULL,
    recipe_id   TEXT NOT NULL,
    recipe_name TEXT NOT NULL,
    portions    INTEGER NOT NULL,
    qty_base    REAL NOT NULL DEFAULT 0,
    qty_unit    TEXT NOT NULL DEFAULT '',
    PRIMARY KEY (run_id, entry_id, day, mealtime)
);
CREATE INDEX meal_shop_parts_entry ON meal_shop_parts (entry_id);
