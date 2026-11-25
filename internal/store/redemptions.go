package store

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/hejmo-home/hejmo/internal/reward"
)

// Redeeming rewards (ADR 0021, ticket 292): a child asks, a parent confirms or
// declines, or a parent redeems directly. Only a confirmed redemption books
// stars, in the same transaction that checks balance and limit.

// RedemptionStatus is where a redemption stands.
type RedemptionStatus string

// The statuses.
const (
	RedemptionOpen      RedemptionStatus = "open"
	RedemptionConfirmed RedemptionStatus = "confirmed"
	RedemptionDeclined  RedemptionStatus = "declined"
	RedemptionWithdrawn RedemptionStatus = "withdrawn"
)

// Why a request was declined.
const (
	DeclinedByParent   = "parent"
	DeclinedRewardGone = "reward_gone"
)

// MaxRedemptionNote is the longest note a parent can leave when declining.
const MaxRedemptionNote = 120

// ErrAlreadyAsked says the person has an open request for the reward.
var ErrAlreadyAsked = fmt.Errorf("%w: there is already an open request for this reward", ErrConflict)

// Redemption is one request or redemption. Name, Symbol, Price and the picture
// are what the reward was when the child asked (when it happened, for a direct one).
type Redemption struct {
	ID, PersonID, RewardID string // RewardID is empty once the reward is deleted
	Name, Symbol           string
	HasPicture             bool
	PictureAt              time.Time
	Price                  int
	Status                 RedemptionStatus
	Note                   string
	AskedAt, DecidedAt     time.Time
	DecidedBy              string // person id; "" for a decline by the box or once that person is gone
	DecidedByName          string
	Direct                 bool
	DeclineReason          string
}

// RedeemError says why a redemption was refused: not enough stars or a limit used up.
type RedeemError struct {
	Check reward.Check
	Have  int // the stars the check went by: the balance, or the free stars for an ask
	Price int
}

func (e *RedeemError) Error() string {
	return fmt.Sprintf("%v: cannot redeem: %s (have %d, price %d)", ErrConflict, e.Check.Reason, e.Have, e.Price)
}

// Unwrap makes a refused redemption a conflict.
func (e *RedeemError) Unwrap() error { return ErrConflict }

// DecidedError says the request was decided already, by whom and when.
type DecidedError struct{ Redemption Redemption }

func (e *DecidedError) Error() string {
	return fmt.Sprintf("%v: already decided (%s)", ErrConflict, e.Redemption.Status)
}

// Unwrap makes a second decision a conflict.
func (e *DecidedError) Unwrap() error { return ErrConflict }

const redemptionColumns = `d.id, COALESCE(d.person_id, ''), COALESCE(d.reward_id, ''), d.name, d.symbol, d.picture IS NOT NULL, d.picture_at, d.price, d.status, d.note,
	d.requested_at, COALESCE(d.decided_at, ''), COALESCE(d.decided_by, ''), COALESCE(p.name, NULLIF(d.decided_by_name, ''), ''), d.direct, COALESCE(d.decline_reason, '')`

func scanRedemption(row interface{ Scan(...any) error }) (Redemption, error) {
	var r Redemption
	var status, pic, asked, decided string
	err := row.Scan(&r.ID, &r.PersonID, &r.RewardID, &r.Name, &r.Symbol, &r.HasPicture, &pic, &r.Price, &status, &r.Note, &asked, &decided, &r.DecidedBy, &r.DecidedByName, &r.Direct, &r.DeclineReason)
	r.Status = RedemptionStatus(status)
	r.PictureAt, _ = time.Parse(time.RFC3339, pic)
	r.AskedAt, _ = time.Parse(time.RFC3339, asked)
	r.DecidedAt, _ = time.Parse(time.RFC3339, decided)
	return r, err
}

func loadRedemptions(q rewardDB, householdID, where string, args ...any) ([]Redemption, error) {
	rows, err := q.Query(`SELECT `+redemptionColumns+` FROM reward_redemptions d LEFT JOIN people p ON p.id = d.decided_by WHERE d.household_id = ? `+where, append([]any{householdID}, args...)...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Redemption
	for rows.Next() {
		r, err := scanRedemption(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func loadRedemption(q rewardDB, householdID, id string) (Redemption, error) {
	list, err := loadRedemptions(q, householdID, `AND d.id = ?`, id)
	if err != nil {
		return Redemption{}, err
	}
	if len(list) == 0 {
		return Redemption{}, ErrNotFound
	}
	return list[0], nil
}

// Redemption loads one redemption of the household.
func (db *DB) Redemption(householdID, id string) (Redemption, error) {
	return loadRedemption(db.sql, householdID, id)
}

// OpenRedemptions returns the open requests of the household, oldest first.
func (db *DB) OpenRedemptions(householdID string) ([]Redemption, error) {
	return loadRedemptions(db.sql, householdID, `AND d.status = 'open' ORDER BY d.requested_at, d.rowid`)
}

// RedemptionsOf returns a person's open requests and the ones decided (confirmed
// or declined) since the given time, newest decision last.
func (db *DB) RedemptionsOf(householdID, personID string, since time.Time) ([]Redemption, error) {
	return loadRedemptions(db.sql, householdID, `AND d.person_id = ? AND (d.status = 'open' OR (d.status IN ('confirmed', 'declined') AND d.decided_at >= ?))
		ORDER BY COALESCE(d.decided_at, d.requested_at), d.rowid`, personID, since.UTC().Format(time.RFC3339))
}

func rstamp(t time.Time) string { return t.UTC().Format(time.RFC3339) }

func balanceOf(q rewardDB, personID string) (int, error) {
	var n int
	err := q.QueryRow(`SELECT COALESCE(SUM(delta), 0) FROM star_log WHERE person_id = ?`, personID).Scan(&n)
	return n, err
}

func reservedOf(q rewardDB, personID string) (int, error) {
	var n int
	err := q.QueryRow(`SELECT COALESCE(SUM(price), 0) FROM reward_redemptions WHERE person_id = ? AND status = 'open'`, personID).Scan(&n)
	return n, err
}

func confirmedTimes(q rewardDB, rewardID, personID string) ([]time.Time, error) {
	rows, err := q.Query(`SELECT COALESCE(decided_at, requested_at) FROM reward_redemptions WHERE reward_id = ? AND person_id = ? AND status = 'confirmed'`, rewardID, personID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []time.Time
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			return nil, err
		}
		t, err := time.Parse(time.RFC3339, s)
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// checkFor says whether the person can redeem the reward with the given stars.
func checkFor(q rewardDB, rw Reward, personID string, stars int, at time.Time, loc *time.Location) error {
	times, err := confirmedTimes(q, rw.ID, personID)
	if err != nil {
		return err
	}
	if c := reward.CanRedeem(rw.Price, stars, rw.Limit, times, at, loc); !c.OK() {
		return &RedeemError{Check: c, Have: stars, Price: rw.Price}
	}
	return nil
}

// redeemable loads the reward and checks it can be redeemed by the person: offered and meant for them.
func redeemable(q rewardDB, householdID, rewardID, personID string) (Reward, error) {
	list, err := loadRewards(q, householdID, `AND id = ?`, rewardID)
	if err != nil {
		return Reward{}, err
	}
	if len(list) == 0 {
		return Reward{}, ErrNotFound
	}
	rw := list[0]
	if rw.Archived() {
		return Reward{}, ErrArchived
	}
	if !rw.MeantFor(personID) {
		return Reward{}, ErrNotFound
	}
	var n int
	if err := q.QueryRow(`SELECT COUNT(*) FROM household_members WHERE household_id = ? AND person_id = ?`, householdID, personID).Scan(&n); err != nil {
		return Reward{}, err
	}
	if n == 0 {
		return Reward{}, fmt.Errorf("%w: %w", ErrInvalid, &FieldError{Field: "person", Msg: "redemption person: not in the household"})
	}
	return rw, nil
}

func insertRedemption(q rewardDB, householdID string, rw Reward, personID string, status RedemptionStatus, at time.Time) (string, error) {
	id := newID()
	_, err := q.Exec(`INSERT INTO reward_redemptions (id, household_id, reward_id, person_id, status, name, symbol, picture, picture_at, price, requested_at)
		SELECT ?, ?, id, ?, ?, name, symbol, picture, picture_at, price, ? FROM rewards WHERE id = ?`, id, householdID, personID, string(status), rstamp(at), rw.ID)
	return id, err
}

// AskReward records a child's request. The price must fit into the free stars:
// the balance minus what the person's other open requests set aside. Nothing is booked.
func (db *DB) AskReward(householdID, personID, rewardID string, at time.Time, loc *time.Location) (Redemption, error) {
	tx, err := db.sql.Begin()
	if err != nil {
		return Redemption{}, err
	}
	defer tx.Rollback()
	rw, err := redeemable(tx, householdID, rewardID, personID)
	if err != nil {
		return Redemption{}, err
	}
	var n int
	if err := tx.QueryRow(`SELECT COUNT(*) FROM reward_redemptions WHERE person_id = ? AND reward_id = ? AND status = 'open'`, personID, rewardID).Scan(&n); err != nil {
		return Redemption{}, err
	}
	if n > 0 {
		return Redemption{}, ErrAlreadyAsked
	}
	bal, err := balanceOf(tx, personID)
	if err != nil {
		return Redemption{}, err
	}
	reserved, err := reservedOf(tx, personID)
	if err != nil {
		return Redemption{}, err
	}
	if err := checkFor(tx, rw, personID, reward.Free(bal, reserved), at, loc); err != nil {
		return Redemption{}, err
	}
	id, err := insertRedemption(tx, householdID, rw, personID, RedemptionOpen, at)
	if err != nil {
		return Redemption{}, err
	}
	if err := tx.Commit(); err != nil {
		return Redemption{}, err
	}
	return db.Redemption(householdID, id)
}

// WithdrawRedemption lets a child take their open request back. One decided
// already is a *DecidedError; one of somebody else is ErrNotFound.
func (db *DB) WithdrawRedemption(householdID, id, personID string, at time.Time) (Redemption, error) {
	tx, err := db.sql.Begin()
	if err != nil {
		return Redemption{}, err
	}
	defer tx.Rollback()
	r, err := loadRedemption(tx, householdID, id)
	if err != nil || r.PersonID != personID {
		return Redemption{}, ErrNotFound
	}
	if r.Status != RedemptionOpen {
		return Redemption{}, &DecidedError{r}
	}
	if _, err := tx.Exec(`UPDATE reward_redemptions SET status = 'withdrawn', decided_at = ? WHERE id = ?`, rstamp(at), id); err != nil {
		return Redemption{}, err
	}
	if err := tx.Commit(); err != nil {
		return Redemption{}, err
	}
	return db.Redemption(householdID, id)
}

func personName(q rewardDB, id string) string {
	var name string
	_ = q.QueryRow(`SELECT name FROM people WHERE id = ?`, id).Scan(&name)
	return name
}

// confirmIn books a redemption: balance and limit are checked and the stars
// booked in the caller's transaction. The redemption row exists and is open.
func confirmIn(tx *sql.Tx, id string, rw Reward, price int, personID, by string, at time.Time, loc *time.Location) error {
	bal, err := balanceOf(tx, personID)
	if err != nil {
		return err
	}
	// The price of the request counts, not the current one: what the child was shown.
	rw.Price = price
	if err := checkFor(tx, rw, personID, bal, at, loc); err != nil {
		return err
	}
	if _, err := tx.Exec(`UPDATE reward_redemptions SET status = 'confirmed', decided_at = ?, decided_by = NULLIF(?, ''), decided_by_name = ? WHERE id = ?`, rstamp(at), by, personName(tx, by), id); err != nil {
		return err
	}
	_, err = tx.Exec(`INSERT INTO star_log (id, person_id, delta, booked_by, created_at, redemption_id) VALUES (?, ?, ?, NULLIF(?, ''), ?, ?)`, newID(), personID, -price, by, rstamp(at), id)
	return err
}

// ConfirmRedemption says yes to an open request: balance and limit are checked
// again and the stars booked in one transaction. When either fails the request
// stays open and the error is a *RedeemError; a request decided already is a *DecidedError.
func (db *DB) ConfirmRedemption(householdID, id, by string, at time.Time, loc *time.Location) (Redemption, error) {
	tx, err := db.sql.Begin()
	if err != nil {
		return Redemption{}, err
	}
	defer tx.Rollback()
	r, err := loadRedemption(tx, householdID, id)
	if err != nil {
		return Redemption{}, err
	}
	if r.Status != RedemptionOpen {
		return Redemption{}, &DecidedError{r}
	}
	rw, err := db.rewardIn(tx, householdID, r.RewardID)
	if err != nil {
		return Redemption{}, err
	}
	if err := confirmIn(tx, id, rw, r.Price, r.PersonID, by, at, loc); err != nil {
		return Redemption{}, err
	}
	if err := tx.Commit(); err != nil {
		return Redemption{}, err
	}
	db.awardAfter(householdID, r.PersonID, at) // the first reward, a reached savings goal (ticket 295)
	return db.Redemption(householdID, id)
}

func (db *DB) rewardIn(q rewardDB, householdID, id string) (Reward, error) {
	list, err := loadRewards(q, householdID, `AND id = ?`, id)
	if err != nil {
		return Reward{}, err
	}
	if len(list) == 0 {
		return Reward{}, ErrNotFound
	}
	return list[0], nil
}

// DeclineRedemption says no to an open request, with an optional note the child sees.
func (db *DB) DeclineRedemption(householdID, id, by, note string, at time.Time) (Redemption, error) {
	note = strings.TrimSpace(note)
	if utf8.RuneCountInString(note) > MaxRedemptionNote {
		return Redemption{}, fmt.Errorf("%w: %w", ErrInvalid, &FieldError{Field: "note", Msg: "note: at most 120 characters"})
	}
	tx, err := db.sql.Begin()
	if err != nil {
		return Redemption{}, err
	}
	defer tx.Rollback()
	r, err := loadRedemption(tx, householdID, id)
	if err != nil {
		return Redemption{}, err
	}
	if r.Status != RedemptionOpen {
		return Redemption{}, &DecidedError{r}
	}
	if _, err := tx.Exec(`UPDATE reward_redemptions SET status = 'declined', decline_reason = 'parent', note = ?, decided_at = ?, decided_by = NULLIF(?, ''), decided_by_name = ? WHERE id = ?`,
		note, rstamp(at), by, personName(tx, by), id); err != nil {
		return Redemption{}, err
	}
	if err := tx.Commit(); err != nil {
		return Redemption{}, err
	}
	return db.Redemption(householdID, id)
}

// RedeemNow is a parent's direct redemption for a person: same balance and
// limit checks, the stars go at once. When the person has an open request for
// the reward, that request is confirmed instead of a second booking.
func (db *DB) RedeemNow(householdID, rewardID, personID, by string, at time.Time, loc *time.Location) (Redemption, error) {
	tx, err := db.sql.Begin()
	if err != nil {
		return Redemption{}, err
	}
	defer tx.Rollback()
	rw, err := redeemable(tx, householdID, rewardID, personID)
	if err != nil {
		return Redemption{}, err
	}
	var id string
	price := rw.Price
	err = tx.QueryRow(`SELECT id, price FROM reward_redemptions WHERE person_id = ? AND reward_id = ? AND status = 'open'`, personID, rewardID).Scan(&id, &price)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		price = rw.Price
		if id, err = insertRedemption(tx, householdID, rw, personID, RedemptionOpen, at); err != nil {
			return Redemption{}, err
		}
		if _, err := tx.Exec(`UPDATE reward_redemptions SET direct = 1 WHERE id = ?`, id); err != nil {
			return Redemption{}, err
		}
	case err != nil:
		return Redemption{}, err
	}
	if err := confirmIn(tx, id, rw, price, personID, by, at, loc); err != nil {
		return Redemption{}, err
	}
	if err := tx.Commit(); err != nil {
		return Redemption{}, err
	}
	db.awardAfter(householdID, personID, at)
	return db.Redemption(householdID, id)
}

// declineOpenOfReward declines the open requests of a reward that goes away; the stars stay.
func declineOpenOfReward(q rewardDB, rewardID string, at time.Time) error {
	_, err := q.Exec(`UPDATE reward_redemptions SET status = 'declined', decline_reason = 'reward_gone', decided_at = ? WHERE reward_id = ? AND status = 'open'`, rstamp(at), rewardID)
	return err
}

// OpenRequestsOfReward returns the open requests of one reward, oldest first:
// what archiving or deleting it declines.
func (db *DB) OpenRequestsOfReward(householdID, rewardID string) ([]Redemption, error) {
	return loadRedemptions(db.sql, householdID, `AND d.reward_id = ? AND d.status = 'open' ORDER BY d.requested_at, d.rowid`, rewardID)
}

// SetBelowZero sets whether unticking a task may take a balance below zero.
func (db *DB) SetBelowZero(householdID string, on bool) error {
	return expectOne(db.sql.Exec(`UPDATE households SET below_zero = ?, updated_at = ? WHERE id = ?`, on, now(), householdID))
}

// BelowZeroCause names what put a negative balance below zero: the task that
// was reopened last and the reward redeemed last before that reopening (a
// later redemption cannot have caused it). Either may be empty.
func (db *DB) BelowZeroCause(personID string) (taskTitle, rewardName string, err error) {
	var reopenedAt string
	err = db.sql.QueryRow(`SELECT COALESCE(MAX(task_title), ''), COALESCE(MAX(created_at), '') FROM (
		SELECT task_title, created_at FROM star_log WHERE person_id = ? AND delta < 0 AND task_id IS NOT NULL ORDER BY created_at DESC, rowid DESC LIMIT 1)`, personID).Scan(&taskTitle, &reopenedAt)
	if err != nil || reopenedAt == "" {
		return taskTitle, "", err
	}
	err = db.sql.QueryRow(`SELECT COALESCE((SELECT name FROM reward_redemptions WHERE person_id = ? AND status = 'confirmed' AND decided_at <= ? ORDER BY decided_at DESC, rowid DESC LIMIT 1), '')`, personID, reopenedAt).Scan(&rewardName)
	return taskTitle, rewardName, err
}

// ReservedStars is what a person's open requests set aside: they stay in the
// balance until a parent says yes, but a new request cannot spend them again.
func (db *DB) ReservedStars(personID string) (int, error) { return reservedOf(db.sql, personID) }

// RedemptionPicture reads the picture the reward had when it was asked for;
// ErrNotFound when it had none.
func (db *DB) RedemptionPicture(householdID, id string) ([]byte, error) {
	var b []byte
	err := db.sql.QueryRow(`SELECT picture FROM reward_redemptions WHERE household_id = ? AND id = ?`, householdID, id).Scan(&b)
	if errors.Is(err, sql.ErrNoRows) || (err == nil && b == nil) {
		return nil, ErrNotFound
	}
	return b, err
}
