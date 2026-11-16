-- First steps card on the start page (ticket 133): a parent can hide it for the
-- whole household, and skip single steps. The skipped steps are a comma-separated
-- list of step ids (see FirstStepIDs in the store); empty means none.
ALTER TABLE households ADD COLUMN first_steps_hidden INTEGER NOT NULL DEFAULT 0 CHECK (first_steps_hidden IN (0, 1));
ALTER TABLE households ADD COLUMN first_steps_skipped TEXT NOT NULL DEFAULT '';
