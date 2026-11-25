package store

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/hejmo-home/hejmo/internal/recipe"
)

// Importing a recipe from a web page (ticket 289): the drafts that wait for a parent's yes, and what an
// imported recipe keeps (its link, its cover and the values the page stated).

// RecipeDraft is a page that was read. Expired says its data is gone: only the note remains, so the page can say so.
type RecipeDraft struct {
	ID, URL, Host      string
	ReadBy, ReadByName string
	ReadAt             time.Time
	Data, Picture      []byte
	Expired            bool
}

// AddRecipeDraft keeps what was read from a page: its data (JSON, with the pictures made from the page's images).
func (db *DB) AddRecipeDraft(householdID, by, url, host string, data []byte, at time.Time) (RecipeDraft, error) {
	id := newID()
	if _, err := db.sql.Exec(`INSERT INTO recipe_drafts (id, household_id, read_by, url, host, data, read_at) VALUES (?, ?, ?, ?, ?, ?, ?)`,
		id, householdID, by, url, host, data, at.UTC().Format(time.RFC3339)); err != nil {
		return RecipeDraft{}, err
	}
	return db.RecipeDraft(householdID, id)
}

// RecipeDraft loads a draft; ErrNotFound when there is none (never read, saved or cancelled and so deleted).
func (db *DB) RecipeDraft(householdID, id string) (RecipeDraft, error) {
	var d RecipeDraft
	var at string
	err := db.sql.QueryRow(`SELECT d.id, d.url, d.host, d.read_by, COALESCE(p.name, ''), d.read_at, d.data, d.expired
		FROM recipe_drafts d LEFT JOIN people p ON p.id = d.read_by WHERE d.household_id = ? AND d.id = ?`, householdID, id).
		Scan(&d.ID, &d.URL, &d.Host, &d.ReadBy, &d.ReadByName, &at, &d.Data, &d.Expired)
	if errors.Is(err, sql.ErrNoRows) {
		return RecipeDraft{}, ErrNotFound
	}
	d.ReadAt, _ = time.Parse(time.RFC3339, at)
	return d, err
}

// DeleteRecipeDraft deletes a draft; ErrNotFound when there is none.
func (db *DB) DeleteRecipeDraft(householdID, id string) error {
	return expectOne(db.sql.Exec(`DELETE FROM recipe_drafts WHERE household_id = ? AND id = ?`, householdID, id))
}

// ExpireRecipeDrafts deletes the data of every draft read before the cutoff and keeps a note
// that it expired; notes older than a day are deleted.
func (db *DB) ExpireRecipeDrafts(cutoff, now time.Time) error {
	tx, err := db.sql.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`UPDATE recipe_drafts SET data = x'', expired = 1 WHERE expired = 0 AND read_at <= ?`, cutoff.UTC().Format(time.RFC3339)); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM recipe_drafts WHERE expired = 1 AND read_at <= ?`, now.Add(-24*time.Hour).UTC().Format(time.RFC3339)); err != nil {
		return err
	}
	return tx.Commit()
}

// PageNutrition is what the page stated per portion; nil is not stated. It holds for the recipe as it was
// saved (Portions and the ingredients); once either changes, DroppedAt says when, by whom and what changed.
type PageNutrition struct {
	KCal, KJ, Fat, Saturates, Carbs, Sugars, Protein, Salt *float64
	URL                                                    string
	At                                                     time.Time
	Portions                                               int
	DroppedAt                                              time.Time
	DroppedBy, DroppedByName                               string
	DroppedWhat                                            string // "portions" or "ingredients"
	DroppedTo                                              int    // the new portions
	ingKey                                                 string
}

// Active says the page's values still hold.
func (p PageNutrition) Active() bool { return p.DroppedAt.IsZero() }

// Imported is what an imported recipe keeps besides the recipe itself.
type Imported struct {
	URL, Host, Lang string
	At              time.Time // zero: now
	Nutrition       *PageNutrition
	DraftID         string // deleted with the save
}

// RecipeImport says where a recipe was imported from.
type RecipeImport struct {
	URL, Host, Lang string
	By, ByName      string
	At              time.Time
}

// ingredientsKey is what two ingredient lists are compared by.
func ingredientsKey(ings []recipe.Ingredient) string {
	var b strings.Builder
	for _, in := range ings {
		fmt.Fprintf(&b, "%g|%s|%s|%s|%s\n", in.Amount, in.Unit, in.Text, in.ArticleID, in.Variety)
	}
	return b.String()
}

// CreateImportedRecipe stores a recipe made from a web page: the recipe with the page's link as its source (which
// never changes), the cover and the page's nutrition values, and deletes the draft. The name rules are those of CreateRecipe.
func (db *DB) CreateImportedRecipe(householdID, personID string, r recipe.Recipe, imp Imported) (Recipe, error) {
	r, err := checkRecipe(r)
	if err != nil {
		return Recipe{}, err
	}
	r.Source = imp.URL
	tx, err := db.sql.Begin()
	if err != nil {
		return Recipe{}, err
	}
	defer tx.Rollback()
	id, err := insertRecipe(tx, householdID, personID, r)
	if err != nil {
		return Recipe{}, err
	}
	at := now()
	if !imp.At.IsZero() {
		at = imp.At.UTC().Format(time.RFC3339)
	}
	if _, err := tx.Exec(`INSERT INTO recipe_imported (recipe_id, url, host, lang, imported_by, imported_at) VALUES (?, ?, ?, ?, NULLIF(?, ''), ?)`,
		id, imp.URL, imp.Host, imp.Lang, personID, at); err != nil {
		return Recipe{}, err
	}
	if n := imp.Nutrition; n != nil {
		if _, err := tx.Exec(`INSERT INTO recipe_page_nutrition (recipe_id, kcal, kj, fat, saturates, carbs, sugars, protein, salt, url, stated_at, portions, ing_key)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			id, n.KCal, n.KJ, n.Fat, n.Saturates, n.Carbs, n.Sugars, n.Protein, n.Salt, imp.URL, at, r.Portions, ingredientsKey(r.Ingredients)); err != nil {
			return Recipe{}, err
		}
	}
	if imp.DraftID != "" {
		if _, err := tx.Exec(`DELETE FROM recipe_drafts WHERE household_id = ? AND id = ?`, householdID, imp.DraftID); err != nil {
			return Recipe{}, err
		}
	}
	if err := tx.Commit(); err != nil {
		return Recipe{}, err
	}
	return db.Recipe(householdID, id)
}

// RecipeImport says where an imported recipe came from; ok is false for a recipe that was not imported.
func (db *DB) RecipeImport(householdID, id string) (RecipeImport, bool, error) {
	var i RecipeImport
	var at string
	err := db.sql.QueryRow(`SELECT i.url, i.host, i.lang, COALESCE(i.imported_by, ''), COALESCE(p.name, ''), i.imported_at
		FROM recipe_imported i JOIN recipes r ON r.id = i.recipe_id LEFT JOIN people p ON p.id = i.imported_by
		WHERE r.household_id = ? AND i.recipe_id = ?`, householdID, id).Scan(&i.URL, &i.Host, &i.Lang, &i.By, &i.ByName, &at)
	if errors.Is(err, sql.ErrNoRows) {
		return RecipeImport{}, false, nil
	}
	i.At, _ = time.Parse(time.RFC3339, at)
	return i, err == nil, err
}

// RecipeImportedRef names a recipe that was imported, and by whom and when.
type RecipeImportedRef struct {
	ID, Name, ByName string
	At               time.Time
}

// RecipeImportedFrom finds the recipe the household imported from this link (the earliest).
func (db *DB) RecipeImportedFrom(householdID, url string) (RecipeImportedRef, bool, error) {
	var ref RecipeImportedRef
	var at string
	err := db.sql.QueryRow(`SELECT r.id, r.name, COALESCE(p.name, ''), i.imported_at FROM recipe_imported i
		JOIN recipes r ON r.id = i.recipe_id LEFT JOIN people p ON p.id = i.imported_by
		WHERE r.household_id = ? AND i.url = ? ORDER BY i.imported_at, r.rowid LIMIT 1`, householdID, url).Scan(&ref.ID, &ref.Name, &ref.ByName, &at)
	if errors.Is(err, sql.ErrNoRows) {
		return RecipeImportedRef{}, false, nil
	}
	ref.At, _ = time.Parse(time.RFC3339, at)
	return ref, err == nil, err
}

// RecipePageNutrition returns the values the page stated; ok is false when the recipe has none.
func (db *DB) RecipePageNutrition(householdID, id string) (PageNutrition, bool, error) {
	var n PageNutrition
	var at, dropped string
	err := db.sql.QueryRow(`SELECT n.kcal, n.kj, n.fat, n.saturates, n.carbs, n.sugars, n.protein, n.salt, n.url, n.stated_at, n.portions, n.ing_key,
		n.dropped_at, COALESCE(n.dropped_by, ''), COALESCE(p.name, ''), n.dropped_what, n.dropped_to
		FROM recipe_page_nutrition n JOIN recipes r ON r.id = n.recipe_id LEFT JOIN people p ON p.id = n.dropped_by
		WHERE r.household_id = ? AND n.recipe_id = ?`, householdID, id).
		Scan(&n.KCal, &n.KJ, &n.Fat, &n.Saturates, &n.Carbs, &n.Sugars, &n.Protein, &n.Salt, &n.URL, &at, &n.Portions, &n.ingKey,
			&dropped, &n.DroppedBy, &n.DroppedByName, &n.DroppedWhat, &n.DroppedTo)
	if errors.Is(err, sql.ErrNoRows) {
		return PageNutrition{}, false, nil
	}
	n.At, _ = time.Parse(time.RFC3339, at)
	n.DroppedAt, _ = time.Parse(time.RFC3339, dropped)
	return n, err == nil, err
}

// keepImport applies what an imported recipe keeps fixed when it is updated: its source stays the page's link,
// and the page's nutrition values are dropped (once, naming the first change) when portions or ingredients change.
func keepImport(tx *sql.Tx, id, personID string, r *recipe.Recipe) error {
	var url string
	err := tx.QueryRow(`SELECT url FROM recipe_imported WHERE recipe_id = ?`, id).Scan(&url)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	r.Source = url
	var portions int
	var key string
	err = tx.QueryRow(`SELECT portions, ing_key FROM recipe_page_nutrition WHERE recipe_id = ? AND dropped_at = ''`, id).Scan(&portions, &key)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	what := ""
	switch {
	case r.Portions != portions:
		what = "portions"
	case ingredientsKey(r.Ingredients) != key:
		what = "ingredients"
	}
	if what == "" {
		return nil
	}
	_, err = tx.Exec(`UPDATE recipe_page_nutrition SET dropped_at = ?, dropped_by = NULLIF(?, ''), dropped_what = ?, dropped_to = ? WHERE recipe_id = ?`,
		now(), personID, what, r.Portions, id)
	return err
}
