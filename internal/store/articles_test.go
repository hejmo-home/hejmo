package store_test

import (
	"errors"
	"reflect"
	"testing"

	"github.com/hejmo-home/hejmo/internal/store"
)

// Ticket 164: own articles, shipped articles hidden by hand and the setting
// that hides articles with an allergen, against a real SQLite file.

func newArticleFixture(t *testing.T) (*store.DB, store.Household, store.Person) {
	t.Helper()
	db, _ := openTemp(t)
	h, mama := seedParent(t, db)
	return db, h, mama
}

func katzenfutter() store.OwnArticle {
	return store.OwnArticle{Name: "Katzenfutter Huhn", Dept: "other", Kind: "nonfood", Unit1: "pack", PicSource: "symbol", Symbol: "🐈"}
}

func TestAnOwnArticleIsStoredAndReadBack(t *testing.T) {
	db, h, mama := newArticleFixture(t)
	in := store.OwnArticle{Name: "Hafer-Drink Barista", Dept: "drinks", Kind: "vegan", Allergens: []string{"gluten", "soy"}, Unit1: "l", Unit2: "pack"}
	a, err := db.CreateArticle(h.ID, mama.ID, in, nil)
	if err != nil || a.ID == "" {
		t.Fatalf("create = %+v %v", a, err)
	}
	got, err := db.Article(h.ID, a.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Name != in.Name || got.Dept != "drinks" || got.Kind != "vegan" || !reflect.DeepEqual(got.Allergens, in.Allergens) || got.Unit1 != "l" || got.Unit2 != "pack" ||
		got.CreatedBy != mama.ID || got.CreatedAt.IsZero() || got.PicSource != "" || got.HasPicture {
		t.Errorf("read back = %+v", got)
	}
	list, _ := db.Articles(h.ID)
	if len(list) != 1 || list[0].ID != a.ID {
		t.Errorf("articles = %+v", list)
	}
}

func TestAPictureIsStoredWithWhereItCameFromAndServedBack(t *testing.T) {
	db, h, mama := newArticleFixture(t)
	jpeg := []byte("\xff\xd8not really a jpeg\xff\xd9")
	pic := &store.ArticlePicture{JPEG: jpeg, Source: "web", Host: "www.mein-bioladen.de", FromW: 1200, FromH: 1200, FromBytes: 480000}
	a, err := db.CreateArticle(h.ID, mama.ID, katzenfutter(), pic)
	if err != nil {
		t.Fatal(err)
	}
	got, _ := db.Article(h.ID, a.ID)
	if !got.HasPicture || got.PicSource != "web" || got.PictureHost != "www.mein-bioladen.de" || got.PictureFromW != 1200 || got.PictureFromH != 1200 || got.PictureFromBytes != 480000 || got.PictureAt.IsZero() {
		t.Errorf("article = %+v", got)
	}
	if got.Symbol != "" {
		t.Errorf("a picture replaces the symbol, got %q", got.Symbol)
	}
	b, err := db.ArticlePicture(h.ID, a.ID)
	if err != nil || string(b) != string(jpeg) {
		t.Errorf("picture = %q, %v", b, err)
	}
	if _, err := db.ArticlePicture(h.ID, "missing"); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("missing = %v", err)
	}
	noPic, _ := db.CreateArticle(h.ID, mama.ID, store.OwnArticle{Name: "Windeln", Dept: "baby", Kind: "nonfood", Unit1: "pack"}, nil)
	if _, err := db.ArticlePicture(h.ID, noPic.ID); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("no picture = %v", err)
	}
}

func TestChangingAnArticleKeepsReplacesOrRemovesThePicture(t *testing.T) {
	db, h, mama := newArticleFixture(t)
	a, _ := db.CreateArticle(h.ID, mama.ID, katzenfutter(), &store.ArticlePicture{JPEG: []byte("one"), Source: "upload"})
	edit := store.OwnArticle{ID: a.ID, Name: "Katzenfutter Rind", Dept: "other", Kind: "nonfood", Unit1: "pack", Unit2: "piece"}

	if _, err := db.UpdateArticle(h.ID, edit, store.KeepPicture, nil); err != nil {
		t.Fatal(err)
	}
	got, _ := db.Article(h.ID, a.ID)
	if got.Name != "Katzenfutter Rind" || got.Unit2 != "piece" || !got.HasPicture || got.PicSource != "upload" {
		t.Errorf("kept = %+v", got)
	}
	if got.CreatedBy != mama.ID {
		t.Error("who made it must not change")
	}

	if _, err := db.UpdateArticle(h.ID, edit, store.SetPicture, &store.ArticlePicture{JPEG: []byte("two"), Source: "web", Host: "example.org"}); err != nil {
		t.Fatal(err)
	}
	b, _ := db.ArticlePicture(h.ID, a.ID)
	got, _ = db.Article(h.ID, a.ID)
	if string(b) != "two" || got.PicSource != "web" || got.PictureHost != "example.org" {
		t.Errorf("replaced = %q %+v", b, got)
	}

	edit.PicSource, edit.Symbol = "symbol", "🐈"
	if _, err := db.UpdateArticle(h.ID, edit, store.RemovePicture, nil); err != nil {
		t.Fatal(err)
	}
	got, _ = db.Article(h.ID, a.ID)
	if got.HasPicture || got.PicSource != "symbol" || got.Symbol != "🐈" || got.PictureHost != "" {
		t.Errorf("removed, then a symbol = %+v", got)
	}
	if _, err := db.ArticlePicture(h.ID, a.ID); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("picture after removal = %v", err)
	}

	edit.PicSource, edit.Symbol = "", ""
	db.UpdateArticle(h.ID, edit, store.RemovePicture, nil)
	got, _ = db.Article(h.ID, a.ID)
	if got.PicSource != "" || got.Symbol != "" {
		t.Errorf("no picture and no symbol = %+v", got)
	}
}

func TestOwnArticlesBelongToTheirHouseholdAndAreDeleted(t *testing.T) {
	db, h, mama := newArticleFixture(t)
	other, _ := db.CreateHousehold(store.Household{Name: "Other", HomeName: "P", Region: "DE-BY"})
	a, _ := db.CreateArticle(h.ID, mama.ID, katzenfutter(), nil)
	if _, err := db.Article(other.ID, a.ID); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("another household reads it: %v", err)
	}
	if err := db.DeleteArticle(other.ID, a.ID); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("another household deletes it: %v", err)
	}
	if err := db.DeleteArticle(h.ID, a.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Article(h.ID, a.ID); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("after delete: %v", err)
	}
}

func TestTheStoreRefusesAnArticleThatBreaksTheRules(t *testing.T) {
	db, h, mama := newArticleFixture(t)
	for name, a := range map[string]store.OwnArticle{
		"no name":      {Name: " ", Dept: "other", Kind: "veg", Unit1: "piece"},
		"unknown kind": {Name: "x", Dept: "other", Kind: "tasty", Unit1: "piece"},
		"unknown dept": {Name: "x", Dept: "garden", Kind: "veg", Unit1: "piece"},
		"no unit":      {Name: "x", Dept: "other", Kind: "veg"},
		"bad allergen": {Name: "x", Dept: "other", Kind: "veg", Unit1: "piece", Allergens: []string{"kiwi"}},
	} {
		if _, err := db.CreateArticle(h.ID, mama.ID, a, nil); !errors.Is(err, store.ErrInvalid) {
			t.Errorf("%s: err = %v", name, err)
		}
	}
	list, _ := db.Articles(h.ID)
	if len(list) != 0 {
		t.Errorf("%d articles were stored", len(list))
	}
}

func TestDeletingTheAuthorKeepsTheArticle(t *testing.T) {
	db, h, _ := newArticleFixture(t)
	mia, _ := db.AddPerson(h.ID, store.Person{Name: "Mia", Role: store.RoleParent})
	a, _ := db.CreateArticle(h.ID, mia.ID, katzenfutter(), nil)
	if _, err := db.SQL().Exec(`DELETE FROM people WHERE id = ?`, mia.ID); err != nil {
		t.Fatal(err)
	}
	got, err := db.Article(h.ID, a.ID)
	if err != nil || got.CreatedBy != "" {
		t.Errorf("article = %+v, %v", got, err)
	}
}

func TestAShippedArticleIsHiddenByHandAndShownAgain(t *testing.T) {
	db, h, _ := newArticleFixture(t)
	if got, _ := db.HiddenArticles(h.ID); len(got) != 0 {
		t.Errorf("hidden at the start: %v", got)
	}
	if err := db.SetArticleHidden(h.ID, "peanut-butter", true); err != nil {
		t.Fatal(err)
	}
	db.SetArticleHidden(h.ID, "peanut-butter", true) // twice is fine
	db.SetArticleHidden(h.ID, "walnuts", true)
	got, _ := db.HiddenArticles(h.ID)
	if !reflect.DeepEqual(got, map[string]bool{"peanut-butter": true, "walnuts": true}) {
		t.Errorf("hidden = %v", got)
	}
	db.SetArticleHidden(h.ID, "walnuts", false)
	got, _ = db.HiddenArticles(h.ID)
	if !reflect.DeepEqual(got, map[string]bool{"peanut-butter": true}) {
		t.Errorf("after showing = %v", got)
	}
	if err := db.SetArticleHidden(h.ID, "not-an-article", true); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("unknown key = %v", err)
	}
}

func TestHidingArticlesWithAllergensIsOffUntilAParentConfirmsTheNotice(t *testing.T) {
	db, h, mama := newArticleFixture(t)
	s, err := db.LibrarySettings(h.ID)
	if err != nil || s.HideAllergen || !s.Everyone || len(s.People) != 0 || s.ConfirmedBy != "" {
		t.Fatalf("default = %+v %v", s, err)
	}
	mia, _ := db.AddPerson(h.ID, store.Person{Name: "Mia", Role: store.RoleChild})
	on := store.LibrarySettings{HideAllergen: true, Everyone: false, People: []string{mia.ID}, ConfirmedBy: mama.ID}
	if err := db.SetLibrarySettings(h.ID, on); err != nil {
		t.Fatal(err)
	}
	got, _ := db.LibrarySettings(h.ID)
	if !got.HideAllergen || got.Everyone || !reflect.DeepEqual(got.People, []string{mia.ID}) || got.ConfirmedBy != mama.ID || got.ConfirmedAt.IsZero() {
		t.Errorf("stored = %+v", got)
	}
	// Switching it off keeps who confirmed and when, so the page can say so.
	on.HideAllergen = false
	db.SetLibrarySettings(h.ID, on)
	got, _ = db.LibrarySettings(h.ID)
	if got.HideAllergen || got.ConfirmedBy != mama.ID {
		t.Errorf("after off = %+v", got)
	}
	if err := db.SetLibrarySettings(h.ID, store.LibrarySettings{HideAllergen: true, Everyone: true}); !errors.Is(err, store.ErrInvalid) {
		t.Errorf("on without a confirmation = %v", err)
	}
}

// Ticket 278 (ADR 0021): the pantry mark of own and shipped articles.

func TestAnOwnArticleKeepsItsPantryMarkAndItCanBeChanged(t *testing.T) {
	db, h, mama := newArticleFixture(t)
	in := katzenfutter()
	in.Pantry = true
	a, err := db.CreateArticle(h.ID, mama.ID, in, nil)
	if err != nil {
		t.Fatal(err)
	}
	got, _ := db.Article(h.ID, a.ID)
	if !got.Pantry {
		t.Fatalf("created pantry = %v, want true", got.Pantry)
	}
	got.Pantry = false
	if _, err := db.UpdateArticle(h.ID, got, store.KeepPicture, nil); err != nil {
		t.Fatal(err)
	}
	if got, _ = db.Article(h.ID, a.ID); got.Pantry {
		t.Error("the mark was not removed")
	}
	got.Pantry = true
	if _, err := db.UpdateArticle(h.ID, got, store.RemovePicture, nil); err != nil {
		t.Fatal(err)
	}
	if got, _ = db.Article(h.ID, a.ID); !got.Pantry {
		t.Error("the mark was not set again")
	}
}

func TestAParentChangesThePantryMarkOfAShippedArticleAndOnlyTheDifferenceIsStored(t *testing.T) {
	db, h, _ := newArticleFixture(t)
	if got, err := db.PantryOverrides(h.ID); err != nil || len(got) != 0 {
		t.Fatalf("overrides = %v %v, want none", got, err)
	}
	if err := db.SetArticlePantry(h.ID, "salt", false); err != nil { // shipped as pantry
		t.Fatal(err)
	}
	if err := db.SetArticlePantry(h.ID, "apples", true); err != nil { // shipped as not pantry
		t.Fatal(err)
	}
	if err := db.SetArticlePantry(h.ID, "pepper", true); err != nil { // the shipped value: nothing to store
		t.Fatal(err)
	}
	got, _ := db.PantryOverrides(h.ID)
	if !reflect.DeepEqual(got, map[string]bool{"salt": false, "apples": true}) {
		t.Errorf("overrides = %v", got)
	}
	if err := db.SetArticlePantry(h.ID, "salt", true); err != nil { // back to the shipped value
		t.Fatal(err)
	}
	if got, _ = db.PantryOverrides(h.ID); !reflect.DeepEqual(got, map[string]bool{"apples": true}) {
		t.Errorf("after reset = %v", got)
	}
	if err := db.SetArticlePantry(h.ID, "no-such-article", true); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("unknown article = %v, want ErrNotFound", err)
	}
}
