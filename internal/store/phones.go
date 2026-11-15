package store

import (
	"database/sql"
	"errors"
	"strings"
	"time"
	"unicode/utf8"
)

// PhoneSessionLifetime is how long a phone's device session lasts.
const PhoneSessionLifetime = 365 * 24 * time.Hour

// MaxPhonePairings is how many unfinished pairing codes a household keeps.
// Creating one more drops the oldest.
const MaxPhonePairings = 20

// ErrPhonePending means a phone opened its pairing code but no parent has
// confirmed it yet.
var ErrPhonePending = errors.New("store: phone pairing not confirmed yet")

// PhonePairing is a pairing code shown by a parent.
type PhonePairing struct {
	ID        string
	ExpiresAt time.Time
	// Device is what the phone said about itself when it opened the code;
	// empty until a phone did.
	Device string
}

// Phone is a paired phone: a device session for one person.
type Phone struct {
	ID       string
	Name     string
	PersonID string
	// PersonName is filled by Phones.
	PersonName string
	PairedAt   time.Time
	LastSeen   time.Time
}

// CreatePhonePairing stores a pairing code by its hash. The code is valid for
// PairingCodeLifetime.
func (db *DB) CreatePhonePairing(householdID, codeHash string, at time.Time) (PhonePairing, error) {
	if _, err := db.sql.Exec(`DELETE FROM phone_pairings WHERE expires_at <= ?`, stamp(at.Add(-24*time.Hour))); err != nil {
		return PhonePairing{}, err
	}
	if _, err := db.sql.Exec(`DELETE FROM phone_pairings WHERE household_id = ? AND id IN (
		SELECT id FROM phone_pairings WHERE household_id = ? ORDER BY created_at DESC, rowid DESC LIMIT -1 OFFSET ?)`,
		householdID, householdID, MaxPhonePairings-1); err != nil {
		return PhonePairing{}, err
	}
	p := PhonePairing{ID: newID(), ExpiresAt: at.Add(PairingCodeLifetime)}
	_, err := db.sql.Exec(`INSERT INTO phone_pairings (id, household_id, code_hash, created_at, expires_at) VALUES (?, ?, ?, ?, ?)`,
		p.ID, householdID, codeHash, stamp(at), stamp(p.ExpiresAt))
	return p, err
}

// JoinPhonePairing records that the phone with the secret claimHash opened the
// code. A code serves one phone: another phone, an unknown, used or expired
// code is ErrNotFound. The same phone may open it again.
func (db *DB) JoinPhonePairing(codeHash, claimHash, device string, at time.Time) error {
	device = clip(device, 120)
	res, err := db.sql.Exec(`UPDATE phone_pairings SET claim_hash = ?, device = ?
		WHERE code_hash = ? AND expires_at > ? AND (claim_hash IS NULL OR claim_hash = ?)`,
		claimHash, device, codeHash, stamp(at), claimHash)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// PendingPhonePairings lists the codes a phone opened that wait for a
// parent's confirmation, oldest first.
func (db *DB) PendingPhonePairings(householdID string, at time.Time) ([]PhonePairing, error) {
	rows, err := db.sql.Query(`SELECT id, device, expires_at FROM phone_pairings
		WHERE household_id = ? AND claim_hash IS NOT NULL AND person_id IS NULL AND expires_at > ?
		ORDER BY created_at, rowid`, householdID, stamp(at))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []PhonePairing
	for rows.Next() {
		var p PhonePairing
		var exp sql.NullString
		if err := rows.Scan(&p.ID, &p.Device, &exp); err != nil {
			return nil, err
		}
		p.ExpiresAt = parseStamp(exp)
		out = append(out, p)
	}
	return out, rows.Err()
}

// ConfirmPhonePairing is the parent's confirmation: the phone that opened the
// pairing gets a device session for personID, named name. The phone it returns
// is the one the phone's next request completes. A pairing that no phone
// opened, or that expired or was confirmed already, is ErrNotFound.
func (db *DB) ConfirmPhonePairing(householdID, id, personID, name string, at time.Time) (Phone, error) {
	var open int
	if err := db.sql.QueryRow(`SELECT COUNT(*) FROM phone_pairings
		WHERE household_id = ? AND id = ? AND claim_hash IS NOT NULL AND person_id IS NULL AND expires_at > ?`, householdID, id, stamp(at)).Scan(&open); err != nil {
		return Phone{}, err
	}
	if open == 0 {
		return Phone{}, ErrNotFound
	}
	name = strings.TrimSpace(name)
	if name == "" || utf8.RuneCountInString(name) > 80 {
		return Phone{}, &FieldError{"name", "a phone needs a name of at most 80 characters"}
	}
	var member int
	if err := db.sql.QueryRow(`SELECT COUNT(*) FROM household_members WHERE household_id = ? AND person_id = ?`, householdID, personID).Scan(&member); err != nil {
		return Phone{}, err
	}
	if member == 0 {
		return Phone{}, &FieldError{"person", "a phone belongs to a member of the household"}
	}
	ph := Phone{ID: newID(), Name: name, PersonID: personID, PairedAt: at}
	err := expectOne(db.sql.Exec(`UPDATE phone_pairings SET person_id = ?, name = ?, phone_id = ?
		WHERE household_id = ? AND id = ? AND claim_hash IS NOT NULL AND person_id IS NULL AND expires_at > ?`,
		personID, name, ph.ID, householdID, id, stamp(at)))
	return ph, err
}

// CompletePhonePairing turns a confirmed pairing into a phone with a device
// session (sessionHash, valid until expires). It succeeds once. A pairing the
// parent has not confirmed yet is ErrPhonePending; an unknown, other phone's,
// used or expired pairing is ErrNotFound.
func (db *DB) CompletePhonePairing(codeHash, claimHash, sessionHash string, at, expires time.Time) (Phone, error) {
	tx, err := db.sql.Begin()
	if err != nil {
		return Phone{}, err
	}
	defer tx.Rollback()
	var id, householdID, name string
	var person, phoneID sql.NullString
	err = tx.QueryRow(`SELECT id, household_id, person_id, name, phone_id FROM phone_pairings
		WHERE code_hash = ? AND claim_hash = ? AND expires_at > ?`, codeHash, claimHash, stamp(at)).Scan(&id, &householdID, &person, &name, &phoneID)
	if errors.Is(err, sql.ErrNoRows) {
		return Phone{}, ErrNotFound
	}
	if err != nil {
		return Phone{}, err
	}
	if !person.Valid {
		return Phone{}, ErrPhonePending
	}
	ph := Phone{ID: phoneID.String, Name: name, PersonID: person.String, PairedAt: at, LastSeen: at}
	if _, err := tx.Exec(`INSERT INTO phones (id, household_id, person_id, name, created_at, last_seen_at) VALUES (?, ?, ?, ?, ?, ?)`,
		ph.ID, householdID, ph.PersonID, ph.Name, stamp(at), stamp(at)); err != nil {
		return Phone{}, err
	}
	if _, err := tx.Exec(`INSERT INTO sessions (token_hash, person_id, created_at, expires_at, phone_id) VALUES (?, ?, ?, ?, ?)`,
		sessionHash, ph.PersonID, stamp(at), stamp(expires), ph.ID); err != nil {
		return Phone{}, err
	}
	if _, err := tx.Exec(`DELETE FROM phone_pairings WHERE id = ?`, id); err != nil {
		return Phone{}, err
	}
	return ph, tx.Commit()
}

// Phones lists a household's paired phones, oldest first.
func (db *DB) Phones(householdID string) ([]Phone, error) {
	rows, err := db.sql.Query(`SELECT ph.id, ph.name, ph.person_id, p.name, ph.created_at, ph.last_seen_at
		FROM phones ph JOIN people p ON p.id = ph.person_id
		WHERE ph.household_id = ? ORDER BY ph.created_at, ph.rowid`, householdID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Phone
	for rows.Next() {
		var ph Phone
		var created, seen sql.NullString
		if err := rows.Scan(&ph.ID, &ph.Name, &ph.PersonID, &ph.PersonName, &created, &seen); err != nil {
			return nil, err
		}
		ph.PairedAt, ph.LastSeen = parseStamp(created), parseStamp(seen)
		out = append(out, ph)
	}
	return out, rows.Err()
}

// RevokePhone removes a phone; its device session ends with it.
func (db *DB) RevokePhone(householdID, id string) error {
	return expectOne(db.sql.Exec(`DELETE FROM phones WHERE household_id = ? AND id = ?`, householdID, id))
}

// TouchPhoneSession records that the phone behind the session was seen at t,
// at most once a minute. A session that belongs to no phone is left alone.
func (db *DB) TouchPhoneSession(sessionHash string, t time.Time) {
	_, _ = db.sql.Exec(`UPDATE phones SET last_seen_at = ?
		WHERE id = (SELECT phone_id FROM sessions WHERE token_hash = ?) AND (last_seen_at IS NULL OR last_seen_at <= ?)`,
		stamp(t), sessionHash, stamp(t.Add(-time.Minute)))
}

// clip shortens s to at most n characters.
func clip(s string, n int) string {
	s = strings.TrimSpace(s)
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	return string([]rune(s)[:n])
}
