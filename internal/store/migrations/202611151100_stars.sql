-- Stars (ADR 0018). A task has a star value; ticking it off credits it,
-- unticking deducts it, and every booking goes into the person's star log.
ALTER TABLE tasks ADD COLUMN stars INTEGER NOT NULL DEFAULT 0 CHECK (stars >= 0);

-- What a tick credited, so unticking deducts exactly that.
ALTER TABLE task_done ADD COLUMN stars INTEGER NOT NULL DEFAULT 0;

-- The star log belongs to the person, not to a household or a membership, so
-- that linked households (V3) can share a child's star account. The balance is
-- the sum of delta. A booking stays when its task is deleted: task_title keeps
-- what it was for.
CREATE TABLE star_log (
    id          TEXT PRIMARY KEY,
    person_id   TEXT NOT NULL REFERENCES people(id) ON DELETE CASCADE,
    delta       INTEGER NOT NULL CHECK (delta <> 0),
    note        TEXT NOT NULL DEFAULT '',                          -- manual bookings
    task_id     TEXT REFERENCES tasks(id) ON DELETE SET NULL,      -- tick bookings
    task_title  TEXT NOT NULL DEFAULT '',
    day         TEXT NOT NULL DEFAULT '',                          -- 'YYYY-MM-DD' of the ticked occurrence
    booked_by   TEXT REFERENCES people(id) ON DELETE SET NULL,     -- NULL: a display
    created_at  TEXT NOT NULL
);
CREATE INDEX star_log_person ON star_log (person_id, created_at);
