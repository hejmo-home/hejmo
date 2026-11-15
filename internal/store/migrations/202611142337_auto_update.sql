-- Auto-update of ADR 0018: the household's choice (on, off, notify only) and
-- the last update event whose notice parents have dismissed.
ALTER TABLE households ADD COLUMN auto_update TEXT NOT NULL DEFAULT 'on' CHECK (auto_update IN ('on', 'off', 'notify'));
ALTER TABLE households ADD COLUMN update_ack TEXT NOT NULL DEFAULT '';
