-- The Slava of the Serbian Orthodox Church (ticket 338).
-- The calendar style of the Orthodox church is households.orthodox_calendar, which the religious holidays of
-- ticket 339 added (migration 202611251630); the Orthodox name days follow the same column.

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
