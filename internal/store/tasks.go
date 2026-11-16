package store

import (
	"database/sql"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/hejmo-home/hejmo/internal/task"
)

// DayTask is a task due on one day, with whether it is done that day.
type DayTask struct {
	task.Task
	Done     bool
	Subtasks []DaySubtask // in their order, with their state on that day
}

// AddTask stores a task. The person must belong to the household.
func (db *DB) AddTask(householdID string, t task.Task) (task.Task, error) {
	t.Title = strings.TrimSpace(t.Title)
	if t.RoutineID == "" {
		if err := t.Validate(); err != nil {
			return task.Task{}, fmt.Errorf("%w: %w", ErrInvalid, err) // keeps the *task.InvalidError and its field
		}
	}
	if _, err := db.Person(householdID, t.PersonID); errors.Is(err, ErrNotFound) {
		return task.Task{}, fmt.Errorf("%w: %w", ErrInvalid, &task.InvalidError{Field: "person", Msg: "unknown person"})
	} else if err != nil {
		return task.Task{}, err
	}
	if t.RoutineID != "" {
		return db.addRoutineTask(householdID, t)
	}
	t.ID = newID()
	ts := now()
	_, err := db.sql.Exec(`INSERT INTO tasks (id, household_id, person_id, title, emoji, rrule, start_date, part_of_day, stars, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		t.ID, householdID, t.PersonID, t.Title, t.Emoji, t.RRule(), t.Date, string(t.PartOfDay), t.Stars, ts, ts)
	return t, err
}

func (db *DB) addRoutineTask(householdID string, t task.Task) (task.Task, error) {
	bad := func(msg string) error {
		return fmt.Errorf("%w: %w", ErrInvalid, &task.InvalidError{Field: "routine", Msg: msg})
	}
	tx, err := db.sql.Begin()
	if err != nil {
		return task.Task{}, err
	}
	defer tx.Rollback()
	r, err := routineIn(tx, householdID, t.RoutineID)
	if errors.Is(err, ErrNotFound) {
		return task.Task{}, bad("unknown routine")
	} else if err != nil {
		return task.Task{}, err
	}
	if r.PersonID != t.PersonID {
		return task.Task{}, bad("a routine takes only tasks of its own person")
	}
	t.PartOfDay = r.Part
	if err := t.Validate(); err != nil {
		return task.Task{}, fmt.Errorf("%w: %w", ErrInvalid, err)
	}
	var last int
	if err := tx.QueryRow(`SELECT COALESCE(MAX(routine_pos), 0) FROM tasks WHERE routine_id = ?`, r.ID).Scan(&last); err != nil {
		return task.Task{}, err
	}
	t.ID, t.Position = newID(), last+1
	ts := now()
	if _, err := tx.Exec(`INSERT INTO tasks (id, household_id, person_id, title, emoji, rrule, start_date, part_of_day, stars, routine_id, routine_pos, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		t.ID, householdID, t.PersonID, t.Title, t.Emoji, t.RRule(), t.Date, string(storedPart(r.Part)), t.Stars, r.ID, t.Position, ts, ts); err != nil {
		return task.Task{}, err
	}
	return t, tx.Commit()
}

// storedPart is the part of the day a task keeps for itself: after school
// exists only in routines, so a task of such a routine is stored for any time.
func storedPart(p task.PartOfDay) task.PartOfDay {
	if p == task.AfterSchool {
		return task.AnyTime
	}
	return p
}

// taskFrom reads a task with the part of the day of its routine, if it has one.
const taskFrom = `SELECT t.id, t.person_id, t.title, t.emoji, t.rrule, t.start_date, COALESCE(r.part, t.part_of_day), t.stars, COALESCE(t.routine_id, ''), t.routine_pos
	FROM tasks t LEFT JOIN routines r ON r.id = t.routine_id`

func scanTask(row interface{ Scan(...any) error }) (task.Task, error) {
	var t task.Task
	var rule, part string
	if err := row.Scan(&t.ID, &t.PersonID, &t.Title, &t.Emoji, &rule, &t.Date, &part, &t.Stars, &t.RoutineID, &t.Position); err != nil {
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
	rows, err := db.sql.Query(taskFrom+` WHERE t.household_id = ? ORDER BY t.created_at, t.rowid`, householdID)
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
	if err := rows.Err(); err != nil {
		return nil, err
	}
	rankRoutineTasks(out)
	return out, nil
}

// rankRoutineTasks turns the stored sort keys of routine tasks into their
// places in the routine: 1, 2, 3 without gaps, whatever was deleted or moved.
func rankRoutineTasks(list []task.Task) {
	byRoutine := map[string][]int{}
	for i, t := range list {
		if t.RoutineID != "" {
			byRoutine[t.RoutineID] = append(byRoutine[t.RoutineID], i)
		}
	}
	for _, idx := range byRoutine {
		sort.SliceStable(idx, func(a, b int) bool { return list[idx[a]].Position < list[idx[b]].Position })
		for place, i := range idx {
			list[i].Position = place + 1
		}
	}
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
	t, err := scanTask(db.sql.QueryRow(taskFrom+` WHERE t.household_id = ? AND t.id = ?`, householdID, id))
	if errors.Is(err, sql.ErrNoRows) {
		return task.Task{}, ErrNotFound
	}
	if err != nil || t.RoutineID == "" {
		return t, err
	}
	// The place in the routine counts the tasks before it.
	list, err := db.RoutineTasks(householdID, t.RoutineID)
	if err != nil {
		return task.Task{}, err
	}
	for _, k := range list {
		if k.ID == t.ID {
			return k, nil
		}
	}
	return t, nil
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
// credited (ADR 0018). Ticking the task ticks all its subtasks for that day,
// unticking it unticks all of them (only if it was done; an open task keeps its single subtask ticks). by is the person who ticked, empty for a
// display.
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
	changed, _, err := setTaskDoneTx(tx, t, day, done, by, at)
	if err != nil {
		return err
	}
	if done {
		_, err = tx.Exec(`INSERT INTO subtask_done (subtask_id, day, completed_at, completed_by)
			SELECT id, ?, ?, NULLIF(?, '') FROM subtasks WHERE task_id = ?
			ON CONFLICT (subtask_id, day) DO NOTHING`, day, at.UTC().Format(time.RFC3339), by, id)
	} else if changed { // unticking an open task leaves single subtask ticks alone
		_, err = tx.Exec(`DELETE FROM subtask_done WHERE day = ? AND subtask_id IN (SELECT id FROM subtasks WHERE task_id = ?)`, day, id)
	}
	if err != nil {
		return err
	}
	return tx.Commit()
}

// setTaskDoneTx ticks a task for a day inside a transaction and books its
// stars. It reports whether the task changed state and how many stars were
// credited or deducted.
func setTaskDoneTx(tx *sql.Tx, t task.Task, day string, done bool, by string, at time.Time) (changed bool, stars int, err error) {
	var delta int
	if done {
		res, err := tx.Exec(`INSERT INTO task_done (task_id, day, completed_at, completed_by, stars) VALUES (?, ?, ?, NULLIF(?, ''), ?)
			ON CONFLICT (task_id, day) DO NOTHING`, t.ID, day, at.UTC().Format(time.RFC3339), by, t.Stars)
		if err != nil {
			return false, 0, err
		}
		if n, _ := res.RowsAffected(); n > 0 {
			changed, delta = true, t.Stars
		}
	} else {
		var credited int
		switch err := tx.QueryRow(`SELECT stars FROM task_done WHERE task_id = ? AND day = ?`, t.ID, day).Scan(&credited); {
		case errors.Is(err, sql.ErrNoRows):
		case err != nil:
			return false, 0, err
		default:
			if _, err := tx.Exec(`DELETE FROM task_done WHERE task_id = ? AND day = ?`, t.ID, day); err != nil {
				return false, 0, err
			}
			changed, delta = true, -credited
		}
	}
	if delta != 0 {
		if _, err := tx.Exec(`INSERT INTO star_log (id, person_id, delta, task_id, task_title, day, booked_by, created_at)
			VALUES (?, ?, ?, ?, ?, ?, NULLIF(?, ''), ?)`,
			newID(), t.PersonID, delta, t.ID, t.Title, day, by, at.UTC().Format(time.RFC3339)); err != nil {
			return false, 0, err
		}
	}
	if delta < 0 {
		delta = -delta
	}
	return changed, delta, nil
}

// SetTaskStars changes the star value of a task (0 to task.MaxStars). It
// counts from the next tick: task_done rows and the star log are not touched,
// so credited ticks keep their value and unticking deducts what was credited
// (ADR 0018).
func (db *DB) SetTaskStars(householdID, id string, stars int) error {
	if stars < 0 || stars > task.MaxStars {
		return fmt.Errorf("%w: %w", ErrInvalid, &task.InvalidError{Field: "stars", Msg: fmt.Sprintf("stars must be between 0 and %d", task.MaxStars)})
	}
	res, err := db.sql.Exec(`UPDATE tasks SET stars = ?, updated_at = ? WHERE household_id = ? AND id = ?`, stars, now(), householdID, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// TaskDoneStars returns the stars credited when a task was ticked off on a
// day, and whether it is ticked off that day.
func (db *DB) TaskDoneStars(id, day string) (stars int, done bool, err error) {
	err = db.sql.QueryRow(`SELECT stars FROM task_done WHERE task_id = ? AND day = ?`, id, day).Scan(&stars)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, false, nil
	}
	return stars, err == nil, err
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
	subs, err := db.SubtasksByTask(householdID, day)
	if err != nil {
		return nil, err
	}
	var out []DayTask
	for _, t := range all {
		if t.DueOn(day) {
			out = append(out, DayTask{Task: t, Done: done[t.ID], Subtasks: subs[t.ID]})
		}
	}
	return out, nil
}
