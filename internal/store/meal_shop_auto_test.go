package store_test

import (
	"errors"
	"testing"

	"github.com/hejmo-home/hejmo/internal/list"
	"github.com/hejmo-home/hejmo/internal/store"
)

// Ticket 285: the household setting of the shopping list from the meal plan.

func TestTheShoppingListIsByHandUntilTheHouseholdSwitchesItOn(t *testing.T) {
	db, h, l := shopping(t)
	got, err := db.MealShopAuto(h.ID)
	if err != nil || got.On || got.Days != store.DefaultShopDays || got.ListID != "" {
		t.Fatalf("default = %+v, %v: by hand, 7 days, no list chosen", got, err)
	}
	if err := db.SetMealShopAuto(h.ID, store.MealShopAuto{On: true, Days: 3, ListID: l.ID}); err != nil {
		t.Fatal(err)
	}
	got, _ = db.MealShopAuto(h.ID)
	if !got.On || got.Days != 3 || got.ListID != l.ID {
		t.Errorf("saved = %+v", got)
	}
	if err := db.DeleteList(h.ID, l.ID); err != nil {
		t.Fatal(err)
	}
	got, _ = db.MealShopAuto(h.ID)
	if !got.On || got.Days != 3 || got.ListID != "" {
		t.Errorf("a deleted list leaves the setting on without a chosen list: %+v", got)
	}
}

func TestTheDaysAndTheListAreChecked(t *testing.T) {
	db, h, l := shopping(t)
	for _, days := range []int{0, -1, store.MaxShopDays + 1} {
		if err := db.SetMealShopAuto(h.ID, store.MealShopAuto{On: true, Days: days}); !errors.Is(err, store.ErrInvalid) {
			t.Errorf("%d days = %v, want ErrInvalid", days, err)
		}
	}
	if err := db.SetMealShopAuto(h.ID, store.MealShopAuto{On: true, Days: 7, ListID: "nope"}); !errors.Is(err, store.ErrInvalid) {
		t.Errorf("an unknown list = %v, want ErrInvalid", err)
	}
	other, err := db.CreateHousehold(store.Household{Name: "Other", Timezone: "Europe/Berlin"})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.SetMealShopAuto(other.ID, store.MealShopAuto{On: true, Days: 7, ListID: l.ID}); !errors.Is(err, store.ErrInvalid) {
		t.Errorf("another household's list = %v, want ErrInvalid", err)
	}
}

// The run writes the entries in the language of the parent who saved the setting.
func TestTheLanguageOfTheParentWhoSavedTheSettingIsKept(t *testing.T) {
	db, h, l := shopping(t)
	if got, _ := db.MealShopAuto(h.ID); got.Lang != "" {
		t.Errorf("before the first save: lang = %q", got.Lang)
	}
	if err := db.SetMealShopAuto(h.ID, store.MealShopAuto{On: true, Days: 7, ListID: l.ID, Lang: "en"}); err != nil {
		t.Fatal(err)
	}
	if got, _ := db.MealShopAuto(h.ID); got.Lang != "en" {
		t.Errorf("lang = %q, want en", got.Lang)
	}
}

// ADR 0015 parity: the web form offers only lists with suggestions, so the API refuses the others.
func TestAListWithoutSuggestionsIsRefused(t *testing.T) {
	db, h, _ := shopping(t)
	plain, err := db.CreateList(h.ID, list.List{Name: "Pinnwand", Color: "blue", Symbol: "circle-filled", Sort: list.Manual})
	if err != nil {
		t.Fatal(err)
	}
	a := store.MealShopAuto{On: true, Days: 7, ListID: plain.ID}
	if err := db.CheckMealShopAuto(h.ID, a); !errors.Is(err, store.ErrInvalid) {
		t.Errorf("check = %v, want ErrInvalid", err)
	}
	if err := db.SetMealShopAuto(h.ID, a); !errors.Is(err, store.ErrInvalid) {
		t.Errorf("set = %v, want ErrInvalid", err)
	}
}
