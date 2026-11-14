-- Household settings of ADR 0016: how times and temperatures are shown.
ALTER TABLE households ADD COLUMN time_format TEXT NOT NULL DEFAULT '24h' CHECK (time_format IN ('24h', '12h'));
ALTER TABLE households ADD COLUMN temperature_unit TEXT NOT NULL DEFAULT 'c' CHECK (temperature_unit IN ('c', 'f'));
