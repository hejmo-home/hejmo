package store

import (
	"database/sql"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/hejmo-home/hejmo/internal/library"
	"github.com/hejmo-home/hejmo/internal/recipe"
)

// Recipe is a stored recipe with who wrote it and who changed it last (ADR
// 0021, ticket 279). A person who left leaves an empty id and name.
type Recipe struct {
	recipe.Recipe
	CreatedBy, CreatedByName string
	CreatedAt                time.Time
	UpdatedBy, UpdatedByName string
	UpdatedAt                time.Time
}

// RecipeNameTakenError says the name is in use already, by the recipe
// Existing (ignoring case and spaces). It matches ErrInvalid.
type RecipeNameTakenError struct{ ExistingID, ExistingName string }

func (e *RecipeNameTakenError) Error() string {
	return fmt.Sprintf("%s: the recipe name %q is taken", ErrInvalid, e.ExistingName)
}
func (e *RecipeNameTakenError) Unwrap() error { return ErrInvalid }

// checkRecipe is recipe.Check with the refusal as the store's *FieldError.
func checkRecipe(r recipe.Recipe) (recipe.Recipe, error) {
	out, err := recipe.Check(r)
	var fe *recipe.FieldError
	if errors.As(err, &fe) {
		return out, &FieldError{Field: fe.Field, Msg: fe.Msg}
	}
	return out, err
}

const recipeColumns = `r.id, r.name, r.category, r.portions, r.minutes, r.mealtimes, r.tips, r.source,
	COALESCE(r.created_by, ''), COALESCE(cp.name, ''), r.created_at, COALESCE(r.updated_by, ''), COALESCE(up.name, ''), r.updated_at`

const recipeFrom = ` FROM recipes r LEFT JOIN people cp ON cp.id = r.created_by LEFT JOIN people up ON up.id = r.updated_by `

func scanRecipe(sc interface{ Scan(...any) error }) (Recipe, error) {
	var r Recipe
	var cat, times, created, updated string
	err := sc.Scan(&r.ID, &r.Name, &cat, &r.Portions, &r.Minutes, &times, &r.Tips, &r.Source,
		&r.CreatedBy, &r.CreatedByName, &created, &r.UpdatedBy, &r.UpdatedByName, &updated)
	r.Category = recipe.Category(cat)
	if times != "" {
		r.Mealtimes = strings.Split(times, ",")
	}
	r.CreatedAt, _ = time.Parse(time.RFC3339, created)
	r.UpdatedAt, _ = time.Parse(time.RFC3339, updated)
	return r, err
}

// loadRecipeParts fills ingredients, steps and tags of the recipes of a
// household (all of them, or the one with onlyID).
func loadRecipeParts(q interface {
	Query(string, ...any) (*sql.Rows, error)
}, householdID, onlyID string, list []Recipe) error {
	at := map[string]int{}
	for i := range list {
		at[list[i].ID] = i
	}
	rows, err := q.Query(`SELECT i.recipe_id, i.amount, i.unit, i.text, i.article_id, i.variety FROM recipe_ingredients i
		JOIN recipes r ON r.id = i.recipe_id WHERE r.household_id = ? AND (? = '' OR r.id = ?) ORDER BY i.recipe_id, i.position`, householdID, onlyID, onlyID)
	if err != nil {
		return err
	}
	for rows.Next() {
		var id string
		var in recipe.Ingredient
		if err := rows.Scan(&id, &in.Amount, &in.Unit, &in.Text, &in.ArticleID, &in.Variety); err != nil {
			rows.Close()
			return err
		}
		if i, ok := at[id]; ok {
			list[i].Ingredients = append(list[i].Ingredients, in)
		}
	}
	if err := rows.Close(); err != nil {
		return err
	}
	for _, t := range []struct{ table, col string }{{"recipe_steps", "text"}, {"recipe_tags", "tag"}} {
		rows, err := q.Query(`SELECT i.recipe_id, i.`+t.col+` FROM `+t.table+` i JOIN recipes r ON r.id = i.recipe_id
			WHERE r.household_id = ? AND (? = '' OR r.id = ?) ORDER BY i.recipe_id, i.position`, householdID, onlyID, onlyID)
		if err != nil {
			return err
		}
		for rows.Next() {
			var id, v string
			if err := rows.Scan(&id, &v); err != nil {
				rows.Close()
				return err
			}
			if i, ok := at[id]; ok {
				if t.table == "recipe_steps" {
					list[i].Steps = append(list[i].Steps, v)
				} else {
					list[i].Tags = append(list[i].Tags, v)
				}
			}
		}
		if err := rows.Close(); err != nil {
			return err
		}
	}
	return nil
}

// Recipes lists the household's recipes from A to Z.
func (db *DB) Recipes(householdID string) ([]Recipe, error) {
	rows, err := db.sql.Query(`SELECT `+recipeColumns+recipeFrom+`WHERE r.household_id = ?`, householdID)
	if err != nil {
		return nil, err
	}
	var list []Recipe
	for rows.Next() {
		r, err := scanRecipe(rows)
		if err != nil {
			rows.Close()
			return nil, err
		}
		list = append(list, r)
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	if err := loadRecipeParts(db.sql, householdID, "", list); err != nil {
		return nil, err
	}
	slices.SortStableFunc(list, func(a, b Recipe) int {
		return strings.Compare(library.Fold(a.Name), library.Fold(b.Name))
	})
	return list, nil
}

// Recipe returns one recipe of the household; ErrNotFound for another's or none.
func (db *DB) Recipe(householdID, id string) (Recipe, error) {
	r, err := scanRecipe(db.sql.QueryRow(`SELECT `+recipeColumns+recipeFrom+`WHERE r.household_id = ? AND r.id = ?`, householdID, id))
	if errors.Is(err, sql.ErrNoRows) {
		return Recipe{}, ErrNotFound
	}
	if err != nil {
		return Recipe{}, err
	}
	list := []Recipe{r}
	if err := loadRecipeParts(db.sql, householdID, id, list); err != nil {
		return Recipe{}, err
	}
	return list[0], nil
}

// nameTaken reports the recipe that has this name, other than except.
func nameTaken(q interface {
	QueryRow(string, ...any) *sql.Row
}, householdID, name, except string) error {
	var id, existing string
	err := q.QueryRow(`SELECT id, name FROM recipes WHERE household_id = ? AND name_key = ? AND id <> ?`, householdID, recipe.NameKey(name), except).Scan(&id, &existing)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return nil
	case err != nil:
		return err
	}
	return &RecipeNameTakenError{ExistingID: id, ExistingName: existing}
}

func writeRecipeParts(tx *sql.Tx, id string, r recipe.Recipe) error {
	for i, in := range r.Ingredients {
		if _, err := tx.Exec(`INSERT INTO recipe_ingredients (recipe_id, position, amount, unit, text, article_id, variety) VALUES (?, ?, ?, ?, ?, ?, ?)`,
			id, i, in.Amount, in.Unit, in.Text, in.ArticleID, in.Variety); err != nil {
			return err
		}
	}
	for i, s := range r.Steps {
		if _, err := tx.Exec(`INSERT INTO recipe_steps (recipe_id, position, text) VALUES (?, ?, ?)`, id, i, s); err != nil {
			return err
		}
	}
	for i, tag := range r.Tags {
		if _, err := tx.Exec(`INSERT INTO recipe_tags (recipe_id, position, tag) VALUES (?, ?, ?)`, id, i, tag); err != nil {
			return err
		}
	}
	return nil
}

// CreateRecipe stores a recipe written by a person (empty for none). The name
// must be free in the household, ignoring case and spaces: otherwise the
// error is a *RecipeNameTakenError.
func (db *DB) CreateRecipe(householdID, personID string, r recipe.Recipe) (Recipe, error) {
	r, err := checkRecipe(r)
	if err != nil {
		return Recipe{}, err
	}
	tx, err := db.sql.Begin()
	if err != nil {
		return Recipe{}, err
	}
	defer tx.Rollback()
	if err := nameTaken(tx, householdID, r.Name, ""); err != nil {
		return Recipe{}, err
	}
	id, ts := newID(), now()
	if _, err := tx.Exec(`INSERT INTO recipes (id, household_id, name, name_key, category, portions, minutes, mealtimes, tips, source, created_by, created_at, updated_by, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, NULLIF(?, ''), ?, NULLIF(?, ''), ?)`,
		id, householdID, r.Name, recipe.NameKey(r.Name), string(r.Category), r.Portions, r.Minutes, strings.Join(r.Mealtimes, ","), r.Tips, r.Source, personID, ts, personID, ts); err != nil {
		return Recipe{}, err
	}
	if err := writeRecipeParts(tx, id, r); err != nil {
		return Recipe{}, err
	}
	if err := tx.Commit(); err != nil {
		return Recipe{}, err
	}
	return db.Recipe(householdID, id)
}

// UpdateRecipe replaces the recipe with r.ID (name, details, ingredients,
// steps and tags) and notes who changed it; who wrote it stays. ErrNotFound
// when the household has no such recipe.
func (db *DB) UpdateRecipe(householdID, personID string, r recipe.Recipe) (Recipe, error) {
	id := r.ID
	r, err := checkRecipe(r)
	if err != nil {
		return Recipe{}, err
	}
	tx, err := db.sql.Begin()
	if err != nil {
		return Recipe{}, err
	}
	defer tx.Rollback()
	var one int
	if err := tx.QueryRow(`SELECT 1 FROM recipes WHERE household_id = ? AND id = ?`, householdID, id).Scan(&one); errors.Is(err, sql.ErrNoRows) {
		return Recipe{}, ErrNotFound
	} else if err != nil {
		return Recipe{}, err
	}
	if err := nameTaken(tx, householdID, r.Name, id); err != nil {
		return Recipe{}, err
	}
	if _, err := tx.Exec(`UPDATE recipes SET name = ?, name_key = ?, category = ?, portions = ?, minutes = ?, mealtimes = ?, tips = ?, source = ?, updated_by = NULLIF(?, ''), updated_at = ?
		WHERE id = ? AND household_id = ?`,
		r.Name, recipe.NameKey(r.Name), string(r.Category), r.Portions, r.Minutes, strings.Join(r.Mealtimes, ","), r.Tips, r.Source, personID, now(), id, householdID); err != nil {
		return Recipe{}, err
	}
	for _, t := range []string{"recipe_ingredients", "recipe_steps", "recipe_tags"} {
		if _, err := tx.Exec(`DELETE FROM `+t+` WHERE recipe_id = ?`, id); err != nil {
			return Recipe{}, err
		}
	}
	if err := writeRecipeParts(tx, id, r); err != nil {
		return Recipe{}, err
	}
	if err := tx.Commit(); err != nil {
		return Recipe{}, err
	}
	return db.Recipe(householdID, id)
}

// DeleteRecipe removes a recipe with its ingredients, steps and tags.
func (db *DB) DeleteRecipe(householdID, id string) error {
	return expectOne(db.sql.Exec(`DELETE FROM recipes WHERE household_id = ? AND id = ?`, householdID, id))
}

// RecipeTags are the tags the household uses, most used first (ties in
// alphabetical order), each once with the spelling most often used.
func (db *DB) RecipeTags(householdID string) ([]string, error) {
	rows, err := db.sql.Query(`SELECT t.tag FROM recipe_tags t JOIN recipes r ON r.id = t.recipe_id WHERE r.household_id = ? ORDER BY r.created_at, r.rowid, t.position`, householdID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	type use struct {
		spellings map[string]int
		n         int
		order     []string
	}
	by := map[string]*use{}
	var keys []string
	for rows.Next() {
		var tag string
		if err := rows.Scan(&tag); err != nil {
			return nil, err
		}
		k := library.Fold(tag)
		u := by[k]
		if u == nil {
			u = &use{spellings: map[string]int{}}
			by[k] = u
			keys = append(keys, k)
		}
		u.n++
		if u.spellings[tag] == 0 {
			u.order = append(u.order, tag)
		}
		u.spellings[tag]++
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	slices.SortStableFunc(keys, func(a, b string) int {
		if by[a].n != by[b].n {
			return by[b].n - by[a].n
		}
		return strings.Compare(a, b)
	})
	out := make([]string, 0, len(keys))
	for _, k := range keys {
		best := by[k].order[0]
		for _, s := range by[k].order {
			if by[k].spellings[s] > by[k].spellings[best] {
				best = s
			}
		}
		out = append(out, best)
	}
	return out, nil
}

// RecipeRef names a recipe.
type RecipeRef struct{ ID, Name string }

// RecipesWithArticle lists the household's recipes that use an article as an
// ingredient, by name (ticket 288: the page of an own article says where it is used).
func (db *DB) RecipesWithArticle(householdID, articleID string) ([]RecipeRef, error) {
	rows, err := db.sql.Query(`SELECT r.id, r.name FROM recipes r
		WHERE r.household_id = ? AND EXISTS (SELECT 1 FROM recipe_ingredients i WHERE i.recipe_id = r.id AND i.article_id = ?)
		ORDER BY r.name_key, r.id`, householdID, articleID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []RecipeRef
	for rows.Next() {
		var r RecipeRef
		if err := rows.Scan(&r.ID, &r.Name); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}
