package store

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/hejmo-home/hejmo/internal/calendar"
	"github.com/hejmo-home/hejmo/internal/i18n"
	"github.com/hejmo-home/hejmo/internal/secret"
)

// SetSecrets sets the key box that seals credentials. Without it, storing or
// reading a credential fails: the box never keeps one in plain text.
func (db *DB) SetSecrets(box *secret.Box) { db.secrets = box }

var errNoKey = errors.New("store: no encryption key set for credentials")

// Connection is a calendar connection as shown to parents. It never carries
// the password.
type Connection struct {
	ID          string
	HouseholdID string
	Kind        string // caldav
	URL         string // server address, or the calendar address for a single calendar
	Username    string
	Single      bool // URL is one calendar, added by hand; nothing to discover
	CreatedAt   time.Time
}

// NewCalDAVConnection describes a CalDAV or iCloud connection to create.
type NewCalDAVConnection struct {
	URL, Username, Password string
	Single                  bool
	Now                     time.Time
}

// NewCalDAVCalendar describes a calendar of a connection that parents ticked.
type NewCalDAVCalendar struct {
	Name, RemoteURL, Color string
	Audience               calendar.Audience
	IntervalMinutes        int
	Now                    time.Time
}

// CreateCalDAVConnection stores a connection with its password sealed.
func (db *DB) CreateCalDAVConnection(householdID string, in NewCalDAVConnection) (Connection, error) {
	if strings.TrimSpace(in.URL) == "" || in.Username == "" || in.Password == "" {
		return Connection{}, fmt.Errorf("%w: address, user name and password required", ErrInvalid)
	}
	if db.secrets == nil {
		return Connection{}, errNoKey
	}
	sealed, err := db.secrets.Seal(in.Password)
	if err != nil {
		return Connection{}, err
	}
	id := newID()
	if _, err := db.sql.Exec(`INSERT INTO calendar_connections (id, household_id, kind, url, username, credential, single_calendar, created_at) VALUES (?, ?, 'caldav', ?, ?, ?, ?, ?)`,
		id, householdID, in.URL, in.Username, sealed, in.Single, stamp(in.Now)); err != nil {
		return Connection{}, err
	}
	return Connection{ID: id, HouseholdID: householdID, Kind: "caldav", URL: in.URL, Username: in.Username, Single: in.Single, CreatedAt: in.Now}, nil
}

// Connections lists a household's CalDAV connections.
func (db *DB) Connections(householdID string) ([]Connection, error) {
	rows, err := db.sql.Query(`SELECT id, household_id, kind, url, username, single_calendar, created_at FROM calendar_connections
		WHERE household_id = ? AND kind = 'caldav' ORDER BY created_at, rowid`, householdID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Connection
	for rows.Next() {
		var c Connection
		var created string
		if err := rows.Scan(&c.ID, &c.HouseholdID, &c.Kind, &c.URL, &c.Username, &c.Single, &created); err != nil {
			return nil, err
		}
		c.CreatedAt, _ = time.Parse(time.RFC3339, created)
		out = append(out, c)
	}
	return out, rows.Err()
}

// Connection returns one CalDAV connection of a household.
func (db *DB) Connection(householdID, connectionID string) (Connection, error) {
	all, err := db.Connections(householdID)
	if err != nil {
		return Connection{}, err
	}
	for _, c := range all {
		if c.ID == connectionID {
			return c, nil
		}
	}
	return Connection{}, ErrNotFound
}

// ConnectionCredentials returns the user name and the unsealed password.
func (db *DB) ConnectionCredentials(householdID, connectionID string) (user, password string, err error) {
	if db.secrets == nil {
		return "", "", errNoKey
	}
	var sealed string
	err = db.sql.QueryRow(`SELECT username, credential FROM calendar_connections WHERE id = ? AND household_id = ? AND kind = 'caldav'`,
		connectionID, householdID).Scan(&user, &sealed)
	if errors.Is(err, sql.ErrNoRows) {
		return "", "", ErrNotFound
	}
	if err != nil {
		return "", "", err
	}
	if password, err = db.secrets.Open(sealed); err != nil {
		return "", "", err
	}
	return user, password, nil
}

// AddCalDAVCalendar adds a calendar of a connection. It is read-only (write-back
// follows with its own ticket) and due for its first sync at once.
func (db *DB) AddCalDAVCalendar(householdID, connectionID string, in NewCalDAVCalendar) (SyncedCalendar, error) {
	if strings.TrimSpace(in.Name) == "" || strings.TrimSpace(in.RemoteURL) == "" {
		return SyncedCalendar{}, fmt.Errorf("%w: name and address required", ErrInvalid)
	}
	if in.IntervalMinutes <= 0 {
		return SyncedCalendar{}, fmt.Errorf("%w: sync interval must be positive", ErrInvalid)
	}
	if _, err := db.Connection(householdID, connectionID); err != nil {
		return SyncedCalendar{}, err
	}
	calID, ts := newID(), stamp(in.Now)
	tx, err := db.sql.Begin()
	if err != nil {
		return SyncedCalendar{}, err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`INSERT INTO calendars (id, household_id, name, kind, color, connection_id, remote_url, read_only, created_at, updated_at)
		VALUES (?, ?, ?, 'caldav', ?, ?, ?, 1, ?, ?)`, calID, householdID, strings.TrimSpace(in.Name), in.Color, connectionID, in.RemoteURL, ts, ts); err != nil {
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

// DeleteConnection removes a connection with all its calendars and their
// events. Nothing is deleted outside the box.
func (db *DB) DeleteConnection(householdID, connectionID string) error {
	return expectOne(db.sql.Exec(`DELETE FROM calendar_connections WHERE id = ? AND household_id = ? AND kind = 'caldav'`, connectionID, householdID))
}

// ObjectChange is one remote object that is new or changed: its address,
// ETag, raw iCalendar text and the events it holds. With KeepEvents the object
// could not be read: its ETag is remembered so it is not downloaded again
// until the server changes it, but the events from its last good version stay.
type ObjectChange struct {
	URL, ETag, Raw string
	Items          []SyncItem
	KeepEvents     bool
}

// RemoteObjects returns the ETag of every remote object the box holds for a
// calendar, by address.
func (db *DB) RemoteObjects(calendarID string) (map[string]string, error) {
	rows, err := db.sql.Query(`SELECT url, etag FROM remote_objects WHERE calendar_id = ?`, calendarID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var url, etag string
		if err := rows.Scan(&url, &etag); err != nil {
			return nil, err
		}
		out[url] = etag
	}
	return out, rows.Err()
}

// CompleteObjectSync stores a successful delta sync of a CalDAV calendar:
// changed objects replace their events, deleted objects take their events with
// them, notes go to the log and the failure state is cleared. All or nothing.
func (db *DB) CompleteObjectSync(householdID, calendarID string, changed []ObjectChange, deleted []string, notes []i18n.Message, at, next time.Time) error {
	tx, err := db.sql.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var one int
	if err := tx.QueryRow(`SELECT 1 FROM calendars WHERE id = ? AND household_id = ? AND connection_id IS NOT NULL`, calendarID, householdID).Scan(&one); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		return err
	}
	for _, url := range deleted {
		if _, err := tx.Exec(`DELETE FROM events WHERE calendar_id = ? AND remote_url = ?`, calendarID, url); err != nil {
			return err
		}
		if _, err := tx.Exec(`DELETE FROM remote_objects WHERE calendar_id = ? AND url = ?`, calendarID, url); err != nil {
			return err
		}
	}
	for _, o := range changed {
		url := o.URL
		if !o.KeepEvents {
			if err := replaceSyncedEvents(tx, householdID, calendarID, o.Items, &url); err != nil {
				return fmt.Errorf("object %s: %w", o.URL, err)
			}
		}
		if _, err := tx.Exec(`INSERT INTO remote_objects (calendar_id, url, etag, raw) VALUES (?, ?, ?, ?)
			ON CONFLICT (calendar_id, url) DO UPDATE SET etag = excluded.etag, raw = excluded.raw`, calendarID, o.URL, o.ETag, o.Raw); err != nil {
			return err
		}
	}
	if err := recordSuccess(tx, householdID, calendarID, notes, at, next); err != nil {
		return err
	}
	return tx.Commit()
}
