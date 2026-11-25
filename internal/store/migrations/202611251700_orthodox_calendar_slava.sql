-- Orthodox name days and the Serbian Slava (ticket 338).
-- orthodox_calendar is the calendar style of the household's Orthodox church. The same column, with the same
-- definition, is added by the religious holidays of ticket 339 (migration 202611251630): whichever of the two
-- reaches main second drops its ALTER line, so the two converge on one column.
ALTER TABLE households ADD COLUMN orthodox_calendar TEXT NOT NULL DEFAULT 'revised_julian' CHECK (orthodox_calendar IN ('revised_julian', 'julian'));

-- The Slava: one feast of the household, off by default and independent of name days. slava is the saint (an id of
-- the embedded list, 'own' or NULL until one was chosen); slava_own_name (at most 60 characters) and slava_own_day
-- ("MM-DD" of our calendar) belong to an own Slava. Switching it off keeps all of it.
ALTER TABLE households ADD COLUMN slava_on INTEGER NOT NULL DEFAULT 0;
ALTER TABLE households ADD COLUMN slava TEXT;
ALTER TABLE households ADD COLUMN slava_own_name TEXT NOT NULL DEFAULT '';
ALTER TABLE households ADD COLUMN slava_own_day TEXT NOT NULL DEFAULT '';

-- The generated Slava event of the calendar Birthdays carries no contact and no age; the event page recomputes
-- what it says from the household.
ALTER TABLE events ADD COLUMN slava INTEGER NOT NULL DEFAULT 0;
