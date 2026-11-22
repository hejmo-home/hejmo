-- What a CalDAV server said about a calendar's version when the last run read
-- it (ticket 54): the CTag (a change since means something changed) and the
-- RFC 6578 sync token (what a sync-collection REPORT continues from). Empty
-- where the server offers none, so the next run lists ETags.
ALTER TABLE calendar_sync ADD COLUMN remote_ctag       TEXT NOT NULL DEFAULT '';
ALTER TABLE calendar_sync ADD COLUMN remote_sync_token TEXT NOT NULL DEFAULT '';
