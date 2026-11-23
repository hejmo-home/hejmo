-- Contact photos (ticket 297): one photo per contact, also for the contact of a
-- person. The photo is the JPEG Hejmo made (square, at most 512 px, no metadata).
-- photo_origin says where it came from: set by hand ('manual', photo_by is the
-- parent) or over contact sync ('sync', photo_device is the phone, ticket 299).
ALTER TABLE contacts ADD COLUMN photo BLOB;
ALTER TABLE contacts ADD COLUMN photo_origin TEXT NOT NULL DEFAULT '' CHECK (photo_origin IN ('', 'manual', 'sync'));
ALTER TABLE contacts ADD COLUMN photo_by TEXT REFERENCES people(id) ON DELETE SET NULL;
ALTER TABLE contacts ADD COLUMN photo_device TEXT NOT NULL DEFAULT '';
ALTER TABLE contacts ADD COLUMN photo_at TEXT NOT NULL DEFAULT '';
