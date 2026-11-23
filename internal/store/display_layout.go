package store

import (
	"database/sql"
	"encoding/json"
	"errors"

	"github.com/hejmo-home/hejmo/internal/layout"
)

type storedBlock struct {
	ID layout.BlockID `json:"id"`
	On bool           `json:"on"`
}

// PersonIDs are the IDs of the people of a household, in their order.
func (db *DB) PersonIDs(householdID string) ([]string, error) {
	ps, err := db.People(householdID)
	if err != nil {
		return nil, err
	}
	ids := make([]string, 0, len(ps))
	for _, p := range ps {
		ids = append(ids, p.ID)
	}
	return ids, nil
}

// DisplayLayout is the layout of a paired display, fitted to the household as it
// is now (people who are gone drop out, new ones join "Ganze Familie"). A
// display without a stored layout shows today's (Q89).
func (db *DB) DisplayLayout(householdID, displayID string) (layout.Layout, error) {
	if _, err := db.Display(householdID, displayID); err != nil {
		return layout.Layout{}, err
	}
	household, err := db.PersonIDs(householdID)
	if err != nil {
		return layout.Layout{}, err
	}
	var l layout.Layout
	var people, blocks string
	err = db.sql.QueryRow(`SELECT whole, rotate, people, blocks, list_id, list_name FROM display_layout WHERE display_id = ?`, displayID).
		Scan(&l.Whole, &l.Rotate, &people, &blocks, &l.ListID, &l.ListName)
	if errors.Is(err, sql.ErrNoRows) {
		return layout.Default(household), nil
	}
	if err != nil {
		return layout.Layout{}, err
	}
	if err := json.Unmarshal([]byte(people), &l.People); err != nil {
		return layout.Layout{}, err
	}
	var bs []storedBlock
	if err := json.Unmarshal([]byte(blocks), &bs); err != nil {
		return layout.Layout{}, err
	}
	for _, b := range bs {
		l.Blocks = append(l.Blocks, layout.Block{ID: b.ID, On: b.On})
	}
	return l.Normalize(household), nil
}

// SetDisplayLayout saves a display's layout. A layout without a person is
// refused with layout.ErrNoPeople.
func (db *DB) SetDisplayLayout(householdID, displayID string, l layout.Layout) error {
	if _, err := db.Display(householdID, displayID); err != nil {
		return err
	}
	if err := l.Validate(); err != nil {
		return err
	}
	people, err := json.Marshal(l.People)
	if err != nil {
		return err
	}
	bs := make([]storedBlock, 0, len(l.Blocks))
	for _, b := range l.Blocks {
		bs = append(bs, storedBlock{b.ID, b.On})
	}
	blocks, err := json.Marshal(bs)
	if err != nil {
		return err
	}
	_, err = db.sql.Exec(`INSERT INTO display_layout (display_id, whole, rotate, people, blocks, list_id, list_name) VALUES (?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(display_id) DO UPDATE SET whole = excluded.whole, rotate = excluded.rotate, people = excluded.people,
		blocks = excluded.blocks, list_id = excluded.list_id, list_name = excluded.list_name`,
		displayID, l.Whole, l.Rotate, string(people), string(blocks), l.ListID, l.ListName)
	return err
}

// CopyDisplayLayout gives the display to the layout of the display from. The
// layouts stay apart afterwards: there are no shared templates (Q84).
func (db *DB) CopyDisplayLayout(householdID, fromID, toID string) error {
	l, err := db.DisplayLayout(householdID, fromID)
	if err != nil {
		return err
	}
	return db.SetDisplayLayout(householdID, toID, l)
}

// SetDisplayOrientation remembers how the display hangs, as it reports it.
func (db *DB) SetDisplayOrientation(tokenHash string, portrait bool) error {
	_, err := db.sql.Exec(`UPDATE displays SET portrait = ? WHERE token_hash = ? AND portrait != ?`, portrait, tokenHash, portrait)
	return err
}
