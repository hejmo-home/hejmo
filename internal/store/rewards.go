package store

import (
	"database/sql"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/hejmo-home/hejmo/internal/library"
	"github.com/hejmo-home/hejmo/internal/reward"
)

// The reward catalogue (ADR 0021, ticket 291).

// ErrRedeemed says a reward was redeemed once and is archived, not deleted (Y).
var ErrRedeemed = fmt.Errorf("%w: the reward was redeemed; archive it instead", ErrConflict)

// ErrArchived says an archived reward cannot be changed; offer it again first.
var ErrArchived = fmt.Errorf("%w: the reward is archived; offer it again to change it", ErrConflict)

// ArchivedAudienceGone is the reason of a reward archived by the box because
// the last person it was meant for left the household.
const ArchivedAudienceGone = "audience_gone"

// Reward is something a child can trade stars for.
type Reward struct {
	ID, Name string
	// PicSource is "symbol" (Symbol, an emoji) or "upload" (a stored picture).
	PicSource, Symbol string
	Price             int
	Limit             reward.Limit
	// PersonIDs are the people the reward is meant for; none means the whole
	// household, including people who join later.
	PersonIDs []string

	HasPicture                 bool
	PictureFromW, PictureFromH int
	PictureFromBytes           int
	PictureAt                  time.Time

	// ArchivedAt is zero for an offered reward; ArchivedReason is
	// ArchivedAudienceGone when the box archived it, else empty.
	ArchivedAt     time.Time
	ArchivedReason string
	// ArchivedFor is the name of the person who left, with ArchivedAudienceGone.
	ArchivedFor string
	CreatedBy   string // the person; "" when that person is gone
	CreatedAt   time.Time
}

// Archived reports whether the reward is hidden from children.
func (r Reward) Archived() bool { return !r.ArchivedAt.IsZero() }

// MeantFor reports whether the reward is meant for the person.
func (r Reward) MeantFor(personID string) bool {
	return len(r.PersonIDs) == 0 || slices.Contains(r.PersonIDs, personID)
}

// Audience is who a reward is meant for: the whole household, or chosen people.
type Audience struct {
	Whole     bool
	PersonIDs []string
}

func (r Reward) input() reward.Input {
	return reward.Input{Name: r.Name, Symbol: r.Symbol, Price: r.Price, Limit: r.Limit}
}

// check applies the rules of the catalogue; the audience is checked against the household by the caller.
func (r Reward) check(pic *ArticlePicture) (Reward, error) {
	in := r.input().Normalized()
	if errs := in.Validate(); len(errs) > 0 {
		return r, &FieldError{Field: errs[0].Field, Msg: "reward " + errs[0].Field + ": " + errs[0].Code}
	}
	if _, ok := library.SymbolByEmoji(in.Symbol); !ok {
		return r, &FieldError{Field: "symbol", Msg: "reward symbol: unknown"}
	}
	if pic != nil && pic.Source != "upload" {
		return r, &FieldError{Field: "picture", Msg: "reward picture: only an upload"}
	}
	r.Name, r.Symbol, r.Price, r.Limit = in.Name, in.Symbol, in.Price, in.Limit
	return r, nil
}

const rewardColumns = `id, name, pic_source, symbol, price, limit_period, COALESCE(limit_count, 0), picture IS NOT NULL, picture_from_w, picture_from_h, picture_from_b, picture_at,
	COALESCE(archived_at, ''), COALESCE(archived_reason, ''), archived_person, COALESCE(created_by, ''), created_at`

func scanReward(row interface{ Scan(...any) error }) (Reward, error) {
	var r Reward
	var period, pat, arch, created string
	err := row.Scan(&r.ID, &r.Name, &r.PicSource, &r.Symbol, &r.Price, &period, &r.Limit.Count, &r.HasPicture, &r.PictureFromW, &r.PictureFromH, &r.PictureFromBytes, &pat,
		&arch, &r.ArchivedReason, &r.ArchivedFor, &r.CreatedBy, &created)
	r.Limit.Period = reward.Period(period)
	r.PictureAt, _ = time.Parse(time.RFC3339, pat)
	r.ArchivedAt, _ = time.Parse(time.RFC3339, arch)
	r.CreatedAt, _ = time.Parse(time.RFC3339, created)
	return r, err
}

type rewardDB interface {
	Query(query string, args ...any) (*sql.Rows, error)
	QueryRow(query string, args ...any) *sql.Row
	Exec(query string, args ...any) (sql.Result, error)
}

// audiences fills PersonIDs of the rewards of a household.
func audiences(q rewardDB, householdID string, list []Reward) error {
	rows, err := q.Query(`SELECT p.reward_id, p.person_id FROM reward_people p JOIN rewards r ON r.id = p.reward_id WHERE r.household_id = ? ORDER BY p.rowid`, householdID)
	if err != nil {
		return err
	}
	defer rows.Close()
	by := map[string][]string{}
	for rows.Next() {
		var rid, pid string
		if err := rows.Scan(&rid, &pid); err != nil {
			return err
		}
		by[rid] = append(by[rid], pid)
	}
	for i := range list {
		list[i].PersonIDs = by[list[i].ID]
	}
	return rows.Err()
}

func loadRewards(q rewardDB, householdID, where string, args ...any) ([]Reward, error) {
	rows, err := q.Query(`SELECT `+rewardColumns+` FROM rewards WHERE household_id = ? `+where+` ORDER BY price, lower(name), rowid`, append([]any{householdID}, args...)...)
	if err != nil {
		return nil, err
	}
	var out []Reward
	for rows.Next() {
		r, err := scanReward(rows)
		if err != nil {
			rows.Close()
			return nil, err
		}
		out = append(out, r)
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, audiences(q, householdID, out)
}

// checkAudience refuses people that are not members of the household.
func checkAudience(q rewardDB, householdID string, ids []string) error {
	for _, id := range ids {
		var n int
		if err := q.QueryRow(`SELECT COUNT(*) FROM household_members WHERE household_id = ? AND person_id = ?`, householdID, id).Scan(&n); err != nil {
			return err
		}
		if n == 0 {
			return fmt.Errorf("%w: %w", ErrInvalid, &FieldError{Field: "person", Msg: "reward person: not in the household"})
		}
	}
	return nil
}

func setAudience(q rewardDB, rewardID string, ids []string) error {
	if _, err := q.Exec(`DELETE FROM reward_people WHERE reward_id = ?`, rewardID); err != nil {
		return err
	}
	for _, id := range dedupe(ids) {
		if _, err := q.Exec(`INSERT INTO reward_people (reward_id, person_id) VALUES (?, ?)`, rewardID, id); err != nil {
			return err
		}
	}
	return nil
}

func dedupe(ids []string) []string {
	var out []string
	for _, id := range ids {
		if !slices.Contains(out, id) {
			out = append(out, id)
		}
	}
	return out
}

func limitArgs(l reward.Limit) (string, any) {
	if l.Period == reward.NoLimit {
		return string(reward.NoLimit), nil
	}
	return string(l.Period), l.Count
}

// CreateReward stores a reward made by a person. No PersonIDs means the whole household.
func (db *DB) CreateReward(householdID, personID string, r Reward, pic *ArticlePicture) (Reward, error) {
	r, err := r.check(pic)
	if err != nil {
		return Reward{}, fmt.Errorf("%w: %w", ErrInvalid, err)
	}
	r.ID = newID()
	ts := now()
	var jpeg []byte
	var at string
	var fw, fh, fb int
	r.PicSource = "symbol"
	if pic != nil {
		r.PicSource = "upload"
		jpeg, at, fw, fh, fb = pic.JPEG, ts, pic.FromW, pic.FromH, pic.FromBytes
	}
	var creator any
	if personID != "" {
		creator = personID
	}
	period, count := limitArgs(r.Limit)
	tx, err := db.sql.Begin()
	if err != nil {
		return Reward{}, err
	}
	defer tx.Rollback()
	if err := checkAudience(tx, householdID, r.PersonIDs); err != nil {
		return Reward{}, err
	}
	if _, err := tx.Exec(`INSERT INTO rewards (id, household_id, name, pic_source, symbol, picture, picture_from_w, picture_from_h, picture_from_b, picture_at, price, limit_period, limit_count, created_by, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		r.ID, householdID, r.Name, r.PicSource, r.Symbol, jpeg, fw, fh, fb, at, r.Price, period, count, creator, ts, ts); err != nil {
		return Reward{}, err
	}
	if err := setAudience(tx, r.ID, r.PersonIDs); err != nil {
		return Reward{}, err
	}
	if err := tx.Commit(); err != nil {
		return Reward{}, err
	}
	return db.Reward(householdID, r.ID)
}

// Reward loads one reward, archived or not.
func (db *DB) Reward(householdID, id string) (Reward, error) {
	list, err := loadRewards(db.sql, householdID, `AND id = ?`, id)
	if err != nil {
		return Reward{}, err
	}
	if len(list) == 0 {
		return Reward{}, ErrNotFound
	}
	return list[0], nil
}

// Rewards returns every reward of the household, archived ones too, by price and name.
func (db *DB) Rewards(householdID string) ([]Reward, error) {
	return loadRewards(db.sql, householdID, ``)
}

// RewardsFor returns the rewards a person may see: meant for them and not archived.
func (db *DB) RewardsFor(householdID, personID string) ([]Reward, error) {
	list, err := loadRewards(db.sql, householdID, `AND archived_at IS NULL`)
	if err != nil {
		return nil, err
	}
	out := list[:0]
	for _, r := range list {
		if r.MeantFor(personID) {
			out = append(out, r)
		}
	}
	return out, nil
}

// RewardPicture reads the stored JPEG of a reward; ErrNotFound when it has none.
func (db *DB) RewardPicture(householdID, id string) ([]byte, error) {
	var b []byte
	err := db.sql.QueryRow(`SELECT picture FROM rewards WHERE household_id = ? AND id = ?`, householdID, id).Scan(&b)
	if errors.Is(err, sql.ErrNoRows) || (err == nil && b == nil) {
		return nil, ErrNotFound
	}
	return b, err
}

// UpdateReward changes an offered reward, its audience included (no PersonIDs
// means the whole household). What happens to the picture is the change, as
// for articles. An archived reward is ErrArchived: offer it again first.
func (db *DB) UpdateReward(householdID string, r Reward, change PictureChange, pic *ArticlePicture) (Reward, error) {
	if change != SetPicture {
		pic = nil
	}
	r, err := r.check(pic)
	if err != nil {
		return Reward{}, fmt.Errorf("%w: %w", ErrInvalid, err)
	}
	old, err := db.Reward(householdID, r.ID)
	if err != nil {
		return Reward{}, err
	}
	if old.Archived() {
		return Reward{}, ErrArchived
	}
	ts := now()
	period, count := limitArgs(r.Limit)
	tx, err := db.sql.Begin()
	if err != nil {
		return Reward{}, err
	}
	defer tx.Rollback()
	if err := checkAudience(tx, householdID, r.PersonIDs); err != nil {
		return Reward{}, err
	}
	var res sql.Result
	switch change {
	case SetPicture:
		res, err = tx.Exec(`UPDATE rewards SET name = ?, price = ?, limit_period = ?, limit_count = ?, pic_source = 'upload', picture = ?, picture_from_w = ?, picture_from_h = ?, picture_from_b = ?, picture_at = ?, updated_at = ?
			WHERE household_id = ? AND id = ?`, r.Name, r.Price, period, count, pic.JPEG, pic.FromW, pic.FromH, pic.FromBytes, ts, ts, householdID, r.ID)
	case RemovePicture:
		res, err = tx.Exec(`UPDATE rewards SET name = ?, price = ?, limit_period = ?, limit_count = ?, pic_source = 'symbol', symbol = ?, picture = NULL, picture_from_w = 0, picture_from_h = 0, picture_from_b = 0, picture_at = '', updated_at = ?
			WHERE household_id = ? AND id = ?`, r.Name, r.Price, period, count, r.Symbol, ts, householdID, r.ID)
	default:
		res, err = tx.Exec(`UPDATE rewards SET name = ?, price = ?, limit_period = ?, limit_count = ?, updated_at = ? WHERE household_id = ? AND id = ?`,
			r.Name, r.Price, period, count, ts, householdID, r.ID)
	}
	if err := expectOne(res, err); err != nil {
		return Reward{}, err
	}
	if err := setAudience(tx, r.ID, r.PersonIDs); err != nil {
		return Reward{}, err
	}
	if err := tx.Commit(); err != nil {
		return Reward{}, err
	}
	return db.Reward(householdID, r.ID)
}

// ArchiveReward hides a reward from children; redemptions stay in the star logs.
// Archiving an archived reward changes nothing.
func (db *DB) ArchiveReward(householdID, id string, at time.Time) (Reward, error) {
	res, err := db.sql.Exec(`UPDATE rewards SET archived_at = COALESCE(archived_at, ?), updated_at = ? WHERE household_id = ? AND id = ?`,
		at.UTC().Format(time.RFC3339), now(), householdID, id)
	if err := expectOne(res, err); err != nil {
		return Reward{}, err
	}
	return db.Reward(householdID, id)
}

// RestoreReward offers an archived reward again. One the box archived because
// its audience left needs a new audience (never silently the whole family):
// without one it is ErrInvalid. A given audience replaces the old one.
func (db *DB) RestoreReward(householdID, id string, aud *Audience) (Reward, error) {
	old, err := db.Reward(householdID, id)
	if err != nil {
		return Reward{}, err
	}
	var ids []string
	if aud != nil {
		switch {
		case aud.Whole && len(aud.PersonIDs) > 0, !aud.Whole && len(aud.PersonIDs) == 0:
			return Reward{}, fmt.Errorf("%w: %w", ErrInvalid, &FieldError{Field: "person", Msg: "reward person: choose the whole household or people"})
		}
		ids = aud.PersonIDs
	} else if old.Archived() && old.ArchivedReason == ArchivedAudienceGone {
		return Reward{}, fmt.Errorf("%w: %w", ErrInvalid, &FieldError{Field: "person", Msg: "reward person: say who it is for now"})
	}
	tx, err := db.sql.Begin()
	if err != nil {
		return Reward{}, err
	}
	defer tx.Rollback()
	if err := checkAudience(tx, householdID, ids); err != nil {
		return Reward{}, err
	}
	if _, err := tx.Exec(`UPDATE rewards SET archived_at = NULL, archived_reason = NULL, archived_person = '', updated_at = ? WHERE household_id = ? AND id = ?`, now(), householdID, id); err != nil {
		return Reward{}, err
	}
	if aud != nil {
		if err := setAudience(tx, id, ids); err != nil {
			return Reward{}, err
		}
	}
	if err := tx.Commit(); err != nil {
		return Reward{}, err
	}
	return db.Reward(householdID, id)
}

// DeleteReward removes a reward that was never redeemed. One with a confirmed
// redemption is ErrRedeemed: archive it, so the star logs stay right (Y).
func (db *DB) DeleteReward(householdID, id string) error {
	tx, err := db.sql.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var n int
	if err := tx.QueryRow(`SELECT COUNT(*) FROM rewards WHERE household_id = ? AND id = ?`, householdID, id).Scan(&n); err != nil {
		return err
	}
	if n == 0 {
		return ErrNotFound
	}
	if err := tx.QueryRow(`SELECT COUNT(*) FROM reward_redemptions WHERE reward_id = ? AND status = 'confirmed'`, id).Scan(&n); err != nil {
		return err
	}
	if n > 0 {
		return ErrRedeemed
	}
	res, err := tx.Exec(`DELETE FROM rewards WHERE household_id = ? AND id = ?`, householdID, id)
	if err := expectOne(res, err); err != nil {
		return err
	}
	return tx.Commit()
}

// RewardPerson is how often one person redeemed a reward.
type RewardPerson struct {
	PersonID string
	Count    int
}

// RewardFacts says how often a reward was redeemed, by whom and when last.
type RewardFacts struct {
	Count    int
	ByPerson []RewardPerson // most often first
	Last     time.Time
	LastBy   string // person id; "" when that person is gone
}

// RewardFacts counts the confirmed redemptions of a reward.
func (db *DB) RewardFacts(householdID, id string) (RewardFacts, error) {
	if _, err := db.Reward(householdID, id); err != nil {
		return RewardFacts{}, err
	}
	rows, err := db.sql.Query(`SELECT COALESCE(person_id, ''), COALESCE(decided_at, requested_at) FROM reward_redemptions WHERE reward_id = ? AND status = 'confirmed' ORDER BY COALESCE(decided_at, requested_at), rowid`, id)
	if err != nil {
		return RewardFacts{}, err
	}
	defer rows.Close()
	var f RewardFacts
	counts := map[string]int{}
	var order []string
	for rows.Next() {
		var who, at string
		if err := rows.Scan(&who, &at); err != nil {
			return RewardFacts{}, err
		}
		f.Count++
		if counts[who] == 0 {
			order = append(order, who)
		}
		counts[who]++
		f.Last, _ = time.Parse(time.RFC3339, at)
		f.LastBy = who
	}
	for _, who := range order {
		f.ByPerson = append(f.ByPerson, RewardPerson{who, counts[who]})
	}
	slices.SortStableFunc(f.ByPerson, func(a, b RewardPerson) int { return b.Count - a.Count })
	return f, rows.Err()
}

// ConfirmedRedemptions returns, per reward and person, when their confirmed
// redemptions happened: what a limit counts.
func (db *DB) ConfirmedRedemptions(householdID string) (map[string]map[string][]time.Time, error) {
	rows, err := db.sql.Query(`SELECT d.reward_id, d.person_id, COALESCE(d.decided_at, d.requested_at) FROM reward_redemptions d JOIN rewards r ON r.id = d.reward_id
		WHERE r.household_id = ? AND d.status = 'confirmed' AND d.person_id IS NOT NULL ORDER BY COALESCE(d.decided_at, d.requested_at)`, householdID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]map[string][]time.Time{}
	for rows.Next() {
		var rid, pid, at string
		if err := rows.Scan(&rid, &pid, &at); err != nil {
			return nil, err
		}
		t, err := time.Parse(time.RFC3339, at)
		if err != nil {
			return nil, err
		}
		if out[rid] == nil {
			out[rid] = map[string][]time.Time{}
		}
		out[rid][pid] = append(out[rid][pid], t)
	}
	return out, rows.Err()
}

// ConfirmedRedemptionCounts is how often each reward was redeemed (confirmed),
// including by people who have left: what DeleteReward goes by.
func (db *DB) ConfirmedRedemptionCounts(householdID string) (map[string]int, error) {
	rows, err := db.sql.Query(`SELECT d.reward_id, COUNT(*) FROM reward_redemptions d JOIN rewards r ON r.id = d.reward_id
		WHERE r.household_id = ? AND d.status = 'confirmed' GROUP BY d.reward_id`, householdID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]int{}
	for rows.Next() {
		var id string
		var n int
		if err := rows.Scan(&id, &n); err != nil {
			return nil, err
		}
		out[id] = n
	}
	return out, rows.Err()
}

// HasRewards reports whether at least one offered reward is meant for the person.
func (db *DB) HasRewards(householdID, personID string) (bool, error) {
	list, err := db.RewardsFor(householdID, personID)
	return len(list) > 0, err
}
