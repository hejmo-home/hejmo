package store_test

import (
	"errors"
	"testing"
	"time"

	"github.com/hejmo-home/hejmo/internal/recipe"
	"github.com/hejmo-home/hejmo/internal/store"
)

// Ticket 289: a recipe imported from a web page.

func f64(v float64) *float64 { return &v }

func importedIn(t *testing.T, f calFixture, name string) store.Recipe {
	t.Helper()
	at := time.Date(2026, 11, 25, 15, 43, 2, 0, time.UTC)
	r := recipe.Recipe{Name: name, Category: recipe.Main, Portions: 4, Source: "ignored",
		Ingredients: []recipe.Ingredient{{Amount: 250, Unit: "g", Text: "Linsen", ArticleID: "lentils"}}}
	got, err := f.db.CreateImportedRecipe(f.h.ID, f.mama.ID, r, store.Imported{
		URL: "https://www.beispielrezepte.de/r/1", Host: "www.beispielrezepte.de", Lang: "de", At: at, Cover: []byte("jpeg"),
		Nutrition: &store.PageNutrition{KCal: f64(380), Fat: f64(17)},
	})
	if err != nil {
		t.Fatal(err)
	}
	return got
}

func TestImportedRecipeKeepsItsSourceAndCover(t *testing.T) {
	f := newCalFixture(t)
	got := importedIn(t, f, "Linsensuppe")
	if got.Source != "https://www.beispielrezepte.de/r/1" {
		t.Errorf("source = %q: the import's link is the source", got.Source)
	}
	imp, ok, err := f.db.RecipeImport(f.h.ID, got.ID)
	if err != nil || !ok || imp.Host != "www.beispielrezepte.de" || imp.ByName != "Mama" || imp.Lang != "de" || !imp.At.Equal(time.Date(2026, 11, 25, 15, 43, 2, 0, time.UTC)) {
		t.Errorf("RecipeImport = %+v, %v, %v", imp, ok, err)
	}
	cover, host, err := f.db.RecipeCover(f.h.ID, got.ID)
	if err != nil || string(cover) != "jpeg" || host != "www.beispielrezepte.de" {
		t.Errorf("cover = %q %q %v", cover, host, err)
	}
	if _, ok, _ := f.db.RecipeImport(f.h.ID, "nope"); ok {
		t.Error("a recipe that is not imported has no import")
	}
}

func TestTheSourceOfAnImportedRecipeCannotChange(t *testing.T) {
	f := newCalFixture(t)
	got := importedIn(t, f, "Linsensuppe")
	got.Recipe.Source = "https://evil.example/"
	upd, err := f.db.UpdateRecipe(f.h.ID, f.mama.ID, got.Recipe, store.MealOptions{})
	if err != nil || upd.Source != "https://www.beispielrezepte.de/r/1" {
		t.Errorf("source after update = %q, %v", upd.Source, err)
	}
}

func TestAnotherRecipeWithTheSameLinkIsFound(t *testing.T) {
	f := newCalFixture(t)
	got := importedIn(t, f, "Linsensuppe")
	ref, ok, err := f.db.RecipeImportedFrom(f.h.ID, "https://www.beispielrezepte.de/r/1")
	if err != nil || !ok || ref.ID != got.ID || ref.Name != "Linsensuppe" || ref.ByName != "Mama" {
		t.Errorf("RecipeImportedFrom = %+v %v %v", ref, ok, err)
	}
	if _, ok, _ := f.db.RecipeImportedFrom(f.h.ID, "https://other/"); ok {
		t.Error("another link is not found")
	}
}

func TestPageNutritionIsDroppedWhenIngredientsOrPortionsChange(t *testing.T) {
	f := newCalFixture(t)
	got := importedIn(t, f, "Linsensuppe")
	n, ok, err := f.db.RecipePageNutrition(f.h.ID, got.ID)
	if err != nil || !ok || !n.Active() || *n.KCal != 380 || n.Salt != nil || n.URL == "" {
		t.Fatalf("page nutrition = %+v %v %v", n, ok, err)
	}
	same := got.Recipe
	same.Name = "Linsensuppe (neu)"
	if _, err := f.db.UpdateRecipe(f.h.ID, f.mama.ID, same, store.MealOptions{}); err != nil {
		t.Fatal(err)
	}
	if n, _, _ = f.db.RecipePageNutrition(f.h.ID, got.ID); !n.Active() {
		t.Error("a new name keeps the page values")
	}
	more := same
	more.Portions = 6
	if _, err := f.db.UpdateRecipe(f.h.ID, f.mama.ID, more, store.MealOptions{}); err != nil {
		t.Fatal(err)
	}
	n, _, _ = f.db.RecipePageNutrition(f.h.ID, got.ID)
	if n.Active() || n.DroppedByName != "Mama" || n.DroppedWhat != "portions" || n.Portions != 4 || n.DroppedTo != 6 {
		t.Errorf("after portions: %+v", n)
	}
	// the first change is the one that is told
	third := more
	third.Ingredients = nil
	if _, err := f.db.UpdateRecipe(f.h.ID, f.mama.ID, third, store.MealOptions{}); err != nil {
		t.Fatal(err)
	}
	if n2, _, _ := f.db.RecipePageNutrition(f.h.ID, got.ID); n2.DroppedWhat != "portions" {
		t.Errorf("the first change stays: %+v", n2)
	}
}

func TestIngredientChangeDropsPageNutrition(t *testing.T) {
	f := newCalFixture(t)
	got := importedIn(t, f, "Linsensuppe")
	r := got.Recipe
	r.Ingredients = append(r.Ingredients, recipe.Ingredient{Text: "Salz"})
	if _, err := f.db.UpdateRecipe(f.h.ID, f.mama.ID, r, store.MealOptions{}); err != nil {
		t.Fatal(err)
	}
	if n, _, _ := f.db.RecipePageNutrition(f.h.ID, got.ID); n.Active() || n.DroppedWhat != "ingredients" {
		t.Errorf("%+v", n)
	}
}

func TestRecipeDraftsBelongToTheParentAndExpire(t *testing.T) {
	f := newCalFixture(t)
	at := time.Date(2026, 11, 25, 15, 0, 0, 0, time.UTC)
	d, err := f.db.AddRecipeDraft(f.h.ID, f.mama.ID, "https://x.de/a", "x.de", []byte(`{"a":1}`), []byte("pic"), at)
	if err != nil || d.ReadByName != "Mama" || string(d.Data) != `{"a":1}` || string(d.Picture) != "pic" || !d.ReadAt.Equal(at) {
		t.Fatalf("AddRecipeDraft = %+v, %v", d, err)
	}
	if err := f.db.ExpireRecipeDrafts(at.Add(-time.Minute), at.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if d, err = f.db.RecipeDraft(f.h.ID, d.ID); err != nil || d.Expired {
		t.Fatalf("not yet expired: %+v %v", d, err)
	}
	if err := f.db.ExpireRecipeDrafts(at.Add(time.Minute), at.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if d, err = f.db.RecipeDraft(f.h.ID, d.ID); err != nil || !d.Expired || len(d.Data) != 0 || len(d.Picture) != 0 {
		t.Fatalf("expired: %+v %v", d, err)
	}
	if err := f.db.DeleteRecipeDraft(f.h.ID, d.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.RecipeDraft(f.h.ID, d.ID); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("deleted: %v", err)
	}
}

func TestSavingADraftDeletesIt(t *testing.T) {
	f := newCalFixture(t)
	d, _ := f.db.AddRecipeDraft(f.h.ID, f.mama.ID, "https://x.de/a", "x.de", []byte(`{}`), nil, time.Now())
	_, err := f.db.CreateImportedRecipe(f.h.ID, f.mama.ID, recipe.Recipe{Name: "A", Category: recipe.Main, Portions: 2}, store.Imported{URL: "https://x.de/a", Host: "x.de", At: time.Now(), DraftID: d.ID})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.RecipeDraft(f.h.ID, d.ID); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("the draft stays after saving: %v", err)
	}
}
