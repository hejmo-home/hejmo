// Package icsfeed turns an iCalendar (ICS) feed into calendar events. It is
// pure: bytes in, events out. Fetching and storing live elsewhere.
//
// Rules (ADR 0012): Windows time zone names map to IANA zones; floating
// times and unknown zones use the box's zone (unknown ones are noted for
// the sync log); only the last 12 months and the whole future are kept, and a
// series is kept whole if any occurrence falls in that window.
package icsfeed

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	ical "github.com/emersion/go-ical"
	"github.com/teambition/rrule-go"

	"github.com/hejmo-home/hejmo/internal/calendar"
)

// Options configures a parse.
type Options struct {
	// Zone is the box's time zone, used for floating times and unknown zones.
	Zone *time.Location
	// Now anchors the 12-month import window.
	Now time.Time
}

// Item is one imported event. Key is stable across syncs (the feed's UID,
// plus the original start for a changed occurrence); SeriesKey is the Key of
// the series master for a changed occurrence, else "".
type Item struct {
	Key       string
	SeriesKey string
	Event     calendar.Event
}

// Result is what a feed holds. Notes are readable remarks for the sync log
// (unknown time zones, events that could not be read).
type Result struct {
	Items []Item
	Notes []string
}

// NormalizeURL accepts http, https, webcal and webcals addresses and returns
// the http(s) address to fetch. webcal means https.
func NormalizeURL(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return "", errors.New("not a web address")
	}
	switch strings.ToLower(u.Scheme) {
	case "http", "https":
		u.Scheme = strings.ToLower(u.Scheme)
	case "webcal", "webcals":
		u.Scheme = "https"
	default:
		return "", fmt.Errorf("unsupported address scheme %q", u.Scheme)
	}
	return u.String(), nil
}

// Parse reads an ICS feed.
func Parse(data []byte, opts Options) (Result, error) {
	if opts.Zone == nil {
		opts.Zone = time.UTC
	}
	cal, err := ical.NewDecoder(bytes.NewReader(data)).Decode()
	if err != nil {
		return Result{}, fmt.Errorf("not an iCalendar feed: %w", err)
	}
	p := &parser{opts: opts, cutoff: opts.Now.AddDate(-1, 0, 0), masters: map[string]int{}, cancelledSeries: map[string]bool{}}
	for _, c := range cal.Children {
		if c.Name == ical.CompEvent {
			p.add(c)
		}
	}
	return p.finish(), nil
}

type parser struct {
	opts            Options
	cutoff          time.Time
	items           []Item
	masters         map[string]int // key -> index in items
	cancels         []cancel
	cancelledSeries map[string]bool // UIDs of cancelled masters
	notes           []string
	zones           map[string]bool // unknown zones already noted
}

type cancel struct {
	key string
	at  time.Time
}

func (p *parser) note(format string, args ...any) {
	p.notes = append(p.notes, fmt.Sprintf(format, args...))
}

func (p *parser) add(c *ical.Component) {
	uid, _ := c.Props.Text(ical.PropUID)
	title, _ := c.Props.Text(ical.PropSummary)
	label := strings.TrimSpace(title)
	if label == "" {
		label = uid
	}
	cancelled := strings.EqualFold(propValue(c, ical.PropStatus), "CANCELLED")

	startProp := c.Props.Get(ical.PropDateTimeStart)
	if startProp == nil {
		p.note("Skipped event %q: it has no start time.", label)
		return
	}
	start, allDay, err := p.parseTime(startProp)
	if err != nil {
		p.note("Skipped event %q: unreadable start time (%v).", label, err)
		return
	}
	var recID time.Time
	isOverride := false
	if rp := c.Props.Get(ical.PropRecurrenceID); rp != nil {
		recID, _, err = p.parseTime(rp)
		if err != nil {
			p.note("Skipped event %q: unreadable recurrence id (%v).", label, err)
			return
		}
		isOverride = true
	}
	if uid == "" {
		sum := sha256.Sum256([]byte(label + "|" + start.String()))
		uid = "nouid-" + hex.EncodeToString(sum[:8])
	}
	if isOverride && cancelled {
		p.cancels = append(p.cancels, cancel{uid, recID})
		return
	}
	if cancelled {
		p.cancelledSeries[uid] = true // its changed occurrences go with it (RFC 5545)
		return
	}

	end := p.eventEnd(c, start, allDay)
	e := calendar.Event{
		Title:       strings.TrimSpace(title),
		Description: text(c, ical.PropDescription),
		Location:    text(c, ical.PropLocation),
		AllDay:      allDay, Start: start, End: end,
	}
	if e.Title == "" {
		e.Title = "(no title)"
	}
	item := Item{Key: uid, Event: e}
	if isOverride {
		item.Key = uid + "#" + recID.UTC().Format(time.RFC3339)
		item.SeriesKey = uid
		item.Event.SeriesID = "-" // set by the store from SeriesKey
		item.Event.RecurrenceID = recID
	} else if rule := propValue(c, ical.PropRecurrenceRule); rule != "" {
		if err := calendar.ValidateRRule(rule); err != nil {
			p.note("Skipped event %q: unreadable repeat rule (%v).", label, err)
			return
		}
		item.Event.RRule = rule
		item.Event.ExDates = p.exDates(c)
	}
	if i, dup := p.masters[item.Key]; dup {
		p.items[i] = item // the last one wins
		return
	}
	p.masters[item.Key] = len(p.items)
	p.items = append(p.items, item)
}

func (p *parser) finish() Result {
	for _, c := range p.cancels {
		if i, ok := p.masters[c.key]; ok {
			p.items[i].Event.ExDates = append(p.items[i].Event.ExDates, c.at)
		}
	}
	var out []Item
	keptSeries := map[string]bool{}
	for _, it := range p.items {
		if it.SeriesKey == "" && p.inWindow(it.Event) {
			keptSeries[it.Key] = true
		}
	}
	for _, it := range p.items {
		switch {
		case it.SeriesKey == "" && keptSeries[it.Key]:
			out = append(out, it)
		case it.SeriesKey != "" && keptSeries[it.SeriesKey]:
			out = append(out, it)
		case it.SeriesKey != "":
			if _, hasMaster := p.masters[it.SeriesKey]; !hasMaster && !p.cancelledSeries[it.SeriesKey] && p.inWindow(it.Event) {
				it.SeriesKey, it.Event.SeriesID = "", ""
				out = append(out, it) // orphaned change: keep as a single event
			}
		}
	}
	return Result{Items: out, Notes: p.notes}
}

// inWindow reports whether an event, or any occurrence of a series, ends
// after the 12-month cutoff.
func (p *parser) inWindow(e calendar.Event) bool {
	if e.RRule == "" {
		return e.End.After(p.cutoff)
	}
	opt, err := rrule.StrToROption(e.RRule)
	if err != nil {
		return false
	}
	opt.Dtstart = e.Start
	r, err := rrule.NewRRule(*opt)
	if err != nil {
		return false
	}
	return !r.After(p.cutoff.Add(-e.End.Sub(e.Start)).In(e.Start.Location()), true).IsZero()
}

func (p *parser) eventEnd(c *ical.Component, start time.Time, allDay bool) time.Time {
	var end time.Time
	if ep := c.Props.Get(ical.PropDateTimeEnd); ep != nil {
		if t, _, err := p.parseTime(ep); err == nil {
			end = t
		}
	} else if dp := c.Props.Get(ical.PropDuration); dp != nil {
		if d, err := dp.Duration(); err == nil {
			end = start.Add(d)
		}
	}
	if !end.After(start) {
		if allDay {
			return start.AddDate(0, 0, 1)
		}
		return start.Add(time.Hour) // iCalendar says zero length; a visible hour is kinder
	}
	return end
}

func (p *parser) exDates(c *ical.Component) []time.Time {
	var out []time.Time
	for _, prop := range c.Props.Values(ical.PropExceptionDates) {
		for _, v := range strings.Split(prop.Value, ",") {
			one := prop
			one.Value = strings.TrimSpace(v)
			if t, _, err := p.parseTime(&one); err == nil {
				out = append(out, t)
			}
		}
	}
	return out
}

// parseTime reads a DATE or DATE-TIME property in its zone.
func (p *parser) parseTime(prop *ical.Prop) (t time.Time, allDay bool, err error) {
	v := strings.TrimSpace(prop.Value)
	switch {
	case strings.EqualFold(prop.Params.Get("VALUE"), "DATE") || len(v) == len("20060102"):
		t, err = time.ParseInLocation("20060102", v, p.opts.Zone)
		return t, true, err
	case strings.HasSuffix(v, "Z"):
		t, err = time.ParseInLocation("20060102T150405Z", v, time.UTC)
		return t, false, err
	}
	loc := p.opts.Zone
	if tzid := prop.Params.Get(ical.PropTimezoneID); tzid != "" {
		loc = p.zone(tzid)
	}
	t, err = time.ParseInLocation("20060102T150405", v, loc)
	return t, false, err
}

func (p *parser) zone(tzid string) *time.Location {
	tzid = strings.Trim(strings.TrimSpace(tzid), `"`)
	if loc, ok := lookupZone(tzid); ok {
		return loc
	}
	if !p.zones[tzid] {
		if p.zones == nil {
			p.zones = map[string]bool{}
		}
		p.zones[tzid] = true
		p.note("Unknown time zone %q: used the box's time zone instead.", tzid)
	}
	return p.opts.Zone
}

func lookupZone(tzid string) (*time.Location, bool) {
	if iana, ok := WindowsZones[tzid]; ok {
		tzid = iana
	}
	// Some producers prefix IANA names with a path ("/example.org/Europe/Berlin").
	for name := tzid; name != ""; {
		if loc, err := time.LoadLocation(name); err == nil && name != "Local" {
			return loc, true
		}
		_, rest, found := strings.Cut(name, "/")
		if !found {
			break
		}
		name = rest
	}
	return nil, false
}

func propValue(c *ical.Component, name string) string {
	if p := c.Props.Get(name); p != nil {
		return strings.TrimSpace(p.Value)
	}
	return ""
}

func text(c *ical.Component, name string) string {
	s, err := c.Props.Text(name)
	if err != nil {
		return propValue(c, name)
	}
	return strings.TrimSpace(s)
}
