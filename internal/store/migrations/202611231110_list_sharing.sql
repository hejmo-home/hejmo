-- Lists shared with other apps (ticket 167, ADR 0019): a parent switches a list
-- on and Apple Reminders and other CalDAV clients get it as a task collection.
-- New lists are off. An entry may carry a note: what a person writes in
-- Reminders, or in the editor of Hejmo (at most 500 characters).
ALTER TABLE lists ADD COLUMN shared INTEGER NOT NULL DEFAULT 0 CHECK (shared IN (0, 1));
ALTER TABLE list_entries ADD COLUMN note TEXT NOT NULL DEFAULT '';
