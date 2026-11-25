-- The shopping list from the meal plan, by itself (ticket 285, ADR 0021): the
-- household chooses between "by hand" (no row, the default) and "automatic":
-- every day at the household's time the ingredients of the meals of the next
-- days go on a list. list_id is the list they go on; without one the runner
-- takes the list of the last run, else the first list with suggestions. lang is
-- the language of the parent who saved the setting: the run writes its entries
-- in it (like the rules of "always on the list"); empty until it is saved.
CREATE TABLE meal_shop_settings (
    household_id TEXT PRIMARY KEY REFERENCES households(id) ON DELETE CASCADE,
    auto         INTEGER NOT NULL DEFAULT 0 CHECK (auto IN (0, 1)),
    days         INTEGER NOT NULL DEFAULT 7 CHECK (days BETWEEN 1 AND 14),
    list_id      TEXT REFERENCES lists(id) ON DELETE SET NULL,
    lang         TEXT NOT NULL DEFAULT ''
);
