-- Lists and their entries (ADR 0015). A list belongs to the household and has
-- a colour (a person colour slot, by name), a black-and-white symbol and a
-- sorting. An entry is text only. Entries keep their position even in an
-- alphabetical list, so switching back to manual restores the manual order.
CREATE TABLE lists (
    id            TEXT PRIMARY KEY,
    household_id  TEXT NOT NULL REFERENCES households(id) ON DELETE CASCADE,
    name          TEXT NOT NULL,
    color         TEXT NOT NULL,
    symbol        TEXT NOT NULL,
    sort          TEXT NOT NULL DEFAULT 'manual' CHECK (sort IN ('manual', 'alphabetical')),
    created_at    TEXT NOT NULL,
    updated_at    TEXT NOT NULL
);
CREATE INDEX lists_household ON lists (household_id, created_at);

CREATE TABLE list_entries (
    id          TEXT PRIMARY KEY,
    list_id     TEXT NOT NULL REFERENCES lists(id) ON DELETE CASCADE,
    text        TEXT NOT NULL,
    done        INTEGER NOT NULL DEFAULT 0 CHECK (done IN (0, 1)),
    position    INTEGER NOT NULL,
    created_at  TEXT NOT NULL
);
CREATE INDEX list_entries_list ON list_entries (list_id, position);
