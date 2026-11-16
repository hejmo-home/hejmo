package store

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/hejmo-home/hejmo/internal/task"
)

// TemplateExistsError says that an own routine template has the name already.
// Names are unique among a household's own templates, ignoring case. It is
// ErrConflict.
type TemplateExistsError struct{ Name string }

func (e *TemplateExistsError) Error() string {
	return fmt.Sprintf("%v: there is a routine template %q already", ErrConflict, e.Name)
}
func (e *TemplateExistsError) Unwrap() error { return ErrConflict }

func invalidField(field, msg string) error {
	return fmt.Errorf("%w: %w", ErrInvalid, &task.InvalidError{Field: field, Msg: msg})
}

const templateColumns = `id, name, part, origin_routine, origin_person, created_at`

func scanTemplate(row interface{ Scan(...any) error }) (task.RoutineTemplate, error) {
	var t task.RoutineTemplate
	var part, created string
	if err := row.Scan(&t.ID, &t.Name, &part, &t.OriginRoutine, &t.OriginPerson, &created); err != nil {
		return task.RoutineTemplate{}, err
	}
	t.Part = task.PartOfDay(part)
	t.SavedAt, _ = time.Parse(time.RFC3339, created)
	return t, nil
}

type rows interface {
	Query(query string, args ...any) (*sql.Rows, error)
}

func templateSteps(q rows, templateID string) ([]task.TemplateStep, error) {
	rs, err := q.Query(`SELECT id, title, emoji, rrule, stars FROM routine_template_steps WHERE template_id = ? ORDER BY pos, rowid`, templateID)
	if err != nil {
		return nil, err
	}
	defer rs.Close()
	var out []task.TemplateStep
	for rs.Next() {
		var s task.TemplateStep
		var rule string
		if err := rs.Scan(&s.ID, &s.Title, &s.Emoji, &rule, &s.Stars); err != nil {
			return nil, err
		}
		parsed, err := task.ParseRRule(rule)
		if err != nil {
			return nil, err
		}
		s.Repeat, s.Weekdays = parsed.Repeat, parsed.Weekdays
		out = append(out, s)
	}
	return out, rs.Err()
}

// RoutineTemplates lists the household's own templates with their steps,
// oldest first. The templates that come with Hejmo are not stored.
func (db *DB) RoutineTemplates(householdID string) ([]task.RoutineTemplate, error) {
	rs, err := db.sql.Query(`SELECT `+templateColumns+` FROM routine_templates WHERE household_id = ? ORDER BY created_at, rowid`, householdID)
	if err != nil {
		return nil, err
	}
	var out []task.RoutineTemplate
	for rs.Next() {
		t, err := scanTemplate(rs)
		if err != nil {
			rs.Close()
			return nil, err
		}
		out = append(out, t)
	}
	if err := rs.Err(); err != nil {
		rs.Close()
		return nil, err
	}
	rs.Close()
	for i := range out {
		if out[i].Steps, err = templateSteps(db.sql, out[i].ID); err != nil {
			return nil, err
		}
	}
	return out, nil
}

// RoutineTemplate loads one own template with its steps.
func (db *DB) RoutineTemplate(householdID, id string) (task.RoutineTemplate, error) {
	t, err := scanTemplate(db.sql.QueryRow(`SELECT `+templateColumns+` FROM routine_templates WHERE household_id = ? AND id = ?`, householdID, id))
	if errors.Is(err, sql.ErrNoRows) {
		return task.RoutineTemplate{}, ErrNotFound
	} else if err != nil {
		return task.RoutineTemplate{}, err
	}
	t.Steps, err = templateSteps(db.sql, id)
	return t, err
}

// templateNameTaken turns a unique-constraint failure into TemplateExistsError.
func (db *DB) templateNameTaken(err error, householdID, name string) error {
	if !isUniqueViolation(err) {
		return err
	}
	var existing string
	if qerr := db.sql.QueryRow(`SELECT name FROM routine_templates WHERE household_id = ? AND name = ?`, householdID, name).Scan(&existing); qerr == nil {
		return &TemplateExistsError{Name: existing}
	}
	return err
}

// SaveRoutineAsTemplate stores a copy of a routine as an own template: its part
// of the day and its steps in order, with emoji, star value and repetition.
// Once-only tasks are left out (a date makes no sense in a reusable template);
// a routine with no other task cannot become a template (ErrInvalid). Ticks,
// stars earned and history stay with the person. A name already used by an
// own template is TemplateExistsError.
func (db *DB) SaveRoutineAsTemplate(householdID, routineID, name string) (task.RoutineTemplate, error) {
	tx, err := db.sql.Begin()
	if err != nil {
		return task.RoutineTemplate{}, err
	}
	defer tx.Rollback()
	r, err := routineIn(tx, householdID, routineID)
	if err != nil {
		return task.RoutineTemplate{}, err
	}
	var person string
	if err := tx.QueryRow(`SELECT name FROM people WHERE id = ?`, r.PersonID).Scan(&person); err != nil {
		return task.RoutineTemplate{}, err
	}
	tpl := task.RoutineTemplate{Name: strings.TrimSpace(name), Part: r.Part, OriginRoutine: r.Name, OriginPerson: person}
	if err := tpl.Validate(); err != nil {
		return task.RoutineTemplate{}, fmt.Errorf("%w: %w", ErrInvalid, err)
	}
	list, err := routineTasksIn(tx, householdID, routineID)
	if err != nil {
		return task.RoutineTemplate{}, err
	}
	for _, k := range list {
		if k.Repeat == task.Once {
			continue
		}
		tpl.Steps = append(tpl.Steps, task.TemplateStep{Title: k.Title, Emoji: k.Emoji, Stars: k.Stars, Repeat: k.Repeat, Weekdays: k.Weekdays})
	}
	if len(tpl.Steps) == 0 {
		return task.RoutineTemplate{}, invalidField("routine", "a routine needs a task that is not once-only to become a template")
	}
	tpl.ID = newID()
	ts := now()
	if _, err := tx.Exec(`INSERT INTO routine_templates (id, household_id, name, part, origin_routine, origin_person, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		tpl.ID, householdID, tpl.Name, string(tpl.Part), tpl.OriginRoutine, tpl.OriginPerson, ts, ts); err != nil {
		return task.RoutineTemplate{}, db.templateNameTaken(err, householdID, tpl.Name)
	}
	for i := range tpl.Steps {
		s := &tpl.Steps[i]
		s.ID = newID()
		if _, err := tx.Exec(`INSERT INTO routine_template_steps (id, template_id, pos, title, emoji, rrule, stars) VALUES (?, ?, ?, ?, ?, ?, ?)`,
			s.ID, tpl.ID, i+1, s.Title, s.Emoji, ruleOf(*s), s.Stars); err != nil {
			return task.RoutineTemplate{}, err
		}
	}
	if err := tx.Commit(); err != nil {
		return task.RoutineTemplate{}, err
	}
	tpl.SavedAt, _ = time.Parse(time.RFC3339, ts)
	return tpl, nil
}

func ruleOf(s task.TemplateStep) string {
	return task.Task{Repeat: s.Repeat, Weekdays: s.Weekdays}.RRule()
}

// routineTasksIn lists a routine's tasks in order inside a transaction.
func routineTasksIn(q rows, householdID, routineID string) ([]task.Task, error) {
	rs, err := q.Query(taskFrom+` WHERE t.household_id = ? AND t.routine_id = ? ORDER BY t.routine_pos, t.created_at, t.rowid`, householdID, routineID)
	if err != nil {
		return nil, err
	}
	defer rs.Close()
	var out []task.Task
	for rs.Next() {
		k, err := scanTask(rs)
		if err != nil {
			return nil, err
		}
		k.Position = len(out) + 1
		out = append(out, k)
	}
	return out, rs.Err()
}

// UpdateRoutineTemplate renames an own template and changes its suggested
// part of the day. Routines made from it stay as they are.
func (db *DB) UpdateRoutineTemplate(householdID, id, name string, part task.PartOfDay) error {
	cur, err := db.RoutineTemplate(householdID, id)
	if err != nil {
		return err
	}
	cur.Name, cur.Part = strings.TrimSpace(name), part
	if err := cur.Validate(); err != nil {
		return fmt.Errorf("%w: %w", ErrInvalid, err)
	}
	_, err = db.sql.Exec(`UPDATE routine_templates SET name = ?, part = ?, updated_at = ? WHERE household_id = ? AND id = ?`, cur.Name, string(part), now(), householdID, id)
	return db.templateNameTaken(err, householdID, cur.Name)
}

// DeleteRoutineTemplate removes an own template; routines made from it stay.
func (db *DB) DeleteRoutineTemplate(householdID, id string) error {
	res, err := db.sql.Exec(`DELETE FROM routine_templates WHERE household_id = ? AND id = ?`, householdID, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// ownsTemplate reports ErrNotFound unless the template is the household's.
func ownsTemplate(q querier, householdID, id string) error {
	var one int
	err := q.QueryRow(`SELECT 1 FROM routine_templates WHERE household_id = ? AND id = ?`, householdID, id).Scan(&one)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	return err
}

func validStep(s *task.TemplateStep) error {
	s.Title = strings.TrimSpace(s.Title)
	if err := s.Validate(); err != nil {
		return fmt.Errorf("%w: %w", ErrInvalid, err)
	}
	return nil
}

// AddTemplateStep adds a step at the end of an own template.
func (db *DB) AddTemplateStep(householdID, templateID string, s task.TemplateStep) (task.TemplateStep, error) {
	if err := validStep(&s); err != nil {
		return task.TemplateStep{}, err
	}
	tx, err := db.sql.Begin()
	if err != nil {
		return task.TemplateStep{}, err
	}
	defer tx.Rollback()
	if err := ownsTemplate(tx, householdID, templateID); err != nil {
		return task.TemplateStep{}, err
	}
	var last int
	if err := tx.QueryRow(`SELECT COALESCE(MAX(pos), 0) FROM routine_template_steps WHERE template_id = ?`, templateID).Scan(&last); err != nil {
		return task.TemplateStep{}, err
	}
	s.ID = newID()
	if _, err := tx.Exec(`INSERT INTO routine_template_steps (id, template_id, pos, title, emoji, rrule, stars) VALUES (?, ?, ?, ?, ?, ?, ?)`,
		s.ID, templateID, last+1, s.Title, s.Emoji, ruleOf(s), s.Stars); err != nil {
		return task.TemplateStep{}, err
	}
	return s, tx.Commit()
}

// UpdateTemplateStep changes the step with s.ID of an own template.
func (db *DB) UpdateTemplateStep(householdID, templateID string, s task.TemplateStep) error {
	if err := validStep(&s); err != nil {
		return err
	}
	if err := ownsTemplate(db.sql, householdID, templateID); err != nil {
		return err
	}
	res, err := db.sql.Exec(`UPDATE routine_template_steps SET title = ?, emoji = ?, rrule = ?, stars = ? WHERE id = ? AND template_id = ?`,
		s.Title, s.Emoji, ruleOf(s), s.Stars, s.ID, templateID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// DeleteTemplateStep removes a step from an own template.
func (db *DB) DeleteTemplateStep(householdID, templateID, stepID string) error {
	if err := ownsTemplate(db.sql, householdID, templateID); err != nil {
		return err
	}
	res, err := db.sql.Exec(`DELETE FROM routine_template_steps WHERE id = ? AND template_id = ?`, stepID, templateID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// SetTemplateOrder puts the steps of an own template in the given order. The
// ids must be exactly the template's steps, each once; anything else is
// ErrInvalid and changes nothing.
func (db *DB) SetTemplateOrder(householdID, templateID string, stepIDs []string) error {
	tpl, err := db.RoutineTemplate(householdID, templateID)
	if err != nil {
		return err
	}
	bad := func() error { return invalidField("steps", "the order must name every step of the template once") }
	want := map[string]bool{}
	for _, s := range tpl.Steps {
		want[s.ID] = true
	}
	if len(stepIDs) != len(tpl.Steps) {
		return bad()
	}
	seen := map[string]bool{}
	for _, id := range stepIDs {
		if !want[id] || seen[id] {
			return bad()
		}
		seen[id] = true
	}
	tx, err := db.sql.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for i, id := range stepIDs {
		if _, err := tx.Exec(`UPDATE routine_template_steps SET pos = ? WHERE id = ? AND template_id = ?`, i+1, id, templateID); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// CreateRoutineFromSteps creates a routine for a person with a new task for
// each step, in order, all or nothing. The tasks are independent copies: they
// start on date and keep no link to where the steps came from. The person must
// not have a routine for the part of the day yet (RoutineExistsError).
func (db *DB) CreateRoutineFromSteps(householdID string, r task.Routine, steps []task.TemplateStep, date string) (task.Routine, error) {
	r.Name = strings.TrimSpace(r.Name)
	if err := r.Validate(); err != nil {
		return task.Routine{}, fmt.Errorf("%w: %w", ErrInvalid, err)
	}
	tasks := make([]task.Task, len(steps))
	for i, s := range steps {
		tasks[i] = task.Task{PersonID: r.PersonID, Title: strings.TrimSpace(s.Title), Emoji: s.Emoji, Stars: s.Stars, Repeat: s.Repeat, Weekdays: s.Weekdays, Date: date, PartOfDay: r.Part, RoutineID: "new"}
		if s.Repeat == task.Once {
			return task.Routine{}, fmt.Errorf("%w: %w", ErrInvalid, &task.InvalidError{Field: "repeat", Msg: "a template step cannot be once-only"})
		}
		if err := tasks[i].Validate(); err != nil {
			return task.Routine{}, fmt.Errorf("%w: %w", ErrInvalid, err)
		}
	}
	if _, err := db.Person(householdID, r.PersonID); errors.Is(err, ErrNotFound) {
		return task.Routine{}, fmt.Errorf("%w: %w", ErrInvalid, &task.InvalidError{Field: "person", Msg: "unknown person"})
	} else if err != nil {
		return task.Routine{}, err
	}
	tx, err := db.sql.Begin()
	if err != nil {
		return task.Routine{}, err
	}
	defer tx.Rollback()
	if existing, found, err := routineFor(tx, r.PersonID, r.Part); err != nil {
		return task.Routine{}, err
	} else if found {
		return task.Routine{}, &RoutineExistsError{Existing: existing}
	}
	r.ID = newID()
	ts := now()
	if _, err := tx.Exec(`INSERT INTO routines (id, household_id, person_id, name, part, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?)`,
		r.ID, householdID, r.PersonID, r.Name, string(r.Part), ts, ts); err != nil {
		return task.Routine{}, db.routineConflict(err, r.PersonID, r.Part)
	}
	for i, k := range tasks {
		if _, err := tx.Exec(`INSERT INTO tasks (id, household_id, person_id, title, emoji, rrule, start_date, part_of_day, stars, routine_id, routine_pos, created_at, updated_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			newID(), householdID, k.PersonID, k.Title, k.Emoji, k.RRule(), k.Date, string(storedPart(r.Part)), k.Stars, r.ID, i+1, ts, ts); err != nil {
			return task.Routine{}, err
		}
	}
	return r, tx.Commit()
}
