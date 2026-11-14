-- Display setting "show tomorrow block" (ADR 0016): off by default.
ALTER TABLE displays ADD COLUMN show_tomorrow INTEGER NOT NULL DEFAULT 0;
