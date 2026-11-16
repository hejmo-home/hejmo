-- Browser sessions a parent can see and end (issue #108).
-- id is the public handle used in addresses (/sessions/{id}); it is random and
-- not derived from token_hash, so knowing it never helps to forge a cookie.
ALTER TABLE sessions ADD COLUMN id TEXT;
UPDATE sessions SET id = lower(hex(randomblob(16))) WHERE id IS NULL;
CREATE UNIQUE INDEX sessions_id ON sessions(id);

-- device holds keys ("windows|edge"), never the raw User-Agent; sessions made
-- before this migration keep '' (shown as an unknown device).
ALTER TABLE sessions ADD COLUMN device TEXT NOT NULL DEFAULT '';

-- When the browser last made a request; NULL for sessions made before.
ALTER TABLE sessions ADD COLUMN last_seen_at TEXT;
