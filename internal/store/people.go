package store

import (
	"crypto/rand"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
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

// UpdateHouseholdSettings stores the household's time format and temperature unit.
func (db *DB) UpdateHouseholdSettings(id string, f TimeFormat, u TemperatureUnit) error {
	if !f.Valid() || !u.Valid() {
		return errors.New("store: unknown time format or temperature unit")
	}
	return expectOne(db.sql.Exec(`UPDATE households SET time_format = ?, temperature_unit = ?, updated_at = ? WHERE id = ?`, f, u, now(), id))
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
	h.TimeFormat, h.TemperatureUnit = Time24h, Celsius
	h.ID = newID()
	ts := now()
	_, err := db.sql.Exec(`INSERT INTO households (id, name, home_name, latitude, longitude, region, timezone, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		h.ID, h.Name, h.HomeName, h.Latitude, h.Longitude, h.Region, h.Timezone, ts, ts)
	return h, err
}

// Household loads a household by id.
func (db *DB) Household(id string) (Household, error) {
	return db.scanHousehold(db.sql.QueryRow(`SELECT id, name, home_name, COALESCE(latitude, 0), COALESCE(longitude, 0), region, timezone, time_format, temperature_unit
		FROM households WHERE id = ?`, id))
}

// HomeHousehold returns the household this box serves (V1: exactly one).
func (db *DB) HomeHousehold() (Household, error) {
	return db.scanHousehold(db.sql.QueryRow(`SELECT id, name, home_name, COALESCE(latitude, 0), COALESCE(longitude, 0), region, timezone, time_format, temperature_unit
		FROM households ORDER BY created_at, rowid LIMIT 1`))
}

func (db *DB) scanHousehold(row *sql.Row) (Household, error) {
	var h Household
	err := row.Scan(&h.ID, &h.Name, &h.HomeName, &h.Latitude, &h.Longitude, &h.Region, &h.Timezone, &h.TimeFormat, &h.TemperatureUnit)
	if errors.Is(err, sql.ErrNoRows) {
		return Household{}, ErrNotFound
	}
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
