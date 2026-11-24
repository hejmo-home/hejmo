package store

import (
	"fmt"
	"sort"
	"time"

	"github.com/hejmo-home/hejmo/internal/badge"
	"github.com/hejmo-home/hejmo/internal/streak"
)

// Badges (ticket 295): ten fixed milestones, awarded once per person and never
// taken away. Awarding is a pure evaluation of the history (internal/badge)
// followed by INSERT OR IGNORE on the unique (person_id, key), so two ticks at
// the same moment award a badge once. Nothing in the store deletes a badge
// (only deleting the person does).

// Badge is a badge a person earned.
type Badge struct {
	Key      badge.Key
	EarnedAt time.Time
	// Quiet says the badge was earned while the person's switch "week count
	// and badges" was off: it is stored, but never shown as new.
	Quiet bool
}

// New reports whether the badge is new at the given moment (see badge.New).
func (b Badge) New(now time.Time, loc *time.Location) bool {
	return badge.New(b.EarnedAt, now, b.Quiet, loc)
}

// Badges lists a person's badges, the newest first (the ones earned together
// in catalogue order).
func (db *DB) Badges(householdID, personID string) ([]Badge, error) {
	if _, err := db.Person(householdID, personID); err != nil {
		return nil, err
	}
	all, err := db.HouseholdBadges(householdID)
	return all[personID], err
}

// HouseholdBadges lists the badges of every person of the household, by person.
func (db *DB) HouseholdBadges(householdID string) (map[string][]Badge, error) {
	rows, err := db.sql.Query(`SELECT b.person_id, b.key, b.earned_at, b.quiet FROM badges b
		JOIN household_members m ON m.person_id = b.person_id AND m.household_id = b.household_id
		WHERE b.household_id = ?`, householdID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string][]Badge{}
	for rows.Next() {
		var person, key, at string
		var b Badge
		if err := rows.Scan(&person, &key, &at, &b.Quiet); err != nil {
			return nil, err
		}
		b.Key = badge.Key(key)
		if b.EarnedAt, err = time.Parse(time.RFC3339, at); err != nil {
			return nil, err
		}
		out[person] = append(out[person], b)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for _, list := range out {
		sort.SliceStable(list, func(i, j int) bool {
			if !list[i].EarnedAt.Equal(list[j].EarnedAt) {
				return list[i].EarnedAt.After(list[j].EarnedAt)
			}
			return list[i].Key.Index() < list[j].Key.Index()
		})
	}
	return out, nil
}

// householdLocation is the household's time zone; UTC when it cannot be loaded.
func (db *DB) householdLocation(householdID string) (*time.Location, error) {
	h, err := db.Household(householdID)
	if err != nil {
		return nil, err
	}
	if loc, err := time.LoadLocation(h.Timezone); err == nil {
		return loc, nil
	}
	return time.UTC, nil
}

// badgeInput gathers the history of a person for the evaluation.
func (db *DB) badgeInput(householdID, personID string, at time.Time, loc *time.Location) (badge.Input, error) {
	in := badge.Input{Today: streak.DayOf(at, loc)}
	routines, err := db.Routines(householdID)
	if err != nil {
		return in, err
	}
	var mine bool
	for _, r := range routines {
		mine = mine || r.PersonID == personID
	}
	if mine {
		tasks, err := db.Tasks(householdID)
		if err != nil {
			return in, err
		}
		done, err := db.RoutineDone(householdID)
		if err != nil {
			return in, err
		}
		pauses, err := db.Pauses(householdID)
		if err != nil {
			return in, err
		}
		for _, r := range routines {
			if r.PersonID != personID {
				continue
			}
			sr := streak.Routine{PersonID: personID, Created: streak.DayOf(r.Created, loc), Done: done, Pauses: pauses}
			for _, t := range tasks {
				if t.RoutineID == r.ID {
					sr.Tasks = append(sr.Tasks, t)
				}
			}
			in.Routines = append(in.Routines, sr)
		}
	}
	// Stars earned in all: the ticks (net of reopening) and the parents'
	// positive bookings. A redemption never lowers it, and neither does a
	// parent's deduction by hand.
	if err := db.sql.QueryRow(`SELECT COALESCE(SUM(delta), 0) FROM star_log
		WHERE person_id = ? AND redemption_id IS NULL AND (day <> '' OR delta > 0)`, personID).Scan(&in.StarsEarned); err != nil {
		return in, err
	}
	if err := db.sql.QueryRow(`SELECT
			EXISTS (SELECT 1 FROM reward_redemptions WHERE person_id = ? AND status = 'confirmed'),
			EXISTS (SELECT 1 FROM savings_goals WHERE person_id = ? AND ended_reason = 'redeemed')`, personID, personID).Scan(&in.Redeemed, &in.GoalRedeemed); err != nil {
		return in, err
	}
	return in, nil
}

// AwardBadges awards the person every badge their history has earned and that
// they do not have yet, dated at, and returns the ones awarded just now in
// catalogue order. Awarding also works while the person's switch is off; those
// badges are stored quiet. A badge awarded by a concurrent call is not
// awarded twice and not reported twice.
func (db *DB) AwardBadges(householdID, personID string, at time.Time) ([]badge.Key, error) {
	p, err := db.Person(householdID, personID)
	if err != nil {
		return nil, err
	}
	loc, err := db.householdLocation(householdID)
	if err != nil {
		return nil, err
	}
	in, err := db.badgeInput(householdID, personID, at, loc)
	if err != nil {
		return nil, err
	}
	have := map[badge.Key]bool{}
	existing, err := db.HouseholdBadges(householdID)
	if err != nil {
		return nil, err
	}
	for _, b := range existing[personID] {
		have[b.Key] = true
	}
	var awarded []badge.Key
	for _, k := range badge.Earned(in) {
		if have[k] {
			continue
		}
		res, err := db.sql.Exec(`INSERT OR IGNORE INTO badges (household_id, person_id, key, earned_at, quiet) VALUES (?, ?, ?, ?, ?)`,
			householdID, personID, string(k), at.UTC().Format(time.RFC3339), !p.StreaksShown)
		if err != nil {
			return awarded, err
		}
		if n, _ := res.RowsAffected(); n > 0 {
			awarded = append(awarded, k)
		}
	}
	return awarded, nil
}

// awardAfter awards badges after a change that was committed already. A
// failure here must not undo or fail that change: the next award or the check
// at startup catches up, so it is dropped.
func (db *DB) awardAfter(householdID, personID string, at time.Time) []badge.Key {
	keys, _ := db.AwardBadges(householdID, personID, at)
	return keys
}

// AwardAllBadges evaluates every person of every household once, dated at. It
// runs at startup, so a box updated to this version gives children the badges
// their history already earned (dated that day), and repairs what a failed
// award left out.
func (db *DB) AwardAllBadges(at time.Time) error {
	rows, err := db.sql.Query(`SELECT household_id, person_id FROM household_members ORDER BY household_id, sort_order`)
	if err != nil {
		return err
	}
	type pair struct{ household, person string }
	var all []pair
	for rows.Next() {
		var p pair
		if err := rows.Scan(&p.household, &p.person); err != nil {
			rows.Close()
			return err
		}
		all = append(all, p)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	for _, p := range all {
		if _, err := db.AwardBadges(p.household, p.person, at); err != nil {
			return fmt.Errorf("award badges of %s: %w", p.person, err)
		}
	}
	return nil
}
