package store

import (
	"database/sql"
	"errors"
	"fmt"
	"math"
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
	// Planned, if above 0, is the portions the meal is planned for: AddMealShop
	// refuses the run (ErrShopStale) when other runs have put more than
	// Planned-Portions of it on a list already, as a double tap would.
	Planned int
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

// ErrShopStale means a meal got on a list since the page was made: nothing was added.
var ErrShopStale = errors.New("store: a meal is on a list already")

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
	run := ShopRun{ID: newID(), ListID: l.ID, ListName: l.Name, Day: in.Day, Meals: in.Meals, LeftOut: in.LeftOut}
	if _, err := tx.Exec(`INSERT INTO meal_shop_runs (id, household_id, list_id, list_name, day, created_at, left_out) VALUES (?, ?, ?, ?, ?, ?, ?)`,
		run.ID, householdID, l.ID, l.Name, in.Day, now(), strings.Join(in.LeftOut, "\n")); err != nil {
		return ShopRun{}, err
	}
	for _, m := range in.Meals { // the run row took the write lock: nobody adds between this check and the commit
		if m.Planned == 0 {
			continue
		}
		var on int
		if err := tx.QueryRow(`SELECT COALESCE(SUM(m.portions), 0) FROM meal_shop_meals m JOIN meal_shop_runs r ON r.id = m.run_id
			WHERE r.household_id = ? AND r.undone_at IS NULL AND m.day = ? AND m.mealtime = ? AND m.recipe_id = ? AND m.run_id <> ?`,
			householdID, m.Day, m.Mealtime, m.RecipeID, run.ID).Scan(&on); err != nil {
			return ShopRun{}, err
		}
		if on+m.Portions > m.Planned {
			return ShopRun{}, ErrShopStale
		}
	}
	es, err := entriesTx(tx, l.ID)
	if err != nil {
		return ShopRun{}, err
	}
	steps := shop.Plan(es, in.Rows, in.Show)
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

// ShopLastList is the list the last run (not taken back) used, "" if there is none. A deleted list takes its runs with it.
func (db *DB) ShopLastList(householdID string) (string, error) {
	var id string
	err := db.sql.QueryRow(`SELECT list_id FROM meal_shop_runs WHERE household_id = ? AND undone_at IS NULL ORDER BY created_at DESC, rowid DESC LIMIT 1`, householdID).Scan(&id)
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
	// Mark first: the conditional update takes the write lock, and a second submit finds the run taken back.
	done, err := tx.Exec(`UPDATE meal_shop_runs SET undone_at = ? WHERE id = ? AND undone_at IS NULL`, now(), runID)
	if err != nil {
		return ShopUndo{}, err
	}
	if n, _ := done.RowsAffected(); n == 0 {
		return ShopUndo{}, ErrShopTaken
	}
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
			if !cur.None() && !s.Added.None() && cur.Kind() == s.Added.Kind() && cur.Base-s.Added.Base > -1e-9 {
				return list.Quantity{} // only what this run added is left: the entry had none from anything else that still counts
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
			if err := set(q); err != nil {
				return ShopUndo{}, err
			}
			res.Reverted = append(res.Reverted, ShopReverted{label, q})
		}
	}
	if err := tx.Commit(); err != nil {
		return ShopUndo{}, fmt.Errorf("undo meal shop: %w", err)
	}
	return res, nil
}

// takeBackCell takes back, from the entries not yet ticked, what a meal's
// recipes brought to a list, as far as the plan no longer needs it (ticket
// 284): keepRecipe stays with keepPortions portions, every other recipe that
// has ingredients on a list for this meal goes. Called in the transaction that
// changes the plan, so the plan and the lists never disagree.
func takeBackCell(tx *sql.Tx, householdID, day string, mealtime meal.Mealtime, keepRecipe string, keepPortions int) error {
	rows, err := tx.Query(`SELECT m.recipe_id, SUM(m.portions) FROM meal_shop_meals m JOIN meal_shop_runs r ON r.id = m.run_id
		WHERE r.household_id = ? AND r.undone_at IS NULL AND m.day = ? AND m.mealtime = ? GROUP BY m.recipe_id`, householdID, day, mealtime)
	if err != nil {
		return err
	}
	type on struct {
		recipe   string
		portions int
	}
	var all []on
	for rows.Next() {
		var o on
		if err := rows.Scan(&o.recipe, &o.portions); err != nil {
			rows.Close()
			return err
		}
		all = append(all, o)
	}
	if err := rows.Close(); err != nil {
		return err
	}
	for _, o := range all {
		keep := 0
		if o.recipe == keepRecipe {
			keep = keepPortions
		}
		if o.portions > keep {
			if err := takeBackMeal(tx, householdID, day, mealtime, o.recipe, o.portions-keep); err != nil {
				return err
			}
		}
	}
	return nil
}

// takeBackRecipe takes back every meal of a recipe that has ingredients on a list.
func takeBackRecipe(tx *sql.Tx, householdID, recipeID string) error {
	rows, err := tx.Query(`SELECT m.day, m.mealtime, SUM(m.portions) FROM meal_shop_meals m JOIN meal_shop_runs r ON r.id = m.run_id
		WHERE r.household_id = ? AND r.undone_at IS NULL AND m.recipe_id = ? GROUP BY m.day, m.mealtime`, householdID, recipeID)
	if err != nil {
		return err
	}
	type cellOn struct {
		day      string
		mealtime meal.Mealtime
		portions int
	}
	var all []cellOn
	for rows.Next() {
		var c cellOn
		if err := rows.Scan(&c.day, &c.mealtime, &c.portions); err != nil {
			rows.Close()
			return err
		}
		all = append(all, c)
	}
	if err := rows.Close(); err != nil {
		return err
	}
	for _, c := range all {
		if err := takeBackMeal(tx, householdID, c.day, c.mealtime, recipeID, c.portions); err != nil {
			return err
		}
	}
	return nil
}

// takeBackMeal takes `portions` portions of one meal off the lists, latest run
// first. Each run's recorded contributions shrink in proportion; an open entry
// loses exactly that quantity. What reaches zero disappears when a run made the
// entry, and an entry nobody shops for any more loses its note. Ticked entries
// keep their quantity and their note.
func takeBackMeal(tx *sql.Tx, householdID, day string, mealtime meal.Mealtime, recipeID string, portions int) error {
	type runMeal struct {
		id       string
		portions int
	}
	rows, err := tx.Query(`SELECT m.run_id, m.portions FROM meal_shop_meals m JOIN meal_shop_runs r ON r.id = m.run_id
		WHERE r.household_id = ? AND r.undone_at IS NULL AND m.day = ? AND m.mealtime = ? AND m.recipe_id = ? ORDER BY r.created_at DESC, r.rowid DESC`,
		householdID, day, mealtime, recipeID)
	if err != nil {
		return err
	}
	var runs []runMeal
	for rows.Next() {
		var r runMeal
		if err := rows.Scan(&r.id, &r.portions); err != nil {
			rows.Close()
			return err
		}
		runs = append(runs, r)
	}
	if err := rows.Close(); err != nil {
		return err
	}
	touched := map[string]bool{}
	for _, r := range runs {
		if portions <= 0 {
			break
		}
		take := min(portions, r.portions)
		portions -= take
		left := r.portions - take
		if err := shrinkRun(tx, r.id, day, mealtime, recipeID, r.portions, left, touched); err != nil {
			return err
		}
		if left == 0 {
			_, err = tx.Exec(`DELETE FROM meal_shop_meals WHERE run_id = ? AND day = ? AND mealtime = ?`, r.id, day, mealtime)
		} else {
			_, err = tx.Exec(`UPDATE meal_shop_meals SET portions = ? WHERE run_id = ? AND day = ? AND mealtime = ?`, left, r.id, day, mealtime)
		}
		if err != nil {
			return err
		}
	}
	for entryID := range touched {
		if err := tidyEntry(tx, entryID); err != nil {
			return err
		}
	}
	return nil
}

// shrinkRun scales the contributions of one run's meal from `was` to `left`
// portions and takes the difference off the open entries.
func shrinkRun(tx *sql.Tx, runID, day string, mealtime meal.Mealtime, recipeID string, was, left int, touched map[string]bool) error {
	type part struct {
		entry string
		qty   list.Quantity
	}
	rows, err := tx.Query(`SELECT entry_id, qty_base, qty_unit FROM meal_shop_parts WHERE run_id = ? AND day = ? AND mealtime = ? AND recipe_id = ?`, runID, day, mealtime, recipeID)
	if err != nil {
		return err
	}
	var parts []part
	for rows.Next() {
		var p part
		if err := rows.Scan(&p.entry, &p.qty.Base, &p.qty.Unit); err != nil {
			rows.Close()
			return err
		}
		parts = append(parts, p)
	}
	if err := rows.Close(); err != nil {
		return err
	}
	for _, p := range parts {
		var done bool
		var cur list.Quantity
		err := tx.QueryRow(`SELECT done, qty_base, qty_unit FROM list_entries WHERE id = ?`, p.entry).Scan(&done, &cur.Base, &cur.Unit)
		if errors.Is(err, sql.ErrNoRows) {
			continue // deleted by hand meanwhile; the foreign key took the record
		}
		if err != nil {
			return err
		}
		if done {
			continue // ticked: bought already, quantity and note stay
		}
		newQty := p.qty.Base * float64(left) / float64(was)
		if left == 0 {
			newQty = 0
		}
		if cut := p.qty.Base - newQty; cut > 0 && !cur.None() && !p.qty.None() && cur.Kind() == p.qty.Kind() {
			rest := cur.Base - cut
			if rest < 1e-9 {
				rest = 0
			}
			if rest > 0 {
				rest = math.Round(rest*1e6) / 1e6
			}
			unit := cur.Unit
			if rest == 0 {
				unit = ""
			}
			if _, err := tx.Exec(`UPDATE list_entries SET qty_base = ?, qty_unit = ?, `+clearOrigin+` WHERE id = ?`, rest, unit, p.entry); err != nil {
				return err
			}
		}
		if left == 0 {
			_, err = tx.Exec(`DELETE FROM meal_shop_parts WHERE run_id = ? AND entry_id = ? AND day = ? AND mealtime = ?`, runID, p.entry, day, mealtime)
		} else {
			_, err = tx.Exec(`UPDATE meal_shop_parts SET portions = ?, qty_base = ? WHERE run_id = ? AND entry_id = ? AND day = ? AND mealtime = ?`, left, newQty, runID, p.entry, day, mealtime)
		}
		if err != nil {
			return err
		}
		touched[p.entry] = true
	}
	return nil
}

// tidyEntry removes an open entry a run made once no meal shops for it any
// more and nothing is left of its quantity.
func tidyEntry(tx *sql.Tx, entryID string) error {
	var qty float64
	var done bool
	if err := tx.QueryRow(`SELECT done, qty_base FROM list_entries WHERE id = ?`, entryID).Scan(&done, &qty); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		return err
	}
	if done || qty > 0 {
		return nil
	}
	var parts, made int
	if err := tx.QueryRow(`SELECT COUNT(*) FROM meal_shop_parts p JOIN meal_shop_runs r ON r.id = p.run_id WHERE p.entry_id = ? AND r.undone_at IS NULL`, entryID).Scan(&parts); err != nil {
		return err
	}
	if parts > 0 {
		return nil
	}
	if err := tx.QueryRow(`SELECT COUNT(*) FROM meal_shop_steps s JOIN meal_shop_runs r ON r.id = s.run_id WHERE s.entry_id = ? AND s.action = ? AND r.undone_at IS NULL`, entryID, list.AddedNew).Scan(&made); err != nil {
		return err
	}
	if made == 0 {
		return nil // an entry somebody typed stays, without a quantity
	}
	_, err := tx.Exec(`DELETE FROM list_entries WHERE id = ?`, entryID)
	return err
}
