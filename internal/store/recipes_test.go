package store_test

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/hejmo-home/hejmo/internal/recipe"
	"github.com/hejmo-home/hejmo/internal/store"
)

// Ticket 279: recipes against a real SQLite file.

func curry() recipe.Recipe {
	return recipe.Recipe{
		Name: "Erdnuss-Curry mit Süßkartoffeln", Category: recipe.Main, Portions: 4, Minutes: 45,
		Mealtimes: []string{"lunch", "dinner"}, Tags: []string{"schnell", "scharf"},
		Tips: "Schmeckt am nächsten Tag besser.", Source: "Kochbuch „Asiatisch für jeden Tag“, Seite 84",
		Ingredients: []recipe.Ingredient{
			{Amount: 600, Unit: "g", Text: "Süßkartoffeln", ArticleID: "sweet_potato"},
			{Amount: 3, Unit: "tbsp", Text: "Currypaste"},
			{Amount: 3, Unit: "tbsp", Text: "Erdnussbutter", ArticleID: "peanut_butter", Variety: "cremig"},
			{Text: "Salz", ArticleID: "salt"},
		},
		Steps: []string{"Süßkartoffeln schälen.", "Currypaste anrösten.", "Alles köcheln lassen."},
	}
}

func TestARecipeIsStoredAndReadBackWithIngredientsAndStepsInOrder(t *testing.T) {
	f := newRewardFixture(t)
	got, err := f.db.CreateRecipe(f.h.ID, f.mama.ID, curry())
	if err != nil || got.ID == "" {
		t.Fatalf("create = %+v, %v", got, err)
	}
	back, err := f.db.Recipe(f.h.ID, got.ID)
	if err != nil {
		t.Fatal(err)
	}
	want := curry()
	want.ID = got.ID
	if !reflect.DeepEqual(back.Recipe, want) {
		t.Errorf("read back\n%+v\nwant\n%+v", back.Recipe, want)
	}
	if back.CreatedBy != f.mama.ID || back.CreatedByName != "Mama" || back.CreatedAt.IsZero() || back.UpdatedBy != f.mama.ID || back.UpdatedAt.IsZero() {
		t.Errorf("who and when = %+v", back)
	}
}

func TestStepsAreAnOrderedListOfTheirOwn(t *testing.T) {
	f := newRewardFixture(t)
	r := curry()
	r.Tips = "nur ein Tipp"
	got, err := f.db.CreateRecipe(f.h.ID, f.mama.ID, r)
	if err != nil {
		t.Fatal(err)
	}
	var n int
	if err := f.db.SQL().QueryRow(`SELECT COUNT(*) FROM recipe_steps WHERE recipe_id = ?`, got.ID).Scan(&n); err != nil || n != 3 {
		t.Errorf("steps rows = %d, %v: one row per step, not inside the tips", n, err)
	}
	r.Steps = []string{"B", "A", "C"}
	r.ID = got.ID
	if _, err := f.db.UpdateRecipe(f.h.ID, f.mama.ID, r); err != nil {
		t.Fatal(err)
	}
	back, _ := f.db.Recipe(f.h.ID, got.ID)
	if !reflect.DeepEqual(back.Steps, []string{"B", "A", "C"}) {
		t.Errorf("steps = %q: the order is kept", back.Steps)
	}
	if back.Tips != "nur ein Tipp" {
		t.Errorf("tips = %q", back.Tips)
	}
}

func TestOnlyTheNameIsRequired(t *testing.T) {
	f := newRewardFixture(t)
	got, err := f.db.CreateRecipe(f.h.ID, f.mama.ID, recipe.Recipe{Name: "Pfannkuchen", Category: recipe.Breakfast, Portions: 4})
	if err != nil {
		t.Fatal(err)
	}
	back, _ := f.db.Recipe(f.h.ID, got.ID)
	if back.Minutes != 0 || len(back.Ingredients) != 0 || len(back.Steps) != 0 || len(back.Tags) != 0 || len(back.Mealtimes) != 0 {
		t.Errorf("read back = %+v", back)
	}
	_, err = f.db.CreateRecipe(f.h.ID, f.mama.ID, recipe.Recipe{Name: " ", Category: recipe.Main, Portions: 4})
	var fe *store.FieldError
	if !errors.Is(err, store.ErrInvalid) || !errors.As(err, &fe) || fe.Field != "name" {
		t.Errorf("err = %v", err)
	}
}

func TestANameIsTakenIgnoringCaseAndSpacesAndTheErrorNamesTheExistingRecipe(t *testing.T) {
	f := newRewardFixture(t)
	first, err := f.db.CreateRecipe(f.h.ID, f.mama.ID, recipe.Recipe{Name: "Pfannkuchen", Category: recipe.Breakfast, Portions: 4})
	if err != nil {
		t.Fatal(err)
	}
	_, err = f.db.CreateRecipe(f.h.ID, f.papa.ID, recipe.Recipe{Name: "  PFANNKUCHEN ", Category: recipe.Main, Portions: 2})
	var taken *store.RecipeNameTakenError
	if !errors.Is(err, store.ErrInvalid) || !errors.As(err, &taken) || taken.ExistingID != first.ID || taken.ExistingName != "Pfannkuchen" {
		t.Fatalf("err = %v", err)
	}
	other, err := f.db.CreateRecipe(f.h.ID, f.mama.ID, recipe.Recipe{Name: "Waffeln", Category: recipe.Breakfast, Portions: 4})
	if err != nil {
		t.Fatal(err)
	}
	other.Name = "pfannkuchen"
	if _, err := f.db.UpdateRecipe(f.h.ID, f.mama.ID, other.Recipe); !errors.As(err, &taken) {
		t.Errorf("renaming onto a taken name: %v", err)
	}
	first.Name = "PFANNKUCHEN"
	if _, err := f.db.UpdateRecipe(f.h.ID, f.mama.ID, first.Recipe); err != nil {
		t.Errorf("a recipe may change the case of its own name: %v", err)
	}
}

// A regression guard, not a fix: the store opens SQLite with _txlock=immediate, so write transactions run one
// at a time and the name check inside the transaction already sees the first save.
func TestTwoParentsSavingTheSameNewNameAtOnceGetTheNameTakenError(t *testing.T) {
	f := newRewardFixture(t)
	const n = 8
	errs := make(chan error, n)
	for i := 0; i < n; i++ {
		go func() {
			_, err := f.db.CreateRecipe(f.h.ID, f.mama.ID, recipe.Recipe{Name: "Gleichzeitig", Category: recipe.Main, Portions: 4})
			errs <- err
		}()
	}
	made := 0
	for i := 0; i < n; i++ {
		var taken *store.RecipeNameTakenError
		switch err := <-errs; {
		case err == nil:
			made++
		case !errors.As(err, &taken) || taken.ExistingID == "":
			t.Errorf("a second save = %v, want the name-taken error naming the recipe", err)
		}
	}
	if made != 1 {
		t.Errorf("%d recipes made, want 1", made)
	}
}

func TestEachHouseholdHasItsOwnRecipes(t *testing.T) {
	f := newRewardFixture(t)
	h2, err := f.db.CreateHousehold(store.Household{Name: "Zweites Haus"})
	if err != nil {
		t.Fatal(err)
	}
	a, err := f.db.CreateRecipe(f.h.ID, f.mama.ID, recipe.Recipe{Name: "Suppe", Category: recipe.Main, Portions: 4})
	if err != nil {
		t.Fatal(err)
	}
	b, err := f.db.CreateRecipe(h2.ID, "", recipe.Recipe{Name: "Suppe", Category: recipe.Main, Portions: 4})
	if err != nil {
		t.Fatalf("the same name in another household: %v", err)
	}
	if _, err := f.db.Recipe(f.h.ID, b.ID); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("another household's recipe: %v", err)
	}
	if err := f.db.DeleteRecipe(f.h.ID, b.ID); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("deleting another household's recipe: %v", err)
	}
	if list, _ := f.db.Recipes(f.h.ID); len(list) != 1 || list[0].ID != a.ID {
		t.Errorf("list = %+v", list)
	}
}

func TestChangingARecipeReplacesIngredientsStepsTagsAndKeepsWhoMadeIt(t *testing.T) {
	f := newRewardFixture(t)
	got, err := f.db.CreateRecipe(f.h.ID, f.mama.ID, curry())
	if err != nil {
		t.Fatal(err)
	}
	r := got.Recipe
	r.Ingredients = []recipe.Ingredient{{Amount: 1.5, Unit: "l", Text: "Wasser"}}
	r.Steps = []string{"Kochen."}
	r.Tags = nil
	r.Mealtimes = nil
	r.Minutes = 0
	r.Portions = 2
	r.Category = recipe.Side
	upd, err := f.db.UpdateRecipe(f.h.ID, f.papa.ID, r)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(upd.Recipe, r) || upd.CreatedBy != f.mama.ID || upd.UpdatedBy != f.papa.ID || upd.UpdatedByName != "Papa" {
		t.Errorf("updated = %+v", upd)
	}
	if upd.UpdatedAt.Before(upd.CreatedAt) {
		t.Errorf("times = %v %v", upd.CreatedAt, upd.UpdatedAt)
	}
	var n int
	f.db.SQL().QueryRow(`SELECT (SELECT COUNT(*) FROM recipe_ingredients WHERE recipe_id = ?) + (SELECT COUNT(*) FROM recipe_steps WHERE recipe_id = ?) + (SELECT COUNT(*) FROM recipe_tags WHERE recipe_id = ?)`, got.ID, got.ID, got.ID).Scan(&n)
	if n != 2 {
		t.Errorf("rows left = %d, want 1 ingredient + 1 step", n)
	}
	r.ID = "nope"
	if _, err := f.db.UpdateRecipe(f.h.ID, f.papa.ID, r); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("unknown recipe: %v", err)
	}
}

func TestDeletingARecipeTakesItsPartsWithIt(t *testing.T) {
	f := newRewardFixture(t)
	got, _ := f.db.CreateRecipe(f.h.ID, f.mama.ID, curry())
	if err := f.db.DeleteRecipe(f.h.ID, got.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.Recipe(f.h.ID, got.ID); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("after delete: %v", err)
	}
	var n int
	f.db.SQL().QueryRow(`SELECT (SELECT COUNT(*) FROM recipe_ingredients) + (SELECT COUNT(*) FROM recipe_steps) + (SELECT COUNT(*) FROM recipe_tags)`).Scan(&n)
	if n != 0 {
		t.Errorf("%d parts left behind", n)
	}
	if err := f.db.DeleteRecipe(f.h.ID, got.ID); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("deleting twice: %v", err)
	}
}

func TestTheListIsSortedFromAToZAndKnownTagsKeepTheirSpelling(t *testing.T) {
	f := newRewardFixture(t)
	for _, c := range []struct {
		name string
		tags []string
	}{{"Zucchinipfanne", []string{"schnell"}}, {"apfelmus", []string{"Kinder mögen es", "schnell"}}, {"Äpfel im Schlafrock", nil}, {"Bohnensuppe", []string{"Schnell"}}} {
		if _, err := f.db.CreateRecipe(f.h.ID, f.mama.ID, recipe.Recipe{Name: c.name, Category: recipe.Main, Portions: 4, Tags: c.tags}); err != nil {
			t.Fatal(err)
		}
	}
	list, err := f.db.Recipes(f.h.ID)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, r := range list {
		names = append(names, r.Name)
	}
	if want := []string{"Äpfel im Schlafrock", "apfelmus", "Bohnensuppe", "Zucchinipfanne"}; !reflect.DeepEqual(names, want) {
		t.Errorf("order = %q", names)
	}
	if list[1].Tags[0] != "Kinder mögen es" || len(list[0].Tags) != 0 {
		t.Errorf("tags of the list = %+v", list)
	}
	tags, err := f.db.RecipeTags(f.h.ID)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"schnell", "Kinder mögen es"}; !reflect.DeepEqual(tags, want) {
		t.Errorf("known tags = %q: most used first, one spelling each", tags)
	}
}

func TestACategoryOutsideTheSixIsRefusedByTheStoreToo(t *testing.T) {
	f := newRewardFixture(t)
	_, err := f.db.CreateRecipe(f.h.ID, f.mama.ID, recipe.Recipe{Name: "Suppe", Category: "soup", Portions: 4})
	if !errors.Is(err, store.ErrInvalid) {
		t.Errorf("err = %v", err)
	}
	long := strings.Repeat("a", 121)
	_, err = f.db.CreateRecipe(f.h.ID, f.mama.ID, recipe.Recipe{Name: long, Category: recipe.Main, Portions: 4})
	if !errors.Is(err, store.ErrInvalid) {
		t.Errorf("long name: %v", err)
	}
}

func TestAPersonWhoLeavesStaysAsAnEmptyNameOnTheirRecipes(t *testing.T) {
	f := newRewardFixture(t)
	got, err := f.db.CreateRecipe(f.h.ID, f.papa.ID, recipe.Recipe{Name: "Suppe", Category: recipe.Main, Portions: 4})
	if err != nil {
		t.Fatal(err)
	}
	if err := f.db.ExecUnchecked(`DELETE FROM people WHERE id = ?`, f.papa.ID); err != nil {
		t.Fatal(err)
	}
	back, err := f.db.Recipe(f.h.ID, got.ID)
	if err != nil || back.CreatedBy != "" || back.CreatedByName != "" || back.UpdatedByName != "" {
		t.Errorf("back = %+v, %v", back, err)
	}
}
