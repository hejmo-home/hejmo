package store

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/hejmo-home/hejmo/internal/task"
)

// DayTask is a task due on one day, with whether it is done that day.
type DayTask struct {
	task.Task
	Done bool
}

// AddTask stores a task. The person must belong to the household.
func (db *DB) AddTask(householdID string, t task.Task) (task.Task, error) {
	t.Title = strings.TrimSpace(t.Title)
	if err := t.Validate(); err != nil {
		return task.Task{}, fmt.Errorf("%w: %w", ErrInvalid, err) // keeps the *task.InvalidError and its field
	}
	if _, err := db.Person(householdID, t.PersonID); errors.Is(err, ErrNotFound) {
		return task.Task{}, fmt.Errorf("%w: %w", ErrInvalid, &task.InvalidError{Field: "person", Msg: "unknown person"})
	} else if err != nil {
		return task.Task{}, err
	}
	t.ID = newID()
	ts := now()
	_, err := db.sql.Exec(`INSERT INTO tasks (id, household_id, person_id, title, emoji, rrule, start_date, part_of_day, stars, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		t.ID, householdID, t.PersonID, t.Title, t.Emoji, t.RRule(), t.Date, string(t.PartOfDay), t.Stars, ts, ts)
	return t, err
}

const taskColumns = `id, person_id, title, emoji, rrule, start_date, part_of_day, stars`

func scanTask(row interface{ Scan(...any) error }) (task.Task, error) {
	var t task.Task
	var rule, part string
	if err := row.Scan(&t.ID, &t.PersonID, &t.Title, &t.Emoji, &rule, &t.Date, &part, &t.Stars); err != nil {
		return task.Task{}, err
	}
	parsed, err := task.ParseRRule(rule)
	if err != nil {
		return task.Task{}, err
	}
	t.Repeat, t.Weekdays, t.PartOfDay = parsed.Repeat, parsed.Weekdays, task.PartOfDay(part)
	return t, nil
}

// Tasks lists all tasks of a household, oldest first.
func (db *DB) Tasks(householdID string) ([]task.Task, error) {
	rows, err := db.sql.Query(`SELECT `+taskColumns+` FROM tasks WHERE household_id = ? ORDER BY created_at, rowid`, householdID)
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
		out = append(out, t)
	}
	return out, rows.Err()
}

// CountTasks says how many tasks the household has and how many belong to each
// person, without loading them.
func (db *DB) CountTasks(householdID string) (int, map[string]int, error) {
	rows, err := db.sql.Query(`SELECT person_id, COUNT(*) FROM tasks WHERE household_id = ? GROUP BY person_id`, householdID)
	if err != nil {
		return 0, nil, err
	}
	defer rows.Close()
	total, by := 0, map[string]int{}
	for rows.Next() {
		var id string
		var n int
		if err := rows.Scan(&id, &n); err != nil {
			return 0, nil, err
		}
		total += n
		by[id] = n
	}
	return total, by, rows.Err()
}

// Task loads one task.
func (db *DB) Task(householdID, id string) (task.Task, error) {
	t, err := scanTask(db.sql.QueryRow(`SELECT `+taskColumns+` FROM tasks WHERE household_id = ? AND id = ?`, householdID, id))
	if errors.Is(err, sql.ErrNoRows) {
		return task.Task{}, ErrNotFound
	}
	return t, err
}

// DeleteTask removes a task and its done days.
func (db *DB) DeleteTask(householdID, id string) error {
	res, err := db.sql.Exec(`DELETE FROM tasks WHERE household_id = ? AND id = ?`, householdID, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// SetTaskDone ticks a task off (or back open) for one day (YYYY-MM-DD, in the
// household's time zone). The task must be due that day. Doing either twice
// changes nothing. A change books the task's stars into the person's star log
// in the same transaction: ticking credits them, unticking deducts what was
// credited (ADR 0018). by is the person who ticked, empty for a display.
func (db *DB) SetTaskDone(householdID, id, day string, done bool, by string, at time.Time) error {
	t, err := db.Task(householdID, id)
	if err != nil {
		return err
	}
	if !t.DueOn(day) {
		return fmt.Errorf("%w: task is not due on %s", ErrInvalid, day)
	}
	tx, err := db.sql.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var delta int
	if done {
		res, err := tx.Exec(`INSERT INTO task_done (task_id, day, completed_at, completed_by, stars) VALUES (?, ?, ?, NULLIF(?, ''), ?)
			ON CONFLICT (task_id, day) DO NOTHING`, id, day, at.UTC().Format(time.RFC3339), by, t.Stars)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n > 0 {
			delta = t.Stars
		}
	} else {
		var credited int
		switch err := tx.QueryRow(`SELECT stars FROM task_done WHERE task_id = ? AND day = ?`, id, day).Scan(&credited); {
		case errors.Is(err, sql.ErrNoRows):
		case err != nil:
			return err
		default:
			if _, err := tx.Exec(`DELETE FROM task_done WHERE task_id = ? AND day = ?`, id, day); err != nil {
				return err
			}
			delta = -credited
		}
	}
	if delta != 0 {
		if _, err := tx.Exec(`INSERT INTO star_log (id, person_id, delta, task_id, task_title, day, booked_by, created_at)
			VALUES (?, ?, ?, ?, ?, ?, NULLIF(?, ''), ?)`,
			newID(), t.PersonID, delta, t.ID, t.Title, day, by, at.UTC().Format(time.RFC3339)); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// TaskDone reports whether a task is ticked off on a day.
func (db *DB) TaskDone(id, day string) (bool, error) {
	var n int
	err := db.sql.QueryRow(`SELECT COUNT(*) FROM task_done WHERE task_id = ? AND day = ?`, id, day).Scan(&n)
	return n > 0, err
}

// TasksDoneOn returns the ids of the household's tasks that are ticked off on a
// day, in one query.
func (db *DB) TasksDoneOn(householdID, day string) (map[string]bool, error) {
	rows, err := db.sql.Query(`SELECT d.task_id FROM task_done d JOIN tasks t ON t.id = d.task_id
		WHERE t.household_id = ? AND d.day = ?`, householdID, day)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]bool{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out[id] = true
	}
	return out, rows.Err()
}

// TasksForDay lists the tasks due on a day with their state: what a display
// needs for today's progress per person.
func (db *DB) TasksForDay(householdID, day string) ([]DayTask, error) {
	all, err := db.Tasks(householdID)
	if err != nil {
		return nil, err
	}
	done, err := db.TasksDoneOn(householdID, day)
	if err != nil {
		return nil, err
	}
	var out []DayTask
	for _, t := range all {
		if t.DueOn(day) {
			out = append(out, DayTask{t, done[t.ID]})
		}
	}
	return out, nil
}
