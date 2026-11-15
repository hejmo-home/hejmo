-- Waste collection reminder (ticket 6.7, ADR 0015/0016): a read-only calendar
-- can be marked as the household's waste collection calendar, and a parent can
-- leave one free-text note for a day, shown in the tomorrow tile.
ALTER TABLE calendars ADD COLUMN waste INTEGER NOT NULL DEFAULT 0;

CREATE TABLE day_notes (
  household_id TEXT NOT NULL REFERENCES households(id) ON DELETE CASCADE,
  day          TEXT NOT NULL, -- YYYY-MM-DD in the household's time zone
  text         TEXT NOT NULL,
  PRIMARY KEY (household_id, day)
);
