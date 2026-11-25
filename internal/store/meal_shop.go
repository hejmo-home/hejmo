package store

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/hejmo-home/hejmo/internal/list"
	"github.com/hejmo-home/hejmo/internal/meal"
	"github.com/hejmo-home/hejmo/internal/shop"
)

// Ingredients of planned recipes on a shopping list (ticket 283): what every
// run recorded per entry and meal. See migration 202611251200_meal_shop.sql.

// ShopMeal is a meal a run shopped for, with the portions it put on the list.
type ShopMeal struct {
	Day        string
	Mealtime   meal.Mealtime
	RecipeID   string
	RecipeName string
	Portions   int
}

// ShopInput is one run: the picked rows, the meals they come from and what was left out.
type ShopInput struct {
	ListID, Day string
	Rows        []shop.Row
	Meals       []ShopMeal
	// LeftOut are the pantry articles the run did not put on the list, for the success line.
	LeftOut []string
	Show    func(shop.Row) list.Show
}

// ShopStepRecord is what a run did to one entry.
type ShopStepRecord struct {
	EntryID string
	Action  list.AddAction
	Text    string
	Variety string
	// Before and After are the quantity of the entry around the run; Added is what the meals brought (none for a kitchen measure).
	Before, After, Added list.Quantity
}

// ShopRun is a run as it was recorded.
type ShopRun struct {
	ID, ListID, ListName, Day string
	Meals                     []ShopMeal
	Steps                     []ShopStepRecord
	LeftOut                   []string
	Undone                    bool
}

// ErrShopTaken means the run was taken back already, or is not there.
var ErrShopTaken = errors.New("store: the run is not there or was taken back")

// AddMealShop puts the rows on a list in one transaction, through list.PlanAdd, and records the run.
func (db *DB) AddMealShop(householdID string, in ShopInput) (ShopRun, error) {
	l, err := db.List(householdID, in.ListID)
	if err != nil {
		return ShopRun{}, err
	}
	tx, err := db.sql.Begin()
	if err != nil {
		return ShopRun{}, err
	}
	defer tx.Rollback()
	es, err := entriesTx(tx, l.ID)
	if err != nil {
		return ShopRun{}, err
	}
	steps := shop.Plan(es, in.Rows, in.Show)
	run := ShopRun{ID: newID(), ListID: l.ID, ListName: l.Name, Day: in.Day, Meals: in.Meals, LeftOut: in.LeftOut}
	if _, err := tx.Exec(`INSERT INTO meal_shop_runs (id, household_id, list_id, list_name, day, created_at, left_out) VALUES (?, ?, ?, ?, ?, ?, ?)`,
		run.ID, householdID, l.ID, l.Name, in.Day, now(), strings.Join(in.LeftOut, "\n")); err != nil {
		return ShopRun{}, err
	}
	for _, m := range in.Meals {
		if _, err := tx.Exec(`INSERT INTO meal_shop_meals (run_id, day, mealtime, recipe_id, recipe_name, portions) VALUES (?, ?, ?, ?, ?, ?)`,
			run.ID, m.Day, m.Mealtime, m.RecipeID, m.RecipeName, m.Portions); err != nil {
			return ShopRun{}, err
		}
	}
	real := map[string]string{} // placeholder id of a new entry to its stored id
	byKey := map[string]ShopMeal{}
	for _, m := range in.Meals {
		byKey[m.Day+"."+string(m.Mealtime)] = m
	}
	for seq, s := range steps {
		if s.Plan.Action == list.Exists {
			continue
		}
		rec := ShopStepRecord{Action: s.Plan.Action, Text: s.Row.Text, Variety: s.Row.Variety, Before: s.Plan.Before, After: s.Plan.Result}
		switch s.Plan.Action {
		case list.AddedTo, list.Filled:
			if _, err := tx.Exec(`UPDATE list_entries SET qty_base = ?, qty_unit = ?, `+clearOrigin+` WHERE id = ?`, s.Plan.Result.Base, s.Plan.Result.Unit, s.Plan.Target.ID); err != nil {
				return ShopRun{}, err
			}
			rec.EntryID = s.Plan.Target.ID
		default:
			cur, err := entriesTx(tx, l.ID)
			if err != nil {
				return ShopRun{}, err
			}
			e := list.Entry{ID: newID(), Text: s.Row.Text, ArticleID: s.Row.ArticleID, Variety: s.Row.Variety, Qty: s.Plan.Result}
			after := s.Plan.After
			if id, ok := real[after]; ok {
				after = id
			}
			if after == "" || !hasEntry(cur, after) {
				err = tx.QueryRow(`INSERT INTO list_entries (id, list_id, text, position, created_at, article_id, variety, qty_base, qty_unit)
					VALUES (?, ?, ?, (SELECT COALESCE(MAX(position) + 1, 0) FROM list_entries WHERE list_id = ?), ?, ?, NULLIF(?, ''), ?, ?) RETURNING position`,
					e.ID, l.ID, e.Text, l.ID, now(), e.ArticleID, e.Variety, e.Qty.Base, e.Qty.Unit).Scan(&e.Position)
			} else {
				err = insertAfter(tx, l.ID, after, cur, &e)
			}
			if err != nil {
				return ShopRun{}, err
			}
			real[s.Entry.ID] = e.ID
			rec.EntryID = e.ID
		}
		if _, err := tx.Exec(`INSERT INTO meal_shop_steps (run_id, entry_id, seq, action, text, variety, before_base, before_unit, after_base, after_unit) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			run.ID, rec.EntryID, seq, rec.Action, rec.Text, rec.Variety, rec.Before.Base, rec.Before.Unit, rec.After.Base, rec.After.Unit); err != nil {
			return ShopRun{}, err
		}
		for _, p := range s.Row.Parts {
			m := byKey[p.Meal]
			if _, err := tx.Exec(`INSERT INTO meal_shop_parts (run_id, entry_id, day, mealtime, recipe_id, recipe_name, portions, qty_base, qty_unit) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
				run.ID, rec.EntryID, m.Day, m.Mealtime, m.RecipeID, m.RecipeName, m.Portions, p.Qty.Base, p.Qty.Unit); err != nil {
				return ShopRun{}, err
			}
			if rec.Added.None() && !p.Qty.None() {
				rec.Added.Unit = p.Qty.Unit
			}
			rec.Added.Base += p.Qty.Base
		}
		run.Steps = append(run.Steps, rec)
	}
	return run, tx.Commit()
}

func hasEntry(es []list.Entry, id string) bool {
	for _, e := range es {
		if e.ID == id {
			return true
		}
	}
	return false
}

// MealShopRun returns a run as it was recorded; ErrNotFound for another household's or none.
func (db *DB) MealShopRun(householdID, id string) (ShopRun, error) {
	var run ShopRun
	var left string
	var undone sql.NullString
	err := db.sql.QueryRow(`SELECT id, list_id, list_name, day, left_out, undone_at FROM meal_shop_runs WHERE household_id = ? AND id = ?`, householdID, id).
		Scan(&run.ID, &run.ListID, &run.ListName, &run.Day, &left, &undone)
	if errors.Is(err, sql.ErrNoRows) {
		return ShopRun{}, ErrNotFound
	}
	if err != nil {
		return ShopRun{}, err
	}
	run.Undone = undone.Valid
	if left != "" {
		run.LeftOut = strings.Split(left, "\n")
	}
	rows, err := db.sql.Query(`SELECT day, mealtime, recipe_id, recipe_name, portions FROM meal_shop_meals WHERE run_id = ? ORDER BY day, mealtime`, id)
	if err != nil {
		return ShopRun{}, err
	}
	for rows.Next() {
		var m ShopMeal
		if err := rows.Scan(&m.Day, &m.Mealtime, &m.RecipeID, &m.RecipeName, &m.Portions); err != nil {
			rows.Close()
			return ShopRun{}, err
		}
		run.Meals = append(run.Meals, m)
	}
	rows.Close()
	rows, err = db.sql.Query(`SELECT s.entry_id, s.action, s.text, s.variety, s.before_base, s.before_unit, s.after_base, s.after_unit,
		COALESCE((SELECT SUM(qty_base) FROM meal_shop_parts p WHERE p.run_id = s.run_id AND p.entry_id = s.entry_id), 0),
		COALESCE((SELECT MAX(qty_unit) FROM meal_shop_parts p WHERE p.run_id = s.run_id AND p.entry_id = s.entry_id), '')
		FROM meal_shop_steps s WHERE s.run_id = ? ORDER BY s.seq`, id)
	if err != nil {
		return ShopRun{}, err
	}
	defer rows.Close()
	for rows.Next() {
		var s ShopStepRecord
		if err := rows.Scan(&s.EntryID, &s.Action, &s.Text, &s.Variety, &s.Before.Base, &s.Before.Unit, &s.After.Base, &s.After.Unit, &s.Added.Base, &s.Added.Unit); err != nil {
			return ShopRun{}, err
		}
		run.Steps = append(run.Steps, s)
	}
	return run, rows.Err()
}

// ShopOnList is a meal that has ingredients on a list: the recipe and the
// portions put there (all runs not taken back, added up), and the last run.
type ShopOnList struct {
	Day        string
	Mealtime   meal.Mealtime
	RecipeID   string
	Portions   int
	ListID     string
	ListName   string
	RunID      string
	RunDay     string
	RecipeName string
}

// ShopMeals returns, for the days from to to, the meals that are on a list: one row per meal and recipe.
func (db *DB) ShopMeals(householdID, from, to string) ([]ShopOnList, error) {
	rows, err := db.sql.Query(`SELECT m.day, m.mealtime, m.recipe_id, m.recipe_name, m.portions, r.list_id, r.list_name, r.id, r.day
		FROM meal_shop_meals m JOIN meal_shop_runs r ON r.id = m.run_id
		WHERE r.household_id = ? AND r.undone_at IS NULL AND m.day BETWEEN ? AND ? ORDER BY m.day, m.mealtime, r.created_at, r.rowid`, householdID, from, to)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ShopOnList
	for rows.Next() {
		var m ShopOnList
		var p int
		if err := rows.Scan(&m.Day, &m.Mealtime, &m.RecipeID, &m.RecipeName, &p, &m.ListID, &m.ListName, &m.RunID, &m.RunDay); err != nil {
			return nil, err
		}
		if n := len(out); n > 0 && out[n-1].Day == m.Day && out[n-1].Mealtime == m.Mealtime && out[n-1].RecipeID == m.RecipeID {
			m.Portions = out[n-1].Portions + p
			out[n-1] = m
			continue
		}
		m.Portions = p
		out = append(out, m)
	}
	return out, rows.Err()
}

// ShopLastList is the list the last run (not taken back) used, "" if none or the list is gone.
func (db *DB) ShopLastList(householdID string) (string, error) {
	var id string
	err := db.sql.QueryRow(`SELECT list_id FROM meal_shop_runs WHERE household_id = ? ORDER BY created_at DESC, rowid DESC LIMIT 1`, householdID).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return id, err
}

// EntryPart is what one meal brought to an entry.
type EntryPart struct {
	Day        string
	Mealtime   meal.Mealtime
	RecipeID   string
	RecipeName string
	Portions   int
	Qty        list.Quantity
}

// EntrySource says where an open entry's quantity came from: Prior is what
// stood on the list before the first meal added to it.
type EntrySource struct {
	Prior list.Quantity
	Parts []EntryPart
}

// EntrySources returns, per entry, what meals brought to it (runs not taken back), by day.
func (db *DB) EntrySources(householdID string, entryIDs []string) (map[string]EntrySource, error) {
	out := map[string]EntrySource{}
	if len(entryIDs) == 0 {
		return out, nil
	}
	marks := strings.TrimSuffix(strings.Repeat("?,", len(entryIDs)), ",")
	args := []any{householdID}
	for _, id := range entryIDs {
		args = append(args, id)
	}
	rows, err := db.sql.Query(`SELECT p.entry_id, p.day, p.mealtime, p.recipe_id, p.recipe_name, p.portions, p.qty_base, p.qty_unit, s.before_base, s.before_unit
		FROM meal_shop_parts p JOIN meal_shop_runs r ON r.id = p.run_id JOIN meal_shop_steps s ON s.run_id = p.run_id AND s.entry_id = p.entry_id
		WHERE r.household_id = ? AND r.undone_at IS NULL AND p.entry_id IN (`+marks+`) ORDER BY r.created_at, r.rowid, p.day, p.mealtime`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		var p EntryPart
		var before list.Quantity
		if err := rows.Scan(&id, &p.Day, &p.Mealtime, &p.RecipeID, &p.RecipeName, &p.Portions, &p.Qty.Base, &p.Qty.Unit, &before.Base, &before.Unit); err != nil {
			return nil, err
		}
		s := out[id]
		if len(s.Parts) == 0 {
			s.Prior = before
		}
		s.Parts = append(s.Parts, p)
		out[id] = s
	}
	return out, rows.Err()
}

// ShopReverted is an entry whose quantity went back to what it was.
type ShopReverted struct {
	Text string
	Qty  list.Quantity
}

// ShopUndo is what taking a run back did.
type ShopUndo struct {
	// Removed are the entries the run made and that are gone now.
	Removed []string
	// Reverted are the entries that have the quantity they had before.
	Reverted []ShopReverted
	// Ticked are the entries someone ticked off since: they stay.
	Ticked []string
}

func stepLabel(text, variety string) string {
	if variety == "" {
		return text
	}
	return text + " · " + variety
}

// UndoMealShop takes back exactly one run: new open entries go, added
// quantities come off, filled ones are empty again, and the meals count as not
// on the list. Entries ticked off meanwhile stay. A run taken back or unknown is ErrShopTaken.
func (db *DB) UndoMealShop(householdID, runID string) (ShopUndo, error) {
	run, err := db.MealShopRun(householdID, runID)
	if errors.Is(err, ErrNotFound) || (err == nil && run.Undone) {
		return ShopUndo{}, ErrShopTaken
	}
	if err != nil {
		return ShopUndo{}, err
	}
	tx, err := db.sql.Begin()
	if err != nil {
		return ShopUndo{}, err
	}
	defer tx.Rollback()
	var res ShopUndo
	for i := len(run.Steps) - 1; i >= 0; i-- {
		s := run.Steps[i]
		var done bool
		var cur list.Quantity
		if err := tx.QueryRow(`SELECT done, qty_base, qty_unit FROM list_entries WHERE id = ?`, s.EntryID).Scan(&done, &cur.Base, &cur.Unit); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				continue // deleted by hand meanwhile
			}
			return ShopUndo{}, err
		}
		label := stepLabel(s.Text, s.Variety)
		if done {
			res.Ticked = append(res.Ticked, label)
			continue
		}
		set := func(q list.Quantity) error {
			_, err := tx.Exec(`UPDATE list_entries SET qty_base = ?, qty_unit = ?, `+clearOrigin+` WHERE id = ?`, q.Base, q.Unit, s.EntryID)
			return err
		}
		restore := func() list.Quantity {
			if cur.Base == s.After.Base && cur.Unit == s.After.Unit {
				return s.Before
			}
			if rest := cur.Base - s.Added.Base; !cur.None() && !s.Added.None() && cur.Kind() == s.Added.Kind() && rest > 1e-9 {
				return list.Quantity{Base: rest, Unit: cur.Unit}
			}
			return s.Before
		}
		switch s.Action {
		case list.AddedNew:
			var others int
			if err := tx.QueryRow(`SELECT COUNT(*) FROM meal_shop_steps s JOIN meal_shop_runs r ON r.id = s.run_id
				WHERE s.entry_id = ? AND s.run_id <> ? AND r.undone_at IS NULL`, s.EntryID, runID).Scan(&others); err != nil {
				return ShopUndo{}, err
			}
			if others == 0 {
				if _, err := tx.Exec(`DELETE FROM list_entries WHERE id = ?`, s.EntryID); err != nil {
					return ShopUndo{}, err
				}
				res.Removed = append(res.Removed, label)
				continue
			}
			q := restore()
			if err := set(q); err != nil {
				return ShopUndo{}, err
			}
			res.Reverted = append(res.Reverted, ShopReverted{label, q})
		default:
			q := restore()
			if s.Action == list.Filled {
				q = list.Quantity{}
			}
			if err := set(q); err != nil {
				return ShopUndo{}, err
			}
			res.Reverted = append(res.Reverted, ShopReverted{label, q})
		}
	}
	if _, err := tx.Exec(`UPDATE meal_shop_runs SET undone_at = ? WHERE id = ?`, now(), runID); err != nil {
		return ShopUndo{}, err
	}
	if err := tx.Commit(); err != nil {
		return ShopUndo{}, fmt.Errorf("undo meal shop: %w", err)
	}
	return res, nil
}
