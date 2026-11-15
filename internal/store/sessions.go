package store

import (
	"database/sql"
	"errors"
	"time"
)

// SessionTouchInterval is how often at most a browser session records that
// its browser was used.
const SessionTouchInterval = 5 * time.Minute

// BrowserSession is the sign-in of one person in one browser (GLOSSARY).
type BrowserSession struct {
	ID     string // public handle, not derived from the token
	Person Person
	// Device is "device|browser" as keys (internal/auth), or "" for a session
	// made before the box kept it.
	Device   string
	SignedIn time.Time
	// LastSeen is zero for a session made before the box kept it.
	LastSeen time.Time
	Expires  time.Time
}

// TouchSession records that the browser behind the session was used at t, at
// most every SessionTouchInterval. A paired phone's session is left alone.
func (db *DB) TouchSession(tokenHash string, t time.Time) {
	_, _ = db.sql.Exec(`UPDATE sessions SET last_seen_at = ?
		WHERE token_hash = ? AND phone_id IS NULL AND (last_seen_at IS NULL OR last_seen_at <= ?)`,
		stamp(t), tokenHash, stamp(t.Add(-SessionTouchInterval)))
}

// SessionID returns the public id of the session behind a token hash.
func (db *DB) SessionID(tokenHash string) (string, error) {
	var id sql.NullString
	err := db.sql.QueryRow(`SELECT id FROM sessions WHERE token_hash = ?`, tokenHash).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) || (err == nil && !id.Valid) {
		return "", ErrNotFound
	}
	return id.String, err
}

const browserSessionSelect = `SELECT s.id, s.device, s.created_at, s.last_seen_at, s.expires_at, ` + personColumns + `
	FROM sessions s
	JOIN people p ON p.id = s.person_id
	JOIN household_members m ON m.person_id = p.id AND m.household_id = ?
	WHERE s.phone_id IS NULL AND s.expires_at > ?`

func scanBrowserSession(r rowScanner) (BrowserSession, error) {
	var b BrowserSession
	var created, expires string
	var seen sql.NullString
	if err := r.Scan(&b.ID, &b.Device, &created, &seen, &expires,
		&b.Person.ID, &b.Person.Name, &b.Person.Role, &b.Person.Color, &b.Person.Shape, &b.Person.Symbol); err != nil {
		return BrowserSession{}, err
	}
	b.SignedIn, b.LastSeen = parseStamp(sql.NullString{String: created, Valid: true}), parseStamp(seen)
	b.Expires = parseStamp(sql.NullString{String: expires, Valid: true})
	return b, nil
}

// ListBrowserSessions returns the browser sessions of a household that have
// not expired at time at: paired phones' device sessions are left out. They
// come in household person order, then last used, newest first.
func (db *DB) ListBrowserSessions(householdID string, at time.Time) ([]BrowserSession, error) {
	rows, err := db.sql.Query(browserSessionSelect+`
		ORDER BY m.sort_order, p.name, COALESCE(s.last_seen_at, s.created_at) DESC, s.created_at DESC, s.id`, householdID, stamp(at))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []BrowserSession
	for rows.Next() {
		b, err := scanBrowserSession(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	return out, rows.Err()
}

// BrowserSession returns one unexpired browser session of the household.
func (db *DB) BrowserSession(householdID, id string, at time.Time) (BrowserSession, error) {
	b, err := scanBrowserSession(db.sql.QueryRow(browserSessionSelect+` AND s.id = ?`, householdID, stamp(at), id))
	if errors.Is(err, sql.ErrNoRows) {
		return BrowserSession{}, ErrNotFound
	}
	return b, err
}

// EndBrowserSession deletes a browser session, so its browser is refused at
// its next request. A paired phone's session is not touched: the phone is
// removed on the phones screen. A session that is gone reports ErrNotFound.
func (db *DB) EndBrowserSession(householdID, id string) error {
	return expectOne(db.sql.Exec(`DELETE FROM sessions WHERE id = ? AND phone_id IS NULL
		AND person_id IN (SELECT person_id FROM household_members WHERE household_id = ?)`, id, householdID))
}
