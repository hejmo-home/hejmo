-- Contacts over CardDAV (ticket 299, ADR 0021).
--
-- What a phone saves on a card that Hejmo does not model (organisation, web addresses, further addresses, social
-- profiles) is kept, as JSON, and written back to the phone, so a change made in Hejmo never takes it from the phone.
ALTER TABLE contacts ADD COLUMN extra TEXT NOT NULL DEFAULT '';
