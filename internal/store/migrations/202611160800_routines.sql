-- Routines (ADR 0018): the ordered sequence of one person's tasks for a part
-- of the day. A person has at most one routine per part of the day. A task
-- belongs to at most one routine and takes its part of the day from it, so
-- the part is read through the routine and never copied onto the task.
CREATE TABLE routines (
    id            TEXT PRIMARY KEY,
    household_id  TEXT NOT NULL REFERENCES households(id) ON DELETE CASCADE,
    person_id     TEXT NOT NULL REFERENCES people(id) ON DELETE CASCADE,
    name          TEXT NOT NULL,
    part          TEXT NOT NULL CHECK (part IN ('morning', 'afterschool', 'evening')),
    created_at    TEXT NOT NULL,
    updated_at    TEXT NOT NULL,
    UNIQUE (person_id, part)
);
CREATE INDEX routines_household ON routines (household_id);

-- Leaving a routine is done by the store (it keeps the part of the day); the
-- foreign key only makes sure no task points at a routine that is gone.
ALTER TABLE tasks ADD COLUMN routine_id  TEXT REFERENCES routines(id) ON DELETE SET NULL;
ALTER TABLE tasks ADD COLUMN routine_pos INTEGER NOT NULL DEFAULT 0;
CREATE INDEX tasks_routine ON tasks (routine_id);
