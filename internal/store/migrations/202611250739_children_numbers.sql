-- "Wichtige Nummern" for children (ticket 301).
--
-- A contact is shown to the children only when a parent switched it on: off for every contact, the contacts of the
-- people included. And a personal device keeps the kind of screen it said when it paired (phone, tablet or device
-- for what could not be told), because a tablet cannot call and the children's page shows its numbers as text.
ALTER TABLE contacts ADD COLUMN for_children INTEGER NOT NULL DEFAULT 0 CHECK (for_children IN (0, 1));
ALTER TABLE personal_devices ADD COLUMN kind TEXT NOT NULL DEFAULT 'device' CHECK (kind IN ('phone', 'tablet', 'device'));
