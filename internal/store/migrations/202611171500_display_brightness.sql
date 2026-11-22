-- Display brightness (ticket 179, ADR 0020): per display Day and Evening in
-- percent (the switch follows the sun like the automatic theme), what quiet
-- mode does to the light (off or dimmed to a level), how long a touch wakes a
-- display that quiet mode switched off, and whether the sun or the device's
-- light sensor regulates it.
ALTER TABLE displays ADD COLUMN bright_day INTEGER NOT NULL DEFAULT 100 CHECK (bright_day BETWEEN 1 AND 100);
ALTER TABLE displays ADD COLUMN bright_evening INTEGER NOT NULL DEFAULT 40 CHECK (bright_evening BETWEEN 1 AND 100);
ALTER TABLE displays ADD COLUMN bright_quiet_mode TEXT NOT NULL DEFAULT 'off' CHECK (bright_quiet_mode IN ('off', 'dim'));
ALTER TABLE displays ADD COLUMN bright_quiet INTEGER NOT NULL DEFAULT 5 CHECK (bright_quiet BETWEEN 1 AND 100);
ALTER TABLE displays ADD COLUMN wake_minutes INTEGER NOT NULL DEFAULT 1 CHECK (wake_minutes BETWEEN 1 AND 30);
ALTER TABLE displays ADD COLUMN light_source TEXT NOT NULL DEFAULT 'sun' CHECK (light_source IN ('sun', 'room'));
