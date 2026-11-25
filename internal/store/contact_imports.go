package store

import (
	"database/sql"
	"errors"
	"time"
)

// A contact file waits on the box between the upload and the parent's yes (ticket 300). Only the parent who
// uploaded it can open it, it is deleted after the import and after an hour at the latest, and the photos made
// from it go with it.

// ContactImport is an uploaded contact file. Expired says the file is gone (Data is empty): only the note
// remains, so the page can say so.
type ContactImport struct {
	ID, FileName               string
	UploadedBy, UploadedByName string
	UploadedAt                 time.Time
	Data                       []byte
	Expired                    bool
}

// AddContactImport keeps an uploaded file and the photos made from its cards (by card number).
func (db *DB) AddContactImport(householdID, by, fileName string, data []byte, photos map[int][]byte, at time.Time) (ContactImport, error) {
	id := newID()
	tx, err := db.sql.Begin()
	if err != nil {
		return ContactImport{}, err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`INSERT INTO contact_imports (id, household_id, uploaded_by, file_name, data, uploaded_at) VALUES (?, ?, ?, ?, ?, ?)`,
		id, householdID, by, fileName, data, at.UTC().Format(time.RFC3339)); err != nil {
		return ContactImport{}, err
	}
	for card, jpeg := range photos {
		if _, err := tx.Exec(`INSERT INTO contact_import_photos (import_id, card, jpeg) VALUES (?, ?, ?)`, id, card, jpeg); err != nil {
			return ContactImport{}, err
		}
	}
	if err := tx.Commit(); err != nil {
		return ContactImport{}, err
	}
	return db.ContactImport(householdID, id)
}

// ContactImport loads an uploaded file; ErrNotFound when there is none (never uploaded, or imported or
// cancelled and so deleted).
func (db *DB) ContactImport(householdID, id string) (ContactImport, error) {
	var c ContactImport
	var at string
	err := db.sql.QueryRow(`SELECT i.id, i.file_name, i.uploaded_by, COALESCE(p.name, ''), i.uploaded_at, i.data, i.expired
		FROM contact_imports i LEFT JOIN people p ON p.id = i.uploaded_by WHERE i.household_id = ? AND i.id = ?`, householdID, id).
		Scan(&c.ID, &c.FileName, &c.UploadedBy, &c.UploadedByName, &at, &c.Data, &c.Expired)
	if errors.Is(err, sql.ErrNoRows) {
		return ContactImport{}, ErrNotFound
	}
	c.UploadedAt, _ = time.Parse(time.RFC3339, at)
	return c, err
}

// ContactImportPhotos reads the photos made from the cards of an uploaded file, by card number.
func (db *DB) ContactImportPhotos(id string) (map[int][]byte, error) {
	rows, err := db.sql.Query(`SELECT card, jpeg FROM contact_import_photos WHERE import_id = ?`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[int][]byte{}
	for rows.Next() {
		var n int
		var b []byte
		if err := rows.Scan(&n, &b); err != nil {
			return nil, err
		}
		out[n] = b
	}
	return out, rows.Err()
}

// DeleteContactImport deletes an uploaded file with its photos; ErrNotFound when there is none.
func (db *DB) DeleteContactImport(householdID, id string) error {
	return expectOne(db.sql.Exec(`DELETE FROM contact_imports WHERE household_id = ? AND id = ?`, householdID, id))
}

// ExpireContactImports deletes the file and the photos of every upload older than the cutoff and keeps a note
// that it expired; notes older than a day are deleted.
func (db *DB) ExpireContactImports(cutoff, now time.Time) error {
	tx, err := db.sql.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	c := cutoff.UTC().Format(time.RFC3339)
	if _, err := tx.Exec(`DELETE FROM contact_import_photos WHERE import_id IN (SELECT id FROM contact_imports WHERE expired = 0 AND uploaded_at <= ?)`, c); err != nil {
		return err
	}
	if _, err := tx.Exec(`UPDATE contact_imports SET data = x'', expired = 1 WHERE expired = 0 AND uploaded_at <= ?`, c); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM contact_imports WHERE expired = 1 AND uploaded_at <= ?`, now.Add(-24*time.Hour).UTC().Format(time.RFC3339)); err != nil {
		return err
	}
	return tx.Commit()
}

// ImportOp is one change of a contact import: a new contact (MergeInto empty), or the complete state a
// contact has after it was completed from the file. Input.ImportFile names the file.
type ImportOp struct {
	MergeInto string
	Input     ContactInput
}

// ApplyContactImport writes every change in one transaction, all or nothing, and deletes the uploaded file. It
// returns the ids of the contacts in the order of the changes.
func (db *DB) ApplyContactImport(householdID, importID, by string, ops []ImportOp) ([]string, error) {
	tx, err := db.sql.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	ids := make([]string, 0, len(ops))
	for _, op := range ops {
		if op.MergeInto == "" {
			id, err := createContact(tx, householdID, by, op.Input)
			if err != nil {
				return nil, err
			}
			ids = append(ids, id)
			continue
		}
		var name string
		var person bool
		if err := tx.QueryRow(`SELECT hejmo_name, person_id IS NOT NULL FROM contacts WHERE id = ? AND household_id = ?`, op.MergeInto, householdID).Scan(&name, &person); errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		} else if err != nil {
			return nil, err
		}
		if err := updateContact(tx, householdID, op.MergeInto, by, person, name, op.Input); err != nil {
			return nil, err
		}
		ids = append(ids, op.MergeInto)
	}
	if err := expectOne(tx.Exec(`DELETE FROM contact_imports WHERE household_id = ? AND id = ?`, householdID, importID)); err != nil {
		return nil, err
	}
	return ids, tx.Commit()
}
