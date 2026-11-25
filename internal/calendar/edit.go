package calendar

import (
	"errors"
	"time"
)

// Scope says which occurrences of a series an edit or delete touches.
type Scope string

// Scopes as offered in the UI: "only this", "this and following", "all".
const (
	ScopeThis      Scope = "this"
	ScopeFollowing Scope = "following"
	ScopeAll       Scope = "all"
)

// Valid reports whether s is a known scope.
func (s Scope) Valid() bool { return s == ScopeThis || s == ScopeFollowing || s == ScopeAll }

// Patch holds the fields to change; nil means unchanged. Start and End are
// the new times of the edited occurrence; for "following" and "all" the
// shift is applied to the whole series.
type Patch struct {
	Title, Description, Location *string
	Start, End                   *time.Time
	RRule                        *string
	People, Groups               *[]string
}

// Change is what the store has to do to apply an edit.
type Change struct {
	Create []Event
	Update []Event
	Delete []string
}

// ErrNoSuchOccurrence is returned when the given start is not part of the event.
var ErrNoSuchOccurrence = errors.New("calendar: no such occurrence")

// Editor computes edits. NewID creates ids for events it has to create.
type Editor struct {
	NewID func() string
}

// Edit changes an occurrence of events (a single event, or a series master
// with its changed occurrences) identified by its original start.
func (ed Editor) Edit(events []Event, occStart time.Time, scope Scope, p Patch) (Change, error) {
	master, overrides, err := split(events)
	if err != nil {
		return Change{}, err
	}
	if !master.Recurring() {
		if !master.Start.Equal(occStart) {
			return Change{}, ErrNoSuchOccurrence
		}
		e := master
		applyTo(&e, p, occStart, e.End.Sub(e.Start))
		return Change{Update: []Event{e}}, nil
	}
	if !isOccurrence(master, overrides, occStart) {
		return Change{}, ErrNoSuchOccurrence
	}
	if scope == ScopeFollowing && occStart.Equal(master.Start) {
		scope = ScopeAll
	}
	if scope != ScopeThis && master.AllDay && p.RRule == nil {
		// An old all-day series with a date-time UNTIL is written again with
		// a date on the next edit that reaches it.
		rule, err := AllDayRule(master.RRule, master.Start.Location())
		if err != nil {
			return Change{}, err
		}
		if rule != master.RRule {
			p.RRule = &rule
		}
	}
	switch scope {
	case ScopeThis:
		return ed.editThis(master, overrides, occStart, p), nil
	case ScopeFollowing:
		return ed.editFollowing(master, overrides, occStart, p)
	case ScopeAll:
		return editAll(master, overrides, occStart, p), nil
	}
	return Change{}, errors.New("calendar: unknown scope")
}

func (ed Editor) editThis(master Event, overrides []Event, occStart time.Time, p Patch) Change {
	for _, o := range overrides {
		if o.RecurrenceID.Equal(occStart) {
			applyTo(&o, p, o.Start, o.End.Sub(o.Start))
			return Change{Update: []Event{o}}
		}
	}
	o := master
	o.ID, o.RRule, o.ExDates = ed.NewID(), "", nil
	o.SeriesID, o.RecurrenceID = master.ID, occStart
	o.Start, o.End = occurrenceTimes(master, occStart)
	applyTo(&o, p, o.Start, o.End.Sub(o.Start))
	return Change{Create: []Event{o}}
}

func (ed Editor) editFollowing(master Event, overrides []Event, occStart time.Time, p Patch) (Change, error) {
	old := master
	before, err := countBefore(master, occStart)
	if err != nil {
		return Change{}, err
	}
	oldRule, newRule, err := splitRule(master, occStart, before)
	if err != nil {
		return Change{}, err
	}
	old.RRule = oldRule

	next := master
	next.ID = ed.NewID()
	next.RRule = newRule
	next.Start, next.End = occurrenceTimes(master, occStart)
	delta := applyTo(&next, p, next.Start, next.End.Sub(next.Start))
	next.ExDates, old.ExDates = nil, nil
	for _, x := range master.ExDates {
		if x.Before(occStart) {
			old.ExDates = append(old.ExDates, x)
		} else {
			next.ExDates = append(next.ExDates, x.Add(delta))
		}
	}
	ch := Change{Update: []Event{old}, Create: []Event{next}}
	for _, o := range overrides {
		if o.RecurrenceID.Before(occStart) {
			continue
		}
		o.SeriesID = next.ID
		o.RecurrenceID = o.RecurrenceID.Add(delta)
		ch.Update = append(ch.Update, o)
	}
	return ch, nil
}

func editAll(master Event, overrides []Event, occStart time.Time, p Patch) Change {
	occ, occEnd := occurrenceTimes(master, occStart)
	// Shift the series by how far the edited occurrence moved.
	probe := master
	probe.Start, probe.End = occ, occEnd
	delta := applyTo(&probe, p, occ, occEnd.Sub(occ))
	duration := probe.End.Sub(probe.Start)

	m := master
	m.Title, m.Description, m.Location, m.RRule = probe.Title, probe.Description, probe.Location, probe.RRule
	m.People, m.Groups = probe.People, probe.Groups
	m.Start = master.Start.Add(delta)
	m.End = m.Start.Add(duration)
	if m.AllDay {
		m.End = m.Start.AddDate(0, 0, DaysBetween(probe.Start, probe.End))
	}
	m.ExDates = nil
	for _, x := range master.ExDates {
		m.ExDates = append(m.ExDates, x.Add(delta))
	}
	ch := Change{Update: []Event{m}}
	if delta != 0 {
		for _, o := range overrides {
			o.RecurrenceID = o.RecurrenceID.Add(delta)
			ch.Update = append(ch.Update, o)
		}
	}
	return ch
}

// Delete removes an occurrence of events with the given scope.
func (ed Editor) Delete(events []Event, occStart time.Time, scope Scope) (Change, error) {
	master, overrides, err := split(events)
	if err != nil {
		return Change{}, err
	}
	all := Change{Delete: []string{master.ID}}
	if !master.Recurring() {
		if !master.Start.Equal(occStart) {
			return Change{}, ErrNoSuchOccurrence
		}
		return all, nil
	}
	if !isOccurrence(master, overrides, occStart) {
		return Change{}, ErrNoSuchOccurrence
	}
	switch {
	case scope == ScopeAll, scope == ScopeFollowing && occStart.Equal(master.Start):
		return all, nil
	case scope == ScopeThis:
		m := master
		m.ExDates = append(append([]time.Time(nil), master.ExDates...), occStart)
		ch := Change{Update: []Event{m}}
		for _, o := range overrides {
			if o.RecurrenceID.Equal(occStart) {
				ch.Delete = append(ch.Delete, o.ID)
			}
		}
		return ch, nil
	case scope == ScopeFollowing:
		before, err := countBefore(master, occStart)
		if err != nil {
			return Change{}, err
		}
		oldRule, _, err := splitRule(master, occStart, before)
		if err != nil {
			return Change{}, err
		}
		m := master
		m.RRule, m.ExDates = oldRule, nil
		for _, x := range master.ExDates {
			if x.Before(occStart) {
				m.ExDates = append(m.ExDates, x)
			}
		}
		ch := Change{Update: []Event{m}}
		for _, o := range overrides {
			if !o.RecurrenceID.Before(occStart) {
				ch.Delete = append(ch.Delete, o.ID)
			}
		}
		return ch, nil
	}
	return Change{}, errors.New("calendar: unknown scope")
}

// split separates the master (or single event) from changed occurrences.
func split(events []Event) (Event, []Event, error) {
	var master *Event
	var overrides []Event
	for i := range events {
		if events[i].SeriesID == "" {
			if master != nil {
				return Event{}, nil, errors.New("calendar: more than one master event")
			}
			master = &events[i]
		} else {
			overrides = append(overrides, events[i])
		}
	}
	if master == nil {
		return Event{}, nil, errors.New("calendar: no master event")
	}
	return *master, overrides, nil
}

func isOccurrence(master Event, overrides []Event, occStart time.Time) bool {
	for _, o := range overrides {
		if o.RecurrenceID.Equal(occStart) {
			return true
		}
	}
	for _, x := range master.ExDates {
		if x.Equal(occStart) {
			return false
		}
	}
	starts, err := seriesStarts(master, occStart, occStart)
	return err == nil && len(starts) == 1
}

func occurrenceTimes(master Event, start time.Time) (time.Time, time.Time) {
	o := occurrence(master, start, start, master.ID)
	return o.Start, o.End
}

// applyTo applies p to e, whose occurrence starts at occStart with duration d.
// It returns how far the start moved.
func applyTo(e *Event, p Patch, occStart time.Time, d time.Duration) time.Duration {
	if p.Title != nil {
		e.Title = *p.Title
	}
	if p.Description != nil {
		e.Description = *p.Description
	}
	if p.Location != nil {
		e.Location = *p.Location
	}
	if p.RRule != nil {
		e.RRule = *p.RRule
	}
	if p.People != nil {
		e.People = *p.People
	}
	if p.Groups != nil {
		e.Groups = *p.Groups
	}
	var delta time.Duration
	if p.Start != nil {
		delta = p.Start.Sub(occStart)
		e.Start = *p.Start
	} else {
		e.Start = occStart
	}
	switch {
	case p.End != nil:
		e.End = *p.End
	default:
		e.End = e.Start.Add(d)
	}
	return delta
}

func countBefore(master Event, occStart time.Time) (int, error) {
	starts, err := seriesStarts(master, master.Start, occStart.Add(-time.Second))
	return len(starts), err
}

// splitRule ends the old rule before occStart and returns the rule for the
// new series. A COUNT is divided between both.
func splitRule(master Event, occStart time.Time, before int) (oldRule, newRule string, err error) {
	oldOpt, err := ParseRule(master.RRule, master.Start.Location())
	if err != nil {
		return "", "", err
	}
	if oldOpt.Count > 0 {
		newOpt := *oldOpt
		newOpt.Count = oldOpt.Count - before
		oldOpt.Count = before
		return oldOpt.RRuleString(), newOpt.RRuleString(), nil
	}
	// The old part ends the day before the split (all-day) or a second before
	// it (timed), written in the value type of DTSTART.
	end := occStart.Add(-time.Second)
	if master.AllDay {
		end = occStart.In(master.Start.Location()).AddDate(0, 0, -1)
	}
	oldOpt.Until = time.Time{}
	return withUntil(oldOpt.RRuleString(), end, master.AllDay), master.RRule, nil
}
