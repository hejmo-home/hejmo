-- Diet and allergies per person (ADR 0019, ticket 163). Health data: they stay
-- in the box and are never part of a sync. A person without a row eats
-- everything and has no allergies.
CREATE TABLE person_food (
    person_id TEXT PRIMARY KEY REFERENCES people(id) ON DELETE CASCADE,
    diet      TEXT NOT NULL DEFAULT 'omnivore',
    allergens TEXT NOT NULL DEFAULT '',  -- keys of the 14 EU allergens, comma-separated
    other     TEXT NOT NULL DEFAULT ''   -- free text the box cannot check
);
