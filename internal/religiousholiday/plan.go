package religiousholiday

import (
	"time"

	"github.com/hejmo-home/hejmo/internal/holiday"
)

// publicNames says which public holidays (by their German names in package holiday) are the holiday of a key of
// the Catholic, Protestant and Orthodox churches. Only these are never added twice: a day of another religion
// that falls on the date of a public holiday is a different festival and stays in the calendar. Orthodox
// Christmas counts as Christmas Day when it is on 25 December; Theophany is not taken for the Three Kings
// (an open question for the product owner, ticket 339).
var publicNames = map[string][]string{
	"epiphany":       {"Heilige Drei Könige"},
	"good_friday":    {"Karfreitag"},
	"easter":         {"Ostersonntag"},
	"easter_monday":  {"Ostermontag"},
	"ascension":      {"Christi Himmelfahrt", "Auffahrt"},
	"pentecost":      {"Pfingstsonntag"},
	"whit_monday":    {"Pfingstmontag"},
	"corpus_christi": {"Fronleichnam"},
	"assumption":     {"Mariä Himmelfahrt"},
	"reformation":    {"Reformationstag"},
	"all_saints":     {"Allerheiligen"},
	"repentance_day": {"Buß- und Bettag"},
	"christmas_days": {"1. Weihnachtstag", "2. Weihnachtstag", "Christtag", "Stefanitag", "Weihnachten", "Stephanstag"},
	"christmas":      {"1. Weihnachtstag", "Christtag", "Weihnachten"},
}

// PublicNames lists the German names of the public holidays that are the holiday with this key (none for most keys).
func PublicNames(key string) []string { return publicNames[key] }

// Plan is what the calendar holds for one religion: the holidays it gets and
// the ones left out because the region has them as public holidays already.
type Plan struct {
	Religion Religion
	Kept     []Holiday
	Skipped  []Skipped
	// TableEnd is the last year a table of the religion covers (0: the dates are computed with no end).
	TableEnd int
}

// PlanFor lists, for each religion, the holidays that start from fromYear to
// toYear and are not public holidays of the region (an empty or unknown region
// has none). The religions keep the order given.
func PlanFor(religions []Religion, o Options, regionCode string, fromYear, toYear int) []Plan {
	type dayName struct {
		day  time.Time
		name string
	}
	public := map[dayName]bool{} // the value says: partial
	for y := fromYear - 1; y <= toYear+1; y++ {
		for _, h := range holiday.For(regionCode, y) {
			public[dayName{h.Date, h.NameDE}] = h.Partial
		}
	}
	isPublic := func(key string, t time.Time) (partial, ok bool) {
		for _, name := range publicNames[key] {
			if p, found := public[dayName{t, name}]; found {
				return p, true
			}
		}
		return false, false
	}
	var out []Plan
	for _, r := range religions {
		kept, skipped := Skip(Compute(r, o, fromYear, toYear), isPublic)
		out = append(out, Plan{Religion: r, Kept: kept, Skipped: skipped, TableEnd: TableEnd(r, o)})
	}
	return out
}
