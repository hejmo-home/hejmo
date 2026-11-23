package store

import (
	"database/sql"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/hejmo-home/hejmo/internal/list"
	"github.com/hejmo-home/hejmo/internal/unit"
)

// Item is a thing put on a list with the article it comes from and how much
// of it (ADR 0019, ticket 165).
type Item struct {
	// ID is the id of a new entry, "" lets the box make one (a reminder
	// created in another app brings its own).
	ID string
	// Note goes with a new entry; an entry that takes the item in keeps its
	// own note unless it has none.
	Note      string
	Text      string
	ArticleID string // "" for free text
	Qty       list.Quantity
	// ChosenUnit is the unit the person picked; it is remembered for the next
	// time ("" without a quantity).
	ChosenUnit string
}

// AddResult says what adding did and what the entry is now.
type AddResult struct {
	Plan  list.AddPlan
	Entry list.Entry // the entry that was added, changed or already there
}

func entriesTx(tx *sql.Tx, listID string) ([]list.Entry, error) {
	rows, err := tx.Query(`SELECT `+entryColumns+` FROM list_entries WHERE list_id = ? ORDER BY position, rowid`, listID)
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
	return out, rows.Err()
}

func setLastUnit(tx *sql.Tx, householdID, articleID, key string) error {
	if articleID == "" || key == "" || !unit.Valid(key) {
		return nil
	}
	_, err := tx.Exec(`INSERT INTO article_last_unit (household_id, article_id, unit) VALUES (?, ?, ?)
		ON CONFLICT(household_id, article_id) DO UPDATE SET unit = excluded.unit`, householdID, articleID, key)
	return err
}

// AddItem puts an item on a list the way a list with suggestions does: the
// same article (or text) with a unit of the same kind adds up, an entry
// without quantity takes the quantity, adding without quantity never makes a
// second entry, and another kind gets its own entry directly under the old
// one. Only open entries merge. show says in which unit a total is shown.
func (db *DB) AddItem(householdID, listID string, in Item, show list.Show) (AddResult, error) {
	in.Text = strings.TrimSpace(in.Text)
	if err := list.ValidateText(in.Text); err != nil {
		return AddResult{}, fmt.Errorf("%w: %w", ErrInvalid, err)
	}
	if _, err := db.List(householdID, listID); err != nil {
		return AddResult{}, err
	}
	tx, err := db.sql.Begin()
	if err != nil {
		return AddResult{}, err
	}
	defer tx.Rollback()
	es, err := entriesTx(tx, listID)
	if err != nil {
		return AddResult{}, err
	}
	plan := list.PlanAdd(es, in.ArticleID, in.Text, in.Qty, show)
	res := AddResult{Plan: plan}
	switch plan.Action {
	case list.AddedTo, list.Filled:
		if _, err := tx.Exec(`UPDATE list_entries SET qty_base = ?, qty_unit = ? WHERE id = ?`, plan.Result.Base, plan.Result.Unit, plan.Target.ID); err != nil {
			return AddResult{}, err
		}
		res.Entry = plan.Target
		res.Entry.Qty = plan.Result
		if err := takeNote(tx, &res.Entry, in.Note); err != nil {
			return AddResult{}, err
		}
	case list.Exists:
		res.Entry = plan.Target
		if err := takeNote(tx, &res.Entry, in.Note); err != nil {
			return AddResult{}, err
		}
	default:
		e := list.Entry{ID: in.ID, Text: in.Text, ArticleID: in.ArticleID, Qty: plan.Result, Note: in.Note}
		if e.ID == "" {
			e.ID = newID()
		}
		if plan.After == "" {
			err = tx.QueryRow(`INSERT INTO list_entries (id, list_id, text, position, created_at, article_id, qty_base, qty_unit, note)
				VALUES (?, ?, ?, (SELECT COALESCE(MAX(position) + 1, 0) FROM list_entries WHERE list_id = ?), ?, ?, ?, ?, ?) RETURNING position`,
				e.ID, listID, e.Text, listID, now(), e.ArticleID, e.Qty.Base, e.Qty.Unit, e.Note).Scan(&e.Position)
		} else {
			err = insertAfter(tx, listID, plan.After, es, &e)
		}
		if err != nil {
			return AddResult{}, err
		}
		res.Entry = e
	}
	if !in.Qty.None() {
		if err := setLastUnit(tx, householdID, in.ArticleID, in.ChosenUnit); err != nil {
			return AddResult{}, err
		}
	}
	return res, tx.Commit()
}

// takeNote gives an entry without a note the note of an item that was added up into it.
func takeNote(tx *sql.Tx, e *list.Entry, note string) error {
	if e.Note != "" || note == "" {
		return nil
	}
	e.Note = note
	_, err := tx.Exec(`UPDATE list_entries SET note = ? WHERE id = ?`, note, e.ID)
	return err
}

// insertAfter stores e directly under the entry with id after, renumbering the
// positions of the list.
func insertAfter(tx *sql.Tx, listID, after string, es []list.Entry, e *list.Entry) error {
	if _, err := tx.Exec(`INSERT INTO list_entries (id, list_id, text, position, created_at, article_id, qty_base, qty_unit, note)
		VALUES (?, ?, ?, 0, ?, ?, ?, ?, ?)`, e.ID, listID, e.Text, now(), e.ArticleID, e.Qty.Base, e.Qty.Unit, e.Note); err != nil {
		return err
	}
	var ids []string
	for _, x := range es {
		ids = append(ids, x.ID)
		if x.ID == after {
			ids = append(ids, e.ID)
		}
	}
	if !slices.Contains(ids, e.ID) {
		ids = append(ids, e.ID)
	}
	for i, id := range ids {
		if _, err := tx.Exec(`UPDATE list_entries SET position = ? WHERE id = ?`, i, id); err != nil {
			return err
		}
		if id == e.ID {
			e.Position = i
		}
	}
	return nil
}

// SetResult says what changing a quantity did.
type SetResult struct{ Plan list.SetPlan }

// SetQuantity gives an open entry a new quantity (none for the zero Quantity):
// it replaces the old one and never adds. When another entry of the article
// has the same kind, this entry goes into it. A list that is gone is
// ErrNotFound; an entry that is gone or ticked off is list.ErrNotOpen.
func (db *DB) SetQuantity(householdID, listID, entryID string, to list.Quantity, chosenUnit string, show list.Show) (SetResult, error) {
	if _, err := db.List(householdID, listID); err != nil {
		return SetResult{}, err
	}
	tx, err := db.sql.Begin()
	if err != nil {
		return SetResult{}, err
	}
	defer tx.Rollback()
	es, err := entriesTx(tx, listID)
	if err != nil {
		return SetResult{}, err
	}
	plan, err := list.PlanSet(es, entryID, to, show)
	if err != nil {
		return SetResult{}, err
	}
	switch plan.Action {
	case list.SetMerged:
		if !to.None() {
			if _, err := tx.Exec(`UPDATE list_entries SET qty_base = ?, qty_unit = ? WHERE id = ?`, plan.Result.Base, plan.Result.Unit, plan.Into.ID); err != nil {
				return SetResult{}, err
			}
		}
		into := plan.Into
		if err := takeNote(tx, &into, plan.Entry.Note); err != nil {
			return SetResult{}, err
		}
		if _, err := tx.Exec(`DELETE FROM list_entries WHERE id = ?`, plan.Entry.ID); err != nil {
			return SetResult{}, err
		}
	default:
		if _, err := tx.Exec(`UPDATE list_entries SET qty_base = ?, qty_unit = ? WHERE id = ?`, plan.Result.Base, plan.Result.Unit, plan.Entry.ID); err != nil {
			return SetResult{}, err
		}
	}
	if !to.None() {
		if err := setLastUnit(tx, householdID, plan.Entry.ArticleID, chosenUnit); err != nil {
			return SetResult{}, err
		}
	}
	return SetResult{Plan: plan}, tx.Commit()
}

// LastUnit is the unit last used for an article in the household, "" if none.
func (db *DB) LastUnit(householdID, articleID string) (string, error) {
	var u string
	err := db.sql.QueryRow(`SELECT unit FROM article_last_unit WHERE household_id = ? AND article_id = ?`, householdID, articleID).Scan(&u)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return u, err
}

// ActiveUnits returns the units a parent switched on; custom is false (and the
// units nil) while the household follows the preset of its home's country.
func (db *DB) ActiveUnits(householdID string) (units []string, custom bool, err error) {
	var s string
	err = db.sql.QueryRow(`SELECT active FROM household_units WHERE household_id = ?`, householdID).Scan(&s)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, false, nil
	}
	return splitKeys(s), true, err
}

// SetActiveUnits stores the units a parent switched on: unknown ones are
// dropped, piece is always on, and each of weight and volume keeps at least
// one (a refused change is a *FieldError naming the group, nothing is stored).
func (db *DB) SetActiveUnits(householdID string, units []string) error {
	on := unit.Normalize(units)
	if g, err := unit.CheckActive(on); err != nil {
		return &FieldError{Field: g, Msg: "units: no " + g + " unit is on"}
	}
	_, err := db.sql.Exec(`INSERT INTO household_units (household_id, active) VALUES (?, ?)
		ON CONFLICT(household_id) DO UPDATE SET active = excluded.active`, householdID, strings.Join(on, ","))
	return err
}

// ResetActiveUnits makes the household follow its home's country again.
func (db *DB) ResetActiveUnits(householdID string) error {
	_, err := db.sql.Exec(`DELETE FROM household_units WHERE household_id = ?`, householdID)
	return err
}

// LinkEntryArticle makes an open entry of free text the article it was added
// to the library as: the entry keeps its quantity and takes the article's id
// and its name. Another entry that stands for the same article and has the same
// kind of quantity is not merged; the next add does that.
func (db *DB) LinkEntryArticle(householdID, listID, entryID, articleID, name string) error {
	if _, err := db.List(householdID, listID); err != nil {
		return err
	}
	res, err := db.sql.Exec(`UPDATE list_entries SET article_id = ?, text = ? WHERE id = ? AND list_id = ? AND article_id = '' AND done = 0`, articleID, strings.TrimSpace(name), entryID, listID)
	return expectOne(res, err)
}
