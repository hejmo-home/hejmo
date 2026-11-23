-- Pause days (ticket 294): spans of days on which routines do not count. A pause
-- with no row in pause_person is for the whole family, read like an event's audience.
CREATE TABLE pauses (
    id           TEXT PRIMARY KEY,
    household_id TEXT NOT NULL REFERENCES households(id) ON DELETE CASCADE,
    from_day     TEXT NOT NULL,                 -- 'YYYY-MM-DD'
    to_day       TEXT NOT NULL,                 -- 'YYYY-MM-DD', not before from_day
    created_by   TEXT REFERENCES people(id) ON DELETE SET NULL,
    created_at   TEXT NOT NULL
);
CREATE INDEX pauses_household ON pauses (household_id, from_day);

CREATE TABLE pause_person (
    pause_id  TEXT NOT NULL REFERENCES pauses(id) ON DELETE CASCADE,
    person_id TEXT NOT NULL REFERENCES people(id) ON DELETE CASCADE,
    PRIMARY KEY (pause_id, person_id)
);

-- The switch "week count and badges": on for children, off for adults.
ALTER TABLE people ADD COLUMN streaks_shown INTEGER NOT NULL DEFAULT 0;
UPDATE people SET streaks_shown = 1
 WHERE id IN (SELECT person_id FROM household_members WHERE role = 'child');
