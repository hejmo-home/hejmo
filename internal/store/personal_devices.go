package store

import (
	"database/sql"
	"errors"
	"strings"
	"time"
	"unicode/utf8"
)

// PersonalDeviceSessionLifetime is how long a personal device's device session lasts.
const PersonalDeviceSessionLifetime = 365 * 24 * time.Hour

// MaxPersonalDevicePairings is how many unfinished pairing codes a household keeps.
// Creating one more drops the oldest.
const MaxPersonalDevicePairings = 20

// ErrPersonalDevicePending means a personal device opened its pairing code but no parent has
// confirmed it yet.
var ErrPersonalDevicePending = errors.New("store: personal device pairing not confirmed yet")

// PersonalDevicePairing is a pairing code shown by a parent.
type PersonalDevicePairing struct {
	ID        string
	ExpiresAt time.Time
	// Device is what the personal device said about itself when it opened the code, as the
	// keys "browser|device" ("safari|iphone", "unknown" for what could not be told); empty
	// until a personal device did. The page translates it.
	Device string
	// Kind is the kind of screen: phone, tablet or device (unknown). Stored with the pairing
	// and never read back from the label.
	Kind string
	// ClaimHash is the hash of the secret of the personal device that opened the code;
	// the verification number shown to both sides derives from it.
	ClaimHash string
	// ForPersonID is whom the personal device is for next when the code came from
	// re-pairing a personal device; empty for an ordinary code.
	ForPersonID string
}

// PersonalDevice is a paired personal device: a device session for one person.
type PersonalDevice struct {
	ID       string
	Name     string
	PersonID string
	// PersonName is filled by PersonalDevices.
	PersonName string
	PairedAt   time.Time
	LastSeen   time.Time
	// SessionEnds is when the personal device's device session runs out.
	SessionEnds time.Time
}

// CreatePersonalDevicePairing stores a pairing code by its hash. The code is valid for
// PairingCodeLifetime.
func (db *DB) CreatePersonalDevicePairing(householdID, codeHash string, at time.Time) (PersonalDevicePairing, error) {
	return createPersonalDevicePairing(db.sql, householdID, codeHash, "", at)
}

// execer is what a database and a transaction share.
type execer interface {
	Exec(query string, args ...any) (sql.Result, error)
}

// createPersonalDevicePairing stores a pairing code, dropping old and surplus codes
// first. forPerson is empty for an ordinary code.
func createPersonalDevicePairing(x execer, householdID, codeHash, forPerson string, at time.Time) (PersonalDevicePairing, error) {
	if _, err := x.Exec(`DELETE FROM personal_device_pairings WHERE expires_at <= ?`, stamp(at.Add(-24*time.Hour))); err != nil {
		return PersonalDevicePairing{}, err
	}
	if _, err := x.Exec(`DELETE FROM personal_device_pairings WHERE household_id = ? AND id IN (
		SELECT id FROM personal_device_pairings WHERE household_id = ? ORDER BY created_at DESC, rowid DESC LIMIT -1 OFFSET ?)`,
		householdID, householdID, MaxPersonalDevicePairings-1); err != nil {
		return PersonalDevicePairing{}, err
	}
	p := PersonalDevicePairing{ID: newID(), ExpiresAt: at.Add(PairingCodeLifetime), ForPersonID: forPerson}
	_, err := x.Exec(`INSERT INTO personal_device_pairings (id, household_id, code_hash, created_at, expires_at, for_person_id) VALUES (?, ?, ?, ?, ?, NULLIF(?, ''))`,
		p.ID, householdID, codeHash, stamp(at), stamp(p.ExpiresAt), forPerson)
	return p, err
}

// RepairPersonalDevice gives a personal device to another person in one step: the personal device is
// removed, its device session ends at once, and a new pairing code (by its
// hash) is stored that says whom the personal device is for next. personID must be a
// member of the household other than the personal device's current person (a field
// error "person" otherwise, with nothing changed); an unknown personal device is
// ErrNotFound.
func (db *DB) RepairPersonalDevice(householdID, personalDeviceID, personID, codeHash string, at time.Time) (PersonalDevicePairing, error) {
	tx, err := db.sql.Begin()
	if err != nil {
		return PersonalDevicePairing{}, err
	}
	defer tx.Rollback()
	var current string
	err = tx.QueryRow(`SELECT person_id FROM personal_devices WHERE household_id = ? AND id = ?`, householdID, personalDeviceID).Scan(&current)
	if errors.Is(err, sql.ErrNoRows) {
		return PersonalDevicePairing{}, ErrNotFound
	}
	if err != nil {
		return PersonalDevicePairing{}, err
	}
	var member int
	if err := tx.QueryRow(`SELECT COUNT(*) FROM household_members WHERE household_id = ? AND person_id = ?`, householdID, personID).Scan(&member); err != nil {
		return PersonalDevicePairing{}, err
	}
	if member == 0 || personID == current {
		return PersonalDevicePairing{}, &FieldError{"person", "choose another person of the household for the personal device"}
	}
	if _, err := tx.Exec(`DELETE FROM personal_devices WHERE household_id = ? AND id = ?`, householdID, personalDeviceID); err != nil {
		return PersonalDevicePairing{}, err
	}
	pr, err := createPersonalDevicePairing(tx, householdID, codeHash, personID, at)
	if err != nil {
		return PersonalDevicePairing{}, err
	}
	return pr, tx.Commit()
}

// PairingForPerson is whom the personal device with the secret claimHash, which opened
// the code, is for next. ok is false for an ordinary code or an unknown one.
func (db *DB) PairingForPerson(codeHash, claimHash string, at time.Time) (Person, bool, error) {
	var p Person
	err := db.sql.QueryRow(`SELECT p.id, p.name FROM personal_device_pairings pp JOIN people p ON p.id = pp.for_person_id
		WHERE pp.code_hash = ? AND pp.claim_hash = ? AND pp.expires_at > ?`, codeHash, claimHash, stamp(at)).Scan(&p.ID, &p.Name)
	if errors.Is(err, sql.ErrNoRows) {
		return Person{}, false, nil
	}
	return p, err == nil, err
}

// JoinPersonalDevicePairing records that the personal device with the secret claimHash opened the
// code. A code serves one personal device: another personal device, an unknown, used or expired
// code is ErrNotFound. The same personal device may open it again.
//
// device holds the keys "browser|device" and kind the kind of screen (phone, tablet; anything
// else is stored as "device").
func (db *DB) JoinPersonalDevicePairing(codeHash, claimHash, device, kind string, at time.Time) error {
	device = clip(device, 120)
	if kind != "phone" && kind != "tablet" {
		kind = "device"
	}
	res, err := db.sql.Exec(`UPDATE personal_device_pairings SET claim_hash = ?, device = ?, device_kind = ?
		WHERE code_hash = ? AND expires_at > ? AND (claim_hash IS NULL OR claim_hash = ?)`,
		claimHash, device, kind, codeHash, stamp(at), claimHash)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// PendingPersonalDevicePairings lists the codes a personal device opened that wait for a
// parent's confirmation, oldest first.
func (db *DB) PendingPersonalDevicePairings(householdID string, at time.Time) ([]PersonalDevicePairing, error) {
	rows, err := db.sql.Query(`SELECT id, device, device_kind, expires_at, claim_hash, COALESCE(for_person_id, '') FROM personal_device_pairings
		WHERE household_id = ? AND claim_hash IS NOT NULL AND person_id IS NULL AND expires_at > ?
		ORDER BY created_at, rowid`, householdID, stamp(at))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []PersonalDevicePairing
	for rows.Next() {
		var p PersonalDevicePairing
		var exp sql.NullString
		if err := rows.Scan(&p.ID, &p.Device, &p.Kind, &exp, &p.ClaimHash, &p.ForPersonID); err != nil {
			return nil, err
		}
		p.ExpiresAt = parseStamp(exp)
		out = append(out, p)
	}
	return out, rows.Err()
}

// ConfirmPersonalDevicePairing is the parent's confirmation: the personal device that opened the
// pairing gets a device session for personID, named name. The personal device it returns
// is the one the personal device's next request completes. A pairing that no personal device
// opened, or that expired or was confirmed already, is ErrNotFound.
func (db *DB) ConfirmPersonalDevicePairing(householdID, id, personID, name string, at time.Time) (PersonalDevice, error) {
	var open int
	if err := db.sql.QueryRow(`SELECT COUNT(*) FROM personal_device_pairings
		WHERE household_id = ? AND id = ? AND claim_hash IS NOT NULL AND person_id IS NULL AND expires_at > ?`, householdID, id, stamp(at)).Scan(&open); err != nil {
		return PersonalDevice{}, err
	}
	if open == 0 {
		return PersonalDevice{}, ErrNotFound
	}
	name = strings.TrimSpace(name)
	if name == "" || utf8.RuneCountInString(name) > 80 {
		return PersonalDevice{}, &FieldError{"name", "a personal device needs a name of at most 80 characters"}
	}
	var member int
	if err := db.sql.QueryRow(`SELECT COUNT(*) FROM household_members WHERE household_id = ? AND person_id = ?`, householdID, personID).Scan(&member); err != nil {
		return PersonalDevice{}, err
	}
	if member == 0 {
		return PersonalDevice{}, &FieldError{"person", "a personal device belongs to a member of the household"}
	}
	ph := PersonalDevice{ID: newID(), Name: name, PersonID: personID, PairedAt: at}
	err := expectOne(db.sql.Exec(`UPDATE personal_device_pairings SET person_id = ?, name = ?, personal_device_id = ?
		WHERE household_id = ? AND id = ? AND claim_hash IS NOT NULL AND person_id IS NULL AND expires_at > ?`,
		personID, name, ph.ID, householdID, id, stamp(at)))
	return ph, err
}

// CompletePersonalDevicePairing turns a confirmed pairing into a personal device with a device
// session (sessionHash, valid until expires). It succeeds once. A pairing the
// parent has not confirmed yet is ErrPersonalDevicePending; an unknown, other personal device's,
// used or expired pairing is ErrNotFound.
func (db *DB) CompletePersonalDevicePairing(codeHash, claimHash, sessionHash string, at, expires time.Time) (PersonalDevice, error) {
	tx, err := db.sql.Begin()
	if err != nil {
		return PersonalDevice{}, err
	}
	defer tx.Rollback()
	var id, householdID, name string
	var person, personalDeviceID sql.NullString
	err = tx.QueryRow(`SELECT id, household_id, person_id, name, personal_device_id FROM personal_device_pairings
		WHERE code_hash = ? AND claim_hash = ? AND expires_at > ?`, codeHash, claimHash, stamp(at)).Scan(&id, &householdID, &person, &name, &personalDeviceID)
	if errors.Is(err, sql.ErrNoRows) {
		return PersonalDevice{}, ErrNotFound
	}
	if err != nil {
		return PersonalDevice{}, err
	}
	if !person.Valid {
		return PersonalDevice{}, ErrPersonalDevicePending
	}
	ph := PersonalDevice{ID: personalDeviceID.String, Name: name, PersonID: person.String, PairedAt: at, LastSeen: at}
	if _, err := tx.Exec(`INSERT INTO personal_devices (id, household_id, person_id, name, created_at, last_seen_at) VALUES (?, ?, ?, ?, ?, ?)`,
		ph.ID, householdID, ph.PersonID, ph.Name, stamp(at), stamp(at)); err != nil {
		return PersonalDevice{}, err
	}
	if _, err := tx.Exec(`INSERT INTO sessions (id, token_hash, person_id, created_at, expires_at, personal_device_id) VALUES (?, ?, ?, ?, ?, ?)`,
		newID(), sessionHash, ph.PersonID, stamp(at), stamp(expires), ph.ID); err != nil {
		return PersonalDevice{}, err
	}
	if _, err := tx.Exec(`DELETE FROM personal_device_pairings WHERE id = ?`, id); err != nil {
		return PersonalDevice{}, err
	}
	return ph, tx.Commit()
}

// Personal devices lists a household's paired personal devices, oldest first.
func (db *DB) PersonalDevices(householdID string) ([]PersonalDevice, error) {
	rows, err := db.sql.Query(`SELECT ph.id, ph.name, ph.person_id, p.name, ph.created_at, ph.last_seen_at,
		(SELECT MAX(expires_at) FROM sessions WHERE personal_device_id = ph.id)
		FROM personal_devices ph JOIN people p ON p.id = ph.person_id
		WHERE ph.household_id = ? ORDER BY ph.created_at, ph.rowid`, householdID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []PersonalDevice
	for rows.Next() {
		var ph PersonalDevice
		var created, seen, ends sql.NullString
		if err := rows.Scan(&ph.ID, &ph.Name, &ph.PersonID, &ph.PersonName, &created, &seen, &ends); err != nil {
			return nil, err
		}
		ph.PairedAt, ph.LastSeen, ph.SessionEnds = parseStamp(created), parseStamp(seen), parseStamp(ends)
		out = append(out, ph)
	}
	return out, rows.Err()
}

// RevokePersonalDevice removes a personal device; its device session ends with it.
func (db *DB) RevokePersonalDevice(householdID, id string) error {
	return expectOne(db.sql.Exec(`DELETE FROM personal_devices WHERE household_id = ? AND id = ?`, householdID, id))
}

// TouchPersonalDeviceSession records that the personal device behind the session was seen at t,
// at most once a minute. A session that belongs to no personal device is left alone.
func (db *DB) TouchPersonalDeviceSession(sessionHash string, t time.Time) {
	_, _ = db.sql.Exec(`UPDATE personal_devices SET last_seen_at = ?
		WHERE id = (SELECT personal_device_id FROM sessions WHERE token_hash = ?) AND (last_seen_at IS NULL OR last_seen_at <= ?)`,
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
