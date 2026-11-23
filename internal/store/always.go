package store

import (
	"database/sql"
	"errors"
	"fmt"

	"github.com/hejmo-home/hejmo/internal/always"
	"github.com/hejmo-home/hejmo/internal/list"
)

// Articles that are always on the list (ticket 168, ADR 0019).

const alwaysColumns = `article_id, enabled, COALESCE(list_id, ''), list_name, weekday, every, start_week, since, qty_base, qty_unit, lang`

func scanRule(row interface{ Scan(...any) error }) (always.Rule, error) {
	var r always.Rule
	err := row.Scan(&r.ArticleID, &r.Enabled, &r.ListID, &r.ListName, &r.Weekday, &r.Every, &r.Start, &r.Since, &r.Qty.Base, &r.Qty.Unit, &r.Lang)
	return r, err
}

// AlwaysRule loads the rule of an article; false when it has none.
func (db *DB) AlwaysRule(householdID, articleID string) (always.Rule, bool, error) {
	r, err := scanRule(db.sql.QueryRow(`SELECT `+alwaysColumns+` FROM list_always WHERE household_id = ? AND article_id = ?`, householdID, articleID))
	if errors.Is(err, sql.ErrNoRows) {
		return always.Rule{}, false, nil
	}
	return r, err == nil, err
}

// AlwaysRules returns every rule of the household, in the order they were made.
func (db *DB) AlwaysRules(householdID string) ([]always.Rule, error) {
	rows, err := db.sql.Query(`SELECT `+alwaysColumns+` FROM list_always WHERE household_id = ? ORDER BY rowid`, householdID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []always.Rule
	for rows.Next() {
		r, err := scanRule(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// SaveAlwaysRule stores a rule that is on. today (YYYY-MM-DD) is the day it is
// saved: the rule starts after it, so saving on the chosen day waits for the
// next time (Q43). A rule saved again unchanged keeps its first day. The list
// must exist; a refused rule is ErrInvalid.
func (db *DB) SaveAlwaysRule(householdID string, r always.Rule, today string) (always.Rule, error) {
	r.Enabled = true
	if err := r.Validate(); err != nil {
		return always.Rule{}, fmt.Errorf("%w: %w", ErrInvalid, err)
	}
	if r.Lang == "" {
		r.Lang = "de"
	}
	l, err := db.List(householdID, r.ListID)
	if err != nil {
		return always.Rule{}, err
	}
	r.ListName, r.Since = l.Name, today
	if old, ok, err := db.AlwaysRule(householdID, r.ArticleID); err != nil {
		return always.Rule{}, err
	} else if ok && old.Enabled && old.ListID == r.ListID && old.Weekday == r.Weekday && old.Every == r.Every && old.Start == r.Start && old.Since != "" {
		r.Since = old.Since
	}
	_, err = db.sql.Exec(`INSERT INTO list_always (household_id, article_id, enabled, list_id, list_name, weekday, every, start_week, since, qty_base, qty_unit, lang)
		VALUES (?, ?, 1, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(household_id, article_id) DO UPDATE SET enabled = 1, list_id = excluded.list_id, list_name = excluded.list_name, weekday = excluded.weekday,
			every = excluded.every, start_week = excluded.start_week, since = excluded.since, qty_base = excluded.qty_base, qty_unit = excluded.qty_unit, lang = excluded.lang`,
		householdID, r.ArticleID, r.ListID, r.ListName, r.Weekday, r.Every, r.Start, r.Since, r.Qty.Base, r.Qty.Unit, r.Lang)
	return r, err
}

// DisableAlwaysRule switches a rule off and keeps what it says; an article
// without a rule stays without one.
func (db *DB) DisableAlwaysRule(householdID, articleID string) error {
	_, err := db.sql.Exec(`UPDATE list_always SET enabled = 0 WHERE household_id = ? AND article_id = ?`, householdID, articleID)
	return err
}

// DeleteAlwaysRule removes the rule of an article.
func (db *DB) DeleteAlwaysRule(householdID, articleID string) error {
	_, err := db.sql.Exec(`DELETE FROM list_always WHERE household_id = ? AND article_id = ?`, householdID, articleID)
	return err
}

// CopyAlwaysRule gives the article to the rule of another: the copy of an
// article carries "always on the list" with it. Nothing happens without a rule.
func (db *DB) CopyAlwaysRule(householdID, from, to string) error {
	_, err := db.sql.Exec(`INSERT INTO list_always (household_id, article_id, enabled, list_id, list_name, weekday, every, start_week, since, qty_base, qty_unit, lang)
		SELECT household_id, ?, enabled, list_id, list_name, weekday, every, start_week, since, qty_base, qty_unit, lang FROM list_always WHERE household_id = ? AND article_id = ?
		ON CONFLICT(household_id, article_id) DO NOTHING`, to, householdID, from)
	return err
}

// MoveAlwaysRule hands the rule of an article over to another one (the
// original of a copy no longer needs it).
func (db *DB) MoveAlwaysRule(householdID, from, to string) error {
	if err := db.CopyAlwaysRule(householdID, from, to); err != nil {
		return err
	}
	return db.DeleteAlwaysRule(householdID, from)
}

// AlwaysSettings returns the household's time of day and holiday.
func (db *DB) AlwaysSettings(householdID string) (always.Settings, error) {
	s := always.Settings{At: always.DefaultAt}
	err := db.sql.QueryRow(`SELECT at, holiday_from, holiday_to FROM list_always_settings WHERE household_id = ?`, householdID).Scan(&s.At, &s.Holiday.From, &s.Holiday.To)
	if errors.Is(err, sql.ErrNoRows) {
		return s, nil
	}
	return s, err
}

// SetAlwaysSettings stores the time and the holiday (both days empty for none).
// The dates are checked by the caller, who knows today.
func (db *DB) SetAlwaysSettings(householdID string, s always.Settings) error {
	if !always.ValidTime(s.At) {
		return fmt.Errorf("%w: %w", ErrInvalid, &always.FieldError{Field: "at", Code: "invalid"})
	}
	_, err := db.sql.Exec(`INSERT INTO list_always_settings (household_id, at, holiday_from, holiday_to) VALUES (?, ?, ?, ?)
		ON CONFLICT(household_id) DO UPDATE SET at = excluded.at, holiday_from = excluded.holiday_from, holiday_to = excluded.holiday_to`,
		householdID, s.At, s.Holiday.From, s.Holiday.To)
	return err
}

// AlwaysLastRun is the day the rules last ran ("" before the first run).
func (db *DB) AlwaysLastRun(householdID string) (string, error) {
	var d string
	err := db.sql.QueryRow(`SELECT last_run FROM list_always_settings WHERE household_id = ?`, householdID).Scan(&d)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return d, err
}

// SetAlwaysLastRun remembers the day the rules ran.
func (db *DB) SetAlwaysLastRun(householdID, day string) error {
	_, err := db.sql.Exec(`INSERT INTO list_always_settings (household_id, last_run) VALUES (?, ?)
		ON CONFLICT(household_id) DO UPDATE SET last_run = excluded.last_run`, householdID, day)
	return err
}

// AlwaysRun is what a rule did on one day.
type AlwaysRun struct {
	ArticleID, ListID, Day, Text string
	Action                       string // "new", "added", "filled" or "exists"
	Part, Total                  list.Quantity
}

// RecordAlwaysRun remembers a run; a rule runs once a day, so a second one is ignored.
func (db *DB) RecordAlwaysRun(householdID string, r AlwaysRun) error {
	_, err := db.sql.Exec(`INSERT OR IGNORE INTO list_always_runs (household_id, article_id, list_id, day, text, action, part_base, part_unit, total_base, total_unit)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		householdID, r.ArticleID, r.ListID, r.Day, r.Text, r.Action, r.Part.Base, r.Part.Unit, r.Total.Base, r.Total.Unit)
	return err
}

// AlwaysRuns returns what the rules did on a day, in the order they ran.
func (db *DB) AlwaysRuns(householdID, day string) ([]AlwaysRun, error) {
	rows, err := db.sql.Query(`SELECT article_id, list_id, day, text, action, part_base, part_unit, total_base, total_unit FROM list_always_runs
		WHERE household_id = ? AND day = ? ORDER BY rowid`, householdID, day)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []AlwaysRun
	for rows.Next() {
		var r AlwaysRun
		if err := rows.Scan(&r.ArticleID, &r.ListID, &r.Day, &r.Text, &r.Action, &r.Part.Base, &r.Part.Unit, &r.Total.Base, &r.Total.Unit); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// PruneAlwaysRuns forgets the runs of days before the given one.
func (db *DB) PruneAlwaysRuns(householdID, before string) error {
	_, err := db.sql.Exec(`DELETE FROM list_always_runs WHERE household_id = ? AND day < ?`, householdID, before)
	return err
}
