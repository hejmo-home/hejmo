-- A meal plan template slot can hold a recipe (ticket 282, ADR 0021). recipe_id
-- '' means free text. The text of a recipe slot is the recipe's name: it follows
-- a rename and stays when the recipe is deleted, so the slot then is that name in
-- free text. Applying the template plans the recipe with as many portions as
-- there are people in the household; the template holds no portions.
ALTER TABLE meal_template_entries ADD COLUMN recipe_id TEXT NOT NULL DEFAULT '';
CREATE INDEX meal_template_entries_recipe ON meal_template_entries (recipe_id) WHERE recipe_id <> '';
