package store

import (
	"database/sql"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/hejmo-home/hejmo/internal/calendar"
	"github.com/hejmo-home/hejmo/internal/i18n"
	"github.com/hejmo-home/hejmo/internal/secret"
)

// SetSecrets sets the sealer for credentials. Without it, storing or reading a
// credential fails: the box never keeps one in plain text. The sealed bytes are
// kept base64-encoded in the text column.
func (db *DB) SetSecrets(s *secret.Sealer) { db.secrets = s }

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
	raw, err := db.secrets.Seal([]byte(in.Password))
	if err != nil {
		return Connection{}, err
	}
	sealed := base64.StdEncoding.EncodeToString(raw)
	id := newID()
	if _, err := db.sql.Exec(`INSERT INTO calendar_connections (id, household_id, kind, url, username, credential, single_calendar, created_at) VALUES (?, ?, 'caldav', ?, ?, ?, ?, ?)`,
		id, householdID, in.URL, in.Username, sealed, in.Single, stamp(in.Now)); err != nil {
		return Connection{}, err
	}
	return Connection{ID: id, HouseholdID: householdID, Kind: "caldav", URL: in.URL, Username: in.Username, Single: in.Single, CreatedAt: in.Now}, nil
}

// SetConnectionPassword replaces the password of a connection, sealed like the
// first one. The caller has checked it against the server.
func (db *DB) SetConnectionPassword(householdID, connectionID, password string) error {
	if password == "" {
		return fmt.Errorf("%w: password required", ErrInvalid)
	}
	if db.secrets == nil {
		return errNoKey
	}
	raw, err := db.secrets.Seal([]byte(password))
	if err != nil {
		return err
	}
	return expectOne(db.sql.Exec(`UPDATE calendar_connections SET credential = ? WHERE id = ? AND household_id = ? AND kind = 'caldav'`,
		base64.StdEncoding.EncodeToString(raw), connectionID, householdID))
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
	raw, err := base64.StdEncoding.DecodeString(sealed)
	if err != nil {
		return "", "", errors.New("store: stored credential is not sealed text")
	}
	plain, err := db.secrets.Open(raw)
	if err != nil {
		return "", "", errors.New("store: cannot open stored credential (wrong key or altered text)")
	}
	return user, string(plain), nil
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
// It returns what the run did to the events.
func (db *DB) CompleteObjectSync(householdID, calendarID string, changed []ObjectChange, deleted []string, notes []i18n.Message, at, next time.Time) (SyncRun, error) {
	tx, err := db.sql.Begin()
	if err != nil {
		return SyncRun{}, err
	}
	defer tx.Rollback()
	var one int
	if err := tx.QueryRow(`SELECT 1 FROM calendars WHERE id = ? AND household_id = ? AND connection_id IS NOT NULL`, calendarID, householdID).Scan(&one); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return SyncRun{}, ErrNotFound
		}
		return SyncRun{}, err
	}
	changes := newRunChanges()
	for _, url := range deleted {
		gone, err := topLevelEvents(tx, calendarID, url)
		if err != nil {
			return SyncRun{}, err
		}
		for _, id := range gone {
			changes.gone[id] = true
		}
		if _, err := tx.Exec(`DELETE FROM events WHERE calendar_id = ? AND remote_url = ?`, calendarID, url); err != nil {
			return SyncRun{}, err
		}
		if _, err := tx.Exec(`DELETE FROM remote_objects WHERE calendar_id = ? AND url = ?`, calendarID, url); err != nil {
			return SyncRun{}, err
		}
	}
	for _, o := range changed {
		url := o.URL
		if !o.KeepEvents {
			got, err := replaceSyncedEvents(tx, householdID, calendarID, o.Items, &url)
			if err != nil {
				return SyncRun{}, fmt.Errorf("object %s: %w", o.URL, err)
			}
			for id := range got.added {
				changes.added[id] = true
			}
			for id := range got.updated {
				changes.updated[id] = true
			}
			for id := range got.gone {
				changes.gone[id] = true
			}
		}
		// An unreadable object keeps the last good text, not the junk.
		if _, err := tx.Exec(`INSERT INTO remote_objects (calendar_id, url, etag, raw) VALUES (?, ?, ?, ?)
			ON CONFLICT (calendar_id, url) DO UPDATE SET etag = excluded.etag, raw = CASE WHEN ? THEN raw ELSE excluded.raw END`, calendarID, o.URL, o.ETag, o.Raw, o.KeepEvents); err != nil {
			return SyncRun{}, err
		}
	}
	run, err := recordSuccess(tx, householdID, calendarID, notes, at, next, changes)
	if err != nil {
		return SyncRun{}, err
	}
	return run, tx.Commit()
}

// topLevelEvents lists the ids of the single events and series masters that
// came from one remote object.
func topLevelEvents(tx *sql.Tx, calendarID, object string) ([]string, error) {
	rows, err := tx.Query(`SELECT id FROM events WHERE calendar_id = ? AND remote_url = ? AND series_id IS NULL`, calendarID, object)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}
