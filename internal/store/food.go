package store

import (
	"database/sql"
	"errors"
	"strings"
)

// Food is a person's diet and allergies (ADR 0019): the 14 main allergens by
// key, and free text the box cannot check. Health data.
type Food struct {
	Diet      string
	Allergens []string
	Other     string
}

func splitKeys(s string) []string {
	if s == "" {
		return []string{}
	}
	return strings.Split(s, ",")
}

// PersonFood loads a member's diet and allergies; a member without an entry
// eats everything and has none.
func (db *DB) PersonFood(householdID, personID string) (Food, error) {
	if _, err := db.Person(householdID, personID); err != nil {
		return Food{}, err
	}
	var f Food
	var keys string
	err := db.sql.QueryRow(`SELECT diet, allergens, other FROM person_food WHERE person_id = ?`, personID).Scan(&f.Diet, &keys, &f.Other)
	if errors.Is(err, sql.ErrNoRows) {
		return Food{Diet: "omnivore", Allergens: []string{}}, nil
	}
	f.Allergens = splitKeys(keys)
	return f, err
}

// SetPersonFood replaces a member's diet and allergies.
func (db *DB) SetPersonFood(householdID, personID string, f Food) error {
	if _, err := db.Person(householdID, personID); err != nil {
		return err
	}
	_, err := db.sql.Exec(`INSERT INTO person_food (person_id, diet, allergens, other) VALUES (?, ?, ?, ?)
		ON CONFLICT(person_id) DO UPDATE SET diet = excluded.diet, allergens = excluded.allergens, other = excluded.other`,
		personID, f.Diet, strings.Join(f.Allergens, ","), f.Other)
	return err
}

// HouseholdFood returns the entries of the household's members by person id;
// members without an entry are left out.
func (db *DB) HouseholdFood(householdID string) (map[string]Food, error) {
	rows, err := db.sql.Query(`SELECT f.person_id, f.diet, f.allergens, f.other FROM person_food f
		JOIN household_members m ON m.person_id = f.person_id WHERE m.household_id = ?`, householdID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]Food{}
	for rows.Next() {
		var id, keys string
		var f Food
		if err := rows.Scan(&id, &f.Diet, &keys, &f.Other); err != nil {
			return nil, err
		}
		f.Allergens = splitKeys(keys)
		out[id] = f
	}
	return out, rows.Err()
}
