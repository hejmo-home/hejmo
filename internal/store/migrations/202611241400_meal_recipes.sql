-- A meal plan entry is free text or a recipe with portions (ticket 281, ADR
-- 0021). recipe_id '' means free text; portions 0 means none (only a recipe
-- has portions, 1 to 99). The text of a recipe entry is the recipe's name: it
-- follows a rename and stays when the recipe is deleted, so the calendar events,
-- the displays and the home page need nothing else. Existing entries stay text.
ALTER TABLE meal_plan ADD COLUMN recipe_id TEXT NOT NULL DEFAULT '';
ALTER TABLE meal_plan ADD COLUMN portions INTEGER NOT NULL DEFAULT 0 CHECK (portions BETWEEN 0 AND 99);
ALTER TABLE meal_manual ADD COLUMN recipe_id TEXT NOT NULL DEFAULT '';
ALTER TABLE meal_manual ADD COLUMN portions INTEGER NOT NULL DEFAULT 0 CHECK (portions BETWEEN 0 AND 99);
ALTER TABLE meal_adjustments ADD COLUMN recipe_id TEXT NOT NULL DEFAULT '';
ALTER TABLE meal_adjustments ADD COLUMN portions INTEGER NOT NULL DEFAULT 0 CHECK (portions BETWEEN 0 AND 99);
CREATE INDEX meal_plan_recipe ON meal_plan (household_id, recipe_id) WHERE recipe_id <> '';
