-- The interface language a person chose in the language switcher (ticket 134).
-- NULL means no choice yet: the browser's language decides.
ALTER TABLE accounts ADD COLUMN language TEXT;
