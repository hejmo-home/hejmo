package store_test

import (
	"errors"
	"testing"

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
