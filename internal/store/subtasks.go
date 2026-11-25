package store

import (
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/hejmo-home/hejmo/internal/badge"
	"github.com/hejmo-home/hejmo/internal/task"
)

// DaySubtask is a subtask with whether it is ticked off on one day.
type DaySubtask struct {
	task.Subtask
	Done bool
}

// SubtaskChange says what a change of a subtask did to its task, so a page can
// name the consequence: the task became done (TaskDone) or open again, and how
// many stars were credited or deducted.
type SubtaskChange struct {
	TaskChanged bool
	TaskDone    bool
	Stars       int
	// Badges are the badges the change awarded the task's person (ticket 295).
	Badges []badge.Key
}

func invalidSubtask(field, msg string) error {
	return fmt.Errorf("%w: %w", ErrInvalid, &task.InvalidError{Field: field, Msg: msg})
}

// Subtasks lists the subtasks of a task in their order, with their state on a day.
func (db *DB) Subtasks(householdID, taskID, day string) ([]DaySubtask, error) {
	if _, err := db.Task(householdID, taskID); err != nil {
		return nil, err
	}
	by, err := db.SubtasksByTask(householdID, day)
	return by[taskID], err
}

// SubtasksByTask lists the subtasks of all tasks of a household, per task in
// their order, with their state on a day: what the lists need, in one query.
func (db *DB) SubtasksByTask(householdID, day string) (map[string][]DaySubtask, error) {
	rows, err := db.sql.Query(`SELECT s.task_id, s.id, s.title, s.emoji, s.pos,
			EXISTS (SELECT 1 FROM subtask_done d WHERE d.subtask_id = s.id AND d.day = ?)
		FROM subtasks s JOIN tasks t ON t.id = s.task_id
		WHERE t.household_id = ? ORDER BY s.task_id, s.pos, s.rowid`, day, householdID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string][]DaySubtask{}
	for rows.Next() {
		var s DaySubtask
		if err := rows.Scan(&s.TaskID, &s.ID, &s.Title, &s.Emoji, &s.Position, &s.Done); err != nil {
			return nil, err
		}
		s.Position = len(out[s.TaskID]) + 1 // places without gaps, whatever was deleted or moved
		out[s.TaskID] = append(out[s.TaskID], s)
	}
	return out, rows.Err()
}

// AddSubtask adds a subtask at the end of a task (at most task.MaxSubtasks).
// If the task is ticked off on day, the new subtask counts as done that day, so
// the child loses no stars (ADR 0018, product owner 15/11/2026).
func (db *DB) AddSubtask(householdID, taskID string, s task.Subtask, day string, at time.Time) (task.Subtask, error) {
	s.Title, s.Emoji = strings.TrimSpace(s.Title), strings.TrimSpace(s.Emoji)
	if err := s.Validate(); err != nil {
		return task.Subtask{}, fmt.Errorf("%w: %w", ErrInvalid, err)
	}
	if _, err := db.Task(householdID, taskID); err != nil {
		return task.Subtask{}, err
	}
	tx, err := db.sql.Begin()
	if err != nil {
		return task.Subtask{}, err
	}
	defer tx.Rollback()
	var n, last int
	if err := tx.QueryRow(`SELECT COUNT(*), COALESCE(MAX(pos), 0) FROM subtasks WHERE task_id = ?`, taskID).Scan(&n, &last); err != nil {
		return task.Subtask{}, err
	}
	if n >= task.MaxSubtasks {
		return task.Subtask{}, invalidSubtask("subtasks", fmt.Sprintf("a task has at most %d subtasks", task.MaxSubtasks))
	}
	s.ID, s.TaskID, s.Position = newID(), taskID, n+1
	ts := now()
	if _, err := tx.Exec(`INSERT INTO subtasks (id, task_id, title, emoji, pos, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?)`,
		s.ID, taskID, s.Title, s.Emoji, last+1, ts, ts); err != nil {
		return task.Subtask{}, err
	}
	if taskDoneOn(tx, taskID, day) {
		if _, err := tx.Exec(`INSERT INTO subtask_done (subtask_id, day, completed_at) VALUES (?, ?, ?)`, s.ID, day, at.UTC().Format(time.RFC3339)); err != nil {
			return task.Subtask{}, err
		}
	}
	return s, tx.Commit()
}

func taskDoneOn(tx *sql.Tx, taskID, day string) bool {
	var n int
	_ = tx.QueryRow(`SELECT COUNT(*) FROM task_done WHERE task_id = ? AND day = ?`, taskID, day).Scan(&n)
	return n > 0
}

// openSubtasks counts the subtasks of a task that are not ticked off on a day.
func openSubtasks(tx *sql.Tx, taskID, day string) (open, total int, err error) {
	err = tx.QueryRow(`SELECT COUNT(*), COALESCE(SUM(NOT EXISTS (SELECT 1 FROM subtask_done d WHERE d.subtask_id = s.id AND d.day = ?)), 0)
		FROM subtasks s WHERE s.task_id = ?`, day, taskID).Scan(&total, &open)
	return open, total, err
}

func (db *DB) subtaskOf(householdID, taskID, id string) (task.Task, error) {
	t, err := db.Task(householdID, taskID)
	if err != nil {
		return task.Task{}, err
	}
	var n int
	if err := db.sql.QueryRow(`SELECT COUNT(*) FROM subtasks WHERE id = ? AND task_id = ?`, id, taskID).Scan(&n); err != nil {
		return task.Task{}, err
	}
	if n == 0 {
		return task.Task{}, ErrNotFound
	}
	return t, nil
}

// UpdateSubtask changes the title and emoji of a subtask.
func (db *DB) UpdateSubtask(householdID, taskID, id, title, emoji string) error {
	s := task.Subtask{Title: strings.TrimSpace(title), Emoji: strings.TrimSpace(emoji)}
	if err := s.Validate(); err != nil {
		return fmt.Errorf("%w: %w", ErrInvalid, err)
	}
	if _, err := db.subtaskOf(householdID, taskID, id); err != nil {
		return err
	}
	_, err := db.sql.Exec(`UPDATE subtasks SET title = ?, emoji = ?, updated_at = ? WHERE id = ? AND task_id = ?`, s.Title, s.Emoji, now(), id, taskID)
	return err
}

// MoveSubtask moves a subtask one place down (or up) within its task.
func (db *DB) MoveSubtask(householdID, taskID, id string, down bool) error {
	if _, err := db.subtaskOf(householdID, taskID, id); err != nil {
		return err
	}
	tx, err := db.sql.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	rows, err := tx.Query(`SELECT id FROM subtasks WHERE task_id = ? ORDER BY pos, rowid`, taskID)
	if err != nil {
		return err
	}
	var ids []string
	at := -1
	for rows.Next() {
		var sid string
		if err := rows.Scan(&sid); err != nil {
			rows.Close()
			return err
		}
		if sid == id {
			at = len(ids)
		}
		ids = append(ids, sid)
	}
	rows.Close()
	to := at - 1
	if down {
		to = at + 1
	}
	if to < 0 || to >= len(ids) {
		return nil // already first or last
	}
	ids[at], ids[to] = ids[to], ids[at]
	for i, sid := range ids {
		if _, err := tx.Exec(`UPDATE subtasks SET pos = ?, updated_at = ? WHERE id = ?`, i+1, now(), sid); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// SetSubtaskDone ticks one subtask off (or open again) for a day. The task
// must be due that day. Ticking the last open subtask completes the task and
// credits its stars; unticking a subtask of a done task reopens the task and
// deducts them, the other subtasks stay ticked (ADR 0018). Doing either twice
// changes nothing.
func (db *DB) SetSubtaskDone(householdID, taskID, id, day string, done bool, by string, at time.Time) (SubtaskChange, error) {
	t, err := db.subtaskOf(householdID, taskID, id)
	if err != nil {
		return SubtaskChange{}, err
	}
	if !t.DueOn(day) {
		return SubtaskChange{}, fmt.Errorf("%w: task is not due on %s", ErrInvalid, day)
	}
	tx, err := db.sql.Begin()
	if err != nil {
		return SubtaskChange{}, err
	}
	defer tx.Rollback()
	if done {
		_, err = tx.Exec(`INSERT INTO subtask_done (subtask_id, day, completed_at, completed_by) VALUES (?, ?, ?, NULLIF(?, ''))
			ON CONFLICT (subtask_id, day) DO NOTHING`, id, day, at.UTC().Format(time.RFC3339), by)
	} else {
		_, err = tx.Exec(`DELETE FROM subtask_done WHERE subtask_id = ? AND day = ?`, id, day)
	}
	if err != nil {
		return SubtaskChange{}, err
	}
	ch, err := settleTask(tx, t, day, by, at)
	if err != nil {
		return SubtaskChange{}, err
	}
	if err := tx.Commit(); err != nil {
		return SubtaskChange{}, err
	}
	return db.withBadges(householdID, t, at, ch), nil
}

// withBadges awards the task's person the badges a change that completed the
// task earned, and notes them in the change.
func (db *DB) withBadges(householdID string, t task.Task, at time.Time, ch SubtaskChange) SubtaskChange {
	if ch.TaskChanged && ch.TaskDone {
		ch.Badges = db.awardAfter(householdID, t.PersonID, at)
	}
	return ch
}

// settleTask makes the task follow its subtasks after one changed: all ticked
// completes it, one open reopens a done task.
func settleTask(tx *sql.Tx, t task.Task, day, by string, at time.Time) (SubtaskChange, error) {
	open, total, err := openSubtasks(tx, t.ID, day)
	if err != nil {
		return SubtaskChange{}, err
	}
	taskDone := taskDoneOn(tx, t.ID, day)
	switch {
	case total > 0 && open == 0 && !taskDone:
		changed, stars, err := setTaskDoneTx(tx, t, day, true, by, at)
		return SubtaskChange{TaskChanged: changed, TaskDone: true, Stars: stars}, err
	case open > 0 && taskDone:
		changed, stars, err := setTaskDoneTx(tx, t, day, false, by, at)
		return SubtaskChange{TaskChanged: changed, TaskDone: false, Stars: stars}, err
	}
	return SubtaskChange{}, nil
}

// DeleteSubtask removes a subtask. Removing the last open one completes the
// task (all that are left are ticked) and credits its stars; removing the only
// subtask leaves a plain task as it was.
func (db *DB) DeleteSubtask(householdID, taskID, id, day, by string, at time.Time) (SubtaskChange, error) {
	t, err := db.subtaskOf(householdID, taskID, id)
	if err != nil {
		return SubtaskChange{}, err
	}
	tx, err := db.sql.Begin()
	if err != nil {
		return SubtaskChange{}, err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`DELETE FROM subtasks WHERE id = ? AND task_id = ?`, id, taskID); err != nil {
		return SubtaskChange{}, err
	}
	var ch SubtaskChange
	if t.DueOn(day) {
		if open, total, err := openSubtasks(tx, taskID, day); err != nil {
			return SubtaskChange{}, err
		} else if total > 0 && open == 0 {
			if ch, err = settleTask(tx, t, day, by, at); err != nil {
				return SubtaskChange{}, err
			}
		}
	}
	if err := tx.Commit(); err != nil {
		return SubtaskChange{}, err
	}
	return db.withBadges(householdID, t, at, ch), nil
}
