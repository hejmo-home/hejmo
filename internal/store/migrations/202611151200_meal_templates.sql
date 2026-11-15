-- Meal plan templates (ticket 6.9). A template is a week pattern that fills the
-- meal plan every X weeks from a start week; a week's change to one meal is an
-- adjustment that wins over the template; a hand-typed entry is the fallback.
-- meal_plan stays what the calendar shows: the effective meal per day and
-- mealtime, with where it came from (source) so that days before today keep
-- what was shown when a template changes.

CREATE TABLE meal_templates (
    id            TEXT PRIMARY KEY,
    household_id  TEXT NOT NULL REFERENCES households(id) ON DELETE CASCADE,
    name          TEXT NOT NULL,
    all_mealtimes INTEGER NOT NULL CHECK (all_mealtimes IN (0, 1)),
    mealtime      TEXT NOT NULL DEFAULT '' CHECK (mealtime IN ('', 'breakfast', 'lunch', 'snack', 'dinner')),
    every         INTEGER NOT NULL CHECK (every BETWEEN 1 AND 52),
    start_week    TEXT NOT NULL,                  -- the Monday of the start week, 'YYYY-MM-DD'
    created_at    TEXT NOT NULL,
    updated_at    TEXT NOT NULL
);
CREATE INDEX meal_templates_household ON meal_templates (household_id);

CREATE TABLE meal_template_entries (
    template_id TEXT NOT NULL REFERENCES meal_templates(id) ON DELETE CASCADE,
    weekday     INTEGER NOT NULL CHECK (weekday BETWEEN 1 AND 7),  -- 1 Monday to 7 Sunday
    mealtime    TEXT NOT NULL CHECK (mealtime IN ('breakfast', 'lunch', 'snack', 'dinner')),
    text        TEXT NOT NULL,
    PRIMARY KEY (template_id, weekday, mealtime)
);

-- The hand-typed entries: what a cell shows when no template fills it.
CREATE TABLE meal_manual (
    household_id TEXT NOT NULL REFERENCES households(id) ON DELETE CASCADE,
    day          TEXT NOT NULL,
    mealtime     TEXT NOT NULL CHECK (mealtime IN ('breakfast', 'lunch', 'snack', 'dinner')),
    text         TEXT NOT NULL,
    PRIMARY KEY (household_id, day, mealtime)
);
INSERT INTO meal_manual (household_id, day, mealtime, text) SELECT household_id, day, mealtime, text FROM meal_plan;

-- A week's change where a template fills the cell. An empty text is an entry
-- emptied for that week.
CREATE TABLE meal_adjustments (
    household_id TEXT NOT NULL REFERENCES households(id) ON DELETE CASCADE,
    day          TEXT NOT NULL,
    mealtime     TEXT NOT NULL CHECK (mealtime IN ('breakfast', 'lunch', 'snack', 'dinner')),
    text         TEXT NOT NULL,
    PRIMARY KEY (household_id, day, mealtime)
);

ALTER TABLE meal_plan ADD COLUMN source TEXT NOT NULL DEFAULT 'manual' CHECK (source IN ('manual', 'template', 'adjusted'));
ALTER TABLE meal_plan ADD COLUMN template_id TEXT NOT NULL DEFAULT '';
ALTER TABLE meal_plan ADD COLUMN template_dish TEXT NOT NULL DEFAULT '';
