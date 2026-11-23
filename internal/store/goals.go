package store

import (
	"database/sql"
	"fmt"
	"time"
)

// The savings goal (ticket 293): one reward a person saves for.

// Why a goal ended on its own.
const (
	GoalRedeemed = "redeemed" // the redemption was confirmed
	GoalArchived = "archived" // the reward was archived
	GoalDeleted  = "deleted"  // the reward was deleted
	GoalAudience = "audience" // the reward is no longer meant for the person
)

// GoalEndedDays is how long the child's page shows that a goal ended.
const GoalEndedDays = 7

// Goal is a person's savings goal: the reward they save for now, or, after it
// ended by itself, why and when.
type Goal struct {
	PersonID string
	// Reward is the reward saved for; empty once the goal ended.
	Reward Reward
	SetAt  time.Time
	// EndedAt, EndedReason and EndedName are set once the goal ended by itself.
	EndedAt     time.Time
	EndedReason string
	EndedName   string
}

// Active reports whether the person is saving for a reward.
func (g Goal) Active() bool { return g.Reward.ID != "" && !g.Ended() }

// Ended reports whether the goal ended by itself (and was not replaced yet).
func (g Goal) Ended() bool { return !g.EndedAt.IsZero() }

// EndedShown reports whether the child's page still says the goal ended.
func (g Goal) EndedShown(at time.Time) bool {
	return g.Ended() && at.Before(g.EndedAt.Add(GoalEndedDays*24*time.Hour+time.Minute))
}

// goalError is the 422 of a goal that cannot be set.
func goalError() error {
	return fmt.Errorf("%w: %w", ErrInvalid, &FieldError{Field: "reward_id", Msg: "savings goal: choose an offered reward meant for this person"})
}

// SetGoal makes the reward the person's goal, replacing any goal or ended
// line. The reward must be offered and meant for the person (else a
// FieldError on reward_id).
func (db *DB) SetGoal(householdID, personID, rewardID string) error {
	if _, err := db.Person(householdID, personID); err != nil {
		return err
	}
	tx, err := db.sql.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	list, err := loadRewards(tx, householdID, `AND id = ?`, rewardID)
	if err != nil {
		return err
	}
	if len(list) == 0 || list[0].Archived() || !list[0].MeantFor(personID) {
		return goalError()
	}
	if _, err := tx.Exec(`INSERT INTO savings_goals (person_id, reward_id, set_at) VALUES (?, ?, ?)
		ON CONFLICT (person_id) DO UPDATE SET reward_id = excluded.reward_id, set_at = excluded.set_at, ended_at = NULL, ended_reason = NULL, ended_name = ''`,
		personID, rewardID, now()); err != nil {
		return err
	}
	return tx.Commit()
}

// ClearGoal removes the person's goal and any ended line; none is no error.
func (db *DB) ClearGoal(householdID, personID string) error {
	if _, err := db.Person(householdID, personID); err != nil {
		return err
	}
	_, err := db.sql.Exec(`DELETE FROM savings_goals WHERE person_id = ?`, personID)
	return err
}

// Goal loads a person's goal; without one the zero Goal comes back.
func (db *DB) Goal(householdID, personID string) (Goal, error) {
	if _, err := db.Person(householdID, personID); err != nil {
		return Goal{}, err
	}
	all, err := db.Goals(householdID)
	return all[personID], err
}

// Goals loads the goals of the household by person.
func (db *DB) Goals(householdID string) (map[string]Goal, error) {
	rows, err := db.sql.Query(`SELECT g.person_id, COALESCE(g.reward_id, ''), g.set_at, COALESCE(g.ended_at, ''), COALESCE(g.ended_reason, ''), g.ended_name
		FROM savings_goals g JOIN household_members m ON m.person_id = g.person_id WHERE m.household_id = ?`, householdID)
	if err != nil {
		return nil, err
	}
	out := map[string]Goal{}
	for rows.Next() {
		var g Goal
		var set, ended string
		if err := rows.Scan(&g.PersonID, &g.Reward.ID, &set, &ended, &g.EndedReason, &g.EndedName); err != nil {
			rows.Close()
			return nil, err
		}
		g.SetAt, _ = time.Parse(time.RFC3339, set)
		g.EndedAt, _ = time.Parse(time.RFC3339, ended)
		out[g.PersonID] = g
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	var rewards map[string]Reward
	for id, g := range out {
		if g.Reward.ID == "" || g.Ended() {
			g.Reward = Reward{}
			out[id] = g
			continue
		}
		if rewards == nil { // one read of the catalogue for all goals
			list, err := db.Rewards(householdID)
			if err != nil {
				return nil, err
			}
			rewards = make(map[string]Reward, len(list))
			for _, r := range list {
				rewards[r.ID] = r
			}
		}
		r, ok := rewards[g.Reward.ID]
		if !ok {
			delete(out, id)
			continue
		}
		g.Reward = r
		out[id] = g
	}
	return out, nil
}

// endGoalsOutsideAudience ends the goals on a reward whose audience no longer
// holds the person. No audience means the whole household: nobody is outside.
func endGoalsOutsideAudience(q rewardDB, rewardID string) error {
	_, err := q.Exec(`UPDATE savings_goals SET ended_at = ?, ended_reason = 'audience', ended_name = (SELECT name FROM rewards WHERE id = ?)
		WHERE reward_id = ? AND ended_at IS NULL
		AND EXISTS (SELECT 1 FROM reward_people WHERE reward_id = ?)
		AND person_id NOT IN (SELECT person_id FROM reward_people WHERE reward_id = ?)`, now(), rewardID, rewardID, rewardID, rewardID)
	return err
}

var _ = sql.ErrNoRows
