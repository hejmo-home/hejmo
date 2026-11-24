package store

import (
	"fmt"
	"strings"
	"time"

	"github.com/hejmo-home/hejmo/internal/task"
)

// StarEntry is one booking in a person's star log: a tick of a task (TaskID,
// TaskTitle and Day set) or a parent's manual booking (Note set).
type StarEntry struct {
	ID              string
	PersonID        string
	Delta           int
	Note            string
	TaskID          string // empty for a manual booking or once the task is deleted
	TaskTitle       string
	Day             string // YYYY-MM-DD of the ticked occurrence
	BookedBy        string // person id; empty when a display booked it
	BookedByName    string // current name; the last name once the booker is removed
	BookedByRemoved bool   // the booker has been removed
	// A confirmed redemption (ticket 292): what it was at the time, and whether
	// a parent redeemed it directly (else BookedBy confirmed a request).
	RedemptionID, RewardName, RewardSymbol string
	Direct                                 bool
	At                                     time.Time
}

// StarAccount is a person's balance and their latest log entries, newest first.
type StarAccount struct {
	PersonID string
	Balance  int
	Shown    bool // stars show only for people with a star task or a booking (ADR 0018)
	Entries  []StarEntry
}

// StarSummary is the balance of one person and whether stars show for them.
type StarSummary struct {
	Balance int
	Shown   bool
}

// BookStars books stars by hand into a person's log: negative amounts deduct
// (for example "redeemed: ice cream"). by is the parent who books.
func (db *DB) BookStars(householdID, personID string, delta int, note, by string, at time.Time) (StarEntry, error) {
	if err := task.ValidateBooking(delta, note); err != nil {
		return StarEntry{}, fmt.Errorf("%w: %s", ErrInvalid, err)
	}
	if _, err := db.Person(householdID, personID); err != nil {
		return StarEntry{}, err
	}
	e := StarEntry{ID: newID(), PersonID: personID, Delta: delta, Note: strings.TrimSpace(note), BookedBy: by, At: at.UTC()}
	_, err := db.sql.Exec(`INSERT INTO star_log (id, person_id, delta, note, booked_by, created_at) VALUES (?, ?, ?, ?, NULLIF(?, ''), ?)`,
		e.ID, personID, delta, e.Note, by, e.At.Format(time.RFC3339))
	if err == nil && delta > 0 {
		db.awardAfter(householdID, personID, at) // a booking of stars can earn a star badge (ticket 295)
	}
	return e, err
}

// StarAccount loads a person's balance and their latest limit log entries.
func (db *DB) StarAccount(householdID, personID string, limit int) (StarAccount, error) {
	if _, err := db.Person(householdID, personID); err != nil {
		return StarAccount{}, err
	}
	acc := StarAccount{PersonID: personID}
	err := db.sql.QueryRow(`SELECT COALESCE(SUM(delta), 0) FROM star_log WHERE person_id = ?`, personID).Scan(&acc.Balance)
	if err != nil {
		return StarAccount{}, err
	}
	sums, err := db.StarSummaries(householdID)
	if err != nil {
		return StarAccount{}, err
	}
	acc.Shown = sums[personID].Shown
	rows, err := db.sql.Query(`SELECT l.id, l.delta, l.note, COALESCE(l.task_id, ''), l.task_title, l.day, COALESCE(l.booked_by, ''), COALESCE(b.name, NULLIF(l.booked_by_name, ''), ''), l.booked_by IS NULL AND l.booked_by_name <> '', l.created_at,
		COALESCE(l.redemption_id, ''), COALESCE(d.name, ''), COALESCE(d.symbol, ''), COALESCE(d.direct, 0)
		FROM star_log l LEFT JOIN people b ON b.id = l.booked_by LEFT JOIN reward_redemptions d ON d.id = l.redemption_id
		WHERE l.person_id = ? ORDER BY l.created_at DESC, l.rowid DESC LIMIT ?`, personID, limit)
	if err != nil {
		return StarAccount{}, err
	}
	defer rows.Close()
	for rows.Next() {
		e := StarEntry{PersonID: personID}
		var at string
		if err := rows.Scan(&e.ID, &e.Delta, &e.Note, &e.TaskID, &e.TaskTitle, &e.Day, &e.BookedBy, &e.BookedByName, &e.BookedByRemoved, &at, &e.RedemptionID, &e.RewardName, &e.RewardSymbol, &e.Direct); err != nil {
			return StarAccount{}, err
		}
		if e.At, err = time.Parse(time.RFC3339, at); err != nil {
			return StarAccount{}, err
		}
		acc.Entries = append(acc.Entries, e)
	}
	return acc, rows.Err()
}

// StarSummaries gives every member of the household their balance and whether
// stars show for them: they have a task worth stars or a booking (ADR 0018).
func (db *DB) StarSummaries(householdID string) (map[string]StarSummary, error) {
	rows, err := db.sql.Query(`SELECT m.person_id,
			COALESCE((SELECT SUM(delta) FROM star_log WHERE person_id = m.person_id), 0),
			EXISTS (SELECT 1 FROM star_log WHERE person_id = m.person_id)
			OR EXISTS (SELECT 1 FROM tasks WHERE person_id = m.person_id AND household_id = m.household_id AND stars > 0)
		FROM household_members m WHERE m.household_id = ?`, householdID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]StarSummary{}
	for rows.Next() {
		var id string
		var s StarSummary
		if err := rows.Scan(&id, &s.Balance, &s.Shown); err != nil {
			return nil, err
		}
		out[id] = s
	}
	return out, rows.Err()
}
