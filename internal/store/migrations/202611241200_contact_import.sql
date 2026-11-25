-- Contacts from a vCard file (ticket 300, ADR 0021, ADR 0026).
--
-- A phone number is kept as typed (value); e164 is the same number written for comparing and for tel: links
-- (ADR 0026), empty when it cannot be read as a valid number. A contact says which file it was taken over from,
-- completed from, and its photo came from, so the contact page can tell (the names of files, not their content).
ALTER TABLE contact_phones ADD COLUMN e164 TEXT NOT NULL DEFAULT '';
ALTER TABLE contacts ADD COLUMN created_file TEXT NOT NULL DEFAULT '';
ALTER TABLE contacts ADD COLUMN updated_file TEXT NOT NULL DEFAULT '';
ALTER TABLE contacts ADD COLUMN photo_file TEXT NOT NULL DEFAULT '';

-- A contact file waits here between the upload and the parent's yes. Only the parent who uploaded it can open
-- it (uploaded_by); after an hour, or after the import, the file and the photos made from it are deleted.
-- A row whose file expired stays as a note (expired = 1, no data) so the page can say so; it goes with the next sweep.
CREATE TABLE contact_imports (
    id           TEXT PRIMARY KEY,
    household_id TEXT NOT NULL REFERENCES households(id) ON DELETE CASCADE,
    uploaded_by  TEXT NOT NULL REFERENCES people(id) ON DELETE CASCADE,
    file_name    TEXT NOT NULL,
    data         BLOB NOT NULL,
    expired      INTEGER NOT NULL DEFAULT 0 CHECK (expired IN (0, 1)),
    uploaded_at  TEXT NOT NULL
);

-- The photos of the cards, made under the picture rules (512 px, no location or camera data), by card number.
CREATE TABLE contact_import_photos (
    import_id TEXT NOT NULL REFERENCES contact_imports(id) ON DELETE CASCADE,
    card      INTEGER NOT NULL,
    jpeg      BLOB NOT NULL,
    PRIMARY KEY (import_id, card)
);
