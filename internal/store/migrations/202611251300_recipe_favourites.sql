-- A recipe is a favourite per person: "Mia likes it" (ticket 287, ADR 0021).
-- One row per person and recipe; a row goes with its person or its recipe.
-- "Last cooked" is not stored: it is the latest planned day of the recipe in
-- meal_plan that is over, so it follows the plan.
CREATE TABLE recipe_favourites (
    recipe_id TEXT NOT NULL REFERENCES recipes(id) ON DELETE CASCADE,
    person_id TEXT NOT NULL REFERENCES people(id) ON DELETE CASCADE,
    PRIMARY KEY (recipe_id, person_id)
);
CREATE INDEX recipe_favourites_person ON recipe_favourites (person_id);
