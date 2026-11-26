package nameday

import (
	"crypto/sha1" //nolint:gosec // UUIDv5, see SlavaEventID
	_ "embed"     // the Slava list
	"encoding/json"
	"fmt"
	"strconv"
	"sync"
	"time"
)

// The Serbian Slava (ticket 338): the Krsna Slava is the feast of a family's patron saint, one per household. Its
// list is compiled from the calendar of the Serbian Orthodox Church. The Slava always follows that church (the
// Julian calendar), whatever calendar style the household chose for its name days.

// MaxOwnSlavaName is the longest name of an own Slava.
const MaxOwnSlavaName = 60

// OwnID is the choice of a saint that is not in the list: the household gives a name and a fixed day.
const OwnID = "own"

// SlavaList is the compiled list of Slava feasts. The entries use the fields of Entry: Name is the feast as the
// Serbs call it ("Nikoljdan"), Saint the saint in Serbian ("Sveti Nikola"), SaintDE in German.
type SlavaList struct {
	Sources map[string]string
	Entries []Entry
	byID    map[string]int
}

//go:embed data/slava.json
var slavaFile []byte

var (
	slavaOnce sync.Once
	slavaList *SlavaList
)

// Slavas returns the embedded list.
func Slavas() *SlavaList {
	slavaOnce.Do(func() {
		l, err := ParseSlava(slavaFile)
		if err != nil {
			panic(fmt.Sprintf("nameday: embedded Slava list: %v", err)) // a build error: the tests load it
		}
		slavaList = l
	})
	return slavaList
}

// ParseSlava reads the list and checks it like Parse: ids unique, a named source on every entry, a day or an Easter
// offset, and a Serbian and a German name of the saint.
func ParseSlava(raw []byte) (*SlavaList, error) {
	var in struct {
		Sources map[string]string `json:"sources"`
		Entries []Entry           `json:"entries"`
	}
	if err := json.Unmarshal(raw, &in); err != nil {
		return nil, err
	}
	l := &SlavaList{Sources: in.Sources, Entries: in.Entries, byID: map[string]int{}}
	for i, e := range l.Entries {
		if e.ID == "" || e.Name == "" || e.Saint == "" || e.SaintDE == "" || e.ID == OwnID {
			return nil, fmt.Errorf("slava %d: id, name, saint and saint_de are required", i)
		}
		if _, dup := l.byID[e.ID]; dup {
			return nil, fmt.Errorf("slava %s: duplicate id", e.ID)
		}
		if _, ok := in.Sources[e.Source]; !ok {
			return nil, fmt.Errorf("slava %s: source %q is not named in the list", e.ID, e.Source)
		}
		switch {
		case e.Easter != nil && e.Day != "":
			return nil, fmt.Errorf("slava %s: a day or an Easter offset, not both", e.ID)
		case e.Easter == nil:
			if _, _, ok := parseDay(e.Day); !ok {
				return nil, fmt.Errorf("slava %s: bad day %q", e.ID, e.Day)
			}
		}
		l.byID[e.ID] = i
	}
	return l, nil
}

// Entry returns a Slava by its id.
func (l *SlavaList) Entry(id string) (Entry, bool) {
	i, ok := l.byID[id]
	if !ok {
		return Entry{}, false
	}
	return l.Entries[i], true
}

// SlavaOn is the day of the Slava in a year: always the Julian style.
func (e Entry) SlavaOn(year int) Occurrence { return e.OnStyle(year, Julian) }

// OwnDay checks the day of an own Slava (a fixed day of our calendar) and writes it as "MM-DD". 29 February is
// allowed: it falls on the last day of February in a common year.
func OwnDay(month, day int) (string, bool) {
	if month < 1 || month > 12 || day < 1 || day > 31 {
		return "", false
	}
	if t := time.Date(2000, time.Month(month), day, 0, 0, 0, 0, time.UTC); int(t.Month()) != month { // 2000 is a leap year
		return "", false
	}
	return fmt.Sprintf("%02d-%02d", month, day), true
}

// OwnEntry is the entry of an own Slava: a name and a fixed day of our calendar; it is the same in every style.
func OwnEntry(name, day string) Entry {
	return Entry{ID: OwnID, Name: name, Saint: name, SaintDE: name, Day: day}
}

// slavaNamespace is the UUID namespace of the Slava event ids (the URL namespace of RFC 4122); the name in it starts
// with "slava:", so no id is a birthday or a name-day id.
var slavaNamespace = eventNamespace

// SlavaEventID is the id of the Slava event of a household: a UUIDv5, the same on every start of the box. Year 0 is
// the yearly series of a fixed day; any other year is the single event of an Easter day in that year.
func SlavaEventID(householdID string, year int) string {
	name := "slava:" + householdID
	if year != 0 {
		name += ":" + strconv.Itoa(year)
	}
	h := sha1.New() //nolint:gosec // UUIDv5 is defined with SHA-1; no security use
	h.Write(slavaNamespace[:])
	h.Write([]byte(name))
	var b [16]byte
	copy(b[:], h.Sum(nil))
	b[6] = b[6]&0x0f | 0x50
	b[8] = b[8]&0x3f | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}
