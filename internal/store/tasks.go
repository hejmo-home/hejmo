package store

import (
	"database/sql"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/hejmo-home/hejmo/internal/reward"
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
	_, err := db.sql.Exec(`INSERT INTO tasks (id, household_id, person_id, title, emoji, notes, rrule, start_date, part_of_day, stars, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		t.ID, householdID, t.PersonID, t.Title, t.Emoji, t.Notes, t.RRule(), t.Date, string(t.PartOfDay), t.Stars, ts, ts)
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
	if _, err := tx.Exec(`INSERT INTO tasks (id, household_id, person_id, title, emoji, notes, rrule, start_date, part_of_day, stars, routine_id, routine_pos, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		t.ID, householdID, t.PersonID, t.Title, t.Emoji, t.Notes, t.RRule(), t.Date, string(storedPart(r.Part)), t.Stars, r.ID, t.Position, ts, ts); err != nil {
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
const taskFrom = `SELECT t.id, t.person_id, t.title, t.emoji, t.rrule, t.start_date, COALESCE(r.part, t.part_of_day), t.stars, COALESCE(t.routine_id, ''), t.routine_pos, t.notes, t.updated_at
	FROM tasks t LEFT JOIN routines r ON r.id = t.routine_id`

func scanTask(row interface{ Scan(...any) error }) (task.Task, error) {
	var t task.Task
	var rule, part, updated string
	if err := row.Scan(&t.ID, &t.PersonID, &t.Title, &t.Emoji, &rule, &t.Date, &part, &t.Stars, &t.RoutineID, &t.Position, &t.Notes, &updated); err != nil {
		return task.Task{}, err
	}
	t.UpdatedAt, _ = time.Parse(time.RFC3339, updated)
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
// unticking a done task unticks all of them (an open task keeps its single
// subtask ticks). by is the person who ticked, empty for a display.
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
		// Stars an earlier untick could not take back are held against this tick.
		var debt int
		if err := tx.QueryRow(`SELECT stars FROM task_star_debt WHERE task_id = ? AND day = ?`, t.ID, day).Scan(&debt); err != nil && !errors.Is(err, sql.ErrNoRows) {
			return false, 0, err
		}
		credit := max(0, t.Stars-debt)
		// task_done keeps the full value so every untick tries to take all of
		// it back and records the shortfall again; only the star log is
		// reduced by the debt paid.
		res, err := tx.Exec(`INSERT INTO task_done (task_id, day, completed_at, completed_by, stars) VALUES (?, ?, ?, NULLIF(?, ''), ?)
			ON CONFLICT (task_id, day) DO NOTHING`, t.ID, day, at.UTC().Format(time.RFC3339), by, t.Stars)
		if err != nil {
			return false, 0, err
		}
		if n, _ := res.RowsAffected(); n > 0 {
			changed, delta = true, credit
			if debt > 0 {
				if _, err := tx.Exec(`DELETE FROM task_star_debt WHERE task_id = ? AND day = ?`, t.ID, day); err != nil {
					return false, 0, err
				}
				if rest := debt - (t.Stars - credit); rest > 0 {
					if _, err := tx.Exec(`INSERT INTO task_star_debt (task_id, day, stars) VALUES (?, ?, ?)`, t.ID, day, rest); err != nil {
						return false, 0, err
					}
				}
			}
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
			bal, err := balanceOf(tx, t.PersonID)
			if err != nil {
				return false, 0, err
			}
			var below bool
			if err := tx.QueryRow(`SELECT h.below_zero FROM households h JOIN tasks k ON k.household_id = h.id WHERE k.id = ?`, t.ID).Scan(&below); err != nil {
				return false, 0, err
			}
			taken := reward.Deduction(credited, bal, below)
			changed, delta = true, -taken
			if short := credited - taken; short > 0 { // not taken back: held against the next tick
				if _, err := tx.Exec(`INSERT INTO task_star_debt (task_id, day, stars) VALUES (?, ?, ?)
					ON CONFLICT (task_id, day) DO UPDATE SET stars = stars + excluded.stars`, t.ID, day, short); err != nil {
					return false, 0, err
				}
			}
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
// so ticks keep the value they had and unticking tries to deduct that full
// value (ADR 0018, ADR 0021 #292).
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

// TaskDoneStars returns the full star value the tick of a task carries on a
// day, and whether it is ticked off that day. It is what reopening tries to
// deduct, not necessarily what was booked: a re-tick that only paid off debt
// (task_star_debt) books fewer stars, or none.
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

// TaskEdit is a change to a task's own text and day; nil fields stay as they are.
// Date only applies to a once task.
type TaskEdit struct{ Title, Notes, Date *string }

// EditTask changes a task's title, notes and, for a once task, its day. Done
// days and credited stars stay: a done once task moved to another day stays
// done on the new day.
func (db *DB) EditTask(householdID, id string, e TaskEdit) error {
	t, err := db.Task(householdID, id)
	if err != nil {
		return err
	}
	oldDay := t.Date
	before := t
	if e.Title != nil {
		t.Title = strings.TrimSpace(*e.Title)
	}
	if e.Notes != nil {
		t.Notes = strings.TrimSpace(*e.Notes)
	}
	if e.Date != nil {
		if t.Repeat != task.Once {
			return fmt.Errorf("%w: %w", ErrInvalid, &task.InvalidError{Field: "date", Msg: "only the day of a once task can change"})
		}
		t.Date = *e.Date
	}
	if err := t.Validate(); err != nil {
		return fmt.Errorf("%w: %w", ErrInvalid, err)
	}
	if t.Title == before.Title && t.Notes == before.Notes && t.Date == before.Date {
		return nil // a no-op re-save must not change the ETag clients sync on
	}
	tx, err := db.sql.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`UPDATE tasks SET title = ?, notes = ?, start_date = ?, updated_at = ? WHERE household_id = ? AND id = ?`,
		t.Title, t.Notes, t.Date, now(), householdID, id); err != nil {
		return err
	}
	if t.Date != oldDay {
		// A once task is due on its day only, so its done day moves with it.
		if _, err := tx.Exec(`UPDATE task_done SET day = ? WHERE task_id = ? AND day = ?`, t.Date, id, oldDay); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// TaskCompletedAt says when a task was ticked off on a day, and whether it is.
func (db *DB) TaskCompletedAt(id, day string) (time.Time, bool, error) {
	var at string
	err := db.sql.QueryRow(`SELECT completed_at FROM task_done WHERE task_id = ? AND day = ?`, id, day).Scan(&at)
	if errors.Is(err, sql.ErrNoRows) {
		return time.Time{}, false, nil
	}
	if err != nil {
		return time.Time{}, false, err
	}
	t, err := time.Parse(time.RFC3339, at)
	return t, err == nil, err
}
