package measure

import (
	"fmt"
	"strings"
	"time"

	"github.com/hejmo-home/hejmo/internal/auth"
	"github.com/hejmo-home/hejmo/internal/calendar"
	"github.com/hejmo-home/hejmo/internal/store"
	"github.com/hejmo-home/hejmo/internal/task"
)

// SeedOptions describes the household to seed.
type SeedOptions struct {
	FeedURL       string // ICS address every synced calendar subscribes to
	Calendars     int    // number of synced calendars (the sync burst syncs them all at once)
	EventsPerFeed int    // events in the feed, for the local family calendar a tenth of it
}

// Seeded is what the measurement needs to drive the seeded box.
type Seeded struct {
	Token       string   // bearer token of a parent with scope "write"
	CalendarIDs []string // the synced calendars
}

// Seed fills an empty database with the example family (Mama, Papa,
// Sebastian, Mia), a family calendar with events, a few tasks and synced
// calendars, and marks the box as set up. No real data.
func Seed(db *store.DB, o SeedOptions) (Seeded, error) {
	h, err := db.CreateHousehold(store.Household{Name: "Example family", HomeName: "Home", Region: "DE-BY", Timezone: "Europe/Berlin"})
	if err != nil {
		return Seeded{}, err
	}
	var ids []string
	for _, p := range []struct {
		name  string
		role  store.Role
		color string
	}{{"Mama", store.RoleParent, "#4a6fa5"}, {"Papa", store.RoleParent, "#7a9e7e"}, {"Sebastian", store.RoleChild, "#c98b5e"}, {"Mia", store.RoleChild, "#a5648f"}} {
		person, err := db.AddPerson(h.ID, store.Person{Name: p.name, Role: p.role, Color: p.color})
		if err != nil {
			return Seeded{}, err
		}
		ids = append(ids, person.ID)
	}
	mama := ids[0]
	// A set-up box has a parent whose one-time code is confirmed.
	if err := db.CreateAccount(store.Account{PersonID: mama, Username: "mama", PasswordHash: "-", TOTPSecret: "-"}); err != nil {
		return Seeded{}, err
	}
	if err := db.ConfirmTOTP(mama); err != nil {
		return Seeded{}, err
	}
	token := auth.NewToken()
	if _, err := db.CreateAPIToken(store.APIToken{Name: "measurement", Hash: auth.HashToken(token), PersonID: mama, Scopes: "write"}); err != nil {
		return Seeded{}, err
	}
	if err := db.EnsureFamilyCalendar(h.ID); err != nil {
		return Seeded{}, err
	}
	fam, err := db.FamilyCalendar(h.ID)
	if err != nil {
		return Seeded{}, err
	}
	now := time.Now()
	day := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
	for i := 0; i < max(o.EventsPerFeed/10, 1); i++ {
		start := day.AddDate(0, 0, i).Add(16 * time.Hour)
		if _, err := db.CreateEvent(h.ID, calendar.Event{CalendarID: fam.ID, Title: fmt.Sprintf("Family event %d", i+1), Start: start, End: start.Add(time.Hour)}); err != nil {
			return Seeded{}, err
		}
	}
	for i, title := range []string{"Brush teeth", "Pack school bag", "Feed the cat", "Tidy up"} {
		t := task.Task{PersonID: ids[2+i%2], Title: title, Repeat: task.Daily, Date: day.Format("2006-01-02")}
		if i == 0 {
			t.PartOfDay = task.Morning
		}
		if _, err := db.AddTask(h.ID, t); err != nil {
			return Seeded{}, err
		}
	}
	// Not due at start: the scheduler would sync every calendar right away and
	// the idle measurement would not be idle. The burst syncs them through the API.
	notDue := now.Add(24 * time.Hour)
	out := Seeded{Token: token}
	for i := 0; i < o.Calendars; i++ {
		c, err := db.CreateICSCalendar(h.ID, store.NewICS{Name: fmt.Sprintf("Feed %d", i+1), URL: o.FeedURL, Color: "#888888", IntervalMinutes: 60, Now: notDue})
		if err != nil {
			return Seeded{}, err
		}
		out.CalendarIDs = append(out.CalendarIDs, c.ID)
	}
	return out, nil
}

// Feed returns an ICS feed of n events from day on, a tenth of them weekly
// series, with descriptions and locations like a school or club calendar.
func Feed(n int, from time.Time) []byte {
	var b strings.Builder
	b.WriteString("BEGIN:VCALENDAR\r\nVERSION:2.0\r\nPRODID:-//Hejmo//measure//EN\r\n")
	day := time.Date(from.Year(), from.Month(), from.Day(), 0, 0, 0, 0, time.UTC)
	for i := 0; i < n; i++ {
		start := day.AddDate(0, 0, i/2).Add(time.Duration(8+(i%2)*6) * time.Hour)
		fmt.Fprintf(&b, "BEGIN:VEVENT\r\nUID:measure-%d@hejmo.example\r\nDTSTAMP:20261101T000000Z\r\nDTSTART:%s\r\nDTEND:%s\r\n",
			i, start.Format("20060102T150405Z"), start.Add(90*time.Minute).Format("20060102T150405Z"))
		fmt.Fprintf(&b, "SUMMARY:Event %d\r\nDESCRIPTION:Bring a snack, a water bottle and sports clothes. Event number %d of the term.\r\nLOCATION:Hall %d\r\n", i, i, i%7)
		if i%10 == 0 {
			b.WriteString("RRULE:FREQ=WEEKLY;COUNT=12\r\n")
		}
		b.WriteString("END:VEVENT\r\n")
	}
	b.WriteString("END:VCALENDAR\r\n")
	return []byte(b.String())
}
