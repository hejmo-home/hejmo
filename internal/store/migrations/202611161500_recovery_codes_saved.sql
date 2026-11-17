-- Ticket 176: the person says they saved the recovery codes (a required
-- checkbox). NULL: not confirmed. A new set clears it again.
ALTER TABLE accounts ADD COLUMN recovery_codes_saved_at TEXT;
-- Accounts that exist already are not nagged about sets from before this ticket.
UPDATE accounts SET recovery_codes_saved_at = totp_confirmed_at WHERE totp_confirmed_at IS NOT NULL;
