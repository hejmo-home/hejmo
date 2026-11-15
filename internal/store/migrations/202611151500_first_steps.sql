-- First steps card on the start page (ticket 133): a parent can hide it for the
-- whole household.
ALTER TABLE households ADD COLUMN first_steps_hidden INTEGER NOT NULL DEFAULT 0 CHECK (first_steps_hidden IN (0, 1));
