package store

import (
	"crypto/rand"
	"database/sql"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/hejmo-home/hejmo/internal/meal"
)

// ErrNotFound is returned when a record does not exist.
var ErrNotFound = errors.New("store: not found")

// Role is a person's role within a household.
type Role string

// Roles in V1. Grandparents, carers and display devices follow in later versions.
const (
	RoleParent Role = "parent"
	RoleChild  Role = "child"
)

// Valid reports whether r is a known role.
func (r Role) Valid() bool { return r == RoleParent || r == RoleChild }

// Household is the family one box serves.
type Household struct {
	ID        string
	Name      string
	HomeName  string
	Latitude  float64
	Longitude float64
	Region    string // ISO 3166-2, e.g. DE-BY
	Timezone  string
	// TimeFormat and TemperatureUnit are household settings (ADR 0016).
	TimeFormat      TimeFormat
	TemperatureUnit TemperatureUnit
	// AutoUpdate is the auto-update mode and UpdateAck the last update event
	// parents dismissed (ADR 0018).
	AutoUpdate UpdateMode
	UpdateAck  string
	// MealCutoffs are the times from which a mealtime is no longer shown on
	// displays (ADR 0016).
	MealCutoffs meal.Cutoffs
	// FirstStepsHidden is set when a parent hid the first steps card on the start page.
	FirstStepsHidden bool
	// FirstStepsSkipped lists the steps of the card a parent skipped, in the
	// order of FirstStepIDs.
	FirstStepsSkipped []string
}

// TimeFormat is how the household wants times shown.
type TimeFormat string

// Time formats; 24-hour is the default.
const (
	Time24h TimeFormat = "24h"
	Time12h TimeFormat = "12h"
)

// TemperatureUnit is how the household wants temperatures shown.
type TemperatureUnit string

// Temperature units; Celsius is the default.
const (
	Celsius    TemperatureUnit = "c"
	Fahrenheit TemperatureUnit = "f"
)

// Valid reports whether f is a known time format.
func (f TimeFormat) Valid() bool { return f == Time24h || f == Time12h }

// Valid reports whether u is a known temperature unit.
func (u TemperatureUnit) Valid() bool { return u == Celsius || u == Fahrenheit }

// UpdateHouseholdSettings stores the household's time format, temperature unit
// and auto-update mode in one statement, so a save is all or nothing.
func (db *DB) UpdateHouseholdSettings(id string, f TimeFormat, u TemperatureUnit, a UpdateMode) error {
	if !f.Valid() || !u.Valid() || !a.Valid() {
		return errors.New("store: unknown time format, temperature unit or update mode")
	}
	return expectOne(db.sql.Exec(`UPDATE households SET time_format = ?, temperature_unit = ?, auto_update = ?, updated_at = ? WHERE id = ?`, f, u, a, now(), id))
}

// SetMealCutoffs stores the times from which a mealtime is no longer shown on
// displays (ADR 0016).
func (db *DB) SetMealCutoffs(id string, c meal.Cutoffs) error {
	if err := c.Validate(); err != nil {
		return fmt.Errorf("%w: %w", ErrInvalid, err) // keeps the *meal.InvalidError and its field
	}
	return expectOne(db.sql.Exec(`UPDATE households SET cutoff_breakfast = ?, cutoff_lunch = ?, cutoff_snack = ?, cutoff_dinner = ?, updated_at = ? WHERE id = ?`,
		c.Breakfast, c.Lunch, c.Snack, c.Dinner, now(), id))
}

// SetFirstStepsHidden shows or hides the first steps card on the start page.
func (db *DB) SetFirstStepsHidden(id string, hidden bool) error {
	return expectOne(db.sql.Exec(`UPDATE households SET first_steps_hidden = ?, updated_at = ? WHERE id = ?`, hidden, now(), id))
}

// FirstStepIDs are the steps of the first steps card, in the order the card
// shows them (ticket 133).
var FirstStepIDs = []string{"family", "parents", "calendars", "tasks", "meals", "display"}

// cleanFirstSteps checks a list of step ids and returns it without doubles, in
// the order of the card.
func cleanFirstSteps(steps []string) ([]string, error) {
	var out []string
	for _, id := range FirstStepIDs {
		if slices.Contains(steps, id) {
			out = append(out, id)
		}
	}
	for _, s := range steps {
		if !slices.Contains(FirstStepIDs, s) {
			return nil, fmt.Errorf("%w: unknown first step %q", ErrInvalid, s)
		}
	}
	return out, nil
}

// SetFirstStepSkipped skips a step of the first steps card, or takes the skip back.
func (db *DB) SetFirstStepSkipped(id, step string, skipped bool) error {
	tx, err := db.sql.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var list string
	if err := tx.QueryRow(`SELECT first_steps_skipped FROM households WHERE id = ?`, id).Scan(&list); errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	} else if err != nil {
		return err
	}
	steps := splitSteps(list)
	if skipped {
		steps = append(steps, step)
	} else {
		steps = slices.DeleteFunc(steps, func(s string) bool { return s == step })
	}
	steps, err = cleanFirstSteps(steps)
	if err != nil {
		return err
	}
	if _, err := tx.Exec(`UPDATE households SET first_steps_skipped = ?, updated_at = ? WHERE id = ?`, strings.Join(steps, ","), now(), id); err != nil {
		return err
	}
	return tx.Commit()
}

func splitSteps(list string) []string {
	if list == "" {
		return nil
	}
	return strings.Split(list, ",")
}

// SaveHouseholdSettings stores time format, temperature unit, auto-update
// mode, meal cut-offs and the first steps state (hidden, skipped steps) in one
// statement, so a save is all or nothing.
func (db *DB) SaveHouseholdSettings(id string, f TimeFormat, u TemperatureUnit, a UpdateMode, c meal.Cutoffs, firstStepsHidden bool, firstStepsSkipped []string) error {
	if !f.Valid() || !u.Valid() || !a.Valid() {
		return errors.New("store: unknown time format, temperature unit or update mode")
	}
	if err := c.Validate(); err != nil {
		return fmt.Errorf("%w: %w", ErrInvalid, err)
	}
	skipped, err := cleanFirstSteps(firstStepsSkipped)
	if err != nil {
		return err
	}
	return expectOne(db.sql.Exec(`UPDATE households SET time_format = ?, temperature_unit = ?, auto_update = ?,
		cutoff_breakfast = ?, cutoff_lunch = ?, cutoff_snack = ?, cutoff_dinner = ?, first_steps_hidden = ?, first_steps_skipped = ?, updated_at = ? WHERE id = ?`,
		f, u, a, c.Breakfast, c.Lunch, c.Snack, c.Dinner, firstStepsHidden, strings.Join(skipped, ","), now(), id))
}

// SetHouseholdCoordinates stores the coordinates of the household's home.
func (db *DB) SetHouseholdCoordinates(id string, lat, lon float64) error {
	return expectOne(db.sql.Exec(`UPDATE households SET latitude = ?, longitude = ?, updated_at = ? WHERE id = ?`, lat, lon, now(), id))
}

// Person is a family member as seen from one household.
type Person struct {
	ID     string
	Name   string
	Role   Role
	Color  string
	Shape  string
	Symbol string
}

func newID() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	b[6] = b[6]&0x0f | 0x40 // UUID version 4
	b[8] = b[8]&0x3f | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

func now() string { return time.Now().UTC().Format(time.RFC3339) }

// CreateHousehold stores a new household.
func (db *DB) CreateHousehold(h Household) (Household, error) {
	if strings.TrimSpace(h.Name) == "" {
		return Household{}, errors.New("store: household name required")
	}
	if h.Timezone == "" {
		h.Timezone = "Europe/Berlin"
	}
	h.TimeFormat, h.TemperatureUnit, h.AutoUpdate, h.MealCutoffs = Time24h, Celsius, UpdateOn, meal.DefaultCutoffs()
	h.ID = newID()
	ts := now()
	_, err := db.sql.Exec(`INSERT INTO households (id, name, home_name, latitude, longitude, region, timezone, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		h.ID, h.Name, h.HomeName, h.Latitude, h.Longitude, h.Region, h.Timezone, ts, ts)
	return h, err
}

// Household loads a household by id.
func (db *DB) Household(id string) (Household, error) {
	return db.scanHousehold(db.sql.QueryRow(`SELECT id, name, home_name, COALESCE(latitude, 0), COALESCE(longitude, 0), region, timezone, time_format, temperature_unit, auto_update, update_ack,
		cutoff_breakfast, cutoff_lunch, cutoff_snack, cutoff_dinner, first_steps_hidden, first_steps_skipped
		FROM households WHERE id = ?`, id))
}

// HomeHousehold returns the household this box serves (V1: exactly one).
func (db *DB) HomeHousehold() (Household, error) {
	return db.scanHousehold(db.sql.QueryRow(`SELECT id, name, home_name, COALESCE(latitude, 0), COALESCE(longitude, 0), region, timezone, time_format, temperature_unit, auto_update, update_ack,
		cutoff_breakfast, cutoff_lunch, cutoff_snack, cutoff_dinner, first_steps_hidden, first_steps_skipped
		FROM households ORDER BY created_at, rowid LIMIT 1`))
}

func (db *DB) scanHousehold(row *sql.Row) (Household, error) {
	var h Household
	var skipped string
	err := row.Scan(&h.ID, &h.Name, &h.HomeName, &h.Latitude, &h.Longitude, &h.Region, &h.Timezone, &h.TimeFormat, &h.TemperatureUnit, &h.AutoUpdate, &h.UpdateAck,
		&h.MealCutoffs.Breakfast, &h.MealCutoffs.Lunch, &h.MealCutoffs.Snack, &h.MealCutoffs.Dinner, &h.FirstStepsHidden, &skipped)
	if errors.Is(err, sql.ErrNoRows) {
		return Household{}, ErrNotFound
	}
	h.FirstStepsSkipped = splitSteps(skipped)
	return h, err
}

// AddPerson creates a person and makes them a member of the household.
func (db *DB) AddPerson(householdID string, p Person) (Person, error) {
	p.Name = strings.TrimSpace(p.Name)
	if p.Name == "" {
		return Person{}, errors.New("store: person name required")
	}
	if !p.Role.Valid() {
		return Person{}, fmt.Errorf("store: unknown role %q", p.Role)
	}
	p.ID = newID()
	ts := now()
	tx, err := db.sql.Begin()
	if err != nil {
		return Person{}, err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`INSERT INTO people (id, name, color, shape, symbol, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?)`,
		p.ID, p.Name, p.Color, p.Shape, p.Symbol, ts, ts); err != nil {
		return Person{}, err
	}
	if _, err := tx.Exec(`INSERT INTO household_members (household_id, person_id, role, sort_order)
		VALUES (?, ?, ?, (SELECT COALESCE(MAX(sort_order), 0) + 1 FROM household_members WHERE household_id = ?))`,
		householdID, p.ID, p.Role, householdID); err != nil {
		return Person{}, err
	}
	return p, tx.Commit()
}

const personColumns = `p.id, p.name, m.role, p.color, p.shape, p.symbol`

// People lists a household's members in display order.
func (db *DB) People(householdID string) ([]Person, error) {
	rows, err := db.sql.Query(`SELECT `+personColumns+` FROM people p
		JOIN household_members m ON m.person_id = p.id
		WHERE m.household_id = ? ORDER BY m.sort_order, p.name`, householdID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Person
	for rows.Next() {
		var p Person
		if err := rows.Scan(&p.ID, &p.Name, &p.Role, &p.Color, &p.Shape, &p.Symbol); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// Person loads one member of a household.
func (db *DB) Person(householdID, personID string) (Person, error) {
	var p Person
	err := db.sql.QueryRow(`SELECT `+personColumns+` FROM people p
		JOIN household_members m ON m.person_id = p.id
		WHERE m.household_id = ? AND p.id = ?`, householdID, personID).
		Scan(&p.ID, &p.Name, &p.Role, &p.Color, &p.Shape, &p.Symbol)
	if errors.Is(err, sql.ErrNoRows) {
		return Person{}, ErrNotFound
	}
	return p, err
}

// UpdatePerson changes a member's name, colour and black-and-white marker.
// An empty colour or shape is stored as is (the family view then picks one by
// place). The role and the account stay as they are.
func (db *DB) UpdatePerson(householdID, personID, name, color, shape string) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return &FieldError{Field: "name", Msg: "person name required"}
	}
	return expectOne(db.sql.Exec(`UPDATE people SET name = ?, color = ?, shape = ?, updated_at = ?
		WHERE id = ? AND id IN (SELECT person_id FROM household_members WHERE household_id = ?)`,
		name, color, shape, now(), personID, householdID))
}

// SetPeopleOrder stores the order of the family view. ids must name every
// member of the household exactly once.
func (db *DB) SetPeopleOrder(householdID string, ids []string) error {
	tx, err := db.sql.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var have int
	if err := tx.QueryRow(`SELECT COUNT(*) FROM household_members WHERE household_id = ?`, householdID).Scan(&have); err != nil {
		return err
	}
	if len(ids) != have {
		return &FieldError{Field: "ids", Msg: "the order must list every person once"}
	}
	seen := map[string]bool{}
	for i, id := range ids {
		if seen[id] {
			return &FieldError{Field: "ids", Msg: "the order must list every person once"}
		}
		seen[id] = true
		res, err := tx.Exec(`UPDATE household_members SET sort_order = ? WHERE household_id = ? AND person_id = ?`, i+1, householdID, id)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return &FieldError{Field: "ids", Msg: "unknown person"}
		}
	}
	return tx.Commit()
}
