-- Badges (ticket 295): ten fixed milestones a person earns once and never
-- loses. Awarded with INSERT OR IGNORE on the unique (person_id, key), so two
-- ticks at the same moment award one badge; no code deletes a row.
-- quiet = 1: the badge was earned while the person's switch "week count and
-- badges" was off. It is stored but never shown as new.
CREATE TABLE badges (
    household_id TEXT NOT NULL REFERENCES households(id) ON DELETE CASCADE,
    person_id    TEXT NOT NULL REFERENCES people(id) ON DELETE CASCADE,
    key          TEXT NOT NULL,
    earned_at    TEXT NOT NULL,                 -- RFC 3339, UTC
    quiet        INTEGER NOT NULL DEFAULT 0 CHECK (quiet IN (0, 1)),
    UNIQUE (person_id, key)
);
CREATE INDEX badges_household ON badges (household_id, person_id);
