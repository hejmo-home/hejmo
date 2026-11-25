-- Own photos of recipes (ticket 280). recipe_ref is the id of an own recipe or of a starter recipe
-- ("starter-<slug>"), so each household can add photos to a starter recipe too; there is no foreign key
-- because starter recipes are not rows. The files lie on the box, never in this table. origin 'web' is a
-- picture taken over from a web page (ticket 289), added_by is empty then. At most one cover per recipe;
-- the code keeps exactly one while photos exist.
CREATE TABLE recipe_photos (
    id           TEXT PRIMARY KEY,
    household_id TEXT NOT NULL REFERENCES households(id) ON DELETE CASCADE,
    recipe_ref   TEXT NOT NULL,
    file_name    TEXT NOT NULL DEFAULT '',
    width        INTEGER NOT NULL CHECK (width > 0),
    height       INTEGER NOT NULL CHECK (height > 0),
    added_by     TEXT REFERENCES people(id) ON DELETE SET NULL,
    origin       TEXT NOT NULL DEFAULT 'upload' CHECK (origin IN ('upload', 'web')),
    added_at     TEXT NOT NULL,                 -- RFC 3339, UTC
    is_cover     INTEGER NOT NULL DEFAULT 0 CHECK (is_cover IN (0, 1))
);
CREATE INDEX recipe_photos_recipe ON recipe_photos (household_id, recipe_ref);
CREATE UNIQUE INDEX recipe_photos_cover ON recipe_photos (household_id, recipe_ref) WHERE is_cover = 1;
