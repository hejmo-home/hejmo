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
	// TOTPReset is set once the codes were reset (ADR 0017): the next sign-in
	// sets up new ones.
	TOTPReset bool
	// TOTPConfirmedAt is when the one-time code was set up; zero until then.
	TOTPConfirmedAt time.Time
	// TOTPResetAt and TOTPResetBy say when and by whom (a person ID) the codes
	// were reset; zero and "" when they were not, or by the box's command line.
	TOTPResetAt time.Time
	TOTPResetBy string
	// Language is the interface language the person chose (ticket 134), or ""
	// when none was chosen yet.
	Language string
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
// exists and has confirmed its TOTP (or had it reset later: a reset does not
// send the box back to the wizard).
func (db *DB) Configured() bool {
	var n int
	_ = db.sql.QueryRow(`SELECT COUNT(*) FROM accounts a
		JOIN household_members m ON m.person_id = a.person_id
		WHERE (a.totp_confirmed_at IS NOT NULL OR a.totp_reset_at IS NOT NULL) AND m.role = 'parent'`).Scan(&n)
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
	return db.scanAccount(db.sql.QueryRow(`SELECT person_id, username, password_hash, totp_secret, totp_confirmed_at IS NOT NULL, totp_reset_at IS NOT NULL, totp_confirmed_at, totp_reset_at, COALESCE(totp_reset_by, ''), COALESCE(language, '')
		FROM accounts WHERE username = ?`, username))
}

// AccountByPerson looks up credentials by person.
func (db *DB) AccountByPerson(personID string) (Account, error) {
	return db.scanAccount(db.sql.QueryRow(`SELECT person_id, username, password_hash, totp_secret, totp_confirmed_at IS NOT NULL, totp_reset_at IS NOT NULL, totp_confirmed_at, totp_reset_at, COALESCE(totp_reset_by, ''), COALESCE(language, '')
		FROM accounts WHERE person_id = ?`, personID))
}

func (db *DB) scanAccount(row *sql.Row) (Account, error) {
	var a Account
	var confirmedAt, resetAt sql.NullString
	err := row.Scan(&a.PersonID, &a.Username, &a.PasswordHash, &a.TOTPSecret, &a.TOTPConfirmed, &a.TOTPReset, &confirmedAt, &resetAt, &a.TOTPResetBy, &a.Language)
	if errors.Is(err, sql.ErrNoRows) {
		return Account{}, ErrNotFound
	}
	a.TOTPConfirmedAt, a.TOTPResetAt = parseStamp(confirmedAt), parseStamp(resetAt)
	return a, err
}

// CreateSession stores a browser session by token hash. device holds the
// derived device and browser keys, never the raw User-Agent.
func (db *DB) CreateSession(tokenHash, personID, device string, created, expires time.Time) error {
	_, err := db.sql.Exec(`INSERT INTO sessions (id, token_hash, person_id, device, created_at, last_seen_at, expires_at) VALUES (?, ?, ?, ?, ?, ?, ?)`,
		newID(), tokenHash, personID, device, stamp(created), stamp(created), stamp(expires))
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

// IsPhoneSession reports whether the session is a paired phone's device session.
func (db *DB) IsPhoneSession(tokenHash string) bool {
	var n int
	err := db.sql.QueryRow(`SELECT COUNT(*) FROM sessions WHERE token_hash = ? AND phone_id IS NOT NULL`, tokenHash).Scan(&n)
	return err == nil && n > 0
}

// DeleteSession ends a session. A phone's device session takes the phone with
// it: signing out on the phone unpairs it.
func (db *DB) DeleteSession(tokenHash string) error {
	if _, err := db.sql.Exec(`DELETE FROM phones WHERE id = (SELECT phone_id FROM sessions WHERE token_hash = ?)`, tokenHash); err != nil {
		return err
	}
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

// APITokenByID returns one active token of the household.
func (db *DB) APITokenByID(householdID, id string) (APIToken, error) {
	tokens, err := db.APITokens(householdID)
	if err != nil {
		return APIToken{}, err
	}
	for _, t := range tokens {
		if t.ID == id {
			return t, nil
		}
	}
	return APIToken{}, ErrNotFound
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

// SetTOTPSecret stores a new one-time code secret for a person whose codes
// were reset.
func (db *DB) SetTOTPSecret(personID, secret string) error {
	return expectOne(db.sql.Exec(`UPDATE accounts SET totp_secret = ? WHERE person_id = ?`, secret, personID))
}

// RecoveryCode is a stored (hashed) recovery code.
type RecoveryCode struct{ ID, Hash string }

// ReplaceRecoveryCodes stores a new set of recovery code hashes and voids the old one.
func (db *DB) ReplaceRecoveryCodes(personID string, hashes []string) error {
	tx, err := db.sql.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`DELETE FROM recovery_codes WHERE person_id = ?`, personID); err != nil {
		return err
	}
	for _, h := range hashes {
		if _, err := tx.Exec(`INSERT INTO recovery_codes (id, person_id, code_hash, created_at) VALUES (?, ?, ?, ?)`,
			newID(), personID, h, now()); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// UnusedRecoveryCodes lists the person's recovery codes that are still valid.
func (db *DB) UnusedRecoveryCodes(personID string) ([]RecoveryCode, error) {
	rows, err := db.sql.Query(`SELECT id, code_hash FROM recovery_codes WHERE person_id = ? AND used_at IS NULL ORDER BY rowid`, personID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []RecoveryCode
	for rows.Next() {
		var c RecoveryCode
		if err := rows.Scan(&c.ID, &c.Hash); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// SpendRecoveryCode marks a recovery code as used. It returns ErrNotFound if
// the code was spent already, so two sign-ins cannot use one code.
func (db *DB) SpendRecoveryCode(id string) error {
	return expectOne(db.sql.Exec(`UPDATE recovery_codes SET used_at = ? WHERE id = ? AND used_at IS NULL`, now(), id))
}

// ResetOTP removes a person's one-time code secret and recovery codes and
// ends their browser sessions. Device tokens and paired phones' device
// sessions stay (ADR 0017). The next sign-in must set up one-time codes again.
// This is the reset from the box's command line; it names nobody as the one
// who reset.
func (db *DB) ResetOTP(personID string) error {
	_, err := db.ResetOTPBy(personID, "", nil)
	return err
}

// ResetOTPBy is ResetOTP by another parent (byID, who is remembered), which
// also removes the given passkeys of the person in the same step (ADR 0018:
// a passkey on the lost device could otherwise keep signing in). Only
// passkeys of the person are removed; the others in removeIDs are ignored.
// It returns the passkeys it removed.
func (db *DB) ResetOTPBy(personID, byID string, removeIDs []string) ([]Passkey, error) {
	tx, err := db.sql.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	by := sql.NullString{String: byID, Valid: byID != ""}
	res, err := tx.Exec(`UPDATE accounts SET totp_secret = '', totp_confirmed_at = NULL, totp_reset_at = ?, totp_reset_by = ? WHERE person_id = ?`, now(), by, personID)
	if err := expectOne(res, err); err != nil {
		return nil, err
	}
	for _, q := range []string{`DELETE FROM recovery_codes WHERE person_id = ?`, `DELETE FROM sessions WHERE person_id = ? AND phone_id IS NULL`} {
		if _, err := tx.Exec(q, personID); err != nil {
			return nil, err
		}
	}
	var removed []Passkey
	for _, id := range removeIDs {
		p, err := scanPasskey(tx.QueryRow(`SELECT `+passkeyColumns+` FROM passkeys WHERE id = ? AND person_id = ?`, id, personID))
		if errors.Is(err, sql.ErrNoRows) {
			continue
		}
		if err != nil {
			return nil, err
		}
		if _, err := tx.Exec(`DELETE FROM passkeys WHERE id = ?`, id); err != nil {
			return nil, err
		}
		removed = append(removed, p)
	}
	return removed, tx.Commit()
}

// SetAccountLanguage stores the interface language a person chose.
func (db *DB) SetAccountLanguage(personID, language string) error {
	return expectOne(db.sql.Exec(`UPDATE accounts SET language = ? WHERE person_id = ?`, language, personID))
}
