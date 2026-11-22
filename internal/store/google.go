package store

import (
	"database/sql"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/hejmo-home/hejmo/internal/calendar"
	"github.com/hejmo-home/hejmo/internal/googleauth"
	"github.com/hejmo-home/hejmo/internal/secret"
)

// Errors of a started sign-in at Google.
var (
	ErrUsed    = errors.New("store: already used")
	ErrExpired = errors.New("store: expired")
)

func (db *DB) seal(plain string) (string, error) {
	if db.secrets == nil {
		return "", errNoKey
	}
	raw, err := db.secrets.Seal([]byte(plain))
	if err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(raw), nil
}

func (db *DB) unseal(sealed string) (string, error) {
	if db.secrets == nil {
		return "", errNoKey
	}
	raw, err := base64.StdEncoding.DecodeString(sealed)
	if err != nil {
		return "", errors.New("store: stored credential is not sealed text")
	}
	plain, err := db.secrets.Open(raw)
	if err != nil {
		return "", errors.New("store: cannot open stored credential (wrong key or altered text)")
	}
	return string(plain), nil
}

// GoogleApp is the family's OAuth client as shown to parents: never the secret.
type GoogleApp struct {
	ClientID  string
	CreatedBy string // person id
	CreatedAt time.Time
}

// GoogleApp returns the household's app registration, or ErrNotFound.
func (db *DB) GoogleApp(householdID string) (GoogleApp, error) {
	var a GoogleApp
	var by sql.NullString
	var created string
	err := db.sql.QueryRow(`SELECT client_id, created_by, created_at FROM google_app WHERE household_id = ?`, householdID).Scan(&a.ClientID, &by, &created)
	if errors.Is(err, sql.ErrNoRows) {
		return GoogleApp{}, ErrNotFound
	}
	if err != nil {
		return GoogleApp{}, err
	}
	a.CreatedBy = by.String
	a.CreatedAt, _ = time.Parse(time.RFC3339, created)
	return a, nil
}

// GoogleAppSecret returns the client ID and the unsealed client secret.
func (db *DB) GoogleAppSecret(householdID string) (clientID, secret string, err error) {
	var sealed string
	err = db.sql.QueryRow(`SELECT client_id, client_secret FROM google_app WHERE household_id = ?`, householdID).Scan(&clientID, &sealed)
	if errors.Is(err, sql.ErrNoRows) {
		return "", "", ErrNotFound
	}
	if err != nil {
		return "", "", err
	}
	secret, err = db.unseal(sealed)
	return clientID, secret, err
}

// SetGoogleApp stores the app registration, sealing the secret. Another
// client ID makes every refresh token of the household useless (they belong to
// the old client): those connections must sign in again, and changed reports
// that. The same client ID with a new secret keeps them.
func (db *DB) SetGoogleApp(householdID, personID, clientID, secret string, at time.Time) (changed bool, err error) {
	clientID = strings.TrimSpace(clientID)
	if clientID == "" || secret == "" {
		return false, fmt.Errorf("%w: client ID and client secret required", ErrInvalid)
	}
	sealed, err := db.seal(secret)
	if err != nil {
		return false, err
	}
	tx, err := db.sql.Begin()
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	var old string
	switch err := tx.QueryRow(`SELECT client_id FROM google_app WHERE household_id = ?`, householdID).Scan(&old); {
	case errors.Is(err, sql.ErrNoRows):
	case err != nil:
		return false, err
	default:
		changed = old != clientID
	}
	var by any
	if personID != "" {
		by = personID
	}
	if _, err := tx.Exec(`INSERT INTO google_app (household_id, client_id, client_secret, created_by, created_at) VALUES (?, ?, ?, ?, ?)
		ON CONFLICT(household_id) DO UPDATE SET client_id = excluded.client_id, client_secret = excluded.client_secret,
		created_by = CASE WHEN google_app.client_id = excluded.client_id THEN google_app.created_by ELSE excluded.created_by END,
		created_at = CASE WHEN google_app.client_id = excluded.client_id THEN google_app.created_at ELSE excluded.created_at END`,
		householdID, clientID, sealed, by, stamp(at)); err != nil {
		return false, err
	}
	if changed {
		if _, err := tx.Exec(`UPDATE calendar_connections SET signin_needed_at = ? WHERE household_id = ? AND kind = 'google' AND signin_needed_at IS NULL`, stamp(at), householdID); err != nil {
			return false, err
		}
	}
	return changed, tx.Commit()
}

// GoogleLogin is a sign-in that was started and not yet finished.
type GoogleLogin struct {
	Nonce, PersonID, Verifier, RedirectURI string
	// ConnectionID is set when an existing connection signs in again.
	ConnectionID string
	ExpiresAt    time.Time
}

// CreateGoogleLogin stores a started sign-in with its verifier sealed, and
// forgets the ones that expired more than a day ago.
func (db *DB) CreateGoogleLogin(householdID string, l GoogleLogin, at time.Time) error {
	sealed, err := db.seal(l.Verifier)
	if err != nil {
		return err
	}
	var person, conn any
	if l.PersonID != "" {
		person = l.PersonID
	}
	if l.ConnectionID != "" {
		conn = l.ConnectionID
	}
	if _, err := db.sql.Exec(`DELETE FROM google_login WHERE expires_at < ?`, stamp(at.Add(-24*time.Hour))); err != nil {
		return err
	}
	_, err = db.sql.Exec(`INSERT INTO google_login (nonce, household_id, person_id, verifier, redirect_uri, connection_id, created_at, expires_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		l.Nonce, householdID, person, sealed, l.RedirectURI, conn, stamp(at), stamp(l.ExpiresAt))
	return err
}

// PruneGoogleLogins forgets what a sign-in no longer needs: once it has expired
// its stashed account and refresh token go, and a day later the whole row.
func (db *DB) PruneGoogleLogins(at time.Time) error {
	if _, err := db.sql.Exec(`UPDATE google_login SET pending_account = NULL, pending_token = NULL WHERE expires_at < ? AND pending_token IS NOT NULL`, stamp(at)); err != nil {
		return err
	}
	_, err := db.sql.Exec(`DELETE FROM google_login WHERE expires_at < ?`, stamp(at.Add(-24*time.Hour)))
	return err
}

// TakeGoogleLogin uses a started sign-in up: it is marked used whatever the
// outcome, so one state is never accepted twice. ErrNotFound for one the box
// never made (or of another household), ErrUsed for one used before,
// ErrExpired for one that is older than its lifetime.
func (db *DB) TakeGoogleLogin(householdID, nonce string, at time.Time) (GoogleLogin, error) {
	tx, err := db.sql.Begin()
	if err != nil {
		return GoogleLogin{}, err
	}
	defer tx.Rollback()
	var l GoogleLogin
	var person, conn, used sql.NullString
	var sealed, expires string
	err = tx.QueryRow(`SELECT nonce, person_id, verifier, redirect_uri, connection_id, expires_at, used_at FROM google_login WHERE nonce = ? AND household_id = ?`,
		nonce, householdID).Scan(&l.Nonce, &person, &sealed, &l.RedirectURI, &conn, &expires, &used)
	if errors.Is(err, sql.ErrNoRows) {
		return GoogleLogin{}, ErrNotFound
	}
	if err != nil {
		return GoogleLogin{}, err
	}
	if used.Valid {
		return GoogleLogin{}, ErrUsed
	}
	if _, err := tx.Exec(`UPDATE google_login SET used_at = ? WHERE nonce = ?`, stamp(at), nonce); err != nil {
		return GoogleLogin{}, err
	}
	if err := tx.Commit(); err != nil {
		return GoogleLogin{}, err
	}
	l.PersonID, l.ConnectionID = person.String, conn.String
	l.ExpiresAt, _ = time.Parse(time.RFC3339, expires)
	if at.After(l.ExpiresAt) {
		return GoogleLogin{}, ErrExpired
	}
	if l.Verifier, err = db.unseal(sealed); err != nil {
		return GoogleLogin{}, err
	}
	return l, nil
}

// CreateGoogleConnection stores a signed-in Google account: the account in
// the user name, the refresh token sealed.
func (db *DB) CreateGoogleConnection(householdID, account, refreshToken string, at time.Time) (Connection, error) {
	account = strings.TrimSpace(account)
	if account == "" || refreshToken == "" {
		return Connection{}, fmt.Errorf("%w: account and refresh token required", ErrInvalid)
	}
	sealed, err := db.seal(refreshToken)
	if err != nil {
		return Connection{}, err
	}
	id := newID()
	if _, err := db.sql.Exec(`INSERT INTO calendar_connections (id, household_id, kind, url, username, credential, single_calendar, created_at, signed_in_at, token_renewed_at)
		VALUES (?, ?, 'google', ?, ?, ?, 0, ?, ?, ?)`, id, householdID, googleauth.CalDAVBase, account, sealed, stamp(at), stamp(at), stamp(at)); err != nil {
		return Connection{}, err
	}
	return Connection{ID: id, HouseholdID: householdID, Kind: "google", URL: googleauth.CalDAVBase, Username: account, CreatedAt: at, SignedInAt: at, TokenRenewedAt: at}, nil
}

// SetGoogleRefreshToken stores the token of a new sign-in for an existing
// connection, clears "sign in again" and makes its calendars due at once.
func (db *DB) SetGoogleRefreshToken(householdID, connectionID, refreshToken string, at time.Time) error {
	if refreshToken == "" {
		return fmt.Errorf("%w: refresh token required", ErrInvalid)
	}
	sealed, err := db.seal(refreshToken)
	if err != nil {
		return err
	}
	tx, err := db.sql.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := expectOne(tx.Exec(`UPDATE calendar_connections SET credential = ?, signed_in_at = ?, token_renewed_at = ?, signin_needed_at = NULL
		WHERE id = ? AND household_id = ? AND kind = 'google'`, sealed, stamp(at), stamp(at), connectionID, householdID)); err != nil {
		return err
	}
	if _, err := tx.Exec(`UPDATE calendar_sync SET next_attempt_at = ?, failures = 0 WHERE calendar_id IN (SELECT id FROM calendars WHERE connection_id = ?)`, stamp(at), connectionID); err != nil {
		return err
	}
	return tx.Commit()
}

// MarkSignInNeeded records that Google refuses the connection's refresh
// token. The first time stays: it says since when.
func (db *DB) MarkSignInNeeded(householdID, connectionID string, at time.Time) error {
	return expectOne(db.sql.Exec(`UPDATE calendar_connections SET signin_needed_at = COALESCE(signin_needed_at, ?) WHERE id = ? AND household_id = ? AND kind = 'google'`,
		stamp(at), connectionID, householdID))
}

// MarkTokenRenewed records a successful renewal of the access token.
func (db *DB) MarkTokenRenewed(householdID, connectionID string, at time.Time) error {
	return expectOne(db.sql.Exec(`UPDATE calendar_connections SET token_renewed_at = ? WHERE id = ? AND household_id = ? AND kind = 'google'`, stamp(at), connectionID, householdID))
}

// NewGoogleCalendar describes a calendar of a Google connection that parents ticked.
type NewGoogleCalendar struct {
	Name, CalendarID, Color string
	Audience                calendar.Audience
	// ReadOnly makes the box never change it; SourceReadOnly says that Google
	// shares it for viewing only, so it can never be two-way.
	ReadOnly, SourceReadOnly bool
	IntervalMinutes          int
	Now                      time.Time
}

// AddGoogleCalendar adds a calendar of a Google connection, due for its first
// sync at once. Its address is Google's CalDAV address of the calendar id.
func (db *DB) AddGoogleCalendar(householdID, connectionID string, in NewGoogleCalendar) (SyncedCalendar, error) {
	if strings.TrimSpace(in.Name) == "" || strings.TrimSpace(in.CalendarID) == "" {
		return SyncedCalendar{}, fmt.Errorf("%w: name and calendar required", ErrInvalid)
	}
	if in.IntervalMinutes <= 0 {
		return SyncedCalendar{}, fmt.Errorf("%w: sync interval must be positive", ErrInvalid)
	}
	var kind string
	if err := db.sql.QueryRow(`SELECT kind FROM calendar_connections WHERE id = ? AND household_id = ?`, connectionID, householdID).Scan(&kind); err != nil || kind != "google" {
		return SyncedCalendar{}, ErrNotFound
	}
	calID, ts := newID(), stamp(in.Now)
	tx, err := db.sql.Begin()
	if err != nil {
		return SyncedCalendar{}, err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`INSERT INTO calendars (id, household_id, name, kind, color, connection_id, remote_url, read_only, source_read_only, created_at, updated_at)
		VALUES (?, ?, ?, 'google', ?, ?, ?, ?, ?, ?, ?)`, calID, householdID, strings.TrimSpace(in.Name), in.Color, connectionID,
		googleauth.CalendarURL(googleauth.CalDAVBase, in.CalendarID), in.ReadOnly || in.SourceReadOnly, in.SourceReadOnly, ts, ts); err != nil {
		return SyncedCalendar{}, err
	}
	if err := writeAudience(tx, "calendar_audience", "calendar_id", calID, in.Audience); err != nil {
		return SyncedCalendar{}, err
	}
	if _, err := tx.Exec(`INSERT INTO calendar_sync (calendar_id, interval_minutes, started_at, next_attempt_at) VALUES (?, ?, ?, ?)`,
		calID, in.IntervalMinutes, ts, ts); err != nil {
		return SyncedCalendar{}, err
	}
	if err := tx.Commit(); err != nil {
		return SyncedCalendar{}, err
	}
	return db.SyncedCalendar(householdID, calID)
}

// StashGoogleToken keeps the refresh token of a sign-in with another account
// than the connection that asked for it, on the used login, so the parent can
// add that account as a new connection instead of signing in again.
func (db *DB) StashGoogleToken(householdID, nonce, account, refreshToken string) error {
	sealed, err := db.seal(refreshToken)
	if err != nil {
		return err
	}
	return expectOne(db.sql.Exec(`UPDATE google_login SET pending_account = ?, pending_token = ? WHERE nonce = ? AND household_id = ?`, account, sealed, nonce, householdID))
}

// TakeStashedGoogleToken returns the stashed account and refresh token once
// and forgets them; ErrNotFound if there are none.
func (db *DB) TakeStashedGoogleToken(householdID, nonce string) (account, refreshToken string, err error) {
	tx, err := db.sql.Begin()
	if err != nil {
		return "", "", err
	}
	defer tx.Rollback()
	var acc, sealed sql.NullString
	err = tx.QueryRow(`SELECT pending_account, pending_token FROM google_login WHERE nonce = ? AND household_id = ?`, nonce, householdID).Scan(&acc, &sealed)
	if errors.Is(err, sql.ErrNoRows) || (err == nil && !sealed.Valid) {
		return "", "", ErrNotFound
	}
	if err != nil {
		return "", "", err
	}
	if _, err := tx.Exec(`UPDATE google_login SET pending_account = NULL, pending_token = NULL WHERE nonce = ?`, nonce); err != nil {
		return "", "", err
	}
	if err := tx.Commit(); err != nil {
		return "", "", err
	}
	token, err := db.unseal(sealed.String)
	return acc.String, token, err
}

// Sealer is the box's key, for values that the box signs without storing them.
func (db *DB) Sealer() *secret.Sealer { return db.secrets }
