package store

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/hejmo-home/hejmo/internal/calendar"
	"github.com/hejmo-home/hejmo/internal/i18n"
)

// syncLogKeep is how long sync log entries stay (ADR 0012).
const syncLogKeep = 30 * 24 * time.Hour

// SyncState is the sync bookkeeping of one synced calendar. Zero times mean
// "never".
type SyncState struct {
	IntervalMinutes int
	StartedAt       time.Time // when syncing began
	LastAttempt     time.Time
	LastSuccess     time.Time
	NextAttempt     time.Time
	Failures        int          // consecutive failures
	LastError       i18n.Message // empty after a success
	LastRun         SyncRun      // what the last successful run did
}

// SyncRun is what one sync run did to a calendar's events. A series, with its
// changed occurrences, counts as one event.
type SyncRun struct {
	Events                int // events the calendar holds after the run
	New, Changed, Removed int
}

// SyncedCalendar is a calendar fed from outside through a calendar connection.
type SyncedCalendar struct {
	Calendar
	ConnectionID string
	URL          string // the feed or calendar address
	Sync         SyncState
}

// NewICS describes an ICS subscription to create.
type NewICS struct {
	Name, URL, Color string
	Audience         calendar.Audience
	IntervalMinutes  int
	Now              time.Time
}

// SyncItem is one event of a feed. Key identifies it across syncs; SeriesKey
// names the master of a changed occurrence.
type SyncItem struct {
	Key, SeriesKey string
	Event          calendar.Event
}

// SyncLogEntry is one line of the sync log.
type SyncLogEntry struct {
	ID           string
	CalendarID   string
	CalendarName string
	At           time.Time
	Kind         string // failure, conflict, note
	Message      i18n.Message
}

func stamp(t time.Time) string { return t.UTC().Format(time.RFC3339) }

func parseStamp(s sql.NullString) time.Time {
	if !s.Valid || s.String == "" {
		return time.Time{}
	}
	t, _ := time.Parse(time.RFC3339, s.String)
	return t
}

// CreateICSCalendar adds a read-only ICS calendar with its connection. It is
// due for its first sync at once.
func (db *DB) CreateICSCalendar(householdID string, in NewICS) (SyncedCalendar, error) {
	if strings.TrimSpace(in.Name) == "" || strings.TrimSpace(in.URL) == "" {
		return SyncedCalendar{}, fmt.Errorf("%w: name and address required", ErrInvalid)
	}
	if in.IntervalMinutes <= 0 {
		return SyncedCalendar{}, fmt.Errorf("%w: sync interval must be positive", ErrInvalid)
	}
	connID, calID, ts := newID(), newID(), stamp(in.Now)
	tx, err := db.sql.Begin()
	if err != nil {
		return SyncedCalendar{}, err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`INSERT INTO calendar_connections (id, household_id, kind, url, created_at) VALUES (?, ?, 'ics', ?, ?)`,
		connID, householdID, in.URL, ts); err != nil {
		return SyncedCalendar{}, err
	}
	if _, err := tx.Exec(`INSERT INTO calendars (id, household_id, name, kind, color, connection_id, read_only, shared, created_at, updated_at)
		VALUES (?, ?, ?, 'ics', ?, ?, 1, 1, ?, ?)`, calID, householdID, strings.TrimSpace(in.Name), in.Color, connID, ts, ts); err != nil {
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

const syncedSelect = `SELECT c.id, c.household_id, c.name, c.kind, c.color, c.read_only, c.shared, n.id, COALESCE(c.remote_url, n.url),
	s.interval_minutes, s.started_at, s.last_attempt_at, s.last_success_at, s.next_attempt_at, s.failures, s.last_error_key, s.last_error_params,
	s.last_run_events, s.last_run_new, s.last_run_changed, s.last_run_removed
	FROM calendars c JOIN calendar_connections n ON n.id = c.connection_id JOIN calendar_sync s ON s.calendar_id = c.id`

// SyncedCalendar returns one synced calendar of a household.
func (db *DB) SyncedCalendar(householdID, calendarID string) (SyncedCalendar, error) {
	out, err := db.syncedCalendars(syncedSelect+` WHERE c.household_id = ? AND c.id = ?`, householdID, calendarID)
	if err != nil {
		return SyncedCalendar{}, err
	}
	if len(out) == 0 {
		return SyncedCalendar{}, ErrNotFound
	}
	return out[0], nil
}

// SyncedCalendars lists a household's synced calendars.
func (db *DB) SyncedCalendars(householdID string) ([]SyncedCalendar, error) {
	return db.syncedCalendars(syncedSelect+` WHERE c.household_id = ? ORDER BY c.created_at, c.rowid`, householdID)
}

// DueCalendars lists the synced calendars of all households whose next sync
// is due at or before now.
func (db *DB) DueCalendars(now time.Time) ([]SyncedCalendar, error) {
	return db.syncedCalendars(syncedSelect+` WHERE s.next_attempt_at <= ? ORDER BY s.next_attempt_at, c.rowid`, stamp(now))
}

func (db *DB) syncedCalendars(query string, args ...any) ([]SyncedCalendar, error) {
	rows, err := db.sql.Query(query, args...)
	if err != nil {
		return nil, err
	}
	var out []SyncedCalendar
	for rows.Next() {
		var c SyncedCalendar
		var started, attempt, success, next sql.NullString
		var errKey, errParams string
		if err := rows.Scan(&c.ID, &c.HouseholdID, &c.Name, &c.Kind, &c.Color, &c.ReadOnly, &c.Shared, &c.ConnectionID, &c.URL,
			&c.Sync.IntervalMinutes, &started, &attempt, &success, &next, &c.Sync.Failures, &errKey, &errParams,
			&c.Sync.LastRun.Events, &c.Sync.LastRun.New, &c.Sync.LastRun.Changed, &c.Sync.LastRun.Removed); err != nil {
			rows.Close()
			return nil, err
		}
		c.Sync.LastError = unpackMessage(errKey, errParams)
		c.Sync.StartedAt, c.Sync.LastAttempt, c.Sync.LastSuccess, c.Sync.NextAttempt = parseStamp(started), parseStamp(attempt), parseStamp(success), parseStamp(next)
		out = append(out, c)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for i := range out {
		if out[i].Audience, err = db.audience("calendar_audience", "calendar_id", out[i].ID); err != nil {
			return nil, err
		}
	}
	return out, nil
}

// UpdateSyncedCalendar changes the name, colour and audience of a synced
// calendar. What it syncs and how often is left alone.
func (db *DB) UpdateSyncedCalendar(householdID, calendarID, name, color string, audience calendar.Audience) error {
	if strings.TrimSpace(name) == "" {
		return fmt.Errorf("%w: name required", ErrInvalid)
	}
	tx, err := db.sql.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := expectOne(tx.Exec(`UPDATE calendars SET name = ?, color = ?, updated_at = ? WHERE id = ? AND household_id = ? AND connection_id IS NOT NULL`,
		strings.TrimSpace(name), color, stamp(time.Now()), calendarID, householdID)); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM calendar_audience WHERE calendar_id = ?`, calendarID); err != nil {
		return err
	}
	if err := writeAudience(tx, "calendar_audience", "calendar_id", calendarID, audience); err != nil {
		return err
	}
	return tx.Commit()
}

// DeleteSyncedCalendar removes a synced calendar with its events and log.
// An ICS subscription takes its connection with it; a CalDAV calendar leaves
// the connection and its other calendars alone. Nothing is deleted outside
// the box.
func (db *DB) DeleteSyncedCalendar(householdID, calendarID string) error {
	var kind string
	if err := db.sql.QueryRow(`SELECT kind FROM calendars WHERE id = ? AND household_id = ? AND connection_id IS NOT NULL`, calendarID, householdID).Scan(&kind); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		return err
	}
	if kind != "ics" {
		return expectOne(db.sql.Exec(`DELETE FROM calendars WHERE id = ? AND household_id = ?`, calendarID, householdID))
	}
	return expectOne(db.sql.Exec(`DELETE FROM calendar_connections WHERE household_id = ?
		AND id = (SELECT connection_id FROM calendars WHERE id = ? AND household_id = ?)`, householdID, calendarID, householdID))
}

// CompleteSync stores a successful sync: the feed's events replace the
// calendar's synced events (ids stay stable per key), notes go to the log
// and the failure state is cleared. All or nothing. It returns what the run
// did to the events.
func (db *DB) CompleteSync(householdID, calendarID string, items []SyncItem, notes []i18n.Message, at, next time.Time) (SyncRun, error) {
	tx, err := db.sql.Begin()
	if err != nil {
		return SyncRun{}, err
	}
	defer tx.Rollback()
	changes, err := replaceSyncedEvents(tx, householdID, calendarID, items, nil)
	if err != nil {
		return SyncRun{}, err
	}
	run, err := recordSuccess(tx, householdID, calendarID, notes, at, next, changes)
	if err != nil {
		return SyncRun{}, err
	}
	return run, tx.Commit()
}

// recordSuccess clears the failure state, stores what the run did, logs new
// notes and prunes the log.
func recordSuccess(tx *sql.Tx, householdID, calendarID string, notes []i18n.Message, at, next time.Time, changes runChanges) (SyncRun, error) {
	run := SyncRun{New: changes.new(), Changed: changes.changed(), Removed: changes.removed()}
	if err := tx.QueryRow(`SELECT COUNT(*) FROM events WHERE calendar_id = ? AND series_id IS NULL`, calendarID).Scan(&run.Events); err != nil {
		return SyncRun{}, err
	}
	if err := expectOne(tx.Exec(`UPDATE calendar_sync SET last_attempt_at = ?, last_success_at = ?, next_attempt_at = ?, failures = 0, last_error_key = '', last_error_params = '{}',
		last_run_events = ?, last_run_new = ?, last_run_changed = ?, last_run_removed = ?
		WHERE calendar_id = ? AND calendar_id IN (SELECT id FROM calendars WHERE household_id = ?)`, stamp(at), stamp(at), stamp(next),
		run.Events, run.New, run.Changed, run.Removed, calendarID, householdID)); err != nil {
		return SyncRun{}, err
	}
	for _, n := range notes {
		var same int
		if err := tx.QueryRow(`SELECT COUNT(*) FROM sync_log WHERE calendar_id = ? AND kind = 'note' AND message_key = ? AND params = ?`,
			calendarID, n.Key, packParams(n)).Scan(&same); err != nil {
			return SyncRun{}, err
		}
		if same == 0 {
			if err := writeLog(tx, householdID, calendarID, at, "note", n); err != nil {
				return SyncRun{}, err
			}
		}
	}
	if err := pruneLog(tx, at); err != nil {
		return SyncRun{}, err
	}
	return run, nil
}

// FailSync records a failed sync. The calendar's events stay as they were.
func (db *DB) FailSync(householdID, calendarID string, message i18n.Message, at, next time.Time) error {
	tx, err := db.sql.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := expectOne(tx.Exec(`UPDATE calendar_sync SET last_attempt_at = ?, next_attempt_at = ?, failures = failures + 1, last_error_key = ?, last_error_params = ?
		WHERE calendar_id = ? AND calendar_id IN (SELECT id FROM calendars WHERE household_id = ?)`, stamp(at), stamp(next), message.Key, packParams(message), calendarID, householdID)); err != nil {
		return err
	}
	if err := writeLog(tx, householdID, calendarID, at, "failure", message); err != nil {
		return err
	}
	if err := pruneLog(tx, at); err != nil {
		return err
	}
	return tx.Commit()
}

func writeLog(tx *sql.Tx, householdID, calendarID string, at time.Time, kind string, message i18n.Message) error {
	_, err := tx.Exec(`INSERT INTO sync_log (id, household_id, calendar_id, at, kind, message_key, params) VALUES (?, ?, ?, ?, ?, ?, ?)`,
		newID(), householdID, calendarID, stamp(at), kind, message.Key, packParams(message))
	return err
}

// packParams stores a message's parameters as JSON (map keys come out sorted,
// so equal messages give equal text).
func packParams(m i18n.Message) string {
	if len(m.Params) == 0 {
		return "{}"
	}
	b, _ := json.Marshal(m.Params)
	return string(b)
}

func unpackMessage(key, params string) i18n.Message {
	m := i18n.Message{Key: key}
	if key != "" && params != "" && params != "{}" {
		_ = json.Unmarshal([]byte(params), &m.Params)
	}
	return m
}

func pruneLog(tx *sql.Tx, now time.Time) error {
	_, err := tx.Exec(`DELETE FROM sync_log WHERE at < ?`, stamp(now.Add(-syncLogKeep)))
	return err
}

// SyncLog returns the newest entries of a household's sync log first.
func (db *DB) SyncLog(householdID string, limit int) ([]SyncLogEntry, error) {
	rows, err := db.sql.Query(`SELECT l.id, l.calendar_id, c.name, l.at, l.kind, l.message_key, l.params FROM sync_log l
		JOIN calendars c ON c.id = l.calendar_id WHERE l.household_id = ? ORDER BY l.at DESC, l.rowid DESC LIMIT ?`, householdID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []SyncLogEntry
	for rows.Next() {
		var e SyncLogEntry
		var at, key, params string
		if err := rows.Scan(&e.ID, &e.CalendarID, &e.CalendarName, &at, &e.Kind, &key, &params); err != nil {
			return nil, err
		}
		e.Message = unpackMessage(key, params)
		e.At, _ = time.Parse(time.RFC3339, at)
		out = append(out, e)
	}
	return out, rows.Err()
}

// runChanges records what a sync run did to the events of one calendar, by
// the id of the top-level event (a single event or a series master), so a
// series with several changed occurrences counts once.
type runChanges struct{ added, updated, gone map[string]bool }

func newRunChanges() runChanges {
	return runChanges{added: map[string]bool{}, updated: map[string]bool{}, gone: map[string]bool{}}
}

func (c runChanges) new() int     { return len(c.added) }
func (c runChanges) removed() int { return len(c.gone) }

// changed counts the events that changed and were neither new nor removed.
func (c runChanges) changed() int {
	n := 0
	for id := range c.updated {
		if !c.added[id] && !c.gone[id] {
			n++
		}
	}
	return n
}

// eventSignature is the stored content of an event as one string, so that an
// event the feed sends again unchanged is not counted (or rewritten) as a
// change.
func eventSignature(e calendar.Event) string {
	start, end, tz, recID, seriesID := eventColumns(e)
	var ex []string
	for _, x := range e.ExDates {
		ex = append(ex, x.UTC().Format(time.RFC3339))
	}
	sort.Strings(ex)
	b, _ := json.Marshal([]any{e.Title, e.Description, e.Location, e.AllDay, start, end, tz, e.RRule, seriesID, recID, ex})
	return string(b)
}

// storedSignature is eventSignature of the event as stored.
func storedSignature(tx *sql.Tx, id string) (string, error) {
	var title, desc, loc, start, end, tz, rrule string
	var allDay bool
	var series, rec sql.NullString
	if err := tx.QueryRow(`SELECT title, description, location, all_day, start_local, end_local, tzid, rrule, series_id, recurrence_id FROM events WHERE id = ?`, id).
		Scan(&title, &desc, &loc, &allDay, &start, &end, &tz, &rrule, &series, &rec); err != nil {
		return "", err
	}
	rows, err := tx.Query(`SELECT occurrence FROM event_exdates WHERE event_id = ? ORDER BY occurrence`, id)
	if err != nil {
		return "", err
	}
	defer rows.Close()
	var ex []string
	for rows.Next() {
		var x string
		if err := rows.Scan(&x); err != nil {
			return "", err
		}
		ex = append(ex, x)
	}
	var seriesID, recID any
	if series.Valid {
		seriesID = series.String
	}
	if rec.Valid {
		recID = rec.String
	}
	b, _ := json.Marshal([]any{title, desc, loc, allDay, start, end, tz, rrule, seriesID, recID, ex})
	return string(b), rows.Err()
}

// replaceSyncedEvents makes the calendar's synced events equal to items. With
// scope set (the address of one remote object) only the events that came from
// that object are replaced, and the new ones are recorded as coming from it.
// Events are matched by key across the whole calendar, so one that moved to
// another object keeps its id.
func replaceSyncedEvents(tx *sql.Tx, householdID, calendarID string, items []SyncItem, scope *string) (runChanges, error) {
	changes := newRunChanges()
	var one int
	if err := tx.QueryRow(`SELECT 1 FROM calendars WHERE id = ? AND household_id = ? AND connection_id IS NOT NULL`, calendarID, householdID).Scan(&one); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return changes, ErrNotFound
		}
		return changes, err
	}
	existing, err := remoteKeys(tx, calendarID)
	if err != nil {
		return changes, err
	}
	inScope := func(r remoteEvent) bool { return scope == nil || (r.url.Valid && r.url.String == *scope) }

	ids := map[string]string{}
	for _, it := range items {
		if r, ok := existing[it.Key]; ok {
			ids[it.Key] = r.id
		} else {
			ids[it.Key] = newID()
		}
	}
	for key, r := range existing {
		if _, kept := ids[key]; !kept && inScope(r) {
			if _, err := tx.Exec(`DELETE FROM events WHERE id = ?`, r.id); err != nil { // cascades to changed occurrences
				return changes, err
			}
			if r.series.Valid {
				changes.updated[r.series.String] = true
			} else {
				changes.gone[r.id] = true
			}
		}
	}
	// Deleting a series master cascades to its stored changed occurrences, so
	// look again: a row that is gone is inserted, not updated.
	if existing, err = remoteKeys(tx, calendarID); err != nil {
		return changes, err
	}
	var object any
	if scope != nil {
		object = *scope
	}
	// Series masters first, so changed occurrences can point at them.
	for _, pass := range []bool{false, true} {
		for _, it := range items {
			if (it.SeriesKey != "") != pass {
				continue
			}
			e := it.Event
			e.ID, e.CalendarID = ids[it.Key], calendarID
			e.SeriesID = ""
			if it.SeriesKey != "" {
				if e.SeriesID = ids[it.SeriesKey]; e.SeriesID == "" {
					continue // changed occurrence of an unknown series
				}
			}
			top := e.ID
			if e.SeriesID != "" {
				top = e.SeriesID
			}
			if _, had := existing[it.Key]; had {
				var before string
				if before, err = storedSignature(tx, e.ID); err == nil && before != eventSignature(e) {
					if err = updateEventRow(tx, householdID, e); err == nil {
						changes.updated[top] = true
					}
				}
			} else if err = insertEventRow(tx, householdID, e); err == nil {
				if e.SeriesID == "" {
					changes.added[e.ID] = true
				} else {
					changes.updated[top] = true
				}
			}
			if err == nil {
				_, err = tx.Exec(`UPDATE events SET remote_key = ?, remote_url = COALESCE(?, remote_url) WHERE id = ?`, it.Key, object, e.ID)
			}
			if err != nil {
				return changes, fmt.Errorf("event %q: %w", e.Title, err)
			}
		}
	}
	return changes, nil
}

type remoteEvent struct {
	id     string
	url    sql.NullString // remote object, empty for ICS
	series sql.NullString // the series master, for a changed occurrence
}

// remoteKeys maps the remote keys of a calendar's synced events to their ids.
func remoteKeys(tx *sql.Tx, calendarID string) (map[string]remoteEvent, error) {
	out := map[string]remoteEvent{}
	rows, err := tx.Query(`SELECT remote_key, id, remote_url, series_id FROM events WHERE calendar_id = ? AND remote_key IS NOT NULL`, calendarID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var key string
		var r remoteEvent
		if err := rows.Scan(&key, &r.id, &r.url, &r.series); err != nil {
			return nil, err
		}
		out[key] = r
	}
	return out, rows.Err()
}
