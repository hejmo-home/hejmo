-- Simple tasks (ADR 0015). Shaped like an iCalendar VTODO so that export
-- needs no remodelling: start_date is DTSTART (DUE for a once task), rrule is
-- the RRULE ('' = once) and every row of task_done is one completed
-- occurrence (a day, in the household's time zone).
CREATE TABLE tasks (
    id            TEXT PRIMARY KEY,
    household_id  TEXT NOT NULL REFERENCES households(id) ON DELETE CASCADE,
    person_id     TEXT NOT NULL REFERENCES people(id) ON DELETE CASCADE,
    title         TEXT NOT NULL,
    emoji         TEXT NOT NULL DEFAULT '',
    rrule         TEXT NOT NULL DEFAULT '',
    start_date    TEXT NOT NULL,                -- 'YYYY-MM-DD'
    part_of_day   TEXT NOT NULL DEFAULT '' CHECK (part_of_day IN ('', 'morning', 'evening')),
    created_at    TEXT NOT NULL,
    updated_at    TEXT NOT NULL
);
CREATE INDEX tasks_household ON tasks (household_id, person_id);

CREATE TABLE task_done (
    task_id      TEXT NOT NULL REFERENCES tasks(id) ON DELETE CASCADE,
    day          TEXT NOT NULL,                 -- 'YYYY-MM-DD'
    completed_at TEXT NOT NULL,
    completed_by TEXT REFERENCES people(id) ON DELETE SET NULL,
    PRIMARY KEY (task_id, day)
);
