// Package caldav maps the box's events to and from iCalendar objects, the
// resources a CalDAV client sees (ADR 0012). It is pure: events in, iCalendar
// out and back. Serving, authentication and storage live elsewhere.
//
// One object is one event, or one series together with its changed
// occurrences, as in an .ics file.
package caldav

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	ical "github.com/emersion/go-ical"

	"github.com/hejmo-home/hejmo/internal/calendar"
	"github.com/hejmo-home/hejmo/internal/icsfeed"
)

const prodID = "-//Hejmo//Box//EN"

// ErrInvalid marks iCalendar data the box cannot take as one event.
var ErrInvalid = errors.New("caldav: invalid calendar object")

// Object is one resource: a single event or a series master with its changed
// occurrences. UID is set by Parse: the identifier the client chose.
type Object struct {
	UID       string
	Master    calendar.Event
	Overrides []calendar.Event
}

// Group joins events (as stored) into objects, in the order of their masters.
func Group(events []calendar.Event) []Object {
	var out []Object
	at := map[string]int{}
	for _, e := range events {
		if e.SeriesID == "" {
			at[e.ID] = len(out)
			out = append(out, Object{UID: e.ID, Master: e})
		}
	}
	for _, e := range events {
		if i, ok := at[e.SeriesID]; ok && e.SeriesID != "" {
			out[i].Overrides = append(out[i].Overrides, e)
		}
	}
	return out
}

// ModTime is the latest change among the object's events.
func (o Object) ModTime() time.Time {
	t := o.Master.UpdatedAt
	for _, e := range o.Overrides {
		if e.UpdatedAt.After(t) {
			t = e.UpdatedAt
		}
	}
	return t
}

// Calendar renders the object as iCalendar. The same object always gives the
// same bytes, so the ETag follows the content.
func (o Object) Calendar() (*ical.Calendar, error) { return o.render(nil) }

// Audience tells what goes out for an event's audience: the value of
// X-HEJMO-AUDIENCE and the readable line at the end of the description. Both
// are empty for the whole family.
type Audience func(calendar.Event) (value, line string)

func (o Object) render(aud Audience) (*ical.Calendar, error) {
	cal := ical.NewCalendar()
	cal.Props.SetText(ical.PropVersion, "2.0")
	cal.Props.SetText(ical.PropProductID, prodID)

	events := append([]calendar.Event{o.Master}, o.Overrides...)
	zones := map[string]*time.Location{}
	minYear, maxYear := 9999, 0
	for _, e := range events {
		if e.AllDay || e.Start.Location() == time.UTC {
			continue
		}
		zones[e.Start.Location().String()] = e.Start.Location()
		if y := e.Start.Year(); y < minYear {
			minYear = y
		}
		if y := e.Start.Year(); y > maxYear {
			maxYear = y
		}
	}
	names := make([]string, 0, len(zones))
	for n := range zones {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		cal.Children = append(cal.Children, timezone(n, zones[n], minYear-1, max(maxYear, 2037)))
	}
	for _, e := range events {
		cal.Children = append(cal.Children, vevent(o.UID, o.Master.ID, e, aud))
	}
	return cal, nil
}

func vevent(uid, masterID string, e calendar.Event, aud Audience) *ical.Component {
	if uid == "" {
		uid = masterID
	}
	c := ical.NewComponent(ical.CompEvent)
	c.Props.SetText(ical.PropUID, uid)
	stamp := e.UpdatedAt
	if stamp.IsZero() {
		stamp = e.Start
	}
	for _, name := range []string{ical.PropDateTimeStamp, ical.PropLastModified} {
		p := ical.NewProp(name)
		p.Value = stamp.UTC().Format("20060102T150405Z")
		c.Props.Set(p)
	}
	c.Props.SetText(ical.PropSummary, e.Title)
	desc := e.Description
	if aud != nil {
		if value, line := aud(e); value != "" {
			desc = withAudienceLine(desc, line)
			ap := ical.NewProp(icsfeed.AudienceProp)
			ap.Value = value
			c.Props.Set(ap)
		}
	}
	if desc != "" {
		c.Props.SetText(ical.PropDescription, desc)
	}
	if e.Location != "" {
		c.Props.SetText(ical.PropLocation, e.Location)
	}
	if e.SeriesID != "" {
		c.Props.Set(dateProp(ical.PropRecurrenceID, e.RecurrenceID, e.AllDay))
	}
	c.Props.Set(dateProp(ical.PropDateTimeStart, e.Start, e.AllDay))
	c.Props.Set(dateProp(ical.PropDateTimeEnd, e.End, e.AllDay))
	if e.SeriesID == "" && e.RRule != "" {
		p := ical.NewProp(ical.PropRecurrenceRule)
		p.Value = e.RRule
		c.Props.Set(p)
		for _, x := range e.ExDates {
			c.Props.Add(dateProp(ical.PropExceptionDates, x, e.AllDay))
		}
	}
	return c
}

// dateProp writes a date or a date-time in its zone (UTC with a "Z").
func dateProp(name string, t time.Time, allDay bool) *ical.Prop {
	p := ical.NewProp(name)
	switch {
	case allDay:
		p.Params.Set(ical.ParamValue, "DATE")
		p.Value = t.Format("20060102")
	case t.Location() == time.UTC:
		p.Value = t.Format("20060102T150405Z")
	default:
		p.Params.Set(ical.PropTimezoneID, t.Location().String())
		p.Value = t.Format("20060102T150405")
	}
	return p
}

// timezone describes a zone's offsets year by year, so clients need no
// zone database of their own.
func timezone(name string, loc *time.Location, fromYear, toYear int) *ical.Component {
	tz := ical.NewComponent(ical.CompTimezone)
	tz.Props.SetText(ical.PropTimezoneID, name)
	utcOffset := func(sec int) string {
		sign := '+'
		if sec < 0 {
			sign, sec = '-', -sec
		}
		return fmt.Sprintf("%c%02d%02d", sign, sec/3600, sec%3600/60)
	}
	t := time.Date(fromYear, 1, 1, 0, 0, 0, 0, loc)
	prevOffset := 0
	for first := true; ; first = false {
		start, end := t.ZoneBounds()
		abbr, offset := t.Zone()
		kind := ical.CompTimezoneStandard
		if t.IsDST() {
			kind = ical.CompTimezoneDaylight
		}
		obs := ical.NewComponent(kind)
		from, begins := prevOffset, start
		if first {
			from, begins = offset, time.Date(1970, 1, 1, 0, 0, 0, 0, time.UTC).Add(time.Duration(offset)*time.Second)
		} else {
			begins = start.Add(time.Duration(prevOffset) * time.Second)
		}
		ds := ical.NewProp(ical.PropDateTimeStart)
		ds.Value = begins.UTC().Format("20060102T150405")
		obs.Props.Set(ds)
		for _, kv := range [][2]string{{ical.PropTimezoneOffsetFrom, utcOffset(from)}, {ical.PropTimezoneOffsetTo, utcOffset(offset)}, {ical.PropTimezoneName, abbr}} {
			p := ical.NewProp(kv[0])
			p.Value = kv[1]
			obs.Props.Set(p)
		}
		tz.Children = append(tz.Children, obs)
		prevOffset = offset
		if end.IsZero() || end.Year() > toYear {
			return tz
		}
		t = end
	}
}

// ETag identifies the content of a rendered object.
func ETag(cal *ical.Calendar) (string, error) {
	var buf bytes.Buffer
	if err := ical.NewEncoder(&buf).Encode(cal); err != nil {
		return "", err
	}
	sum := sha256.Sum256(buf.Bytes())
	return hex.EncodeToString(sum[:16]), nil
}

// Parse reads what a client sends: one event or series with its changed
// occurrences. Times in zones the box does not know, and floating times, are
// read in zone. Event ids are not set (the caller assigns them).
func Parse(cal *ical.Calendar, zone *time.Location) (Object, error) {
	// The encoder insists on a DTSTAMP, which some clients leave out.
	for _, c := range cal.Children {
		if c.Name == ical.CompEvent && c.Props.Get(ical.PropDateTimeStamp) == nil {
			p := ical.NewProp(ical.PropDateTimeStamp)
			p.Value = time.Now().UTC().Format("20060102T150405Z")
			c.Props.Set(p)
		}
	}
	var buf bytes.Buffer
	if err := ical.NewEncoder(&buf).Encode(cal); err != nil {
		return Object{}, fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	// Unlike a subscribed feed, nothing the client writes is cut off by age.
	res, err := icsfeed.Parse(buf.Bytes(), icsfeed.Options{Zone: zone, Now: time.Date(1900, 1, 1, 0, 0, 0, 0, time.UTC)})
	if err != nil {
		return Object{}, fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	var out Object
	hasMaster := false
	for _, it := range res.Items {
		if it.SeriesKey != "" {
			if out.UID != "" && out.UID != it.SeriesKey {
				return Object{}, fmt.Errorf("%w: one resource holds one event", ErrInvalid)
			}
			out.UID = it.SeriesKey
			o := it.Event
			o.SeriesID = ""
			out.Overrides = append(out.Overrides, o)
			continue
		}
		if hasMaster {
			return Object{}, fmt.Errorf("%w: one resource holds one event", ErrInvalid)
		}
		hasMaster = true
		out.Master, out.UID = it.Event, it.Key
	}
	if !hasMaster {
		return Object{}, fmt.Errorf("%w: no event found", ErrInvalid)
	}
	return out, nil
}

// withAudienceLine puts the readable audience line at the end of a description.
func withAudienceLine(desc, line string) string {
	switch {
	case line == "":
		return desc
	case strings.TrimSpace(desc) == "":
		return line
	}
	return strings.TrimRight(desc, " \t\r\n") + "\n\n" + line
}
