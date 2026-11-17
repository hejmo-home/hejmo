package store

import (
	"database/sql"
	"errors"

	"github.com/hejmo-home/hejmo/internal/list"
)

// PersonListView is the view a person chose for lists, or "" when the person
// never chose (the default of the role applies). ErrNotFound for a stranger.
func (db *DB) PersonListView(householdID, personID string) (list.View, error) {
	if _, err := db.Person(householdID, personID); err != nil {
		return "", err
	}
	var v string
	err := db.sql.QueryRow(`SELECT view FROM person_list_view WHERE person_id = ?`, personID).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return list.View(v), err
}

// SetPersonListView remembers a person's view of lists, for every device.
func (db *DB) SetPersonListView(householdID, personID string, v list.View) error {
	if _, ok := list.ParseView(string(v)); !ok {
		return ErrInvalid
	}
	if _, err := db.Person(householdID, personID); err != nil {
		return err
	}
	_, err := db.sql.Exec(`INSERT INTO person_list_view (person_id, view) VALUES (?, ?)
		ON CONFLICT(person_id) DO UPDATE SET view = excluded.view`, personID, string(v))
	return err
}

// DisplayListView is the view of lists of a paired display; tiles until
// someone at the display or a parent switches it.
func (db *DB) DisplayListView(householdID, displayID string) (list.View, error) {
	if _, err := db.Display(householdID, displayID); err != nil {
		return "", err
	}
	var v string
	err := db.sql.QueryRow(`SELECT view FROM display_list_view WHERE display_id = ?`, displayID).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return list.Tiles, nil
	}
	return list.View(v), err
}

// SetDisplayListView remembers a display's view of lists.
func (db *DB) SetDisplayListView(householdID, displayID string, v list.View) error {
	if _, ok := list.ParseView(string(v)); !ok {
		return ErrInvalid
	}
	if _, err := db.Display(householdID, displayID); err != nil {
		return err
	}
	_, err := db.sql.Exec(`INSERT INTO display_list_view (display_id, view) VALUES (?, ?)
		ON CONFLICT(display_id) DO UPDATE SET view = excluded.view`, displayID, string(v))
	return err
}
