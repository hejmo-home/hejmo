package store

import (
	"database/sql"
	"errors"
	"time"
)

// Account holds an adult's sign-in credentials.
type Account struct {
	PersonID      string
	Username      string
	PasswordHash  string
	TOTPSecret    string
	TOTPConfirmed bool
}

// APIToken is a bearer token acting on behalf of a person, limited by scopes.
type APIToken struct {
	ID       string
	Name     string
	Hash     string
	PersonID string
	Scopes   string
	// Filled by APITokens only.
	PersonName string
	Created    time.Time
	LastUsed   time.Time // zero = never
}

// Configured reports whether first-run setup is complete: a parent account
// exists and has confirmed its TOTP.
func (db *DB) Configured() bool {
	var n int
	_ = db.sql.QueryRow(`SELECT COUNT(*) FROM accounts a
		JOIN household_members m ON m.person_id = a.person_id
		WHERE a.totp_confirmed_at IS NOT NULL AND m.role = 'parent'`).Scan(&n)
	return n > 0
}

// ResetUnfinishedSetup removes a half-finished setup so it can start over.
func (db *DB) ResetUnfinishedSetup() error {
	if db.Configured() {
		return errors.New("store: box is already configured")
	}
	tx, err := db.sql.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, q := range []string{`DELETE FROM api_tokens`, `DELETE FROM sessions`, `DELETE FROM accounts`,
		`DELETE FROM household_members`, `DELETE FROM people`, `DELETE FROM households`} {
		if _, err := tx.Exec(q); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// CreateAccount stores credentials for an existing person.
func (db *DB) CreateAccount(a Account) error {
	_, err := db.sql.Exec(`INSERT INTO accounts (person_id, username, password_hash, totp_secret, created_at)
		VALUES (?, ?, ?, ?, ?)`, a.PersonID, a.Username, a.PasswordHash, a.TOTPSecret, now())
	return err
}

// ConfirmTOTP marks a person's TOTP as set up.
func (db *DB) ConfirmTOTP(personID string) error {
	return expectOne(db.sql.Exec(`UPDATE accounts SET totp_confirmed_at = ? WHERE person_id = ?`, now(), personID))
}

// AccountByUsername looks up credentials (case-insensitive).
func (db *DB) AccountByUsername(username string) (Account, error) {
	return db.scanAccount(db.sql.QueryRow(`SELECT person_id, username, password_hash, totp_secret, totp_confirmed_at IS NOT NULL
		FROM accounts WHERE username = ?`, username))
}

// AccountByPerson looks up credentials by person.
func (db *DB) AccountByPerson(personID string) (Account, error) {
	return db.scanAccount(db.sql.QueryRow(`SELECT person_id, username, password_hash, totp_secret, totp_confirmed_at IS NOT NULL
		FROM accounts WHERE person_id = ?`, personID))
}

func (db *DB) scanAccount(row *sql.Row) (Account, error) {
	var a Account
	err := row.Scan(&a.PersonID, &a.Username, &a.PasswordHash, &a.TOTPSecret, &a.TOTPConfirmed)
	if errors.Is(err, sql.ErrNoRows) {
		return Account{}, ErrNotFound
	}
	return a, err
}

// CreateSession stores a browser session by token hash.
func (db *DB) CreateSession(tokenHash, personID string, expires time.Time) error {
	_, err := db.sql.Exec(`INSERT INTO sessions (token_hash, person_id, created_at, expires_at) VALUES (?, ?, ?, ?)`,
		tokenHash, personID, now(), expires.UTC().Format(time.RFC3339))
	return err
}

// SessionPerson returns the person of a valid session at time t.
func (db *DB) SessionPerson(tokenHash string, t time.Time) (string, error) {
	var id string
	err := db.sql.QueryRow(`SELECT person_id FROM sessions WHERE token_hash = ? AND expires_at > ?`,
		tokenHash, t.UTC().Format(time.RFC3339)).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrNotFound
	}
	return id, err
}

// DeleteSession ends a session.
func (db *DB) DeleteSession(tokenHash string) error {
	_, err := db.sql.Exec(`DELETE FROM sessions WHERE token_hash = ?`, tokenHash)
	return err
}

// CreateAPIToken stores a token (only its hash).
func (db *DB) CreateAPIToken(t APIToken) (APIToken, error) {
	t.ID = newID()
	_, err := db.sql.Exec(`INSERT INTO api_tokens (id, name, token_hash, person_id, scopes, created_at) VALUES (?, ?, ?, ?, ?, ?)`,
		t.ID, t.Name, t.Hash, t.PersonID, t.Scopes, now())
	return t, err
}

// APITokenByHash returns an active (not revoked) token.
func (db *DB) APITokenByHash(hash string) (APIToken, error) {
	var t APIToken
	var used sql.NullString
	err := db.sql.QueryRow(`SELECT id, name, token_hash, person_id, scopes, last_used_at FROM api_tokens
		WHERE token_hash = ? AND revoked_at IS NULL`, hash).Scan(&t.ID, &t.Name, &t.Hash, &t.PersonID, &t.Scopes, &used)
	if errors.Is(err, sql.ErrNoRows) {
		return APIToken{}, ErrNotFound
	}
	t.LastUsed = parseStamp(used)
	return t, err
}

// APITokens lists a household's active (not revoked) tokens, oldest first.
// The token hash is left out.
func (db *DB) APITokens(householdID string) ([]APIToken, error) {
	rows, err := db.sql.Query(`SELECT t.id, t.name, t.person_id, p.name, t.scopes, t.created_at, t.last_used_at
		FROM api_tokens t JOIN household_members m ON m.person_id = t.person_id JOIN people p ON p.id = t.person_id
		WHERE m.household_id = ? AND t.revoked_at IS NULL ORDER BY t.created_at, t.rowid`, householdID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []APIToken
	for rows.Next() {
		var t APIToken
		var created string
		var used sql.NullString
		if err := rows.Scan(&t.ID, &t.Name, &t.PersonID, &t.PersonName, &t.Scopes, &created, &used); err != nil {
			return nil, err
		}
		t.Created, t.LastUsed = parseStamp(sql.NullString{String: created, Valid: true}), parseStamp(used)
		out = append(out, t)
	}
	return out, rows.Err()
}

// TouchAPIToken records that a token was used at t.
func (db *DB) TouchAPIToken(id string, t time.Time) error {
	return expectOne(db.sql.Exec(`UPDATE api_tokens SET last_used_at = ? WHERE id = ?`, stamp(t), id))
}

// RevokeAPIToken disables a token for good.
func (db *DB) RevokeAPIToken(id string) error {
	return expectOne(db.sql.Exec(`UPDATE api_tokens SET revoked_at = ? WHERE id = ? AND revoked_at IS NULL`, now(), id))
}

// SetDisplayPIN stores the hashed parent PIN for displays.
func (db *DB) SetDisplayPIN(householdID, hash string) error {
	return expectOne(db.sql.Exec(`UPDATE households SET display_pin_hash = ?, updated_at = ? WHERE id = ?`, hash, now(), householdID))
}

// DisplayPINHash returns the hashed display PIN.
func (db *DB) DisplayPINHash(householdID string) (string, error) {
	var h string
	err := db.sql.QueryRow(`SELECT display_pin_hash FROM households WHERE id = ?`, householdID).Scan(&h)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrNotFound
	}
	return h, err
}

func expectOne(res sql.Result, err error) error {
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}
