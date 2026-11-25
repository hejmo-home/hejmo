package store

import (
	"database/sql"
	"errors"
	"time"
)

// Own photos of recipes (ticket 280). The files are on disk; this is the list: which recipe, who added it, which
// is the cover. A recipe is an own recipe or a starter recipe ("starter-<slug>").

// MaxRecipePhotos is the most photos one recipe holds.
const MaxRecipePhotos = 12

// ErrRecipePhotosFull is returned when a recipe holds MaxRecipePhotos already.
var ErrRecipePhotosFull = errors.New("store: a recipe holds at most 12 photos")

// Where a recipe photo came from.
const (
	PhotoUploaded = "upload"
	PhotoFromWeb  = "web" // taken over from a web page (ticket 289)
)

// RecipePhoto is one photo of a recipe.
type RecipePhoto struct {
	ID, RecipeRef, FileName string
	Width, Height           int // of the full file
	AddedBy, AddedByName    string
	Origin                  string
	AddedAt                 time.Time
	IsCover                 bool
}

// NewRecipePhoto is what a new photo is made from. An empty PersonID is a photo nobody of the household added.
type NewRecipePhoto struct {
	PersonID, FileName, Origin string
	Width, Height              int
}

const recipePhotoColumns = `p.id, p.recipe_ref, p.file_name, p.width, p.height, COALESCE(p.added_by, ''), COALESCE(pe.name, ''), p.origin, p.added_at, p.is_cover`
const recipePhotoFrom = ` FROM recipe_photos p LEFT JOIN people pe ON pe.id = p.added_by `
const recipePhotoOrder = ` ORDER BY p.is_cover DESC, p.added_at DESC, p.rowid DESC`

func scanRecipePhoto(sc interface{ Scan(...any) error }) (RecipePhoto, error) {
	var p RecipePhoto
	var at string
	var cover int
	err := sc.Scan(&p.ID, &p.RecipeRef, &p.FileName, &p.Width, &p.Height, &p.AddedBy, &p.AddedByName, &p.Origin, &at, &cover)
	p.AddedAt, _ = time.Parse(time.RFC3339, at)
	p.IsCover = cover == 1
	return p, err
}

// AddRecipePhoto adds a photo to a recipe. The first photo becomes the cover; later ones never change it.
// ErrRecipePhotosFull when the recipe holds MaxRecipePhotos.
func (db *DB) AddRecipePhoto(householdID, recipeRef string, n NewRecipePhoto) (RecipePhoto, error) {
	tx, err := db.sql.Begin()
	if err != nil {
		return RecipePhoto{}, err
	}
	defer tx.Rollback()
	var count int
	if err := tx.QueryRow(`SELECT COUNT(*) FROM recipe_photos WHERE household_id = ? AND recipe_ref = ?`, householdID, recipeRef).Scan(&count); err != nil {
		return RecipePhoto{}, err
	}
	if count >= MaxRecipePhotos {
		return RecipePhoto{}, ErrRecipePhotosFull
	}
	origin := n.Origin
	if origin == "" {
		origin = PhotoUploaded
	}
	id := newID()
	if _, err := tx.Exec(`INSERT INTO recipe_photos (id, household_id, recipe_ref, file_name, width, height, added_by, origin, added_at, is_cover)
		VALUES (?, ?, ?, ?, ?, ?, NULLIF(?, ''), ?, ?, ?)`,
		id, householdID, recipeRef, n.FileName, n.Width, n.Height, n.PersonID, origin, now(), count == 0); err != nil {
		return RecipePhoto{}, err
	}
	p, err := scanRecipePhoto(tx.QueryRow(`SELECT `+recipePhotoColumns+recipePhotoFrom+`WHERE p.id = ?`, id))
	if err != nil {
		return RecipePhoto{}, err
	}
	return p, tx.Commit()
}

// RecipePhotos lists the photos of a recipe: the cover first, then the newest first.
func (db *DB) RecipePhotos(householdID, recipeRef string) ([]RecipePhoto, error) {
	rows, err := db.sql.Query(`SELECT `+recipePhotoColumns+recipePhotoFrom+`WHERE p.household_id = ? AND p.recipe_ref = ?`+recipePhotoOrder, householdID, recipeRef)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []RecipePhoto
	for rows.Next() {
		p, err := scanRecipePhoto(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// RecipePhoto returns one photo of a recipe; ErrNotFound for another's or none.
func (db *DB) RecipePhoto(householdID, recipeRef, id string) (RecipePhoto, error) {
	p, err := scanRecipePhoto(db.sql.QueryRow(`SELECT `+recipePhotoColumns+recipePhotoFrom+`WHERE p.household_id = ? AND p.recipe_ref = ? AND p.id = ?`, householdID, recipeRef, id))
	if errors.Is(err, sql.ErrNoRows) {
		return RecipePhoto{}, ErrNotFound
	}
	return p, err
}

// RecipeCovers returns the cover of every recipe of the household that has photos, by recipe id.
func (db *DB) RecipeCovers(householdID string) (map[string]RecipePhoto, error) {
	rows, err := db.sql.Query(`SELECT `+recipePhotoColumns+recipePhotoFrom+`WHERE p.household_id = ? AND p.is_cover = 1`, householdID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]RecipePhoto{}
	for rows.Next() {
		p, err := scanRecipePhoto(rows)
		if err != nil {
			return nil, err
		}
		out[p.RecipeRef] = p
	}
	return out, rows.Err()
}

// SetRecipePhotoCover makes a photo the cover of its recipe.
func (db *DB) SetRecipePhotoCover(householdID, recipeRef, id string) error {
	tx, err := db.sql.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var n int
	if err := tx.QueryRow(`SELECT COUNT(*) FROM recipe_photos WHERE household_id = ? AND recipe_ref = ? AND id = ?`, householdID, recipeRef, id).Scan(&n); err != nil {
		return err
	}
	if n == 0 {
		return ErrNotFound
	}
	if _, err := tx.Exec(`UPDATE recipe_photos SET is_cover = 0 WHERE household_id = ? AND recipe_ref = ? AND is_cover = 1`, householdID, recipeRef); err != nil {
		return err
	}
	if _, err := tx.Exec(`UPDATE recipe_photos SET is_cover = 1 WHERE id = ?`, id); err != nil {
		return err
	}
	return tx.Commit()
}

// DeleteRecipePhoto removes a photo. When it was the cover, the newest remaining photo becomes the cover and is
// returned as next; else next is nil. The caller removes the files.
func (db *DB) DeleteRecipePhoto(householdID, recipeRef, id string) (gone RecipePhoto, next *RecipePhoto, err error) {
	tx, err := db.sql.Begin()
	if err != nil {
		return RecipePhoto{}, nil, err
	}
	defer tx.Rollback()
	gone, err = scanRecipePhoto(tx.QueryRow(`SELECT `+recipePhotoColumns+recipePhotoFrom+`WHERE p.household_id = ? AND p.recipe_ref = ? AND p.id = ?`, householdID, recipeRef, id))
	if errors.Is(err, sql.ErrNoRows) {
		return RecipePhoto{}, nil, ErrNotFound
	}
	if err != nil {
		return RecipePhoto{}, nil, err
	}
	if _, err := tx.Exec(`DELETE FROM recipe_photos WHERE id = ?`, id); err != nil {
		return RecipePhoto{}, nil, err
	}
	if gone.IsCover {
		n, err := scanRecipePhoto(tx.QueryRow(`SELECT `+recipePhotoColumns+recipePhotoFrom+`WHERE p.household_id = ? AND p.recipe_ref = ?`+recipePhotoOrder+` LIMIT 1`, householdID, recipeRef))
		switch {
		case errors.Is(err, sql.ErrNoRows):
		case err != nil:
			return RecipePhoto{}, nil, err
		default:
			if _, err := tx.Exec(`UPDATE recipe_photos SET is_cover = 1 WHERE id = ?`, n.ID); err != nil {
				return RecipePhoto{}, nil, err
			}
			n.IsCover = true
			next = &n
		}
	}
	return gone, next, tx.Commit()
}

// DeleteRecipeWithPhotos is DeleteRecipe that also removes the photo rows of the recipe. It returns the ids of
// the photos, so that the caller removes their files.
func (db *DB) DeleteRecipeWithPhotos(householdID, id string) ([]string, error) {
	photos, err := db.RecipePhotos(householdID, id)
	if err != nil {
		return nil, err
	}
	if err := db.DeleteRecipe(householdID, id); err != nil {
		return nil, err
	}
	if _, err := db.sql.Exec(`DELETE FROM recipe_photos WHERE household_id = ? AND recipe_ref = ?`, householdID, id); err != nil {
		return nil, err
	}
	ids := make([]string, len(photos))
	for i, p := range photos {
		ids[i] = p.ID
	}
	return ids, nil
}
