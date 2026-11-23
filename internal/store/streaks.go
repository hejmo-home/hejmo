package store

import (
	"database/sql"
	"errors"
	"fmt"
	"slices"
	"sort"

	"github.com/hejmo-home/hejmo/internal/streak"
	"github.com/hejmo-home/hejmo/internal/task"
)

// SetStreaksShown switches the week count and badges of a person on or off.
func (db *DB) SetStreaksShown(householdID, personID string, on bool) error {
	return expectOne(db.sql.Exec(`UPDATE people SET streaks_shown = ?, updated_at = ?
		WHERE id = ? AND id IN (SELECT person_id FROM household_members WHERE household_id = ?)`, on, now(), personID, householdID))
}

// RoutineDone returns the days each task of a routine is ticked off, by task
// id. Single tasks are not part of it.
func (db *DB) RoutineDone(householdID string) (map[string]map[string]bool, error) {
	rows, err := db.sql.Query(`SELECT d.task_id, d.day FROM task_done d JOIN tasks t ON t.id = d.task_id
		WHERE t.household_id = ? AND t.routine_id IS NOT NULL`, householdID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]map[string]bool{}
	for rows.Next() {
		var id, day string
		if err := rows.Scan(&id, &day); err != nil {
			return nil, err
		}
		if out[id] == nil {
			out[id] = map[string]bool{}
		}
		out[id][day] = true
	}
	return out, rows.Err()
}

// Pauses lists the household's pause days, the earliest first.
func (db *DB) Pauses(householdID string) ([]streak.Pause, error) {
	rows, err := db.sql.Query(`SELECT id, from_day, to_day FROM pauses WHERE household_id = ? ORDER BY from_day, to_day, created_at, rowid`, householdID)
	if err != nil {
		return nil, err
	}
	var out []streak.Pause
	for rows.Next() {
		var p streak.Pause
		if err := rows.Scan(&p.ID, &p.From, &p.To); err != nil {
			rows.Close()
			return nil, err
		}
		out = append(out, p)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for i := range out {
		if out[i].People, err = pausePeople(db.sql, out[i].ID); err != nil {
			return nil, err
		}
	}
	return out, nil
}

func pausePeople(q interface {
	Query(query string, args ...any) (*sql.Rows, error)
}, id string) ([]string, error) {
	rows, err := q.Query(`SELECT person_id FROM pause_person WHERE pause_id = ? ORDER BY person_id`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var person string
		if err := rows.Scan(&person); err != nil {
			return nil, err
		}
		out = append(out, person)
	}
	return out, rows.Err()
}

// Pause loads one pause.
func (db *DB) Pause(householdID, id string) (streak.Pause, error) {
	var p streak.Pause
	err := db.sql.QueryRow(`SELECT id, from_day, to_day FROM pauses WHERE household_id = ? AND id = ?`, householdID, id).Scan(&p.ID, &p.From, &p.To)
	if errors.Is(err, sql.ErrNoRows) {
		return streak.Pause{}, ErrNotFound
	}
	if err != nil {
		return streak.Pause{}, err
	}
	p.People, err = pausePeople(db.sql, p.ID)
	return p, err
}

// SavePause stores a new pause. People empty means the whole family; they must
// be members of the household. Pauses of exactly the same people that overlap
// it are merged into it; the result is the pause as stored.
func (db *DB) SavePause(householdID string, p streak.Pause, by string) (streak.Pause, error) {
	p.ID = ""
	return db.writePause(householdID, p, by)
}

// UpdatePause changes the days and people of a pause, merging as SavePause does.
func (db *DB) UpdatePause(householdID string, p streak.Pause) (streak.Pause, error) {
	if _, err := db.Pause(householdID, p.ID); err != nil {
		return streak.Pause{}, err
	}
	return db.writePause(householdID, p, "")
}

func (db *DB) writePause(householdID string, p streak.Pause, by string) (streak.Pause, error) {
	if err := p.Validate(); err != nil {
		return streak.Pause{}, fmt.Errorf("%w: %w", ErrInvalid, err)
	}
	p.People = slices.Compact(sortedCopy(p.People))
	for _, id := range p.People {
		if _, err := db.Person(householdID, id); errors.Is(err, ErrNotFound) {
			return streak.Pause{}, fmt.Errorf("%w: %w", ErrInvalid, &task.InvalidError{Field: "people", Msg: "unknown person"})
		} else if err != nil {
			return streak.Pause{}, err
		}
	}
	all, err := db.Pauses(householdID)
	if err != nil {
		return streak.Pause{}, err
	}
	tx, err := db.sql.Begin()
	if err != nil {
		return streak.Pause{}, err
	}
	defer tx.Rollback()
	for _, o := range all {
		if o.ID == p.ID || o.From > p.To || o.To < p.From || !slices.Equal(o.People, p.People) {
			continue
		}
		p.From, p.To = min(p.From, o.From), max(p.To, o.To)
		if p.ID == "" {
			p.ID = o.ID // the new pause becomes the first one it reaches
			continue
		}
		if _, err := tx.Exec(`DELETE FROM pauses WHERE id = ?`, o.ID); err != nil {
			return streak.Pause{}, err
		}
	}
	if p.ID == "" {
		p.ID = newID()
		if _, err := tx.Exec(`INSERT INTO pauses (id, household_id, from_day, to_day, created_by, created_at) VALUES (?, ?, ?, ?, NULLIF(?, ''), ?)`,
			p.ID, householdID, p.From, p.To, by, now()); err != nil {
			return streak.Pause{}, err
		}
	} else {
		if _, err := tx.Exec(`UPDATE pauses SET from_day = ?, to_day = ? WHERE id = ?`, p.From, p.To, p.ID); err != nil {
			return streak.Pause{}, err
		}
		if _, err := tx.Exec(`DELETE FROM pause_person WHERE pause_id = ?`, p.ID); err != nil {
			return streak.Pause{}, err
		}
	}
	for _, id := range p.People {
		if _, err := tx.Exec(`INSERT INTO pause_person (pause_id, person_id) VALUES (?, ?)`, p.ID, id); err != nil {
			return streak.Pause{}, err
		}
	}
	return p, tx.Commit()
}

func sortedCopy(s []string) []string {
	out := slices.Clone(s)
	sort.Strings(out)
	return out
}

// DeletePause removes a pause. What was ticked off stays.
func (db *DB) DeletePause(householdID, id string) error {
	return expectOne(db.sql.Exec(`DELETE FROM pauses WHERE household_id = ? AND id = ?`, householdID, id))
}
