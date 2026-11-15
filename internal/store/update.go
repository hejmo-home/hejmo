package store

import (
	"errors"
	"os"
	"strings"
)

// UpdateMode is what the box's auto-update does (ADR 0018).
type UpdateMode string

// Update modes; on is the default.
const (
	UpdateOn     UpdateMode = "on"     // install the newest published version at night
	UpdateOff    UpdateMode = "off"    // never check
	UpdateNotify UpdateMode = "notify" // only tell parents that an update is available
)

// Valid reports whether m is a known update mode.
func (m UpdateMode) Valid() bool { return m == UpdateOn || m == UpdateOff || m == UpdateNotify }

// SetAutoUpdate stores the household's auto-update mode.
func (db *DB) SetAutoUpdate(id string, m UpdateMode) error {
	if !m.Valid() {
		return errors.New("store: unknown update mode")
	}
	return expectOne(db.sql.Exec(`UPDATE households SET auto_update = ?, updated_at = ? WHERE id = ?`, m, now(), id))
}

// AckUpdate remembers the update event (the time the host script recorded)
// whose notice a parent dismissed, so it is shown only once.
func (db *DB) AckUpdate(id, event string) error {
	return expectOne(db.sql.Exec(`UPDATE households SET update_ack = ?, updated_at = ? WHERE id = ?`, event, now(), id))
}

// Backup writes a consistent copy of the live database to dest with SQLite's
// online backup (VACUUM INTO): the box keeps running and the copy includes
// everything committed, also what is still in the write-ahead log. It refuses
// to overwrite an existing file.
func (db *DB) Backup(dest string) error {
	if _, err := os.Stat(dest); err == nil {
		return errors.New("store: backup target already exists")
	}
	if strings.ContainsRune(dest, 0) {
		return errors.New("store: bad backup path")
	}
	_, err := db.sql.Exec(`VACUUM INTO ?`, dest)
	return err
}
