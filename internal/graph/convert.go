package graph

import (
	"errors"
	"fmt"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/teambition/rrule-go"

	"github.com/hejmo-home/hejmo/internal/calendar"
	"github.com/hejmo-home/hejmo/internal/icsfeed"
)

// Event is a Graph event as far as the box reads it.
type Event struct {
	ID             string          `json:"id"`
	SeriesMasterID string          `json:"seriesMasterId"`
	Type           string          `json:"type"` // singleInstance, occurrence, exception, seriesMaster
	Subject        string          `json:"subject"`
	Body           *Body           `json:"body"`
	Location       *Location       `json:"location"`
	Start          *DateTimeZone   `json:"start"`
	End            *DateTimeZone   `json:"end"`
	OriginalStart  string          `json:"originalStart"`
	IsAllDay       bool            `json:"isAllDay"`
	IsCancelled    bool            `json:"isCancelled"`
	Recurrence     *Recurrence     `json:"recurrence"`
	Attendees      []Attendee      `json:"attendees"`
	Organizer      *Recipient      `json:"organizer"`
	ResponseStatus *ResponseStatus `json:"responseStatus"`
	WebLink        string          `json:"webLink"`
	ChangeKey      string          `json:"changeKey"`
	LastModified   string          `json:"lastModifiedDateTime"`
	Removed        *struct {
		Reason string `json:"reason"`
	} `json:"@removed"`
}

// Body is an event's notes.
type Body struct {
	ContentType string `json:"contentType"`
	Content     string `json:"content"`
}

// Location is an event's place.
type Location struct {
	DisplayName string `json:"displayName"`
}

// DateTimeZone is a local time with its zone name (Windows or IANA).
type DateTimeZone struct {
	DateTime string `json:"dateTime"`
	TimeZone string `json:"timeZone"`
}

// Attendee is one invited person.
type Attendee struct {
	EmailAddress Email `json:"emailAddress"`
}

// Recipient is a person with an address.
type Recipient struct {
	EmailAddress Email `json:"emailAddress"`
}

// Email is a name and an address.
type Email struct {
	Name    string `json:"name"`
	Address string `json:"address"`
}

// ResponseStatus is the account's answer to an invitation.
type ResponseStatus struct {
	Response string `json:"response"`
}

// Recurrence is a Graph recurrence: a pattern and a range.
type Recurrence struct {
	Pattern Pattern `json:"pattern"`
	Range   Range   `json:"range"`
}

// Pattern says how often an event repeats.
type Pattern struct {
	Type           string   `json:"type"`
	Interval       int      `json:"interval"`
	DaysOfWeek     []string `json:"daysOfWeek,omitempty"`
	FirstDayOfWeek string   `json:"firstDayOfWeek,omitempty"`
	DayOfMonth     int      `json:"dayOfMonth,omitempty"`
	Month          int      `json:"month,omitempty"`
	Index          string   `json:"index,omitempty"`
}

// Range says when a series starts and ends.
type Range struct {
	Type                string `json:"type"`
	StartDate           string `json:"startDate,omitempty"`
	EndDate             string `json:"endDate,omitempty"`
	NumberOfOccurrences int    `json:"numberOfOccurrences,omitempty"`
}

// Item is an event of the box with the key it is known by outside: a Graph id
// for a single event or a series, the series id plus the original start for a
// changed occurrence (SeriesKey names the series).
type Item struct {
	Key, SeriesKey string
	Event          calendar.Event
	// UnknownZone names a time zone the box did not know; the household's was used.
	UnknownZone string
}

var days = []struct{ graph, ical string }{
	{"monday", "MO"}, {"tuesday", "TU"}, {"wednesday", "WE"}, {"thursday", "TH"}, {"friday", "FR"}, {"saturday", "SA"}, {"sunday", "SU"},
}

var indexes = map[string]string{"first": "1", "second": "2", "third": "3", "fourth": "4", "last": "-1"}

func icalDay(g string) string {
	for _, d := range days {
		if d.graph == strings.ToLower(g) {
			return d.ical
		}
	}
	return ""
}

// RRuleOf writes a Graph recurrence as an RRULE. An end date counts to the end
// of that day in zone. UNTIL has the value type of the series' DTSTART: a date
// for an all-day series, a UTC date-time for a timed one (RFC 5545, 3.3.10).
func RRuleOf(r Recurrence, zone *time.Location, allDay bool) (string, error) {
	p := r.Pattern
	n := max(p.Interval, 1)
	var parts []string
	var byday []string
	for _, d := range p.DaysOfWeek {
		ic := icalDay(d)
		if ic == "" {
			return "", fmt.Errorf("graph: unknown weekday %q", d)
		}
		byday = append(byday, ic)
	}
	switch p.Type {
	case "daily":
		parts = []string{"FREQ=DAILY", "INTERVAL=" + strconv.Itoa(n)}
	case "weekly":
		parts = []string{"FREQ=WEEKLY", "INTERVAL=" + strconv.Itoa(n)}
		if len(byday) > 0 {
			parts = append(parts, "BYDAY="+strings.Join(byday, ","))
		}
		if wk := icalDay(p.FirstDayOfWeek); wk != "" {
			parts = append(parts, "WKST="+wk)
		}
	case "absoluteMonthly":
		parts = []string{"FREQ=MONTHLY", "INTERVAL=" + strconv.Itoa(n), "BYMONTHDAY=" + strconv.Itoa(p.DayOfMonth)}
	case "relativeMonthly":
		parts = []string{"FREQ=MONTHLY", "INTERVAL=" + strconv.Itoa(n), "BYDAY=" + indexed(p.Index, byday)}
	case "absoluteYearly":
		parts = []string{"FREQ=YEARLY", "INTERVAL=" + strconv.Itoa(n), "BYMONTH=" + strconv.Itoa(p.Month), "BYMONTHDAY=" + strconv.Itoa(p.DayOfMonth)}
	case "relativeYearly":
		parts = []string{"FREQ=YEARLY", "INTERVAL=" + strconv.Itoa(n), "BYMONTH=" + strconv.Itoa(p.Month), "BYDAY=" + indexed(p.Index, byday)}
	default:
		return "", fmt.Errorf("graph: unknown recurrence %q", p.Type)
	}
	switch r.Range.Type {
	case "numbered":
		parts = append(parts, "COUNT="+strconv.Itoa(r.Range.NumberOfOccurrences))
	case "endDate":
		d, err := time.ParseInLocation("2006-01-02", r.Range.EndDate, zone)
		if err != nil {
			return "", err
		}
		until := time.Date(d.Year(), d.Month(), d.Day(), 23, 59, 59, 0, zone)
		parts = append(parts, "UNTIL="+calendar.UntilValue(until, allDay))
	}
	return strings.Join(parts, ";"), nil
}

func indexed(index string, days []string) string {
	pre := indexes[strings.ToLower(index)]
	var out []string
	for _, d := range days {
		out = append(out, pre+d)
	}
	return strings.Join(out, ",")
}

// RecurrenceOf writes an RRULE as a Graph recurrence starting on start's day.
// A rule Outlook cannot express is an error.
func RecurrenceOf(rule string, start time.Time) (*Recurrence, error) {
	o, err := calendar.ParseRule(rule, start.Location())
	if err != nil {
		return nil, err
	}
	rec := &Recurrence{Pattern: Pattern{Interval: max(o.Interval, 1)}, Range: Range{Type: "noEnd", StartDate: start.Format("2006-01-02")}}
	var wd []string
	idx := ""
	for _, w := range o.Byweekday {
		wd = append(wd, days[w.Day()].graph)
		if w.N() != 0 {
			idx = indexName(w.N())
		}
	}
	for i, d := range wd {
		if d == "" {
			return nil, fmt.Errorf("graph: weekday %d of %q", i, rule)
		}
	}
	switch o.Freq {
	case rrule.DAILY:
		rec.Pattern.Type = "daily"
	case rrule.WEEKLY:
		rec.Pattern.Type = "weekly"
		if len(wd) == 0 {
			wd = []string{strings.ToLower(start.Weekday().String())}
		}
		rec.Pattern.DaysOfWeek, rec.Pattern.FirstDayOfWeek = wd, "monday"
	case rrule.MONTHLY:
		switch {
		case idx != "":
			rec.Pattern.Type, rec.Pattern.DaysOfWeek, rec.Pattern.Index = "relativeMonthly", wd, idx
		case len(o.Bymonthday) > 0:
			rec.Pattern.Type, rec.Pattern.DayOfMonth = "absoluteMonthly", o.Bymonthday[0]
		default:
			rec.Pattern.Type, rec.Pattern.DayOfMonth = "absoluteMonthly", start.Day()
		}
	case rrule.YEARLY:
		rec.Pattern.Month = int(start.Month())
		if len(o.Bymonth) > 0 {
			rec.Pattern.Month = o.Bymonth[0]
		}
		switch {
		case idx != "":
			rec.Pattern.Type, rec.Pattern.DaysOfWeek, rec.Pattern.Index = "relativeYearly", wd, idx
		case len(o.Bymonthday) > 0:
			rec.Pattern.Type, rec.Pattern.DayOfMonth = "absoluteYearly", o.Bymonthday[0]
		default:
			rec.Pattern.Type, rec.Pattern.DayOfMonth = "absoluteYearly", start.Day()
		}
	default:
		return nil, errors.New("graph: Outlook cannot repeat an event this way")
	}
	switch {
	case o.Count > 0:
		rec.Range.Type, rec.Range.NumberOfOccurrences = "numbered", o.Count
	case !o.Until.IsZero():
		rec.Range.Type, rec.Range.EndDate = "endDate", o.Until.In(start.Location()).Format("2006-01-02")
	}
	return rec, nil
}

func indexName(n int) string {
	for k, v := range indexes {
		if v == strconv.Itoa(n) {
			return k
		}
	}
	return ""
}

func parseZone(name string, household *time.Location) (*time.Location, string) {
	if name == "" || name == "UTC" {
		return time.UTC, ""
	}
	if iana, ok := icsfeed.WindowsZones[name]; ok {
		name = iana
	}
	if loc, err := time.LoadLocation(name); err == nil && name != "Local" {
		return loc, ""
	}
	return household, name
}

func (d *DateTimeZone) parse(household *time.Location) (time.Time, string, error) {
	if d == nil {
		return time.Time{}, "", errors.New("graph: event without a time")
	}
	loc, unknown := parseZone(d.TimeZone, household)
	s := d.DateTime
	if i := strings.IndexByte(s, '.'); i >= 0 {
		s = s[:i]
	}
	t, err := time.ParseInLocation("2006-01-02T15:04:05", strings.TrimSuffix(s, "Z"), loc)
	return t, unknown, err
}

// ToEvent turns a Graph single event, occurrence, exception or series master
// into an event of the box. household is the zone for times Graph gives in a
// zone the box does not know.
func ToEvent(g Event, household *time.Location) (Item, error) {
	start, unknown, err := g.Start.parse(household)
	if err != nil {
		return Item{}, err
	}
	end, _, err := g.End.parse(household)
	if err != nil {
		return Item{}, err
	}
	e := calendar.Event{Title: g.Subject, AllDay: g.IsAllDay, Start: start, End: end}
	if g.Body != nil {
		e.Description = strings.TrimSpace(strings.ReplaceAll(g.Body.Content, "\r\n", "\n"))
	}
	if g.Location != nil {
		e.Location = g.Location.DisplayName
	}
	if g.Recurrence != nil && g.Type == "seriesMaster" {
		if e.RRule, err = RRuleOf(*g.Recurrence, start.Location(), g.IsAllDay); err != nil {
			return Item{}, err
		}
	}
	if len(g.Attendees) > 0 {
		inv := &calendar.Invitation{WebLink: g.WebLink}
		if g.Organizer != nil {
			inv.Organizer = g.Organizer.EmailAddress.Name
			if inv.Organizer == "" {
				inv.Organizer = g.Organizer.EmailAddress.Address
			}
		}
		if g.ResponseStatus != nil {
			inv.Response = g.ResponseStatus.Response
		}
		e.Invitation = inv
	}
	return Item{Key: g.ID, Event: e, UnknownZone: unknown}, nil
}

// OverrideKey is the key of a changed occurrence: its series and original start.
func OverrideKey(seriesID string, original time.Time) string {
	return seriesID + "#" + original.UTC().Format(time.RFC3339)
}

// SeriesItems turns a series master and its instances in [from, to) into the
// master, one override per exception, and an EXDATE for every occurrence the
// rule gives that Outlook no longer has.
func SeriesItems(master Event, instances []Event, household *time.Location, from, to time.Time) ([]Item, error) {
	m, err := ToEvent(master, household)
	if err != nil {
		return nil, err
	}
	m.Event.ID = "series"
	items := []Item{{}}
	present := map[int64]bool{}
	for _, in := range instances {
		if in.SeriesMasterID != "" && in.SeriesMasterID != master.ID {
			continue
		}
		switch in.Type {
		case "exception":
			orig, err := time.Parse(time.RFC3339, in.OriginalStart)
			if err != nil {
				return nil, fmt.Errorf("graph: original start of %s: %w", in.ID, err)
			}
			present[orig.Unix()] = true
			it, err := ToEvent(in, household)
			if err != nil {
				return nil, err
			}
			it.Key, it.SeriesKey = OverrideKey(master.ID, orig), master.ID
			it.Event.RecurrenceID = orig.In(m.Event.Start.Location())
			it.Event.RRule = ""
			items = append(items, it)
		default:
			s, _, err := in.Start.parse(household)
			if err != nil {
				return nil, err
			}
			present[s.Unix()] = true
		}
	}
	if m.Event.RRule != "" {
		occ, err := calendar.Expand([]calendar.Event{m.Event}, from, to)
		if err != nil {
			return nil, err
		}
		for _, o := range occ {
			if !present[o.Start.Unix()] && !o.Start.Before(from) && o.Start.Before(to) {
				m.Event.ExDates = append(m.Event.ExDates, o.Start)
			}
		}
		sort.Slice(m.Event.ExDates, func(i, j int) bool { return m.Event.ExDates[i].Before(m.Event.ExDates[j]) })
	}
	m.Event.ID = ""
	m.Key = master.ID
	items[0] = m
	return items, nil
}

func zoneName(loc *time.Location) string {
	name := loc.String()
	var keys []string
	for k := range icsfeed.WindowsZones {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		if icsfeed.WindowsZones[k] == name {
			return k
		}
	}
	return name
}

// BodyOf writes a box event as the fields of a Graph create or update. The
// audience line, if any, goes at the end of the notes. A changed occurrence
// carries no recurrence.
func BodyOf(e calendar.Event, audienceLine string) (map[string]any, error) {
	layout := "2006-01-02T15:04:05"
	zone := zoneName(e.Start.Location())
	content := e.Description
	if audienceLine != "" {
		if content != "" {
			content += "\n"
		}
		content += audienceLine
	}
	body := map[string]any{
		"subject":  e.Title,
		"body":     map[string]any{"contentType": "text", "content": content},
		"location": map[string]any{"displayName": e.Location},
		"isAllDay": e.AllDay,
		"start":    map[string]any{"dateTime": e.Start.Format(layout), "timeZone": zone},
		"end":      map[string]any{"dateTime": e.End.Format(layout), "timeZone": zoneName(e.End.Location())},
	}
	if e.RRule != "" && e.SeriesID == "" {
		rec, err := RecurrenceOf(e.RRule, e.Start)
		if err != nil {
			return nil, err
		}
		body["recurrence"] = rec
	}
	return body, nil
}

func plain(s string) string { return strings.TrimSpace(strings.ReplaceAll(s, "\r\n", "\n")) }

// ChangesOf is BodyOf for an event Outlook already holds: only the fields the
// box models and that differ from remote. What the box does not model (the
// formatting of the notes, a location's address and coordinates) stays as
// Outlook has it. The result is empty when nothing changed. household is the
// zone remote's times are in.
func ChangesOf(e calendar.Event, audienceLine string, remote Event, household *time.Location) (map[string]any, error) {
	full, err := BodyOf(e, audienceLine)
	if err != nil {
		return nil, err
	}
	out := map[string]any{}
	if remote.Subject != e.Title {
		out["subject"] = full["subject"]
	}
	have := ""
	if remote.Body != nil {
		have = plain(remote.Body.Content)
	}
	if want := plain(full["body"].(map[string]any)["content"].(string)); want != have {
		out["body"] = full["body"]
	}
	place := ""
	if remote.Location != nil {
		place = remote.Location.DisplayName
	}
	if place != e.Location {
		out["location"] = full["location"]
	}
	if remote.IsAllDay != e.AllDay {
		out["isAllDay"] = full["isAllDay"]
	}
	rs, _, serr := remote.Start.parse(household)
	re, _, eerr := remote.End.parse(household)
	if serr != nil || eerr != nil || !rs.Equal(e.Start) || !re.Equal(e.End) || remote.IsAllDay != e.AllDay {
		out["start"], out["end"] = full["start"], full["end"]
	}
	if rec, ok := full["recurrence"]; ok {
		if serr != nil || remote.Recurrence == nil || !sameRecurrence(rec.(*Recurrence), *remote.Recurrence, rs, remote.IsAllDay) {
			out["recurrence"] = rec
		}
	}
	return out, nil
}

// sameRecurrence compares a recurrence the box wrote with the one Outlook
// has, both brought into the same form.
func sameRecurrence(want *Recurrence, have Recurrence, start time.Time, allDay bool) bool {
	rule, err := RRuleOf(have, start.Location(), allDay)
	if err != nil {
		return false
	}
	h, err := RecurrenceOf(rule, start)
	if err != nil {
		return false
	}
	a, b := *want, *h
	a.Pattern.DaysOfWeek = sortedCopy(a.Pattern.DaysOfWeek)
	b.Pattern.DaysOfWeek = sortedCopy(b.Pattern.DaysOfWeek)
	return reflect.DeepEqual(a, b)
}

func sortedCopy(in []string) []string {
	out := append([]string(nil), in...)
	sort.Strings(out)
	return out
}
