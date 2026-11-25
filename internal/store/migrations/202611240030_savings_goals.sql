-- The savings goal (ticket 293): one reward a person saves for. The row stays
-- after the goal ended, with why and when, so the child's page can say so for
-- 7 days or until a new goal is chosen. A goal the person cleared by hand
-- leaves no row. Progress is the plain star balance; nothing is set aside.
CREATE TABLE savings_goals (
    person_id     TEXT PRIMARY KEY REFERENCES people(id) ON DELETE CASCADE,
    reward_id     TEXT REFERENCES rewards(id) ON DELETE SET NULL,
    set_at        TEXT NOT NULL,
    ended_at      TEXT,
    ended_reason  TEXT CHECK (ended_reason IS NULL OR ended_reason IN ('redeemed', 'archived', 'deleted', 'audience')),
    ended_name    TEXT NOT NULL DEFAULT '', -- the name of the reward when it ended
    CHECK ((ended_at IS NULL) = (ended_reason IS NULL)),
    CHECK (ended_at IS NOT NULL OR reward_id IS NOT NULL)
);
CREATE INDEX savings_goals_reward ON savings_goals (reward_id);

-- Archiving a reward (by hand, or because its people left) ends the goals on it.
CREATE TRIGGER savings_goals_reward_archived AFTER UPDATE OF archived_at ON rewards
WHEN NEW.archived_at IS NOT NULL
BEGIN
    UPDATE savings_goals SET ended_at = strftime('%Y-%m-%dT%H:%M:%SZ', 'now'), ended_reason = 'archived', ended_name = NEW.name
    WHERE reward_id = NEW.id AND ended_at IS NULL;
END;

CREATE TRIGGER savings_goals_reward_deleted BEFORE DELETE ON rewards
BEGIN
    UPDATE savings_goals SET ended_at = strftime('%Y-%m-%dT%H:%M:%SZ', 'now'), ended_reason = 'deleted', ended_name = OLD.name
    WHERE reward_id = OLD.id AND ended_at IS NULL;
END;

-- A confirmed redemption ends the person's goal on that reward, whoever
-- confirms it and however the row got its status.
CREATE TRIGGER savings_goals_redeemed_insert AFTER INSERT ON reward_redemptions
WHEN NEW.status = 'confirmed' AND NEW.person_id IS NOT NULL AND NEW.reward_id IS NOT NULL
BEGIN
    UPDATE savings_goals SET ended_at = strftime('%Y-%m-%dT%H:%M:%SZ', 'now'), ended_reason = 'redeemed', ended_name = COALESCE((SELECT name FROM rewards WHERE id = NEW.reward_id), '')
    WHERE person_id = NEW.person_id AND reward_id = NEW.reward_id AND ended_at IS NULL;
END;

CREATE TRIGGER savings_goals_redeemed_update AFTER UPDATE OF status ON reward_redemptions
WHEN NEW.status = 'confirmed' AND NEW.person_id IS NOT NULL AND NEW.reward_id IS NOT NULL
BEGIN
    UPDATE savings_goals SET ended_at = strftime('%Y-%m-%dT%H:%M:%SZ', 'now'), ended_reason = 'redeemed', ended_name = COALESCE((SELECT name FROM rewards WHERE id = NEW.reward_id), '')
    WHERE person_id = NEW.person_id AND reward_id = NEW.reward_id AND ended_at IS NULL;
END;

-- Someone who leaves the household takes their goal with them.
CREATE TRIGGER savings_goals_member_left BEFORE DELETE ON household_members
BEGIN
    DELETE FROM savings_goals WHERE person_id = OLD.person_id;
END;
