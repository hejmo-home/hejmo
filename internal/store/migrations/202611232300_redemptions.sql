-- Redeeming rewards (ticket 292). A child asks, a parent confirms or declines;
-- a parent can also redeem directly. The row keeps what the reward was at the
-- time (name, symbol, picture, price), so a later change or deletion of the
-- reward does not change history. Only a confirmed redemption books stars.
CREATE TABLE reward_redemptions_new (
    id             TEXT PRIMARY KEY,
    household_id   TEXT NOT NULL REFERENCES households(id) ON DELETE CASCADE,
    reward_id      TEXT REFERENCES rewards(id) ON DELETE SET NULL,
    person_id      TEXT REFERENCES people(id) ON DELETE SET NULL,
    status         TEXT NOT NULL CHECK (status IN ('open', 'confirmed', 'declined', 'withdrawn')),
    -- The snapshot: taken when the child asks, or at redemption time for a direct one.
    name           TEXT NOT NULL DEFAULT '',
    symbol         TEXT NOT NULL DEFAULT '',
    picture        BLOB,
    picture_at     TEXT NOT NULL DEFAULT '',
    price          INTEGER NOT NULL,
    note           TEXT NOT NULL DEFAULT '' CHECK (length(note) <= 120),
    requested_at   TEXT NOT NULL,         -- when the child asked (for a direct redemption: when it happened)
    decided_at     TEXT,
    decided_by     TEXT REFERENCES people(id) ON DELETE SET NULL,
    decided_by_name TEXT NOT NULL DEFAULT '',
    direct         INTEGER NOT NULL DEFAULT 0 CHECK (direct IN (0, 1)),
    decline_reason TEXT CHECK (decline_reason IS NULL OR decline_reason IN ('parent', 'reward_gone'))
);
INSERT INTO reward_redemptions_new (id, household_id, reward_id, person_id, status, name, symbol, price, requested_at, decided_at)
    SELECT d.id, r.household_id, d.reward_id, d.person_id, d.status, r.name, r.symbol, d.price, d.requested_at, d.decided_at
    FROM reward_redemptions d JOIN rewards r ON r.id = d.reward_id;
DROP TABLE reward_redemptions;
ALTER TABLE reward_redemptions_new RENAME TO reward_redemptions;
CREATE INDEX reward_redemptions_reward ON reward_redemptions (reward_id, person_id, decided_at);
CREATE INDEX reward_redemptions_household ON reward_redemptions (household_id, status, requested_at);
-- At most one open request per person and reward.
CREATE UNIQUE INDEX reward_redemptions_one_open ON reward_redemptions (person_id, reward_id) WHERE status = 'open';

-- A person who leaves takes their open requests back.
CREATE TRIGGER redemptions_withdraw_people BEFORE DELETE ON people
BEGIN
    UPDATE reward_redemptions SET status = 'withdrawn', decided_at = strftime('%Y-%m-%dT%H:%M:%SZ', 'now') WHERE person_id = OLD.id AND status = 'open';
END;

CREATE TRIGGER redemptions_withdraw_member BEFORE DELETE ON household_members
BEGIN
    UPDATE reward_redemptions SET status = 'withdrawn', decided_at = strftime('%Y-%m-%dT%H:%M:%SZ', 'now') WHERE person_id = OLD.person_id AND household_id = OLD.household_id AND status = 'open';
END;

-- The star log row of a confirmed redemption points at it.
ALTER TABLE star_log ADD COLUMN redemption_id TEXT REFERENCES reward_redemptions(id) ON DELETE SET NULL;

-- Whether unticking a task may take a balance below zero (product owner, 23/11/2026).
ALTER TABLE households ADD COLUMN below_zero INTEGER NOT NULL DEFAULT 1 CHECK (below_zero IN (0, 1));

-- With below-zero off, unticking may take back less than the tick credited.
-- What it could not take back is remembered per task and day and is held
-- against the next tick of that task, so unticking and ticking again mints
-- no stars.
CREATE TABLE task_star_debt (
    task_id TEXT NOT NULL REFERENCES tasks(id) ON DELETE CASCADE,
    day     TEXT NOT NULL,
    stars   INTEGER NOT NULL CHECK (stars > 0),
    PRIMARY KEY (task_id, day)
);
