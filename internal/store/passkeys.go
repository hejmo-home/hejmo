package store

import (
	"database/sql"
	"errors"
	"time"
)

// Passkey is a WebAuthn credential of an adult's account (GLOSSARY: Passkey).
type Passkey struct {
	ID       string
	PersonID string
	// CredentialID is the ID the authenticator knows the passkey by.
	CredentialID []byte
	// Credential is the library's record as JSON: public key and counters.
	Credential []byte
	Name       string
	// RPID is the box name the passkey was created for; it stops working when
	// the box has another name.
	RPID string
	// Device is the kind of device it was created on, as a key (internal/auth).
	Device   string
	Created  time.Time
	LastUsed time.Time // zero = not used yet
}

const passkeyColumns = `id, person_id, credential_id, credential, name, rp_id, device, created_at, last_used_at`

func scanPasskey(r rowScanner) (Passkey, error) {
	var p Passkey
	var credential, created string
	var used sql.NullString
	if err := r.Scan(&p.ID, &p.PersonID, &p.CredentialID, &credential, &p.Name, &p.RPID, &p.Device, &created, &used); err != nil {
		return Passkey{}, err
	}
	p.Credential = []byte(credential)
	p.Created = parseStamp(sql.NullString{String: created, Valid: true})
	p.LastUsed = parseStamp(used)
	return p, nil
}

// AddPasskey stores a passkey and returns it with its ID.
func (db *DB) AddPasskey(p Passkey) (Passkey, error) {
	p.ID = newID()
	_, err := db.sql.Exec(`INSERT INTO passkeys (`+passkeyColumns+`) VALUES (?, ?, ?, ?, ?, ?, ?, ?, NULL)`,
		p.ID, p.PersonID, p.CredentialID, string(p.Credential), p.Name, p.RPID, p.Device, stamp(p.Created))
	return p, err
}

// Passkeys returns a person's passkeys, the oldest first.
func (db *DB) Passkeys(personID string) ([]Passkey, error) {
	rows, err := db.sql.Query(`SELECT `+passkeyColumns+` FROM passkeys WHERE person_id = ? ORDER BY created_at, rowid`, personID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Passkey
	for rows.Next() {
		p, err := scanPasskey(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// Passkey returns one passkey of the person.
func (db *DB) Passkey(personID, id string) (Passkey, error) {
	p, err := scanPasskey(db.sql.QueryRow(`SELECT `+passkeyColumns+` FROM passkeys WHERE id = ? AND person_id = ?`, id, personID))
	if errors.Is(err, sql.ErrNoRows) {
		return Passkey{}, ErrNotFound
	}
	return p, err
}

// PasskeyByCredential finds a passkey by the ID its authenticator knows it by.
func (db *DB) PasskeyByCredential(credentialID []byte) (Passkey, error) {
	p, err := scanPasskey(db.sql.QueryRow(`SELECT `+passkeyColumns+` FROM passkeys WHERE credential_id = ?`, credentialID))
	if errors.Is(err, sql.ErrNoRows) {
		return Passkey{}, ErrNotFound
	}
	return p, err
}

// UsePasskey records that the passkey was used at time at and keeps the
// credential record with its new counters.
func (db *DB) UsePasskey(id string, credential []byte, at time.Time) error {
	return expectOne(db.sql.Exec(`UPDATE passkeys SET credential = ?, last_used_at = ? WHERE id = ?`, string(credential), stamp(at), id))
}

// RenamePasskey changes the name of a passkey of the person.
func (db *DB) RenamePasskey(personID, id, name string) error {
	return expectOne(db.sql.Exec(`UPDATE passkeys SET name = ? WHERE id = ? AND person_id = ?`, name, id, personID))
}

// RemovePasskey deletes a passkey of the person; it cannot sign in any more.
func (db *DB) RemovePasskey(personID, id string) error {
	return expectOne(db.sql.Exec(`DELETE FROM passkeys WHERE id = ? AND person_id = ?`, id, personID))
}

// SetSessionPasskey records the passkey a session was made with.
func (db *DB) SetSessionPasskey(tokenHash, passkeyID string) error {
	return expectOne(db.sql.Exec(`UPDATE sessions SET passkey_id = ? WHERE token_hash = ?`, passkeyID, tokenHash))
}

// SessionPasskey returns the passkey a session was made with, "" for a
// session made with a password.
func (db *DB) SessionPasskey(tokenHash string) (string, error) {
	var id sql.NullString
	err := db.sql.QueryRow(`SELECT passkey_id FROM sessions WHERE token_hash = ?`, tokenHash).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrNotFound
	}
	return id.String, err
}

// PasskeyCount is the number of passkeys on the box.
func (db *DB) PasskeyCount() (int, error) {
	var n int
	err := db.sql.QueryRow(`SELECT COUNT(*) FROM passkeys`).Scan(&n)
	return n, err
}
