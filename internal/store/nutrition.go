package store

// SetNutritionShown switches the nutrition values of recipes on or off for a
// person (ticket 288). Parents see them whatever the switch says; it decides
// for children, and it is off until a parent switches it on.
func (db *DB) SetNutritionShown(householdID, personID string, on bool) error {
	return expectOne(db.sql.Exec(`UPDATE people SET nutrition_shown = ?, updated_at = ?
		WHERE id = ? AND id IN (SELECT person_id FROM household_members WHERE household_id = ?)`, on, now(), personID, householdID))
}
