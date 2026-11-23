-- The reward catalogue (ADR 0021, ticket 291): what children trade their stars
-- for. Redemptions come with ticket 292; the table here holds what the
-- catalogue must know about them (was it ever redeemed, by whom, when, and
-- what counts against a limit).
CREATE TABLE rewards (
    id             TEXT PRIMARY KEY,
    household_id   TEXT NOT NULL REFERENCES households(id) ON DELETE CASCADE,
    name           TEXT NOT NULL CHECK (length(name) BETWEEN 1 AND 60),
    -- What the reward shows: a symbol (an emoji, the gift by default) or an
    -- uploaded photo. Rewards never fetch a picture from the web.
    pic_source     TEXT NOT NULL DEFAULT 'symbol' CHECK (pic_source IN ('symbol', 'upload')),
    symbol         TEXT NOT NULL DEFAULT '🎁',
    picture        BLOB,                     -- the stored JPEG, at most 512 px, no metadata
    picture_from_w INTEGER NOT NULL DEFAULT 0,
    picture_from_h INTEGER NOT NULL DEFAULT 0,
    picture_from_b INTEGER NOT NULL DEFAULT 0,
    picture_at     TEXT NOT NULL DEFAULT '',
    price          INTEGER NOT NULL CHECK (price BETWEEN 1 AND 999),
    limit_period   TEXT NOT NULL DEFAULT 'none' CHECK (limit_period IN ('none', 'day', 'week', 'month')),
    limit_count    INTEGER CHECK (limit_count IS NULL OR limit_count BETWEEN 1 AND 99),
    -- Archived rewards are hidden from children and from the child API.
    archived_at     TEXT,
    archived_reason TEXT CHECK (archived_reason IS NULL OR archived_reason = 'audience_gone'),
    archived_person TEXT NOT NULL DEFAULT '', -- the name of the person who left, to say so on the reward's page
    created_by     TEXT REFERENCES people(id) ON DELETE SET NULL,
    created_at     TEXT NOT NULL,
    updated_at     TEXT NOT NULL,
    CHECK ((limit_period = 'none') = (limit_count IS NULL))
);
CREATE INDEX rewards_household ON rewards (household_id, price);

-- Who a reward is meant for. No rows means the whole household.
CREATE TABLE reward_people (
    reward_id TEXT NOT NULL REFERENCES rewards(id) ON DELETE CASCADE,
    person_id TEXT NOT NULL REFERENCES people(id) ON DELETE CASCADE,
    PRIMARY KEY (reward_id, person_id)
);

-- A person's redemption of a reward. Ticket 292 decides the statuses and
-- adds what it keeps of the reward at that time; the catalogue counts the
-- confirmed ones per reward, person and period.
CREATE TABLE reward_redemptions (
    id           TEXT PRIMARY KEY,
    reward_id    TEXT NOT NULL REFERENCES rewards(id) ON DELETE CASCADE,
    person_id    TEXT REFERENCES people(id) ON DELETE SET NULL,
    status       TEXT NOT NULL CHECK (status IN ('open', 'confirmed', 'declined', 'withdrawn')),
    price        INTEGER NOT NULL,       -- the price at that time
    requested_at TEXT NOT NULL,
    decided_at   TEXT
);
CREATE INDEX reward_redemptions_reward ON reward_redemptions (reward_id, person_id, decided_at);

-- When the last person a reward is meant for leaves, the reward is archived
-- in the same transaction, with the reason (a reward archived by hand keeps its
-- archived_at and gets the reason too). It must never fall back to
-- "whole family" because its audience became empty.
CREATE TRIGGER rewards_audience_gone_people BEFORE DELETE ON people
BEGIN
    UPDATE rewards SET archived_at = COALESCE(archived_at, strftime('%Y-%m-%dT%H:%M:%SZ', 'now')), archived_reason = 'audience_gone', archived_person = COALESCE((SELECT name FROM people WHERE id = OLD.id), '')
    WHERE id IN (SELECT reward_id FROM reward_people GROUP BY reward_id HAVING COUNT(*) = 1 AND MIN(person_id) = OLD.id);
END;

CREATE TRIGGER rewards_audience_gone_member BEFORE DELETE ON household_members
BEGIN
    UPDATE rewards SET archived_at = COALESCE(archived_at, strftime('%Y-%m-%dT%H:%M:%SZ', 'now')), archived_reason = 'audience_gone', archived_person = COALESCE((SELECT name FROM people WHERE id = OLD.person_id), '')
    WHERE household_id = OLD.household_id
      AND id IN (SELECT reward_id FROM reward_people GROUP BY reward_id HAVING COUNT(*) = 1 AND MIN(person_id) = OLD.person_id);
    DELETE FROM reward_people WHERE person_id = OLD.person_id
      AND reward_id IN (SELECT id FROM rewards WHERE household_id = OLD.household_id);
END;
