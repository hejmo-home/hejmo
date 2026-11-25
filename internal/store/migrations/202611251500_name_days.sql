-- Name days in the Birthdays calendar (ticket 318).
-- The household chooses once whether name days are in the calendar and after
-- which church. name_day_list is free text on purpose (no CHECK): further lists
-- (tickets 338, 339) are new values, not a new migration. NULL until a church
-- was chosen; switching name days off keeps it.
ALTER TABLE households ADD COLUMN name_days_on INTEGER NOT NULL DEFAULT 0;
ALTER TABLE households ADD COLUMN name_day_list TEXT;
-- The first word of the titles of the events ("Namenstag"), in the language of the parent who switched name days
-- on, like the name of the calendar.
ALTER TABLE households ADD COLUMN name_day_word TEXT NOT NULL DEFAULT '';

-- What a contact says about its name day: auto (the usual day of the first
-- given name), entry (another day of the list), name (another name) or none.
-- The choices stay when name days are switched off.
ALTER TABLE contacts ADD COLUMN name_day_mode TEXT NOT NULL DEFAULT 'auto' CHECK (name_day_mode IN ('auto', 'entry', 'name', 'none'));
ALTER TABLE contacts ADD COLUMN name_day_entry TEXT NOT NULL DEFAULT '';
ALTER TABLE contacts ADD COLUMN name_day_name TEXT NOT NULL DEFAULT '';
ALTER TABLE contacts ADD COLUMN name_day_by TEXT REFERENCES people(id) ON DELETE SET NULL;

-- A name-day event of the generated calendar Birthdays names its contact (the
-- event page links to it); it carries no age. The events go with the contact.
ALTER TABLE events ADD COLUMN name_day_contact TEXT REFERENCES contacts(id) ON DELETE CASCADE;
CREATE INDEX events_name_day ON events (name_day_contact) WHERE name_day_contact IS NOT NULL;
