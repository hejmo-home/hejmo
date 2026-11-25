-- Microsoft's delta baseline (ticket 226): when the delta link began. It used
-- to travel in remote_ctag; it gets its own column, and an Outlook calendar's
-- stored value moves over so no sync starts again from scratch.
ALTER TABLE calendar_sync ADD COLUMN remote_delta_baseline TEXT NOT NULL DEFAULT '';
UPDATE calendar_sync
   SET remote_delta_baseline = remote_ctag, remote_ctag = ''
 WHERE calendar_id IN (SELECT id FROM calendars WHERE kind = 'outlook');
