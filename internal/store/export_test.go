package store

import (
	"time"

	"github.com/hejmo-home/hejmo/internal/task"
)

// InsertRoutineUnchecked writes a routine without the duplicate pre-check, as
// the second of two concurrent requests would.
func (db *DB) InsertRoutineUnchecked(householdID string, r task.Routine) error {
	r.ID = newID()
	return db.insertRoutine(householdID, r)
}

// ExecUnchecked runs a statement as another part of the program, or a person
// with a database tool, would: for removing a person, which has no page yet.
func (db *DB) ExecUnchecked(query string, args ...any) error {
	_, err := db.sql.Exec(query, args...)
	return err
}

// InsertRedemption writes a redemption as ticket 292 will: a reward, a person,
// a status and the time it was decided.
func (db *DB) InsertRedemption(rewardID, personID, status string, at time.Time) error {
	ts := at.UTC().Format(time.RFC3339)
	_, err := db.sql.Exec(`INSERT INTO reward_redemptions (id, reward_id, person_id, status, price, requested_at, decided_at) VALUES (?, ?, ?, ?, 5, ?, ?)`,
		newID(), rewardID, personID, status, ts, ts)
	return err
}
