package store

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/hejmo-home/hejmo/internal/task"
)

// RoutineExistsError says that the person already has a routine for the part
// of the day, and which one. It is ErrConflict.
type RoutineExistsError struct{ Existing task.Routine }

func (e *RoutineExistsError) Error() string {
	return fmt.Sprintf("%v: the person already has the routine %q for this part of the day", ErrConflict, e.Existing.Name)
}
func (e *RoutineExistsError) Unwrap() error { return ErrConflict }

const routineColumns = `id, person_id, name, part`

// querier is what a transaction and the database share.
type querier interface {
	QueryRow(query string, args ...any) *sql.Row
	Exec(query string, args ...any) (sql.Result, error)
}

func scanRoutine(row interface{ Scan(...any) error }) (task.Routine, error) {
	var r task.Routine
	var part string
	if err := row.Scan(&r.ID, &r.PersonID, &r.Name, &part); err != nil {
		return task.Routine{}, err
	}
	r.Part = task.PartOfDay(part)
	return r, nil
}

func routineIn(q querier, householdID, id string) (task.Routine, error) {
	r, err := scanRoutine(q.QueryRow(`SELECT `+routineColumns+` FROM routines WHERE household_id = ? AND id = ?`, householdID, id))
	if errors.Is(err, sql.ErrNoRows) {
		return task.Routine{}, ErrNotFound
	}
	return r, err
}

// routineFor finds the routine of a person for a part of the day, if any.
func routineFor(q querier, personID string, part task.PartOfDay) (task.Routine, bool, error) {
	r, err := scanRoutine(q.QueryRow(`SELECT `+routineColumns+` FROM routines WHERE person_id = ? AND part = ?`, personID, string(part)))
	if errors.Is(err, sql.ErrNoRows) {
		return task.Routine{}, false, nil
	}
	return r, err == nil, err
}

// CreateRoutine stores a routine. The person must belong to the household and
// must not have a routine for that part of the day yet (RoutineExistsError).
func (db *DB) CreateRoutine(householdID string, r task.Routine) (task.Routine, error) {
	r.Name = strings.TrimSpace(r.Name)
	if err := r.Validate(); err != nil {
		return task.Routine{}, fmt.Errorf("%w: %w", ErrInvalid, err)
	}
	if _, err := db.Person(householdID, r.PersonID); errors.Is(err, ErrNotFound) {
		return task.Routine{}, fmt.Errorf("%w: %w", ErrInvalid, &task.InvalidError{Field: "person", Msg: "unknown person"})
	} else if err != nil {
		return task.Routine{}, err
	}
	if existing, found, err := routineFor(db.sql, r.PersonID, r.Part); err != nil {
		return task.Routine{}, err
	} else if found {
		return task.Routine{}, &RoutineExistsError{Existing: existing}
	}
	r.ID = newID()
	ts := now()
	_, err := db.sql.Exec(`INSERT INTO routines (id, household_id, person_id, name, part, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?)`,
		r.ID, householdID, r.PersonID, r.Name, string(r.Part), ts, ts)
	return r, err
}

// Routines lists the household's routines, oldest first.
func (db *DB) Routines(householdID string) ([]task.Routine, error) {
	rows, err := db.sql.Query(`SELECT `+routineColumns+` FROM routines WHERE household_id = ? ORDER BY created_at, rowid`, householdID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []task.Routine
	for rows.Next() {
		r, err := scanRoutine(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// Routine loads one routine.
func (db *DB) Routine(householdID, id string) (task.Routine, error) {
	return routineIn(db.sql, householdID, id)
}

// RoutineTasks lists the tasks of a routine in their order, numbered from 1.
func (db *DB) RoutineTasks(householdID, routineID string) ([]task.Task, error) {
	rows, err := db.sql.Query(taskFrom+` WHERE t.household_id = ? AND t.routine_id = ? ORDER BY t.routine_pos, t.created_at, t.rowid`, householdID, routineID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []task.Task
	for rows.Next() {
		t, err := scanTask(rows)
		if err != nil {
			return nil, err
		}
		t.Position = len(out) + 1
		out = append(out, t)
	}
	return out, rows.Err()
}

// UpdateRoutine changes a routine's name and part of the day. Its tasks take
// the new part with it.
func (db *DB) UpdateRoutine(householdID, id, name string, part task.PartOfDay) error {
	cur, err := db.Routine(householdID, id)
	if err != nil {
		return err
	}
	next := cur
	next.Name, next.Part = strings.TrimSpace(name), part
	if err := next.Validate(); err != nil {
		return fmt.Errorf("%w: %w", ErrInvalid, err)
	}
	if existing, found, err := routineFor(db.sql, cur.PersonID, part); err != nil {
		return err
	} else if found && existing.ID != id {
		return &RoutineExistsError{Existing: existing}
	}
	_, err = db.sql.Exec(`UPDATE routines SET name = ?, part = ?, updated_at = ? WHERE household_id = ? AND id = ?`, next.Name, string(part), now(), householdID, id)
	return err
}

// DissolveRoutine removes a routine and nothing else: its tasks stay as single
// tasks of the person, with their part of the day (after school becomes any
// time), their star value and their history.
func (db *DB) DissolveRoutine(householdID, id string) error {
	tx, err := db.sql.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	r, err := routineIn(tx, householdID, id)
	if err != nil {
		return err
	}
	if _, err := tx.Exec(`UPDATE tasks SET part_of_day = ?, routine_id = NULL, routine_pos = 0, updated_at = ? WHERE routine_id = ?`, string(storedPart(r.Part)), now(), id); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM routines WHERE id = ?`, id); err != nil {
		return err
	}
	return tx.Commit()
}

// AddTaskToRoutine takes a person's single task into their routine, at the end.
// The task takes the routine's part of the day. A task of another person is
// ErrInvalid, a task that is in a routine already ErrConflict.
func (db *DB) AddTaskToRoutine(householdID, routineID, taskID string) error {
	tx, err := db.sql.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	r, err := routineIn(tx, householdID, routineID)
	if err != nil {
		return err
	}
	var person string
	var current sql.NullString
	switch err := tx.QueryRow(`SELECT person_id, routine_id FROM tasks WHERE household_id = ? AND id = ?`, householdID, taskID).Scan(&person, &current); {
	case errors.Is(err, sql.ErrNoRows):
		return ErrNotFound
	case err != nil:
		return err
	}
	if person != r.PersonID {
		return fmt.Errorf("%w: %w", ErrInvalid, &task.InvalidError{Field: "task", Msg: "a routine takes only tasks of its own person"})
	}
	if current.Valid && current.String != "" {
		return fmt.Errorf("%w: the task belongs to a routine already", ErrConflict)
	}
	var last int
	if err := tx.QueryRow(`SELECT COALESCE(MAX(routine_pos), 0) FROM tasks WHERE routine_id = ?`, routineID).Scan(&last); err != nil {
		return err
	}
	if _, err := tx.Exec(`UPDATE tasks SET routine_id = ?, routine_pos = ?, updated_at = ? WHERE id = ?`, routineID, last+1, now(), taskID); err != nil {
		return err
	}
	return tx.Commit()
}

// RemoveTaskFromRoutine makes a routine's task a single task again. It keeps
// the routine's part of the day (after school becomes any time). A task that is
// in no routine is ErrInvalid.
func (db *DB) RemoveTaskFromRoutine(householdID, taskID string) error {
	tx, err := db.sql.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var part sql.NullString
	switch err := tx.QueryRow(`SELECT r.part FROM tasks t LEFT JOIN routines r ON r.id = t.routine_id WHERE t.household_id = ? AND t.id = ?`, householdID, taskID).Scan(&part); {
	case errors.Is(err, sql.ErrNoRows):
		return ErrNotFound
	case err != nil:
		return err
	}
	if !part.Valid {
		return fmt.Errorf("%w: %w", ErrInvalid, &task.InvalidError{Field: "task", Msg: "the task is in no routine"})
	}
	if _, err := tx.Exec(`UPDATE tasks SET part_of_day = ?, routine_id = NULL, routine_pos = 0, updated_at = ? WHERE id = ?`,
		string(storedPart(task.PartOfDay(part.String))), now(), taskID); err != nil {
		return err
	}
	return tx.Commit()
}

// SetRoutineOrder puts a routine's tasks in the given order. The ids must be
// exactly the routine's tasks, each once; anything else is ErrInvalid and
// changes nothing.
func (db *DB) SetRoutineOrder(householdID, routineID string, taskIDs []string) error {
	current, err := db.RoutineTasks(householdID, routineID)
	if err != nil {
		return err
	}
	if _, err := db.Routine(householdID, routineID); err != nil {
		return err
	}
	want := map[string]bool{}
	for _, k := range current {
		want[k.ID] = true
	}
	seen := map[string]bool{}
	for _, id := range taskIDs {
		if !want[id] || seen[id] {
			return fmt.Errorf("%w: %w", ErrInvalid, &task.InvalidError{Field: "tasks", Msg: "the order must name every task of the routine once"})
		}
		seen[id] = true
	}
	if len(taskIDs) != len(current) {
		return fmt.Errorf("%w: %w", ErrInvalid, &task.InvalidError{Field: "tasks", Msg: "the order must name every task of the routine once"})
	}
	tx, err := db.sql.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for i, id := range taskIDs {
		if _, err := tx.Exec(`UPDATE tasks SET routine_pos = ?, updated_at = ? WHERE id = ? AND routine_id = ?`, i+1, now(), id, routineID); err != nil {
			return err
		}
	}
	return tx.Commit()
}
