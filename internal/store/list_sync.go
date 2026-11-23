package store

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/hejmo-home/hejmo/internal/list"
)

// What another app (Apple Reminders over CalDAV, ticket 167) may change of a
// shared list's entry: the words, the article they name, the quantity, the
// note and ticking. Dates, times and flags are not stored.

// ErrIDTaken means an entry id is used already (by another list of the household).
var ErrIDTaken = errors.New("store: the entry id is taken")

// SyncEdit is an entry as another app saved it.
type SyncEdit struct {
	Text, ArticleID string
	Qty             list.Quantity
	Note            string
	Done            bool
}

func (in SyncEdit) check() (SyncEdit, error) {
	in.Text, in.Note = strings.TrimSpace(in.Text), strings.TrimSpace(in.Note)
	if err := list.ValidateText(in.Text); err != nil {
		return in, fmt.Errorf("%w: %w", ErrInvalid, err)
	}
	if err := list.ValidateNote(in.Note); err != nil {
		return in, fmt.Errorf("%w: %w", ErrInvalid, err)
	}
	return in, nil
}

// SyncEntry makes an existing entry what another app saved: ErrNotFound if it
// is gone. An open entry whose quantity changes follows the rule of changing a
// quantity (it goes into another entry of the same article and kind); a
// ticked-off one just takes it. The result is the entry that stands now (the
// one it went into, if it merged) and whether it merged.
func (db *DB) SyncEntry(householdID, listID, id string, in SyncEdit, show list.Show) (list.Entry, bool, error) {
	in, err := in.check()
	if err != nil {
		return list.Entry{}, false, err
	}
	if _, err := db.List(householdID, listID); err != nil {
		return list.Entry{}, false, err
	}
	tx, err := db.sql.Begin()
	if err != nil {
		return list.Entry{}, false, err
	}
	defer tx.Rollback()
	res, err := tx.Exec(`UPDATE list_entries SET text = ?, article_id = ?, note = ?, done = ? WHERE id = ? AND list_id = ?`,
		in.Text, in.ArticleID, in.Note, in.Done, id, listID)
	if err := expectOne(res, err); err != nil {
		return list.Entry{}, false, err
	}
	es, err := entriesTx(tx, listID)
	if err != nil {
		return list.Entry{}, false, err
	}
	var cur list.Entry
	for _, e := range es {
		if e.ID == id {
			cur = e
		}
	}
	merged := false
	switch {
	case cur.Done && sameQty(cur.Qty, in.Qty):
	case cur.Done:
		if _, err := tx.Exec(`UPDATE list_entries SET qty_base = ?, qty_unit = ? WHERE id = ?`, in.Qty.Base, in.Qty.Unit, id); err != nil {
			return list.Entry{}, false, err
		}
		cur.Qty = in.Qty
	default:
		plan, err := list.PlanSet(es, id, in.Qty, show)
		if err != nil {
			return list.Entry{}, false, err
		}
		if plan.Action == list.SetMerged {
			if !in.Qty.None() {
				if _, err := tx.Exec(`UPDATE list_entries SET qty_base = ?, qty_unit = ? WHERE id = ?`, plan.Result.Base, plan.Result.Unit, plan.Into.ID); err != nil {
					return list.Entry{}, false, err
				}
			}
			into := plan.Into
			into.Qty = plan.Result
			if err := takeNote(tx, &into, cur.Note); err != nil {
				return list.Entry{}, false, err
			}
			if _, err := tx.Exec(`DELETE FROM list_entries WHERE id = ?`, id); err != nil {
				return list.Entry{}, false, err
			}
			cur, merged = into, true
		} else {
			if _, err := tx.Exec(`UPDATE list_entries SET qty_base = ?, qty_unit = ? WHERE id = ?`, plan.Result.Base, plan.Result.Unit, id); err != nil {
				return list.Entry{}, false, err
			}
			cur.Qty = plan.Result
		}
	}
	return cur, merged, tx.Commit()
}

func sameQty(a, b list.Quantity) bool {
	return a.Unit == b.Unit && (a.None() || abs(a.Base-b.Base) < 1e-9)
}

func abs(f float64) float64 {
	if f < 0 {
		return -f
	}
	return f
}

// SyncNewEntry adds an entry another app created, with the id it chose. An
// open one is added up by the rule of adding (the entry of the same article
// takes it in; the result is that entry and the plan says so); a ticked-off
// one is stored as it is. ErrIDTaken if the id is used by another entry.
func (db *DB) SyncNewEntry(householdID, listID, id string, in SyncEdit, show list.Show) (AddResult, error) {
	in, err := in.check()
	if err != nil {
		return AddResult{}, err
	}
	if _, err := db.List(householdID, listID); err != nil {
		return AddResult{}, err
	}
	var x string
	switch err := db.sql.QueryRow(`SELECT e.id FROM list_entries e JOIN lists l ON l.id = e.list_id WHERE e.id = ? AND l.household_id = ?`, id, householdID).Scan(&x); {
	case err == nil:
		return AddResult{}, ErrIDTaken
	case !errors.Is(err, sql.ErrNoRows):
		return AddResult{}, err
	}
	if !in.Done {
		return db.AddItem(householdID, listID, Item{ID: id, Note: in.Note, Text: in.Text, ArticleID: in.ArticleID, Qty: in.Qty}, show)
	}
	e := list.Entry{ID: id, Text: in.Text, ArticleID: in.ArticleID, Qty: in.Qty, Note: in.Note, Done: true}
	err = db.sql.QueryRow(`INSERT INTO list_entries (id, list_id, text, done, position, created_at, article_id, qty_base, qty_unit, note)
		VALUES (?, ?, ?, 1, (SELECT COALESCE(MAX(position) + 1, 0) FROM list_entries WHERE list_id = ?), ?, ?, ?, ?, ?) RETURNING position`,
		e.ID, listID, e.Text, listID, now(), e.ArticleID, e.Qty.Base, e.Qty.Unit, e.Note).Scan(&e.Position)
	return AddResult{Plan: list.AddPlan{Action: list.AddedNew, Result: e.Qty}, Entry: e}, err
}
