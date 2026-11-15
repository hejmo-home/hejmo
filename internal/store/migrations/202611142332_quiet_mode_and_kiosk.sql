-- Quiet mode and kiosk adapter per display (ADR 0005, 0015). Quiet mode is a
-- daily window "HH:MM" to "HH:MM" in the household's time zone, off by default.
-- The kiosk address is the FreeKiosk REST API of the display's tablet; its API
-- key is sealed (internal/secret) and never leaves the box.
ALTER TABLE displays ADD COLUMN quiet_enabled INTEGER NOT NULL DEFAULT 0;
ALTER TABLE displays ADD COLUMN quiet_from TEXT NOT NULL DEFAULT '22:00';
ALTER TABLE displays ADD COLUMN quiet_to TEXT NOT NULL DEFAULT '06:00';
ALTER TABLE displays ADD COLUMN kiosk_url TEXT NOT NULL DEFAULT '';
ALTER TABLE displays ADD COLUMN kiosk_key TEXT NOT NULL DEFAULT '';
