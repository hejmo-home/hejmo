-- A star booking keeps the last name of the person who booked it once that
-- person is removed (product owner decision Q2). booked_by itself turns NULL
-- on removal (ON DELETE SET NULL); this column is filled just before, so a
-- booking with booked_by NULL and a name was made by someone who is gone, and
-- one without a name was made by a display.
ALTER TABLE star_log ADD COLUMN booked_by_name TEXT NOT NULL DEFAULT '';

CREATE TRIGGER star_log_keep_booker_name BEFORE DELETE ON people
BEGIN
    UPDATE star_log SET booked_by_name = OLD.name WHERE booked_by = OLD.id;
END;
