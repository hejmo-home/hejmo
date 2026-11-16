-- Ticket #147: how dates in numbers are written. Existing households read day first (24.12.2026), the closest to what the box showed before.
ALTER TABLE households ADD COLUMN date_format TEXT NOT NULL DEFAULT 'dmy' CHECK (date_format IN ('dmy', 'mdy'));
