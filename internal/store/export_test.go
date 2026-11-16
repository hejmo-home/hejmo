package store

import "github.com/hejmo-home/hejmo/internal/task"

// InsertRoutineUnchecked writes a routine without the duplicate pre-check, as
// the second of two concurrent requests would.
func (db *DB) InsertRoutineUnchecked(householdID string, r task.Routine) error {
	r.ID = newID()
	return db.insertRoutine(householdID, r)
}
