package store

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/hejmo-home/hejmo/internal/list"
)

// EntryCount is how many entries of a list are open and ticked off.
type EntryCount struct{ Open, Done int }

// CreateList stores a list of the household.
func (db *DB) CreateList(householdID string, l list.List) (list.List, error) {
	l.Name = strings.TrimSpace(l.Name)
	if err := l.Validate(); err != nil {
		return list.List{}, fmt.Errorf("%w: %w", ErrInvalid, err)
	}
	l.ID = newID()
	ts := now()
	_, err := db.sql.Exec(`INSERT INTO lists (id, household_id, name, color, symbol, sort, suggestions, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`, l.ID, householdID, l.Name, l.Color, l.Symbol, string(l.Sort), l.Suggestions, ts, ts)
	return l, err
}

const listColumns = `id, name, color, symbol, sort, suggestions`

func scanList(row interface{ Scan(...any) error }) (list.List, error) {
	var l list.List
	var s string
	err := row.Scan(&l.ID, &l.Name, &l.Color, &l.Symbol, &s, &l.Suggestions)
	l.Sort = list.Sort(s)
	return l, err
}

// Lists returns the household's lists, oldest first.
func (db *DB) Lists(householdID string) ([]list.List, error) {
	rows, err := db.sql.Query(`SELECT `+listColumns+` FROM lists WHERE household_id = ? ORDER BY created_at, rowid`, householdID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []list.List
	for rows.Next() {
		l, err := scanList(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, l)
	}
	return out, rows.Err()
}

// List loads one list.
func (db *DB) List(householdID, id string) (list.List, error) {
	l, err := scanList(db.sql.QueryRow(`SELECT `+listColumns+` FROM lists WHERE household_id = ? AND id = ?`, householdID, id))
	if errors.Is(err, sql.ErrNoRows) {
		return list.List{}, ErrNotFound
	}
	return l, err
}

// UpdateList changes name, colour, symbol, sorting and suggestions of a list.
func (db *DB) UpdateList(householdID string, l list.List) (list.List, error) {
	l.Name = strings.TrimSpace(l.Name)
	if err := l.Validate(); err != nil {
		return list.List{}, fmt.Errorf("%w: %w", ErrInvalid, err)
	}
	res, err := db.sql.Exec(`UPDATE lists SET name = ?, color = ?, symbol = ?, sort = ?, suggestions = ?, updated_at = ? WHERE household_id = ? AND id = ?`,
		l.Name, l.Color, l.Symbol, string(l.Sort), l.Suggestions, now(), householdID, l.ID)
	return l, expectOne(res, err)
}

// DeleteList removes a list with its entries.
func (db *DB) DeleteList(householdID, id string) error {
	res, err := db.sql.Exec(`DELETE FROM lists WHERE household_id = ? AND id = ?`, householdID, id)
	return expectOne(res, err)
}

const entryColumns = `id, text, done, position, article_id, COALESCE(variety, ''), qty_base, qty_unit`

func scanEntry(row interface{ Scan(...any) error }) (list.Entry, error) {
	var e list.Entry
	err := row.Scan(&e.ID, &e.Text, &e.Done, &e.Position, &e.ArticleID, &e.Variety, &e.Qty.Base, &e.Qty.Unit)
	return e, err
}

// Entries returns the entries of a list in the order the list shows them.
func (db *DB) Entries(householdID, listID string) ([]list.Entry, error) {
	l, err := db.List(householdID, listID)
	if err != nil {
		return nil, err
	}
	rows, err := db.sql.Query(`SELECT `+entryColumns+` FROM list_entries WHERE list_id = ? ORDER BY position, rowid`, listID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []list.Entry
	for rows.Next() {
		e, err := scanEntry(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return list.Order(out, l.Sort), rows.Err()
}

// AddEntry appends an entry to a list; an alphabetical list shows it in place.
func (db *DB) AddEntry(householdID, listID, text string) (list.Entry, error) {
	text = strings.TrimSpace(text)
	if err := list.ValidateText(text); err != nil {
		return list.Entry{}, fmt.Errorf("%w: %w", ErrInvalid, err)
	}
	if _, err := db.List(householdID, listID); err != nil {
		return list.Entry{}, err
	}
	e := list.Entry{ID: newID(), Text: text}
	err := db.sql.QueryRow(`INSERT INTO list_entries (id, list_id, text, position, created_at)
		VALUES (?, ?, ?, (SELECT COALESCE(MAX(position) + 1, 0) FROM list_entries WHERE list_id = ?), ?) RETURNING position`,
		e.ID, listID, text, listID, now()).Scan(&e.Position)
	return e, err
}

// SetEntryDone ticks an entry off or back open. Doing either twice changes nothing.
func (db *DB) SetEntryDone(householdID, listID, id string, done bool) error {
	res, err := db.sql.Exec(`UPDATE list_entries SET done = ? WHERE id = ? AND list_id IN (SELECT id FROM lists WHERE id = ? AND household_id = ?)`,
		done, id, listID, householdID)
	return expectOne(res, err)
}

// DeleteEntry removes one entry.
func (db *DB) DeleteEntry(householdID, listID, id string) error {
	res, err := db.sql.Exec(`DELETE FROM list_entries WHERE id = ? AND list_id IN (SELECT id FROM lists WHERE id = ? AND household_id = ?)`,
		id, listID, householdID)
	return expectOne(res, err)
}

// DeleteCompleted removes the ticked-off entries of a list and says how many.
func (db *DB) DeleteCompleted(householdID, listID string) (int, error) {
	if _, err := db.List(householdID, listID); err != nil {
		return 0, err
	}
	res, err := db.sql.Exec(`DELETE FROM list_entries WHERE list_id = ? AND done = 1`, listID)
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	return int(n), nil
}

// MoveEntry puts an entry at index (0 is first) of a manually sorted list.
func (db *DB) MoveEntry(householdID, listID, id string, index int) error {
	l, err := db.List(householdID, listID)
	if err != nil {
		return err
	}
	if l.Sort != list.Manual {
		return fmt.Errorf("%w: the list is sorted alphabetically", ErrInvalid)
	}
	tx, err := db.sql.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	rows, err := tx.Query(`SELECT id FROM list_entries WHERE list_id = ? ORDER BY position, rowid`, listID)
	if err != nil {
		return err
	}
	var ids []string
	from := -1
	for rows.Next() {
		var x string
		if err := rows.Scan(&x); err != nil {
			rows.Close()
			return err
		}
		if x == id {
			from = len(ids)
		}
		ids = append(ids, x)
	}
	rows.Close()
	if from < 0 {
		return ErrNotFound
	}
	index = max(0, min(index, len(ids)-1))
	ids = append(ids[:from], ids[from+1:]...)
	ids = append(ids[:index], append([]string{id}, ids[index:]...)...)
	for i, x := range ids {
		if _, err := tx.Exec(`UPDATE list_entries SET position = ? WHERE id = ?`, i, x); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// EntryCounts returns open and ticked-off entries per list of the household.
func (db *DB) EntryCounts(householdID string) (map[string]EntryCount, error) {
	rows, err := db.sql.Query(`SELECT e.list_id, SUM(e.done = 0), SUM(e.done = 1) FROM list_entries e
		JOIN lists l ON l.id = e.list_id WHERE l.household_id = ? GROUP BY e.list_id`, householdID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]EntryCount{}
	for rows.Next() {
		var id string
		var c EntryCount
		if err := rows.Scan(&id, &c.Open, &c.Done); err != nil {
			return nil, err
		}
		out[id] = c
	}
	return out, rows.Err()
}
