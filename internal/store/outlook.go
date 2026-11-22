package store

import (
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/hejmo-home/hejmo/internal/calendar"
)

// outlookURL is what the url column holds for an Outlook connection (the
// column is required; Graph's address comes from the client's configuration).
const outlookURL = "https://graph.microsoft.com"

// NewOutlookConnection describes an Outlook connection to create after a
// device sign-in. ClientID is empty when the Hejmo project's app registration
// was used.
type NewOutlookConnection struct {
	Account, ClientID, RefreshToken string
	Now                             time.Time
}

// NewOutlookCalendar describes a calendar of an Outlook connection that
// parents switched on. RemoteID is Graph's id of the calendar. SourceLocked
// calendars (shared with the account, birthdays) are read-only for good.
type NewOutlookCalendar struct {
	Name, RemoteID, Color, SourceOwner, SourceName string
	Audience                                       calendar.Audience
	IntervalMinutes                                int
	SourceLocked                                   bool
	Now                                            time.Time
}

// CreateOutlookConnection stores a connection with its refresh token sealed.
func (db *DB) CreateOutlookConnection(householdID string, in NewOutlookConnection) (Connection, error) {
	if strings.TrimSpace(in.Account) == "" || in.RefreshToken == "" {
		return Connection{}, fmt.Errorf("%w: account and sign-in required", ErrInvalid)
	}
	sealed, err := db.seal(in.RefreshToken)
	if err != nil {
		return Connection{}, err
	}
	id := newID()
	if _, err := db.sql.Exec(`INSERT INTO calendar_connections (id, household_id, kind, url, username, credential, client_id, renewed_at, created_at)
		VALUES (?, ?, 'outlook', ?, ?, ?, ?, ?, ?)`, id, householdID, outlookURL, in.Account, sealed, strings.TrimSpace(in.ClientID), stamp(in.Now), stamp(in.Now)); err != nil {
		return Connection{}, err
	}
	return db.Connection(householdID, id)
}

// FindOutlookConnection returns the household's Outlook connection for an
// account (compared without regard to case), or ErrNotFound.
func (db *DB) FindOutlookConnection(householdID, account string) (Connection, error) {
	all, err := db.Connections(householdID)
	if err != nil {
		return Connection{}, err
	}
	for _, c := range all {
		if c.Kind == "outlook" && strings.EqualFold(c.Username, strings.TrimSpace(account)) {
			return c, nil
		}
	}
	return Connection{}, ErrNotFound
}

// SaveRefreshToken stores a refresh token Microsoft rotated during a sync.
func (db *DB) SaveRefreshToken(connectionID, refresh string, at time.Time) error {
	sealed, err := db.seal(refresh)
	if err != nil {
		return err
	}
	return expectOne(db.sql.Exec(`UPDATE calendar_connections SET credential = ?, renewed_at = ? WHERE id = ? AND kind = 'outlook'`, sealed, stamp(at), connectionID))
}

// MarkReauth records that Microsoft ended the sign-in. The first time stays.
func (db *DB) MarkReauth(connectionID string, at time.Time) error {
	return expectOne(db.sql.Exec(`UPDATE calendar_connections SET reauth_since = CASE WHEN reauth_since = '' THEN ? ELSE reauth_since END WHERE id = ? AND kind = 'outlook'`, stamp(at), connectionID))
}

// ReauthConnections lists the household's Outlook connections whose sign-in
// has ended, longest first.
func (db *DB) ReauthConnections(householdID string) ([]Connection, error) {
	all, err := db.Connections(householdID)
	if err != nil {
		return nil, err
	}
	var out []Connection
	for _, c := range all {
		if c.Kind == "outlook" && !c.ReauthSince.IsZero() {
			out = append(out, c)
		}
	}
	return out, nil
}

// Reconnected stores a new sign-in for a connection: the old failure is
// forgotten and its calendars sync again at once. Names, colours and
// audiences stay.
func (db *DB) Reconnected(householdID, connectionID, refresh string, at time.Time) error {
	sealed, err := db.seal(refresh)
	if err != nil {
		return err
	}
	tx, err := db.sql.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := expectOne(tx.Exec(`UPDATE calendar_connections SET credential = ?, renewed_at = ?, reauth_since = '' WHERE id = ? AND household_id = ? AND kind = 'outlook'`,
		sealed, stamp(at), connectionID, householdID)); err != nil {
		return err
	}
	if _, err := tx.Exec(`UPDATE calendar_sync SET next_attempt_at = ?, failures = 0, last_error_key = '', last_error_params = '{}'
		WHERE calendar_id IN (SELECT id FROM calendars WHERE connection_id = ?)`, stamp(at), connectionID); err != nil {
		return err
	}
	return tx.Commit()
}

// AddOutlookCalendar adds a calendar of an Outlook connection, due for its
// first sync at once. It is two-way unless the source locks it.
func (db *DB) AddOutlookCalendar(householdID, connectionID string, in NewOutlookCalendar) (SyncedCalendar, error) {
	if strings.TrimSpace(in.Name) == "" || strings.TrimSpace(in.RemoteID) == "" {
		return SyncedCalendar{}, fmt.Errorf("%w: name and calendar required", ErrInvalid)
	}
	if in.IntervalMinutes <= 0 {
		return SyncedCalendar{}, fmt.Errorf("%w: sync interval must be positive", ErrInvalid)
	}
	if c, err := db.Connection(householdID, connectionID); err != nil {
		return SyncedCalendar{}, err
	} else if c.Kind != "outlook" {
		return SyncedCalendar{}, fmt.Errorf("%w: not an Outlook connection", ErrInvalid)
	}
	calID, ts := newID(), stamp(in.Now)
	tx, err := db.sql.Begin()
	if err != nil {
		return SyncedCalendar{}, err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`INSERT INTO calendars (id, household_id, name, kind, color, connection_id, remote_url, read_only, source_locked, source_owner, source_name, created_at, updated_at)
		VALUES (?, ?, ?, 'outlook', ?, ?, ?, ?, ?, ?, ?, ?, ?)`, calID, householdID, strings.TrimSpace(in.Name), in.Color, connectionID, in.RemoteID,
		in.SourceLocked, in.SourceLocked, in.SourceOwner, in.SourceName, ts, ts); err != nil {
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

// OutlookCreated records that a new event of the box was created in Outlook
// under graphID (for the changes up to rev): the events of the object, which
// carried a stand-in address, take the real one, and the object keeps the
// version Outlook reported. A change made meanwhile stays queued.
func (db *DB) OutlookCreated(calendarID, address, graphID string, rev int, changeKey string) error {
	tx, err := db.sql.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	rows, err := tx.Query(`SELECT id, series_id, recurrence_id FROM events WHERE calendar_id = ? AND remote_url = ?`, calendarID, address)
	if err != nil {
		return err
	}
	type row struct {
		id     string
		series sql.NullString
		rec    sql.NullString
	}
	var evs []row
	for rows.Next() {
		var r row
		if err := rows.Scan(&r.id, &r.series, &r.rec); err != nil {
			rows.Close()
			return err
		}
		evs = append(evs, r)
	}
	rows.Close()
	// Free the keys first: an override's key follows its series' new id.
	if _, err := tx.Exec(`UPDATE events SET remote_key = NULL WHERE calendar_id = ? AND remote_url = ?`, calendarID, address); err != nil {
		return err
	}
	for _, r := range evs {
		key := graphID
		if r.series.Valid {
			rec, err := time.Parse(time.RFC3339, r.rec.String)
			if err != nil {
				return err
			}
			key = OverrideKey(graphID, rec)
		}
		if _, err := tx.Exec(`UPDATE events SET remote_url = ?, remote_key = ? WHERE id = ?`, graphID, key, r.id); err != nil {
			return err
		}
	}
	if err := expectOne(tx.Exec(`UPDATE remote_objects SET url = ?, etag = ?, raw = '', pending = CASE WHEN pending = ? THEN 0 ELSE pending END
		WHERE calendar_id = ? AND url = ?`, graphID, changeKey, rev, calendarID, address)); err != nil {
		return err
	}
	return tx.Commit()
}

// SeriesObjects lists the remote objects of a calendar that hold a series.
func (db *DB) SeriesObjects(calendarID string) ([]string, error) {
	return db.strings(`SELECT DISTINCT remote_url FROM events WHERE calendar_id = ? AND rrule != '' AND series_id IS NULL AND remote_url IS NOT NULL`, calendarID)
}
