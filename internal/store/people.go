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
	rh "github.com/hejmo-home/hejmo/internal/religiousholiday"
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
	ID       string
	Name     string
	HomeName string
	// HomePlaceID is the GeoNames id of the place the home was picked from (0:
	// none, for coordinates set through the API); see internal/places.
	HomePlaceID int
	Latitude    float64
	Longitude   float64
	Region      string // ISO 3166-2, e.g. DE-BY
	Timezone    string
	// DateFormat, TimeFormat and TemperatureUnit are household settings (ADR 0016, ticket #147).
	DateFormat      DateFormat
	TimeFormat      TimeFormat
	TemperatureUnit TemperatureUnit
	// AutoUpdate is the auto-update mode and UpdateAck the last update event
	// parents dismissed (ADR 0018).
	AutoUpdate UpdateMode
	UpdateAck  string
	// MealCutoffs are the times from which a mealtime is no longer shown on
	// displays (ADR 0016).
	MealCutoffs meal.Cutoffs
	// MCPEnabled is whether the MCP server answers (ticket 7.7); off by default.
	MCPEnabled bool
	// NameDaysOn puts the name days of the contacts into the calendar Birthdays, after the list NameDayList
	// (ticket 318). Off by default; the list is kept while name days are off.
	NameDaysOn  bool
	NameDayList string
	// FirstStepsHidden is set when a parent hid the first steps card on the start page.
	FirstStepsHidden bool
	// BelowZero says unticking a task may take a star balance below zero (ticket 292); on by default.
	BelowZero bool
	// FirstStepsSkipped lists the steps of the card a parent skipped, in the
	// order of FirstStepIDs.
	FirstStepsSkipped []string
	// ReligiousHolidaysOn puts the holidays of the chosen Religions into the generated calendar Religious holidays
	// (ticket 339). Off by default; the religions are kept while it is off. IslamicCalendar and OrthodoxCalendar
	// are the two calendar choices. ReligiousLang is the language of the titles of the events.
	ReligiousHolidaysOn bool
	Religions           []rh.Religion
	IslamicCalendar     rh.IslamicCalendar
	OrthodoxCalendar    rh.OrthodoxCalendar
	ReligiousLang       string
}

// DateFormat is how the household wants dates in numbers written.
type DateFormat string

// Date formats; day first (24.12.2026) is the default, month first is 12/24/2026.
const (
	DateDMY DateFormat = "dmy"
	DateMDY DateFormat = "mdy"
)

// Valid reports whether f is a known date format.
func (f DateFormat) Valid() bool { return f == DateDMY || f == DateMDY }

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

// SetMCPEnabled switches the MCP server on or off (ticket 7.7). Tokens stay
// stored either way.
func (db *DB) SetMCPEnabled(id string, on bool) error {
	return expectOne(db.sql.Exec(`UPDATE households SET mcp_enabled = ?, updated_at = ? WHERE id = ?`, on, now(), id))
}

// SetFirstStepsHidden shows or hides the first steps card on the start page.
func (db *DB) SetFirstStepsHidden(id string, hidden bool) error {
	return expectOne(db.sql.Exec(`UPDATE households SET first_steps_hidden = ?, updated_at = ? WHERE id = ?`, hidden, now(), id))
}

// FirstStepIDs are the steps of the first steps card, in the order the card
// shows them (ticket 133).
var FirstStepIDs = []string{"family", "parents", "calendars", "tasks", "meals", "display"}

// UnknownStepError says which step id is not a first step. It matches
// ErrInvalid with errors.Is.
type UnknownStepError struct{ Step string }

func (e *UnknownStepError) Error() string {
	return fmt.Sprintf("%s: unknown first step %q", ErrInvalid, e.Step)
}
func (e *UnknownStepError) Unwrap() error { return ErrInvalid }

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
			return nil, &UnknownStepError{Step: s}
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

// HouseholdSettings are the settings a parent can change. The first steps
// state is optional: nil leaves what is stored alone, so a save of the other
// settings never undoes a skip or hide made at the same time.
type HouseholdSettings struct {
	DateFormat       DateFormat
	TimeFormat       TimeFormat
	TemperatureUnit  TemperatureUnit
	AutoUpdate       UpdateMode
	MealCutoffs      meal.Cutoffs
	FirstStepsHidden *bool
	// FirstStepsSkipped is a pointer to a slice on purpose: nil keeps the
	// stored list, a pointer to an empty list clears it.
	FirstStepsSkipped *[]string
	// Home is the home place; nil leaves the stored place and its
	// coordinates alone.
	Home *Home
}

// Home is a household's home: the place's name, its coordinates and the id of
// the place in the list on the box (0 when the coordinates came without one).
type Home struct {
	Name                string
	Latitude, Longitude float64
	PlaceID             int
}

func (h Home) validate() error {
	// Written so that NaN fails too.
	if !(h.Latitude >= -90 && h.Latitude <= 90 && h.Longitude >= -180 && h.Longitude <= 180) {
		return fmt.Errorf("%w: home coordinates out of range", ErrInvalid)
	}
	if h.Latitude == 0 && h.Longitude == 0 { // stored as "no location yet"
		return fmt.Errorf("%w: home coordinates (0, 0) mean no location", ErrInvalid)
	}
	if strings.TrimSpace(h.Name) == "" {
		return fmt.Errorf("%w: home name required", ErrInvalid)
	}
	return nil
}

// SaveHouseholdSettings stores the settings in one statement, so a save is all
// or nothing. Invalid values give ErrInvalid.
func (db *DB) SaveHouseholdSettings(id string, in HouseholdSettings) error {
	if !in.DateFormat.Valid() || !in.TimeFormat.Valid() || !in.TemperatureUnit.Valid() || !in.AutoUpdate.Valid() {
		return fmt.Errorf("%w: unknown date format, time format, temperature unit or update mode", ErrInvalid)
	}
	if err := in.MealCutoffs.Validate(); err != nil {
		return fmt.Errorf("%w: %w", ErrInvalid, err)
	}
	var skipped *string // NULL keeps the stored list
	if in.FirstStepsSkipped != nil {
		clean, err := cleanFirstSteps(*in.FirstStepsSkipped)
		if err != nil {
			return err
		}
		list := strings.Join(clean, ",")
		skipped = &list
	}
	var homeName *string // NULL keeps the stored home
	var lat, lon *float64
	var placeID any // NULL
	setPlace := 0   // 1: write home_place_id (also NULL), 0: keep it
	if in.Home != nil {
		if err := in.Home.validate(); err != nil {
			return err
		}
		homeName, lat, lon = &in.Home.Name, &in.Home.Latitude, &in.Home.Longitude
		setPlace = 1
		if in.Home.PlaceID != 0 {
			placeID = in.Home.PlaceID
		}
	}
	c := in.MealCutoffs
	tx, err := db.sql.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := expectOne(tx.Exec(`UPDATE households SET date_format = ?, time_format = ?, temperature_unit = ?, auto_update = ?,
		cutoff_breakfast = ?, cutoff_lunch = ?, cutoff_snack = ?, cutoff_dinner = ?,
		first_steps_hidden = COALESCE(?, first_steps_hidden), first_steps_skipped = COALESCE(?, first_steps_skipped),
		home_name = COALESCE(?, home_name), latitude = COALESCE(?, latitude), longitude = COALESCE(?, longitude),
		home_place_id = CASE WHEN ? = 1 THEN ? ELSE home_place_id END, updated_at = ? WHERE id = ?`,
		in.DateFormat, in.TimeFormat, in.TemperatureUnit, in.AutoUpdate, c.Breakfast, c.Lunch, c.Snack, c.Dinner, in.FirstStepsHidden, skipped, homeName, lat, lon, setPlace, placeID, now(), id)); err != nil {
		return err
	}
	if in.Home != nil { // Hindu days and the public holidays of the region depend on the place (ticket 339)
		if _, err := db.syncReligious(tx, id, ""); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// Person is a family member as seen from one household.
type Person struct {
	ID     string
	Name   string
	Role   Role
	Color  string
	Shape  string
	Symbol string
	// StreaksShown is the switch "week count and badges" (ticket 294): when
	// off, no week, best streak or badge is shown for the person anywhere.
	// Stars are not affected. It is set by role when the person is created
	// (child on, adult off) and can be changed per person.
	StreaksShown bool
	// NutritionShown is the switch "nutrition values at recipes" (ticket 288):
	// off until a parent switches it on. It decides for children; parents
	// always see the values.
	NutritionShown bool
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
	h.DateFormat, h.TimeFormat, h.TemperatureUnit, h.AutoUpdate, h.MealCutoffs = DateDMY, Time24h, Celsius, UpdateOn, meal.DefaultCutoffs()
	h.ID = newID()
	ts := now()
	var placeID any // NULL
	if h.HomePlaceID != 0 {
		placeID = h.HomePlaceID
	}
	tx, err := db.sql.Begin()
	if err != nil {
		return h, err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`INSERT INTO households (id, name, home_name, home_place_id, latitude, longitude, region, timezone, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		h.ID, h.Name, h.HomeName, placeID, h.Latitude, h.Longitude, h.Region, h.Timezone, ts, ts); err != nil {
		return h, err
	}
	if err := seedContactGroups(tx, h.ID, ts); err != nil { // the five groups of the address book (ticket 296)
		return h, err
	}
	return h, tx.Commit()
}

// Household loads a household by id.
func (db *DB) Household(id string) (Household, error) {
	return db.scanHousehold(db.sql.QueryRow(`SELECT id, name, home_name, COALESCE(home_place_id, 0), COALESCE(latitude, 0), COALESCE(longitude, 0), region, timezone, date_format, time_format, temperature_unit, auto_update, update_ack,
		cutoff_breakfast, cutoff_lunch, cutoff_snack, cutoff_dinner, first_steps_hidden, first_steps_skipped, mcp_enabled, below_zero, name_days_on, COALESCE(name_day_list, ''),
		religious_holidays_on, religious_holidays, islamic_calendar, orthodox_calendar, religious_holidays_lang
		FROM households WHERE id = ?`, id))
}

// HomeHousehold returns the household this box serves (V1: exactly one).
func (db *DB) HomeHousehold() (Household, error) {
	return db.scanHousehold(db.sql.QueryRow(`SELECT id, name, home_name, COALESCE(home_place_id, 0), COALESCE(latitude, 0), COALESCE(longitude, 0), region, timezone, date_format, time_format, temperature_unit, auto_update, update_ack,
		cutoff_breakfast, cutoff_lunch, cutoff_snack, cutoff_dinner, first_steps_hidden, first_steps_skipped, mcp_enabled, below_zero, name_days_on, COALESCE(name_day_list, ''),
		religious_holidays_on, religious_holidays, islamic_calendar, orthodox_calendar, religious_holidays_lang
		FROM households ORDER BY created_at, rowid LIMIT 1`))
}

func (db *DB) scanHousehold(row *sql.Row) (Household, error) {
	var h Household
	var skipped, religions string
	err := row.Scan(&h.ID, &h.Name, &h.HomeName, &h.HomePlaceID, &h.Latitude, &h.Longitude, &h.Region, &h.Timezone, &h.DateFormat, &h.TimeFormat, &h.TemperatureUnit, &h.AutoUpdate, &h.UpdateAck,
		&h.MealCutoffs.Breakfast, &h.MealCutoffs.Lunch, &h.MealCutoffs.Snack, &h.MealCutoffs.Dinner, &h.FirstStepsHidden, &skipped, &h.MCPEnabled, &h.BelowZero, &h.NameDaysOn, &h.NameDayList,
		&h.ReligiousHolidaysOn, &religions, &h.IslamicCalendar, &h.OrthodoxCalendar, &h.ReligiousLang)
	if errors.Is(err, sql.ErrNoRows) {
		return Household{}, ErrNotFound
	}
	h.FirstStepsSkipped = splitSteps(skipped)
	h.Religions = splitReligions(religions)
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
	p.StreaksShown = p.Role == RoleChild
	ts := now()
	tx, err := db.sql.Begin()
	if err != nil {
		return Person{}, err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`INSERT INTO people (id, name, color, shape, symbol, streaks_shown, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		p.ID, p.Name, p.Color, p.Shape, p.Symbol, p.Role == RoleChild, ts, ts); err != nil {
		return Person{}, err
	}
	if _, err := tx.Exec(`INSERT INTO household_members (household_id, person_id, role, sort_order)
		VALUES (?, ?, ?, (SELECT COALESCE(MAX(sort_order), 0) + 1 FROM household_members WHERE household_id = ?))`,
		householdID, p.ID, p.Role, householdID); err != nil {
		return Person{}, err
	}
	if err := addPersonContact(tx, householdID, p, ts); err != nil { // every person is a contact (ticket 296)
		return Person{}, err
	}
	return p, tx.Commit()
}

const personColumns = `p.id, p.name, m.role, p.color, p.shape, p.symbol, p.streaks_shown, p.nutrition_shown`

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
		if err := rows.Scan(&p.ID, &p.Name, &p.Role, &p.Color, &p.Shape, &p.Symbol, &p.StreaksShown, &p.NutritionShown); err != nil {
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
		Scan(&p.ID, &p.Name, &p.Role, &p.Color, &p.Shape, &p.Symbol, &p.StreaksShown, &p.NutritionShown)
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
	tx, err := db.sql.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	ts := now()
	if err := expectOne(tx.Exec(`UPDATE people SET name = ?, color = ?, shape = ?, updated_at = ?
		WHERE id = ? AND id IN (SELECT person_id FROM household_members WHERE household_id = ?)`,
		name, color, shape, ts, personID, householdID)); err != nil {
		return err
	}
	// The Hejmo name of the person's contact is the person's name (ticket 296).
	if _, err := tx.Exec(`UPDATE contacts SET hejmo_name = ?, updated_at = ? WHERE person_id = ? AND household_id = ?`, name, ts, personID, householdID); err != nil {
		return err
	}
	if err := syncBirthdays(tx, householdID, ""); err != nil { // the event carries the name; no calendar is made here, so no calendar name is needed
		return err
	}
	return tx.Commit()
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
