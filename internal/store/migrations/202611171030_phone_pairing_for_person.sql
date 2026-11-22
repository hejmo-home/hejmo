-- Re-pairing a phone for another person (#198): the pairing code a parent
-- shows after "Pair for another person" already says whom the phone is for, so
-- the waiting phone can show it and the parent's confirmation starts with it.
ALTER TABLE phone_pairings ADD COLUMN for_person_id TEXT REFERENCES people(id) ON DELETE SET NULL;
