-- Articles that are always on the list (ticket 168, ADR 0019, Q27 and Q43 to
-- Q45): per article one rule, the household's time of day and holiday, the
-- runs of a day (what the list page says came by itself) and where an entry
-- came from.

-- One rule per article of the household (the key of a shipped article or the
-- id of an own one). A rule whose list was deleted keeps everything but the
-- list: it is paused. list_name is the list's name for the sentence that says
-- which list is gone.
CREATE TABLE list_always (
    household_id TEXT NOT NULL REFERENCES households(id) ON DELETE CASCADE,
    article_id   TEXT NOT NULL,
    enabled      INTEGER NOT NULL DEFAULT 1 CHECK (enabled IN (0, 1)),
    list_id      TEXT REFERENCES lists(id) ON DELETE SET NULL,
    list_name    TEXT NOT NULL DEFAULT '',
    weekday      INTEGER NOT NULL CHECK (weekday BETWEEN 1 AND 7), -- 1 Monday to 7 Sunday
    every        INTEGER NOT NULL CHECK (every BETWEEN 1 AND 52),
    start_week   TEXT NOT NULL,                                   -- the Monday of the first week
    since        TEXT NOT NULL,                                   -- due only after this day
    qty_base     REAL NOT NULL DEFAULT 0,
    qty_unit     TEXT NOT NULL DEFAULT '',
    lang         TEXT NOT NULL DEFAULT 'de',
    PRIMARY KEY (household_id, article_id)
);

-- The household's time of day for all rules, its one holiday (both days
-- included, '' for none) and the day the rules last ran.
CREATE TABLE list_always_settings (
    household_id TEXT PRIMARY KEY REFERENCES households(id) ON DELETE CASCADE,
    at           TEXT NOT NULL DEFAULT '03:00',
    holiday_from TEXT NOT NULL DEFAULT '',
    holiday_to   TEXT NOT NULL DEFAULT '',
    last_run     TEXT NOT NULL DEFAULT ''
);

-- What a rule did on a day, for the line "came by itself today".
CREATE TABLE list_always_runs (
    household_id TEXT NOT NULL REFERENCES households(id) ON DELETE CASCADE,
    article_id   TEXT NOT NULL,
    list_id      TEXT NOT NULL REFERENCES lists(id) ON DELETE CASCADE,
    day          TEXT NOT NULL,
    text         TEXT NOT NULL,
    action       TEXT NOT NULL, -- new, added, filled or exists
    part_base    REAL NOT NULL DEFAULT 0,
    part_unit    TEXT NOT NULL DEFAULT '',
    total_base   REAL NOT NULL DEFAULT 0,
    total_unit   TEXT NOT NULL DEFAULT '',
    PRIMARY KEY (household_id, article_id, day)
);

-- Where an entry came from: put on a list by a rule (auto), the quantity the
-- rule brought (auto_part) and the quantity that was already entered (auto_prior),
-- both exact, in the base of ADR 0019. A quantity changed by hand clears them.
ALTER TABLE list_entries ADD COLUMN auto INTEGER NOT NULL DEFAULT 0;
ALTER TABLE list_entries ADD COLUMN auto_part REAL NOT NULL DEFAULT 0;
ALTER TABLE list_entries ADD COLUMN auto_prior REAL NOT NULL DEFAULT 0;
