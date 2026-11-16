-- Routine templates (ADR 0018): a routine without a person, saved by a parent.
-- The templates that come with Hejmo are code and locale texts, not rows.
-- Using a template copies its steps into new tasks, so nothing here points at
-- a routine or a task: the copy is independent in both directions.
CREATE TABLE routine_templates (
    id             TEXT PRIMARY KEY,
    household_id   TEXT NOT NULL REFERENCES households(id) ON DELETE CASCADE,
    name           TEXT NOT NULL COLLATE NOCASE,
    part           TEXT NOT NULL CHECK (part IN ('morning', 'afterschool', 'evening')),
    origin_routine TEXT NOT NULL DEFAULT '',
    origin_person  TEXT NOT NULL DEFAULT '',
    created_at     TEXT NOT NULL,
    updated_at     TEXT NOT NULL,
    UNIQUE (household_id, name)
);

CREATE TABLE routine_template_steps (
    id          TEXT PRIMARY KEY,
    template_id TEXT NOT NULL REFERENCES routine_templates(id) ON DELETE CASCADE,
    pos         INTEGER NOT NULL,
    title       TEXT NOT NULL,
    emoji       TEXT NOT NULL DEFAULT '',
    rrule       TEXT NOT NULL,
    stars       INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX routine_template_steps_template ON routine_template_steps (template_id, pos);
