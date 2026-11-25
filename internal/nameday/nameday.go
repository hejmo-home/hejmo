// Package nameday holds the name-day lists of the churches and the rules that
// turn a contact's name into a day (ticket 318): which name counts, how a
// name is matched, on which day it falls in a year (including the days that
// follow the Orthodox Easter) and the ids of the generated calendar events. It
// knows nothing about storage or HTTP.
//
// The lists are compiled from the calendars of the churches, one data file per
// list, embedded in the binary. Every entry records its source; nothing is
// copied from name-day websites. More lists (more Orthodox traditions, the
// Slava) are new files and a new List value: nothing else here changes.
package nameday

import (
	"crypto/sha1" //nolint:gosec // UUIDv5, see EventID
	"embed"
	"encoding/json"
	"fmt"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"

	rh "github.com/hejmo-home/hejmo/internal/religiousholiday"

	"golang.org/x/text/unicode/norm"
)

// List is a name-day list: one church calendar.
type List string

const (
	Catholic         List = "catholic"          // Regionalkalender for DE/AT/CH; also for Old Catholics
	Protestant       List = "protestant"        // Evangelischer Namenkalender
	RomanianOrthodox List = "romanian_orthodox" // Calendar of the Romanian Orthodox Church
	GreekOrthodox    List = "greek_orthodox"    // Calendar of the Church of Greece

	BulgarianOrthodox List = "bulgarian_orthodox" // Calendar of the Bulgarian Orthodox Church (ticket 338)
	RussianOrthodox   List = "russian_orthodox"   // Calendar of the Russian Orthodox Church, Moscow Patriarchate
	UkrainianOrthodox List = "ukrainian_orthodox" // Calendar of the Orthodox Church of Ukraine
)

// Lists are the lists in the order the settings page shows them: the Western churches, then the Orthodox ones
// alphabetically by their German name.
func Lists() []List {
	return []List{Catholic, Protestant, BulgarianOrthodox, GreekOrthodox, RomanianOrthodox, RussianOrthodox, UkrainianOrthodox}
}

// Orthodox reports whether the list is the calendar of an Orthodox church: the lists that follow the calendar style.
func (l List) Orthodox() bool {
	return l != Catholic && l != Protestant && l.Valid()
}

// Calendar is the style the church of the list celebrates by, when it has one fixed style. The Orthodox Church of
// Ukraine switched to the revised Julian calendar on 01/09/2023 while parishes of the Moscow Patriarchate keep the
// Julian one, so the Ukrainian list has none (ok is false), like the Western lists.
func (l List) Calendar() (style Style, ok bool) {
	switch l {
	case RussianOrthodox:
		return Julian, true
	case BulgarianOrthodox, GreekOrthodox, RomanianOrthodox:
		return RevisedJulian, true
	}
	return "", false
}

// Style is the calendar a household celebrates the fixed feasts by (ticket 338): the revised Julian calendar (our
// dates) or the Julian one (13 days later until 2100). Easter is the Orthodox Easter in both. It is the type of the
// Orthodox holidays (ticket 339), not a second one: the household has one calendar style, so one type.
type Style = rh.OrthodoxCalendar

const (
	RevisedJulian = rh.RevisedJulian
	Julian        = rh.Julian
)

// ParseStyle reads a stored style; anything else is the default, the revised Julian calendar.
func ParseStyle(s string) Style {
	if Style(s) == Julian {
		return Julian
	}
	return RevisedJulian
}

// Valid reports whether l is a list of this version.
func (l List) Valid() bool { return slices.Contains(Lists(), l) }

// Entry is one name day of a list: a saint (or feast) and the names that
// celebrate it.
type Entry struct {
	ID       string   `json:"id"`
	Name     string   `json:"name"`            // the name as the list writes it
	Gloss    string   `json:"gloss,omitempty"` // the form a household knows it by, shown in brackets
	Variants []string `json:"variants,omitempty"`
	Saint    string   `json:"saint"`
	SaintDE  string   `json:"saint_de,omitempty"` // the saint in German, for the Slava list (Saint is Serbian there)
	Day      string   `json:"day,omitempty"`      // "MM-DD": a fixed day
	Easter   *int     `json:"easter,omitempty"`   // days after the Orthodox Easter
	Move     string   `json:"move,omitempty"`     // MoveEasterMonday: a fixed day that moves
	Usual    bool     `json:"usual,omitempty"`    // the day a name gets when it has several
	Source   string   `json:"source"`             // key in the list's sources
	// Unchecked says the day was written down but not yet checked against the source it names.
	Unchecked bool `json:"unchecked,omitempty"`
}

// MoveEasterMonday moves a fixed day to the Monday after Easter when it falls
// before Easter (Saint George, 23 April).
const MoveEasterMonday = "easter_monday"

// Calendar is a loaded list.
type Calendar struct {
	List    List
	Sources map[string]string // key to the name of the source
	Entries []Entry

	byID   map[string]int
	byName map[string][]int
	names  []string
}

//go:embed data/*.json
var files embed.FS

var (
	loadOnce sync.Once
	loaded   map[List]*Calendar
)

// Get returns the embedded calendar of a list.
func Get(l List) (*Calendar, bool) {
	loadOnce.Do(func() {
		loaded = map[List]*Calendar{}
		for _, l := range Lists() {
			raw, err := files.ReadFile("data/" + string(l) + ".json")
			if err != nil {
				continue
			}
			c, err := Parse(raw)
			if err != nil {
				panic(fmt.Sprintf("nameday: embedded list %s: %v", l, err)) // a build error: the tests load every list
			}
			loaded[l] = c
		}
	})
	c, ok := loaded[l]
	return c, ok
}

// Parse reads a list from its data file and checks it: every entry has an id
// that is unique, a source that the list names, and a day, an Easter offset or
// both a day and a move rule.
func Parse(raw []byte) (*Calendar, error) {
	var in struct {
		List    List              `json:"list"`
		Sources map[string]string `json:"sources"`
		Entries []Entry           `json:"entries"`
	}
	if err := json.Unmarshal(raw, &in); err != nil {
		return nil, err
	}
	if !in.List.Valid() {
		return nil, fmt.Errorf("unknown list %q", in.List)
	}
	c := &Calendar{List: in.List, Sources: in.Sources, Entries: in.Entries, byID: map[string]int{}, byName: map[string][]int{}}
	seen := map[string]bool{}
	for i, e := range c.Entries {
		if e.ID == "" || e.Name == "" || e.Saint == "" {
			return nil, fmt.Errorf("entry %d: id, name and saint are required", i)
		}
		if _, dup := c.byID[e.ID]; dup {
			return nil, fmt.Errorf("entry %s: duplicate id", e.ID)
		}
		if _, ok := in.Sources[e.Source]; !ok {
			return nil, fmt.Errorf("entry %s: source %q is not named in the list", e.ID, e.Source)
		}
		switch {
		case e.Easter != nil && e.Day != "":
			return nil, fmt.Errorf("entry %s: a day or an Easter offset, not both", e.ID)
		case e.Easter == nil && e.Day == "":
			return nil, fmt.Errorf("entry %s: no day", e.ID)
		case e.Day != "":
			if _, _, ok := parseDay(e.Day); !ok {
				return nil, fmt.Errorf("entry %s: bad day %q", e.ID, e.Day)
			}
		}
		if e.Move != "" && (e.Move != MoveEasterMonday || e.Day == "") {
			return nil, fmt.Errorf("entry %s: bad move rule", e.ID)
		}
		c.byID[e.ID] = i
		for _, n := range append([]string{e.Name}, e.Variants...) {
			k := fold(n)
			if !slices.Contains(c.byName[k], i) {
				c.byName[k] = append(c.byName[k], i)
			}
			if !seen[n] {
				seen[n] = true
				c.names = append(c.names, n)
			}
		}
	}
	for k, idx := range c.byName {
		// The usual day first, the others in the order of the list.
		sort.SliceStable(idx, func(a, b int) bool { return c.Entries[idx[a]].Usual && !c.Entries[idx[b]].Usual })
		c.byName[k] = idx
	}
	sort.Strings(c.names)
	return c, nil
}

func parseDay(s string) (month, day int, ok bool) {
	m, d, found := strings.Cut(s, "-")
	if !found {
		return 0, 0, false
	}
	mi, err1 := strconv.Atoi(m)
	di, err2 := strconv.Atoi(d)
	if err1 != nil || err2 != nil || mi < 1 || mi > 12 || di < 1 || di > 31 {
		return 0, 0, false
	}
	if t := time.Date(2000, time.Month(mi), di, 0, 0, 0, 0, time.UTC); int(t.Month()) != mi { // 2000 is a leap year
		return 0, 0, false
	}
	return mi, di, true
}

// Entry returns an entry by its id.
func (c *Calendar) Entry(id string) (Entry, bool) {
	i, ok := c.byID[id]
	if !ok {
		return Entry{}, false
	}
	return c.Entries[i], true
}

// Lookup returns the entries of a name, the usual day first. Case and accents
// do not matter; the forms the list records do. nil when the list does not
// know the name.
func (c *Calendar) Lookup(name string) []Entry {
	idx := c.byName[fold(name)]
	if len(idx) == 0 {
		return nil
	}
	out := make([]Entry, len(idx))
	for i, j := range idx {
		out[i] = c.Entries[j]
	}
	return out
}

// Names are the names the list knows, with their recorded forms, sorted: the
// suggestions while typing a name.
func (c *Calendar) Names() []string { return slices.Clone(c.names) }

// Match is the result of Resolve.
type Match struct {
	Entries []Entry // the days of the name, the usual one first
	Matched string  // the name as it was written in the contact
	Part    int     // 0: the whole first name; 1, 2, ...: that part of a hyphenated name
	Parts   int     // how many parts the first name has
}

// Resolve finds the name day of a contact: the first given name of the full
// name, else the Hejmo name. For a hyphenated name it tries the whole name,
// then each part in order. No match means no name day: nothing is guessed.
func (c *Calendar) Resolve(fullName, hejmoName string) (Match, bool) {
	first := FirstName(fullName, hejmoName)
	if first == "" {
		return Match{}, false
	}
	parts := strings.FieldsFunc(first, func(r rune) bool { return r == '-' || r == '‐' || r == '–' })
	if es := c.Lookup(first); es != nil {
		return Match{Entries: es, Matched: first, Parts: len(parts)}, true
	}
	if len(parts) > 1 {
		for i, p := range parts {
			if es := c.Lookup(p); es != nil {
				return Match{Entries: es, Matched: p, Part: i + 1, Parts: len(parts)}, true
			}
		}
	}
	return Match{}, false
}

// FirstName is the name that counts: the first given name of the full name
// (a title such as "Dr." is not a given name), else the Hejmo name as written.
func FirstName(fullName, hejmoName string) string {
	for _, w := range strings.Fields(fullName) {
		if strings.HasSuffix(w, ".") {
			continue
		}
		return w
	}
	for _, w := range strings.Fields(hejmoName) {
		return w
	}
	return ""
}

// fold makes names comparable: lower case, no accents, a few letters that have
// no accent form written as their usual Latin letters.
func fold(s string) string {
	var b strings.Builder
	for _, r := range norm.NFD.String(strings.TrimSpace(s)) {
		if unicode.Is(unicode.Mn, r) {
			continue
		}
		r = unicode.ToLower(r)
		switch r {
		case 'ß':
			b.WriteString("ss")
		case 'ø':
			b.WriteByte('o')
		case 'ł':
			b.WriteByte('l')
		case 'đ':
			b.WriteByte('d')
		case 'ı':
			b.WriteByte('i')
		case 'æ':
			b.WriteString("ae")
		case 'œ':
			b.WriteString("oe")
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

// Same reports whether two names are the same name for the list (case and
// accents ignored): the title of an event names the name day only when it
// differs from the name in Hejmo.
func Same(a, b string) bool { return fold(a) == fold(b) }

// Yearly reports whether the entry falls on the same day every year, so that
// one yearly series says it all. Days that follow Easter or move with it are
// written year by year.
func (e Entry) Yearly() bool { return e.Easter == nil && e.Move == "" }

// Occurrence is the day of an entry in one year.
type Occurrence struct {
	Date            time.Time // the day the household celebrates, midnight UTC
	From            time.Time // the day it would be on without the move; zero when it was not moved
	Moved           bool
	EasterDependent bool      // follows the Orthodox Easter
	Easter          time.Time // the Orthodox Easter of the year; zero when it does not matter
	Church          time.Time // the day as the church calendar numbers it (same month and day as the list); zero for an Easter day
	Offset          int       // days the Julian style adds to the church day; 0 in the revised Julian style
}

// On is the day of the entry in a year in the revised Julian calendar: the day of the list. A day that is not in
// the year (29 February in a common year) is the last day of February.
func (e Entry) On(year int) Occurrence { return e.OnStyle(year, RevisedJulian) }

// OnStyle is the day of the entry in a year for a calendar style. The list holds the days as the church numbers
// them; the Julian style adds the offset of the year (JulianOffset), so the day moves 13 days later until 28/02/2100.
// An Easter day is the same in both styles, and so is the move to Easter Monday (it is decided on the day the
// household celebrates).
func (e Entry) OnStyle(year int, st Style) Occurrence {
	if e.Easter != nil {
		easter := OrthodoxEaster(year)
		return Occurrence{Date: easter.AddDate(0, 0, *e.Easter), EasterDependent: true, Easter: easter}
	}
	m, d, _ := parseDay(e.Day)
	var church, date time.Time
	var offset int
	if st == Julian {
		if m == 2 && d == 29 && year%4 != 0 { // the Julian leap rule: every fourth year
			d = 28
		}
		church = time.Date(year, time.Month(m), d, 0, 0, 0, 0, time.UTC)
		date = julianToGregorian(year, m, d)
		offset = JulianOffset(year, m, d)
	} else {
		date = time.Date(year, time.Month(m), d, 0, 0, 0, 0, time.UTC)
		if int(date.Month()) != m {
			date = time.Date(year, time.Month(m)+1, 0, 0, 0, 0, 0, time.UTC)
		}
		church = date
	}
	o := Occurrence{Date: date, Church: church, Offset: offset}
	if e.Move == MoveEasterMonday {
		o.Easter = OrthodoxEaster(year)
		if !date.After(o.Easter) {
			o.From, o.Date, o.Moved = date, o.Easter.AddDate(0, 0, 1), true
		}
	}
	return o
}

// JulianOffset is the number of days the Julian calendar is behind the Gregorian one on a Julian date: 13 from
// 29/02/1900 to 28/02/2100 (Julian), 14 from 29/02/2100 on. A church day of 1 January to 28 February still has the
// offset of the century before.
func JulianOffset(year, month, day int) int {
	y := year
	if month < 3 && !(month == 2 && day == 29) {
		y--
	}
	return y/100 - y/400 - 2
}

// julianToGregorian converts a date of the Julian calendar to the Gregorian one through the Julian day number.
func julianToGregorian(y, m, d int) time.Time {
	a := (14 - m) / 12
	yy, mm := y+4800-a, m+12*a-3
	jdn := d + (153*mm+2)/5 + 365*yy + yy/4 - 32083
	return time.Date(1970, 1, 1+jdn-2440588, 0, 0, 0, 0, time.UTC)
}

// OrthodoxEaster is the date (Gregorian, midnight UTC) of Easter in the
// Orthodox Churches that keep the Julian computus: the Meeus Julian algorithm,
// moved to the Gregorian calendar by the difference of the century.
func OrthodoxEaster(year int) time.Time {
	a, b, c := year%4, year%7, year%19
	d := (19*c + 15) % 30
	e := (2*a + 4*b - d + 34) % 7
	month, day := (d+e+114)/31, (d+e+114)%31+1
	diff := year/100 - year/400 - 2 // Julian to Gregorian
	return time.Date(year, time.Month(month), day+diff, 0, 0, 0, 0, time.UTC)
}

// eventNamespace is the UUID namespace of the event ids (the URL namespace of
// RFC 4122); the name in it starts with "nameday:", so no id is a birthday id.
var eventNamespace = [16]byte{0x6b, 0xa7, 0xb8, 0x11, 0x9d, 0xad, 0x11, 0xd1, 0x80, 0xb4, 0x00, 0xc0, 0x4f, 0xd4, 0x30, 0xc8}

// EventID is the id of a name-day event of a contact: a UUIDv5, the same on
// every start of the box. Year 0 is the yearly series of a fixed day; any other
// year is the single event of an Easter-dependent or moved day in that year.
func EventID(contactID string, year int) string {
	name := "nameday:" + contactID
	if year != 0 {
		name += ":" + strconv.Itoa(year)
	}
	h := sha1.New() //nolint:gosec // UUIDv5 is defined with SHA-1; no security use
	h.Write(eventNamespace[:])
	h.Write([]byte(name))
	var b [16]byte
	copy(b[:], h.Sum(nil))
	b[6] = b[6]&0x0f | 0x50
	b[8] = b[8]&0x3f | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

// How a contact's name day is chosen (the column name_day_mode).
const (
	ModeAuto  = "auto"  // the usual day of the name that counts
	ModeEntry = "entry" // another day of the list, chosen by a parent
	ModeName  = "name"  // another name, chosen by a parent
	ModeNone  = "none"  // no name day
)

// Choice is what a contact says about its name day.
type Choice struct{ Mode, Entry, Name string }

// Via says where a name day comes from.
type Via string

const (
	ViaName  Via = "name"  // the first given name
	ViaPart  Via = "part"  // a part of a hyphenated first name
	ViaEntry Via = "entry" // a day a parent chose
	ViaOther Via = "other" // another name a parent chose
)

// Why a contact has no name day.
const (
	WhyNone    = "none"    // a parent chose no name day
	WhyUnknown = "unknown" // the list does not know the name
)

// Result is the name day of a contact in a list.
type Result struct {
	Found bool
	Entry Entry
	Via   Via
	Match Match  // the name that counted, for ViaName and ViaPart
	Why   string // when not Found
}

// For is the name day of a contact: what the parents chose, else the first
// given name. A choice the list cannot satisfy (a day or a name it does not
// have) falls back to the name; the settings page drops such choices when the
// list changes, so this is a guard.
func (c *Calendar) For(fullName, hejmoName string, ch Choice) Result {
	switch ch.Mode {
	case ModeNone:
		return Result{Why: WhyNone}
	case ModeEntry:
		if e, ok := c.Entry(ch.Entry); ok {
			return Result{Found: true, Entry: e, Via: ViaEntry}
		}
	case ModeName:
		if es := c.Lookup(ch.Name); es != nil {
			return Result{Found: true, Entry: es[0], Via: ViaOther}
		}
	}
	m, ok := c.Resolve(fullName, hejmoName)
	if !ok {
		return Result{Why: WhyUnknown}
	}
	via := ViaName
	if m.Part > 0 {
		via = ViaPart
	}
	return Result{Found: true, Entry: m.Entries[0], Via: via, Match: m}
}

// Title is the title of the event: the word, the Hejmo name, and the name of
// the name day in brackets when it is another name.
func Title(word, hejmoName, nameDayName string) string {
	t := word + " " + hejmoName
	if !Same(hejmoName, nameDayName) {
		t += " (" + nameDayName + ")"
	}
	return t
}
