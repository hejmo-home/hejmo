package store

import (
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/hejmo-home/hejmo/internal/calendar"
	"github.com/hejmo-home/hejmo/internal/i18n"
)

// SetCalendarReadOnly switches a CalDAV calendar between two-way and
// read-only (ADR 0012). Other kinds have no switch: ICS calendars are always
// read-only and the family calendar is not synced.
func (db *DB) SetCalendarReadOnly(householdID, calendarID string, readOnly bool) error {
	var kind string
	err := db.sql.QueryRow(`SELECT kind FROM calendars WHERE id = ? AND household_id = ?`, calendarID, householdID).Scan(&kind)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	if kind != "caldav" {
		return fmt.Errorf("%w: only calendars from CalDAV can be switched between two-way and read-only", ErrInvalid)
	}
	return expectOne(db.sql.Exec(`UPDATE calendars SET read_only = ?, updated_at = ? WHERE id = ? AND household_id = ?`, readOnly, now(), calendarID, householdID))
}

// pushMarker records, inside the transaction of a box change, which remote
// objects the change touches, so the change is written to the server later
// even if the box is offline now. Only two-way CalDAV calendars qualify.
type pushMarker struct {
	tx   *sql.Tx
	seen map[[2]string]bool // calendar id, object address
	list [][2]string
}

func (m *pushMarker) add(calendarID, address string) {
	k := [2]string{calendarID, address}
	if m.seen == nil {
		m.seen = map[[2]string]bool{}
	}
	if !m.seen[k] {
		m.seen[k] = true
		m.list = append(m.list, k)
	}
}

// old marks the object an existing event belongs to.
func (m *pushMarker) old(eventID string) error {
	var cal, address string
	err := m.tx.QueryRow(`SELECT e.calendar_id, e.remote_url FROM events e JOIN calendars c ON c.id = e.calendar_id
		WHERE e.id = ? AND e.remote_url IS NOT NULL AND c.kind = 'caldav' AND c.read_only = 0`, eventID).Scan(&cal, &address)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	m.add(cal, address)
	return nil
}

type eventRow struct {
	id, calendarID, base string
	seriesID             sql.NullString
	recurrenceID         sql.NullString
	remoteURL, remoteKey sql.NullString
}

func (m *pushMarker) row(id string) (eventRow, bool, error) {
	r := eventRow{id: id}
	err := m.tx.QueryRow(`SELECT e.calendar_id, c.remote_url, e.series_id, e.recurrence_id, e.remote_url, e.remote_key
		FROM events e JOIN calendars c ON c.id = e.calendar_id
		WHERE e.id = ? AND c.kind = 'caldav' AND c.read_only = 0 AND c.remote_url IS NOT NULL`, id).
		Scan(&r.calendarID, &r.base, &r.seriesID, &r.recurrenceID, &r.remoteURL, &r.remoteKey)
	if errors.Is(err, sql.ErrNoRows) {
		return r, false, nil
	}
	return r, err == nil, err
}

// assign gives events of two-way calendars the address and key they have
// outside (a new event becomes its own object, named after its id; a changed
// occurrence belongs to its series' object) and marks their objects.
func (m *pushMarker) assign(ids []string) error {
	var rows []eventRow
	for _, id := range ids {
		r, ok, err := m.row(id)
		if err != nil {
			return err
		}
		if ok {
			rows = append(rows, r)
		}
	}
	// Keys of changed occurrences follow their series and original start, which
	// an edit may change; free them first so none collides with another.
	for _, r := range rows {
		if r.seriesID.Valid {
			if _, err := m.tx.Exec(`UPDATE events SET remote_key = NULL WHERE id = ?`, r.id); err != nil {
				return err
			}
		}
	}
	for _, r := range rows {
		masterID := r.id
		if r.seriesID.Valid {
			masterID = r.seriesID.String
		}
		var address, uid sql.NullString
		if err := m.tx.QueryRow(`SELECT remote_url, remote_key FROM events WHERE id = ?`, masterID).Scan(&address, &uid); err != nil {
			return err
		}
		if !address.Valid {
			address = sql.NullString{String: strings.TrimSuffix(r.base, "/") + "/" + url.PathEscape(masterID) + ".ics", Valid: true}
			uid = sql.NullString{String: masterID, Valid: true}
			if _, err := m.tx.Exec(`UPDATE events SET remote_url = ?, remote_key = ? WHERE id = ?`, address.String, uid.String, masterID); err != nil {
				return err
			}
		}
		if r.seriesID.Valid {
			rec, err := time.Parse(time.RFC3339, r.recurrenceID.String)
			if err != nil {
				return err
			}
			if _, err := m.tx.Exec(`UPDATE events SET remote_url = ?, remote_key = ? WHERE id = ?`, address.String, OverrideKey(uid.String, rec), r.id); err != nil {
				return err
			}
		}
		m.add(r.calendarID, address.String)
	}
	return nil
}

// OverrideKey is the key a changed occurrence is known by outside: its
// series' UID and its original start.
func OverrideKey(uid string, recurrenceID time.Time) string {
	return uid + "#" + recurrenceID.UTC().Format(time.RFC3339)
}

func (m *pushMarker) flush() error {
	for _, k := range m.list {
		if _, err := m.tx.Exec(`INSERT INTO remote_objects (calendar_id, url, etag, raw, pending, changed_at) VALUES (?, ?, '', '', 1, ?)
			ON CONFLICT (calendar_id, url) DO UPDATE SET pending = pending + 1, changed_at = excluded.changed_at`, k[0], k[1], now()); err != nil {
			return err
		}
	}
	return nil
}

// PendingObject is a remote object with box changes that are not yet written
// to the server. A new object has an empty ETag and Raw.
type PendingObject struct {
	URL, ETag, Raw string
	Rev            int // changes counted; pass it back when the push is done
	ChangedAt      time.Time
}

// PendingObjects lists the objects of a calendar waiting to be pushed, oldest change first.
func (db *DB) PendingObjects(calendarID string) ([]PendingObject, error) {
	rows, err := db.sql.Query(`SELECT url, etag, raw, pending, changed_at FROM remote_objects WHERE calendar_id = ? AND pending > 0 ORDER BY changed_at, url`, calendarID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []PendingObject
	for rows.Next() {
		var p PendingObject
		var changed string
		if err := rows.Scan(&p.URL, &p.ETag, &p.Raw, &p.Rev, &changed); err != nil {
			return nil, err
		}
		p.ChangedAt, _ = time.Parse(time.RFC3339, changed)
		out = append(out, p)
	}
	return out, rows.Err()
}

// ObjectContent is what the box holds for one remote object: the UID it has
// outside and its events (series master first, then changed occurrences).
type ObjectContent struct {
	UID    string
	Events []calendar.Event
}

// ObjectEvents returns the box's events of one remote object. None means the
// object was deleted on the box.
func (db *DB) ObjectEvents(householdID, calendarID, address string) (ObjectContent, error) {
	evs, err := db.queryEvents(eventSelect+` WHERE household_id = ? AND calendar_id = ? AND remote_url = ? ORDER BY series_id IS NOT NULL, start_local, id`,
		householdID, calendarID, address)
	if err != nil {
		return ObjectContent{}, err
	}
	out := ObjectContent{Events: evs}
	err = db.sql.QueryRow(`SELECT remote_key FROM events WHERE calendar_id = ? AND remote_url = ? AND series_id IS NULL AND remote_key IS NOT NULL LIMIT 1`,
		calendarID, address).Scan(&out.UID)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return ObjectContent{}, err
	}
	return out, nil
}

// PushDone records that the box's changes up to rev were written to the
// server, which now holds raw under etag. A change made meanwhile stays queued.
func (db *DB) PushDone(calendarID, address string, rev int, etag, raw string) error {
	_, err := db.sql.Exec(`UPDATE remote_objects SET etag = ?, raw = ?, pending = CASE WHEN pending = ? THEN 0 ELSE pending END
		WHERE calendar_id = ? AND url = ?`, etag, raw, rev, calendarID, address)
	return err
}

// PushDeleted records that the object was deleted on the server for the
// changes up to rev, and forgets it.
func (db *DB) PushDeleted(calendarID, address string, rev int) error {
	_, err := db.sql.Exec(`DELETE FROM remote_objects WHERE calendar_id = ? AND url = ? AND pending = ?`, calendarID, address, rev)
	return err
}

// Conflict is a sync conflict for the log: what happened and the version that lost.
type Conflict struct {
	Message i18n.Message
	Detail  string
}

// LogConflict records a conflict the box won: the version that lost stays in
// the sync log for 30 days.
func (db *DB) LogConflict(householdID, calendarID string, message i18n.Message, detail string, at time.Time) error {
	tx, err := db.sql.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := writeLogDetail(tx, householdID, calendarID, at, "conflict", message, detail); err != nil {
		return err
	}
	if err := pruneLog(tx, at); err != nil {
		return err
	}
	return tx.Commit()
}

// ResolveToOutside ends a conflict the server won: the object's events become
// what the server holds, the box's queued changes up to rev are dropped (a
// later change stays queued) and the conflict is logged.
func (db *DB) ResolveToOutside(householdID, calendarID string, change ObjectChange, rev int, c Conflict, at time.Time) error {
	tx, err := db.sql.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	address := change.URL
	if err := replaceSyncedEvents(tx, householdID, calendarID, change.Items, &address); err != nil {
		return fmt.Errorf("object %s: %w", address, err)
	}
	if _, err := tx.Exec(`UPDATE remote_objects SET etag = ?, raw = ?, pending = CASE WHEN pending = ? THEN 0 ELSE pending END
		WHERE calendar_id = ? AND url = ?`, change.ETag, change.Raw, rev, calendarID, address); err != nil {
		return err
	}
	if err := writeLogDetail(tx, householdID, calendarID, at, "conflict", c.Message, c.Detail); err != nil {
		return err
	}
	if err := pruneLog(tx, at); err != nil {
		return err
	}
	return tx.Commit()
}
