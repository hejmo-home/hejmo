-- Ticket 306: the server of a connection asked to wait (429/503). Until then
-- nothing goes to it, from any calendar of the connection. RFC 3339 UTC; NULL
-- when it did not ask.
ALTER TABLE calendar_connections ADD COLUMN throttled_until TEXT;
