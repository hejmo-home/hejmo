-- A task's optional free-text notes (ADR 0018). They are the DESCRIPTION of the
-- task's VTODO and can be changed from CalDAV clients.
ALTER TABLE tasks ADD COLUMN notes TEXT NOT NULL DEFAULT '';
