-- Display themes (ADR 0015): light, dark or automatic by sunrise and sunset,
-- each switch shifted by its own offset in minutes (-180..180). New displays
-- get the default of their type when a parent confirms the pairing.
ALTER TABLE displays ADD COLUMN theme TEXT NOT NULL DEFAULT 'light' CHECK (theme IN ('light', 'dark', 'auto'));
ALTER TABLE displays ADD COLUMN sunrise_offset INTEGER NOT NULL DEFAULT 0 CHECK (sunrise_offset BETWEEN -180 AND 180);
ALTER TABLE displays ADD COLUMN sunset_offset INTEGER NOT NULL DEFAULT 0 CHECK (sunset_offset BETWEEN -180 AND 180);
-- Already paired normal screens get their default too.
UPDATE displays SET theme = 'auto' WHERE type = 'screen';
