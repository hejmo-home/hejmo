package caldav

import (
	"bytes"
	"errors"
	"fmt"
	"slices"
	"sort"
	"strings"
	"time"

	ical "github.com/emersion/go-ical"

	"github.com/hejmo-home/hejmo/internal/calendar"
	"github.com/hejmo-home/hejmo/internal/icsfeed"
)

// PatchOptions configures Patch.
type PatchOptions struct {
	// Zone is the box's time zone (floating times, unknown zones).
	Zone *time.Location
	// Now stamps DTSTAMP and LAST-MODIFIED of every event that changes.
	Now time.Time
	// AudienceLines are the beginnings of the readable audience line in every
	// language, so an existing line is recognised (see icsfeed.Options).
	AudienceLines []string
	// Audience says what goes out for an event's audience; nil sends none.
	Audience Audience
}

// ErrUnpatchable means the stored text cannot be changed to match the box:
// an event it holds cannot be found in it again.
var ErrUnpatchable = errors.New("caldav: the stored calendar object cannot be patched")

// Patch changes the stored raw iCalendar text of an object so that it says
// what the box says, and nothing more (ADR 0012). It compares the events the
// text holds with the box's events and rewrites only the lines of the fields
// the box models (title, description, location, start, end, repeat rule,
// excluded dates, audience) that differ. Every other line (attendees,
// alarms, duplicates iCloud keeps, unknown properties) stays byte for byte.
// With no raw text the object is rendered from scratch. An object that
// already says what the box says comes back unchanged.
func Patch(raw string, o Object, opts PatchOptions) (string, error) {
	if opts.Zone == nil {
		opts.Zone = time.UTC
	}
	if strings.TrimSpace(raw) == "" {
		cal, err := o.render(opts.Audience)
		if err != nil {
			return "", err
		}
		var buf bytes.Buffer
		if err := ical.NewEncoder(&buf).Encode(cal); err != nil {
			return "", err
		}
		return buf.String(), nil
	}
	res, err := icsfeed.Parse([]byte(raw), icsfeed.Options{Zone: opts.Zone, Now: time.Date(1900, 1, 1, 0, 0, 0, 0, time.UTC), AudienceLines: opts.AudienceLines})
	if err != nil {
		return "", fmt.Errorf("%w: %v", ErrUnpatchable, err)
	}
	old := map[string]calendar.Event{}
	for _, it := range res.Items {
		old[it.Key] = it.Event
	}
	want := map[string]calendar.Event{o.UID: o.Master}
	for _, ov := range o.Overrides {
		want[overrideKey(o.UID, ov.RecurrenceID)] = ov
	}

	d := scan(raw)
	p := &patcher{opts: opts, doc: d}
	var out []entry
	seen := map[string]bool{}
	blocks := d.eventBlocks(opts.Zone)
	// The last block of a key is the one the import reads.
	last := map[string]int{}
	for i, b := range blocks {
		last[b.key] = i
	}
	pos := 0
	for i, b := range blocks {
		out = append(out, d.entries[pos:b.lo]...)
		pos = b.hi + 1
		block := d.entries[b.lo : b.hi+1]
		oldEvent, isOld := old[b.key]
		if !isOld || last[b.key] != i {
			out = append(out, block...) // cancelled, unreadable or shadowed: not ours
			continue
		}
		seen[b.key] = true
		newEvent, wanted := want[b.key]
		if !wanted {
			continue // the box no longer has this event
		}
		out = append(out, p.patchBlock(block, oldEvent, newEvent)...)
	}
	out = append(out, d.entries[pos:]...)
	d.entries = out

	var added []string
	for _, key := range sortedKeys(want) {
		if seen[key] {
			continue
		}
		if _, was := old[key]; was {
			return "", fmt.Errorf("%w: event %s is in the data but not in a readable block", ErrUnpatchable, key)
		}
		added = append(added, key)
	}
	for _, key := range added {
		e := want[key]
		if key != o.UID {
			e.SeriesID = "-" // a changed occurrence is written with its RECURRENCE-ID
		}
		comp := vevent(o.UID, o.Master.ID, e, opts.Audience)
		text, err := encodeComponent(comp)
		if err != nil {
			return "", err
		}
		p.noteZones(e)
		d.insertBeforeEnd(scan(text).entries)
	}
	p.addZones()
	return d.String(), nil
}

func overrideKey(uid string, recurrenceID time.Time) string {
	return uid + "#" + recurrenceID.UTC().Format(time.RFC3339)
}

func sortedKeys(m map[string]calendar.Event) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// encodeComponent renders one VEVENT as text.
func encodeComponent(c *ical.Component) (string, error) {
	cal := ical.NewCalendar()
	cal.Props.SetText(ical.PropVersion, "2.0")
	cal.Props.SetText(ical.PropProductID, prodID)
	cal.Children = append(cal.Children, c)
	var buf bytes.Buffer
	if err := ical.NewEncoder(&buf).Encode(cal); err != nil {
		return "", err
	}
	s := buf.String()
	from := strings.Index(s, "BEGIN:"+c.Name)
	to := strings.LastIndex(s, "END:VCALENDAR")
	return s[from:to], nil
}

type patcher struct {
	opts  PatchOptions
	doc   *doc
	zones map[string]*time.Location // zones the patch wrote
	years [2]int
}

func (p *patcher) noteZones(e calendar.Event) {
	p.noteZone(e.Start, e.AllDay)
	for _, x := range e.ExDates {
		p.noteZone(x, e.AllDay)
	}
	if !e.RecurrenceID.IsZero() {
		p.noteZone(e.RecurrenceID, e.AllDay)
	}
}

func (p *patcher) noteZone(t time.Time, allDay bool) {
	if allDay || t.Location() == time.UTC || t.Location() == time.Local {
		return
	}
	if p.zones == nil {
		p.zones = map[string]*time.Location{}
		p.years = [2]int{9999, 0}
	}
	p.zones[t.Location().String()] = t.Location()
	p.years[0], p.years[1] = min(p.years[0], t.Year()), max(p.years[1], t.Year())
}

// addZones defines the time zones the patch wrote and the text does not yet
// define, so clients need no zone database.
func (p *patcher) addZones() {
	defined := map[string]bool{}
	for _, e := range p.doc.entries {
		if e.name == "TZID" && e.depth == 2 {
			defined[strings.Trim(e.value, `"`)] = true
		}
	}
	var names []string
	for n := range p.zones {
		if !defined[n] {
			names = append(names, n)
		}
	}
	sort.Strings(names)
	var add []entry
	for _, n := range names {
		text, err := encodeComponent(timezone(n, p.zones[n], p.years[0]-1, max(p.years[1], 2037)))
		if err != nil {
			continue
		}
		add = append(add, scan(text).entries...)
	}
	p.doc.insertBeforeFirstComponent(add)
}

// patchBlock rewrites the lines of one VEVENT block for the fields that differ.
func (p *patcher) patchBlock(block []entry, old, want calendar.Event) []entry {
	var changed, significant bool
	set := func(name string, lines ...string) {
		block = setProp(block, name, lines)
		changed = true
	}
	trim := strings.TrimSpace
	if trim(old.Title) != trim(want.Title) {
		set(ical.PropSummary, textLine(ical.PropSummary, want.Title))
	}
	if trim(old.Location) != trim(want.Location) {
		if trim(want.Location) == "" {
			set(ical.PropLocation)
		} else {
			set(ical.PropLocation, textLine(ical.PropLocation, want.Location))
		}
	}
	value, line := "", ""
	if p.opts.Audience != nil {
		value, line = p.opts.Audience(want)
	}
	oldAud := icsfeed.FormatAudience(sortedCopy(old.People), sortedCopy(old.Groups))
	wantAud := icsfeed.FormatAudience(sortedCopy(want.People), sortedCopy(want.Groups))
	if trim(old.Description) != trim(want.Description) || oldAud != wantAud {
		desc := withAudienceLine(trim(want.Description), line)
		if desc == "" {
			set(ical.PropDescription)
		} else {
			set(ical.PropDescription, textLine(ical.PropDescription, desc))
		}
		if value == "" {
			set(icsfeed.AudienceProp)
		} else {
			set(icsfeed.AudienceProp, formatLine(icsfeed.AudienceProp, "", value))
		}
	}
	sameTimes := old.AllDay == want.AllDay && old.Start.Equal(want.Start) && old.End.Equal(want.End)
	if !sameTimes {
		significant = true
		set(ical.PropDateTimeStart, dateLine(ical.PropDateTimeStart, want.Start, want.AllDay))
		set(ical.PropDuration)
		set(ical.PropDateTimeEnd, dateLine(ical.PropDateTimeEnd, want.End, want.AllDay))
		p.noteZones(want)
	}
	if want.SeriesID == "" && want.RRule != "" || old.RRule != "" {
		if old.RRule != want.RRule {
			significant = true
			if want.RRule == "" {
				set(ical.PropRecurrenceRule)
			} else {
				set(ical.PropRecurrenceRule, formatLine(ical.PropRecurrenceRule, "", want.RRule))
			}
		}
		if !sameInstants(old.ExDates, want.ExDates) {
			var lines []string
			for _, x := range want.ExDates {
				lines = append(lines, dateLine(ical.PropExceptionDates, x, want.AllDay))
			}
			set(ical.PropExceptionDates, lines...)
			p.noteZones(want)
		}
	}
	if !changed {
		return block
	}
	stamp := p.opts.Now.UTC().Format("20060102T150405Z")
	block = setProp(block, ical.PropDateTimeStamp, []string{formatLine(ical.PropDateTimeStamp, "", stamp)})
	block = setProp(block, ical.PropLastModified, []string{formatLine(ical.PropLastModified, "", stamp)})
	return bumpSequence(block, significant)
}

func sortedCopy(s []string) []string {
	c := slices.Clone(s)
	sort.Strings(c)
	return c
}

func sameInstants(a, b []time.Time) bool {
	if len(a) != len(b) {
		return false
	}
	ua, ub := map[int64]bool{}, map[int64]bool{}
	for _, t := range a {
		ua[t.Unix()] = true
	}
	for _, t := range b {
		ub[t.Unix()] = true
	}
	if len(ua) != len(ub) {
		return false
	}
	for k := range ua {
		if !ub[k] {
			return false
		}
	}
	return true
}

// entry is one logical line of the text with the physical lines it was
// written on (folded lines included), exactly as they were.
type entry struct {
	text   string
	name   string // upper case
	params string // between the name and the colon, with its leading ";"
	value  string // unfolded
	depth  int    // components open before a BEGIN, after an END, around other lines
}

type doc struct {
	entries []entry
	eol     string
}

func (d *doc) String() string {
	var b strings.Builder
	for _, e := range d.entries {
		b.WriteString(e.text)
	}
	return b.String()
}

// scan splits text into logical lines.
func scan(raw string) *doc {
	d := &doc{eol: "\r\n"}
	if !strings.Contains(raw, "\r\n") && strings.Contains(raw, "\n") {
		d.eol = "\n"
	}
	var physical []string
	rest := raw
	for rest != "" {
		i := strings.Index(rest, "\n")
		if i < 0 {
			physical, rest = append(physical, rest), ""
			continue
		}
		physical, rest = append(physical, rest[:i+1]), rest[i+1:]
	}
	depth := 0
	for _, ph := range physical {
		content := strings.TrimRight(ph, "\r\n")
		if len(d.entries) > 0 && (strings.HasPrefix(content, " ") || strings.HasPrefix(content, "\t")) {
			last := &d.entries[len(d.entries)-1]
			if last.text != "" && strings.HasSuffix(last.text, "\n") {
				last.text += ph
				last.value += content[1:]
				continue
			}
		}
		e := entry{text: ph}
		name, params, value := splitLine(content)
		e.name, e.params, e.value = strings.ToUpper(name), params, value
		switch e.name {
		case "BEGIN":
			e.depth = depth
			depth++
		case "END":
			depth--
			e.depth = depth
		default:
			e.depth = depth
		}
		d.entries = append(d.entries, e)
	}
	return d
}

// splitLine splits "NAME;PARAM=x:value" at the first colon outside quotes.
func splitLine(s string) (name, params, value string) {
	inQuote := false
	end := -1
	for i, r := range s {
		switch {
		case r == '"':
			inQuote = !inQuote
		case r == ':' && !inQuote:
			end = i
		}
		if end >= 0 {
			break
		}
	}
	if end < 0 {
		return s, "", ""
	}
	head, value := s[:end], s[end+1:]
	name, params, _ = strings.Cut(head, ";")
	if params != "" {
		params = ";" + params
	}
	return name, params, value
}

func (e entry) param(name string) string {
	for _, part := range splitParams(e.params) {
		k, v, _ := strings.Cut(part, "=")
		if strings.EqualFold(k, name) {
			return strings.Trim(v, `"`)
		}
	}
	return ""
}

func splitParams(params string) []string {
	var out []string
	inQuote := false
	start := 0
	params = strings.TrimPrefix(params, ";")
	for i, r := range params {
		switch {
		case r == '"':
			inQuote = !inQuote
		case r == ';' && !inQuote:
			out = append(out, params[start:i])
			start = i + 1
		}
	}
	if params != "" {
		out = append(out, params[start:])
	}
	return out
}

func (e entry) prop() *ical.Prop {
	p := ical.NewProp(e.name)
	p.Value = e.value
	for _, part := range splitParams(e.params) {
		k, v, _ := strings.Cut(part, "=")
		p.Params.Set(k, strings.Trim(v, `"`))
	}
	return p
}

type block struct {
	lo, hi int
	key    string
}

// eventBlocks finds the top-level VEVENTs and the key each is read under.
func (d *doc) eventBlocks(zone *time.Location) []block {
	var out []block
	for i := 0; i < len(d.entries); i++ {
		e := d.entries[i]
		if e.name != "BEGIN" || !strings.EqualFold(e.value, "VEVENT") || e.depth != 1 {
			continue
		}
		hi := i
		for hi < len(d.entries) && !(d.entries[hi].name == "END" && d.entries[hi].depth == 1) {
			hi++
		}
		if hi >= len(d.entries) {
			break
		}
		b := block{lo: i, hi: hi}
		var uid string
		var rec *entry
		for j := i + 1; j < hi; j++ {
			x := d.entries[j]
			if x.depth != 2 {
				continue
			}
			if x.name == "UID" && uid == "" {
				uid = strings.TrimSpace(x.value)
			}
			if x.name == "RECURRENCE-ID" && rec == nil {
				rec = &d.entries[j]
			}
		}
		b.key = uid
		if rec != nil {
			if t, _, err := icsfeed.ParseTime(rec.prop(), zone); err == nil {
				b.key = overrideKey(uid, t)
			} else {
				b.key = ""
			}
		}
		if uid == "" {
			b.key = ""
		}
		out = append(out, b)
		i = hi
	}
	return out
}

func (d *doc) insertBeforeEnd(add []entry) {
	for i := len(d.entries) - 1; i >= 0; i-- {
		if d.entries[i].name == "END" && d.entries[i].depth == 0 {
			d.entries = append(d.entries[:i], append(slices.Clone(add), d.entries[i:]...)...)
			return
		}
	}
	d.entries = append(d.entries, add...)
}

func (d *doc) insertBeforeFirstComponent(add []entry) {
	if len(add) == 0 {
		return
	}
	for i, e := range d.entries {
		if e.name == "BEGIN" && e.depth == 1 {
			d.entries = append(d.entries[:i], append(slices.Clone(add), d.entries[i:]...)...)
			return
		}
	}
	d.insertBeforeEnd(add)
}

// setProp replaces the direct property name of a VEVENT block with lines: the
// first existing one is replaced in place, further ones are dropped; without
// one the lines go before the end of the block. No lines removes the property.
func setProp(block []entry, name string, lines []string) []entry {
	var out []entry
	placed := false
	put := func() {
		for _, l := range lines {
			out = append(out, entry{text: l, name: name, depth: 2})
		}
		placed = true
	}
	for i, e := range block {
		if i > 0 && i < len(block)-1 && e.depth == 2 && e.name == name {
			if !placed {
				put()
			}
			continue
		}
		if i == len(block)-1 && !placed {
			put()
		}
		out = append(out, e)
	}
	return out
}

// bumpSequence raises SEQUENCE; a significant change (start, end or RRULE) of
// an object without one adds SEQUENCE:1, as RFC 5545 expects.
func bumpSequence(block []entry, significant bool) []entry {
	for i, e := range block {
		if e.depth == 2 && e.name == "SEQUENCE" {
			var n int
			if _, err := fmt.Sscanf(strings.TrimSpace(e.value), "%d", &n); err == nil {
				eol := lineEnd(e.text)
				block[i].text = fmt.Sprintf("SEQUENCE:%d%s", n+1, eol)
			}
			return block
		}
	}
	if significant {
		return setProp(block, "SEQUENCE", []string{formatLine("SEQUENCE", "", "1")})
	}
	return block
}

func lineEnd(s string) string {
	if strings.HasSuffix(s, "\r\n") {
		return "\r\n"
	}
	return "\n"
}

// textLine writes a TEXT property.
func textLine(name, value string) string {
	r := strings.NewReplacer(`\`, `\\`, `;`, `\;`, `,`, `\,`, "\r\n", `\n`, "\n", `\n`)
	return formatLine(name, "", r.Replace(value))
}

// dateLine writes a date or date-time property in its zone.
func dateLine(name string, t time.Time, allDay bool) string {
	switch {
	case allDay:
		return formatLine(name, ";VALUE=DATE", t.Format("20060102"))
	case t.Location() == time.UTC || t.Location() == time.Local:
		// Local is no zone a server knows; write the instant in UTC.
		return formatLine(name, "", t.UTC().Format("20060102T150405Z"))
	}
	return formatLine(name, ";TZID="+t.Location().String(), t.Format("20060102T150405"))
}

// formatLine writes one property, folded at 75 octets, ending in CR LF. Such
// lines are written with CR LF as iCalendar prescribes; the other lines of an
// existing text keep the line ends they had.
func formatLine(name, params, value string) string {
	full := name + params + ":" + value
	var b strings.Builder
	n := 0
	for _, r := range full {
		size := len(string(r))
		if n+size > 75 {
			b.WriteString("\r\n ")
			n = 1
		}
		b.WriteRune(r)
		n += size
	}
	b.WriteString("\r\n")
	return b.String()
}
