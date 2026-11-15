-- What the last successful sync run did (ticket 5.17): how many events the
-- calendar holds afterwards (a series counts once) and how many were new,
-- changed or removed in that run. A failed run leaves them as they were.
ALTER TABLE calendar_sync ADD COLUMN last_run_events  INTEGER NOT NULL DEFAULT 0;
ALTER TABLE calendar_sync ADD COLUMN last_run_new     INTEGER NOT NULL DEFAULT 0;
ALTER TABLE calendar_sync ADD COLUMN last_run_changed INTEGER NOT NULL DEFAULT 0;
ALTER TABLE calendar_sync ADD COLUMN last_run_removed INTEGER NOT NULL DEFAULT 0;
