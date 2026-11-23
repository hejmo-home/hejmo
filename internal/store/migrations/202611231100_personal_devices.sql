-- Personal devices (#203): the phones a parent pairs are personal devices (a phone,
-- a tablet or any other device that belongs to one person). Tables and columns
-- take the new name; the data stays.
ALTER TABLE phones RENAME TO personal_devices;
ALTER TABLE phone_pairings RENAME TO personal_device_pairings;
ALTER TABLE sessions RENAME COLUMN phone_id TO personal_device_id;
ALTER TABLE personal_device_pairings RENAME COLUMN phone_id TO personal_device_id;
