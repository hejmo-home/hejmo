package store

import (
	"database/sql"
	"errors"
	"fmt"
)

// The household setting of the shopping list from the meal plan (ticket 285).
// See migration 202611260900_meal_shop_auto.sql.

// DefaultShopDays is how many days ahead the automatic shopping list looks.
const DefaultShopDays = 7

// MaxShopDays is the most days ahead it may look.
const MaxShopDays = 14

// MealShopAuto says whether the ingredients of the meals of the next Days days
// go on a list by themselves, and which one ("" for the list of the last run).
type MealShopAuto struct {
	On     bool
	Days   int
	ListID string
}

// MealShopAuto returns the household's setting: by hand, 7 days, until it is saved.
func (db *DB) MealShopAuto(householdID string) (MealShopAuto, error) {
	a := MealShopAuto{Days: DefaultShopDays}
	var list sql.NullString
	err := db.sql.QueryRow(`SELECT auto, days, list_id FROM meal_shop_settings WHERE household_id = ?`, householdID).Scan(&a.On, &a.Days, &list)
	if errors.Is(err, sql.ErrNoRows) {
		return a, nil
	}
	a.ListID = list.String
	return a, err
}

// SetMealShopAuto stores the setting. Days are 1 to MaxShopDays and the list,
// if there is one, is a list of the household (else ErrInvalid).
func (db *DB) SetMealShopAuto(householdID string, a MealShopAuto) error {
	if err := db.CheckMealShopAuto(householdID, a); err != nil {
		return err
	}
	_, err := db.sql.Exec(`INSERT INTO meal_shop_settings (household_id, auto, days, list_id) VALUES (?, ?, ?, NULLIF(?, ''))
		ON CONFLICT(household_id) DO UPDATE SET auto = excluded.auto, days = excluded.days, list_id = excluded.list_id`,
		householdID, a.On, a.Days, a.ListID)
	return err
}

// CheckMealShopAuto says whether SetMealShopAuto would accept the setting (ErrInvalid if not).
func (db *DB) CheckMealShopAuto(householdID string, a MealShopAuto) error {
	if a.Days < 1 || a.Days > MaxShopDays {
		return fmt.Errorf("%w: days must be from 1 to %d", ErrInvalid, MaxShopDays)
	}
	if a.ListID != "" {
		var n int
		if err := db.sql.QueryRow(`SELECT COUNT(*) FROM lists WHERE household_id = ? AND id = ?`, householdID, a.ListID).Scan(&n); err != nil {
			return err
		}
		if n == 0 {
			return fmt.Errorf("%w: list_id is not a list of this household", ErrInvalid)
		}
	}
	return nil
}
