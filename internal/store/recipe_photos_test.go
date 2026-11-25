package store_test

import (
	"errors"
	"testing"

	"github.com/hejmo-home/hejmo/internal/store"
)

// Ticket 280: the photos of recipes against a real SQLite file. The cover rule: exactly one cover while photos
// exist; the first photo is it; later uploads never change it; removing it makes the newest remaining photo the
// cover; removing the last leaves none.

func addRcpPhoto(t *testing.T, f rewardFixture, ref, name string) store.RecipePhoto {
	t.Helper()
	p, err := f.db.AddRecipePhoto(f.h.ID, ref, store.NewRecipePhoto{PersonID: f.mama.ID, FileName: name, Width: 1600, Height: 1200})
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func coverCount(ps []store.RecipePhoto) (n int) {
	for _, p := range ps {
		if p.IsCover {
			n++
		}
	}
	return n
}

func TestTheFirstPhotoIsTheCoverAndLaterOnesNeverChangeIt(t *testing.T) {
	f := newRewardFixture(t)
	r, _ := f.db.CreateRecipe(f.h.ID, f.mama.ID, curry())
	a := addRcpPhoto(t, f, r.ID, "a.jpg")
	b := addRcpPhoto(t, f, r.ID, "b.jpg")
	c := addRcpPhoto(t, f, r.ID, "c.jpg")
	if !a.IsCover || b.IsCover || c.IsCover {
		t.Fatalf("covers: %v %v %v", a.IsCover, b.IsCover, c.IsCover)
	}
	list, err := f.db.RecipePhotos(f.h.ID, r.ID)
	if err != nil || len(list) != 3 || coverCount(list) != 1 {
		t.Fatalf("list = %+v, %v", list, err)
	}
	if list[0].ID != a.ID || list[1].ID != c.ID || list[2].ID != b.ID {
		t.Errorf("order = %s %s %s: cover first, then newest first", list[0].FileName, list[1].FileName, list[2].FileName)
	}
	if list[0].AddedByName != "Mama" || list[0].Width != 1600 {
		t.Errorf("who and size = %+v", list[0])
	}
}

func TestChoosingACoverMovesIt(t *testing.T) {
	f := newRewardFixture(t)
	r, _ := f.db.CreateRecipe(f.h.ID, f.mama.ID, curry())
	addRcpPhoto(t, f, r.ID, "a.jpg")
	b := addRcpPhoto(t, f, r.ID, "b.jpg")
	if err := f.db.SetRecipePhotoCover(f.h.ID, r.ID, b.ID); err != nil {
		t.Fatal(err)
	}
	list, _ := f.db.RecipePhotos(f.h.ID, r.ID)
	if coverCount(list) != 1 || list[0].ID != b.ID || !list[0].IsCover {
		t.Errorf("list = %+v", list)
	}
	if err := f.db.SetRecipePhotoCover(f.h.ID, r.ID, "nope"); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("unknown photo = %v", err)
	}
}

func TestRemovingTheCoverMakesTheNewestRemainingPhotoTheCoverAndTheLastLeavesNone(t *testing.T) {
	f := newRewardFixture(t)
	r, _ := f.db.CreateRecipe(f.h.ID, f.mama.ID, curry())
	a := addRcpPhoto(t, f, r.ID, "a.jpg")
	addRcpPhoto(t, f, r.ID, "b.jpg")
	c := addRcpPhoto(t, f, r.ID, "c.jpg")
	gone, next, err := f.db.DeleteRecipePhoto(f.h.ID, r.ID, a.ID)
	if err != nil || gone.ID != a.ID || next == nil || next.ID != c.ID {
		t.Fatalf("delete = %+v, next %+v, %v: the newest remaining photo is the next cover", gone, next, err)
	}
	list, _ := f.db.RecipePhotos(f.h.ID, r.ID)
	if len(list) != 2 || coverCount(list) != 1 || list[0].ID != c.ID {
		t.Errorf("list = %+v", list)
	}
	if _, next, err = f.db.DeleteRecipePhoto(f.h.ID, r.ID, list[1].ID); err != nil || next != nil {
		t.Errorf("deleting a plain photo: next %+v, %v", next, err)
	}
	if _, next, err = f.db.DeleteRecipePhoto(f.h.ID, r.ID, c.ID); err != nil || next != nil {
		t.Errorf("deleting the last: next %+v, %v", next, err)
	}
	if list, _ = f.db.RecipePhotos(f.h.ID, r.ID); len(list) != 0 {
		t.Errorf("list = %+v", list)
	}
	if _, _, err = f.db.DeleteRecipePhoto(f.h.ID, r.ID, c.ID); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("twice = %v", err)
	}
}

func TestAtMostTwelvePhotosPerRecipeAndStarterRecipesHaveTheirOwn(t *testing.T) {
	f := newRewardFixture(t)
	for i := 0; i < store.MaxRecipePhotos; i++ {
		addRcpPhoto(t, f, "starter-pancakes", "p.jpg")
	}
	_, err := f.db.AddRecipePhoto(f.h.ID, "starter-pancakes", store.NewRecipePhoto{PersonID: f.mama.ID, FileName: "x.jpg", Width: 1, Height: 1})
	if !errors.Is(err, store.ErrRecipePhotosFull) {
		t.Fatalf("13th = %v", err)
	}
	addRcpPhoto(t, f, "starter-other", "p.jpg") // another recipe is not full
}

func TestCoversOfAHouseholdAndWebOrigin(t *testing.T) {
	f := newRewardFixture(t)
	r, _ := f.db.CreateRecipe(f.h.ID, f.mama.ID, curry())
	p, err := f.db.AddRecipePhoto(f.h.ID, r.ID, store.NewRecipePhoto{Origin: store.PhotoFromWeb, FileName: "dish.jpg", Width: 800, Height: 600})
	if err != nil || p.Origin != store.PhotoFromWeb || p.AddedBy != "" {
		t.Fatalf("web photo = %+v, %v", p, err)
	}
	addRcpPhoto(t, f, r.ID, "b.jpg")
	covers, err := f.db.RecipeCovers(f.h.ID)
	if err != nil || len(covers) != 1 || covers[r.ID].ID != p.ID {
		t.Errorf("covers = %+v, %v", covers, err)
	}
}

func TestDeletingARecipeDeletesItsPhotoRowsAndNamesThem(t *testing.T) {
	f := newRewardFixture(t)
	r, _ := f.db.CreateRecipe(f.h.ID, f.mama.ID, curry())
	a := addRcpPhoto(t, f, r.ID, "a.jpg")
	b := addRcpPhoto(t, f, r.ID, "b.jpg")
	ids, err := f.db.DeleteRecipeWithPhotos(f.h.ID, r.ID)
	if err != nil || len(ids) != 2 {
		t.Fatalf("ids = %v, %v", ids, err)
	}
	if ids[0] != a.ID && ids[1] != a.ID || ids[0] != b.ID && ids[1] != b.ID {
		t.Errorf("ids = %v", ids)
	}
	if list, _ := f.db.RecipePhotos(f.h.ID, r.ID); len(list) != 0 {
		t.Errorf("rows left: %+v", list)
	}
}
