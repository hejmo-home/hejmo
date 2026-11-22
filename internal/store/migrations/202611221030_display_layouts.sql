-- Display layouts (ticket 180, ADR 0020): per display the people it shows and
-- their base order (see layout.Order), the switches and order of the layout blocks, and the list of the
-- block "one list". A display without a row shows today's layout. The blocks are
-- a JSON array of {id, on} in order, so a new block needs no migration; people
-- is a JSON array of person IDs. The list name is kept so a deleted list can be
-- named on the settings page.
CREATE TABLE display_layout (
    display_id TEXT PRIMARY KEY REFERENCES displays(id) ON DELETE CASCADE,
    whole      INTEGER NOT NULL CHECK (whole IN (0, 1)),
    rotate     INTEGER NOT NULL CHECK (rotate IN (0, 1)),
    people     TEXT NOT NULL CHECK (json_valid(people)),
    blocks     TEXT NOT NULL CHECK (json_valid(blocks)),
    list_id    TEXT NOT NULL,
    list_name  TEXT NOT NULL
);
-- How the display hangs, as it last reported (the layout preview follows it).
ALTER TABLE displays ADD COLUMN portrait INTEGER NOT NULL DEFAULT 0 CHECK (portrait IN (0, 1));
