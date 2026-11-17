-- The view of lists (ADR 0019, ticket 166): tiles or rows. A person chooses once
-- for all lists; a person without a row has the default of the role. A display
-- is not a person and keeps its own view; one without a row shows tiles.
CREATE TABLE person_list_view (
    person_id TEXT PRIMARY KEY REFERENCES people(id) ON DELETE CASCADE,
    view      TEXT NOT NULL CHECK (view IN ('tiles', 'list'))
);
CREATE TABLE display_list_view (
    display_id TEXT PRIMARY KEY REFERENCES displays(id) ON DELETE CASCADE,
    view       TEXT NOT NULL CHECK (view IN ('tiles', 'list'))
);
