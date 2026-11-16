-- Subtasks (ADR 0018): items a task is split into, each ticked off on its own.
-- One level only, at most 10 per task (checked by the store). A tick belongs to
-- one occurrence of the task, a day in the household's time zone, like
-- task_done: the next occurrence starts open again. Stars belong to the task.
CREATE TABLE subtasks (
    id          TEXT PRIMARY KEY,
    task_id     TEXT NOT NULL REFERENCES tasks(id) ON DELETE CASCADE,
    title       TEXT NOT NULL,
    emoji       TEXT NOT NULL DEFAULT '',
    pos         INTEGER NOT NULL,
    created_at  TEXT NOT NULL,
    updated_at  TEXT NOT NULL
);
CREATE INDEX subtasks_task ON subtasks (task_id, pos);

CREATE TABLE subtask_done (
    subtask_id   TEXT NOT NULL REFERENCES subtasks(id) ON DELETE CASCADE,
    day          TEXT NOT NULL,                 -- 'YYYY-MM-DD'
    completed_at TEXT NOT NULL,
    completed_by TEXT REFERENCES people(id) ON DELETE SET NULL,
    PRIMARY KEY (subtask_id, day)
);
