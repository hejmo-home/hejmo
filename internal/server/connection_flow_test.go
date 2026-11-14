package server_test

import (
	"errors"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/hejmo-home/hejmo/internal/caldavclient"
	"github.com/hejmo-home/hejmo/internal/calsync"
	"github.com/hejmo-home/hejmo/internal/store"
)

const flowCalendar = "https://caldav.example.org/cal/home/"

// connectedBox connects the box to a stub CalDAV server with one calendar and
// returns the id of the connection.
func connectedBox(t *testing.T, b *box) string {
	t.Helper()
	b.sync.SetDialer(func(string, string, string) (calsync.Remote, error) {
		return hostedRemote{found: []caldavclient.Calendar{{URL: flowCalendar, Name: "Home"}}}, nil
	})
	r := b.post("/connections", url.Values{"provider": {"caldav"}, "url": {"https://caldav.example.org"}, "username": {"u"}, "password": {"pw-1"}})
	if r.status != http.StatusOK {
		t.Fatalf("connect = %d\n%s", r.status, r.body)
	}
	h, _ := b.db.HomeHousehold()
	conns, err := b.db.Connections(h.ID)
	if err != nil || len(conns) != 1 {
		t.Fatalf("connections = %+v, %v", conns, err)
	}
	return conns[0].ID
}

// Ticket 5.16 applies to the connection flow too: errors sit at their field.
func TestConnectionErrorsAppearAtTheirField(t *testing.T) {
	for _, c := range []struct {
		name  string
		form  url.Values
		field string
		fatal bool
	}{
		{"unknown service", url.Values{"provider": {"x"}, "username": {"u"}, "password": {"p"}}, "provider", false},
		{"bad address", url.Values{"provider": {"caldav"}, "url": {"nope"}, "username": {"u"}, "password": {"p"}}, "url", false},
		{"no user name", url.Values{"provider": {"caldav"}, "url": {"https://caldav.example.org"}, "username": {""}, "password": {"p"}}, "username", false},
		{"no password", url.Values{"provider": {"caldav"}, "url": {"https://caldav.example.org"}, "username": {"u"}, "password": {""}}, "password", false},
		{"icloud server problem", url.Values{"provider": {"icloud"}, "username": {"u"}, "password": {"p"}}, "provider", true},
	} {
		t.Run(c.name, func(t *testing.T) {
			b := signedInAPI(t).b
			if c.fatal {
				b.sync.SetDialer(func(string, string, string) (calsync.Remote, error) {
					return errRemote{err: errors.New("offline in test")}, nil
				})
			}
			r := b.do(http.MethodPost, "/connections", c.form, map[string]string{"Accept-Language": "en"})
			if r.status != http.StatusUnprocessableEntity {
				t.Fatalf("status = %d\n%s", r.status, r.body)
			}
			form := formOf(t, r.body, "/connections")
			if !strings.Contains(form, `id="err-`+c.field+`"`) || strings.Count(r.body, `class="ferr"`) != 1 {
				t.Errorf("want exactly one error with id err-%s:\n%s", c.field, form)
			}
			if !strings.Contains(form, `aria-describedby="err-`+c.field+`"`) {
				t.Errorf("field %s is not marked invalid:\n%s", c.field, form)
			}
			if strings.Contains(r.body, `<p class="error"`) {
				t.Error("error also shown as a generic message above the form")
			}
		})
	}
}

func TestConnectionCalendarChoiceUsesTheWholeFamilyChip(t *testing.T) {
	b := signedInAPI(t).b
	id := connectedBox(t, b)
	h, _ := b.db.HomeHousehold()
	_, _ = b.db.AddPerson(h.ID, store.Person{Name: "Mia", Role: store.RoleChild})
	page := formOf(t, getEN(b, "/connections/"+id).body, "/connections/"+id+"/calendars")
	for _, want := range []string{`name="whole_family"`, `name="people_` + flowCalendar + `"`, "Whole family", `class="pchip all"`} {
		if !strings.Contains(page, want) {
			t.Errorf("selection page lacks %q:\n%s", want, page)
		}
	}
	if strings.Contains(page, "leave it empty") || strings.Contains(page, "leer heißt") {
		t.Error("selection page still explains an empty audience instead of showing the chip")
	}
	// Whole family chip alone: no audience.
	b.post("/connections/"+id+"/calendars", url.Values{"calendar": {flowCalendar}, "name_" + flowCalendar: {"Home"}, "whole_family": {"1"}, "interval": {"60"}})
	cals, _ := b.db.SyncedCalendars(h.ID)
	if len(cals) != 1 || len(cals[0].Audience.People) != 0 || len(cals[0].Audience.Groups) != 0 {
		t.Fatalf("whole family chip stored an audience: %+v", cals)
	}
}

func TestConnectionCalendarChoiceStoresAChosenPerson(t *testing.T) {
	b := signedInAPI(t).b
	id := connectedBox(t, b)
	h, _ := b.db.HomeHousehold()
	mia, _ := b.db.AddPerson(h.ID, store.Person{Name: "Mia", Role: store.RoleChild})
	b.post("/connections/"+id+"/calendars", url.Values{"calendar": {flowCalendar}, "name_" + flowCalendar: {"Home"}, "people_" + flowCalendar: {mia.ID}, "interval": {"60"}})
	cals, _ := b.db.SyncedCalendars(h.ID)
	if len(cals) != 1 || len(cals[0].Audience.People) != 1 || cals[0].Audience.People[0] != mia.ID {
		t.Fatalf("chosen person not stored as audience: %+v", cals)
	}
}

const flowCalendar2 = "https://caldav.example.org/cal/work/"

// twoCalendarBox connects a box whose server has two calendars.
func twoCalendarBox(t *testing.T) (*box, string) {
	b := signedInAPI(t).b
	id := connectedBox(t, b)
	b.sync.SetDialer(func(string, string, string) (calsync.Remote, error) {
		return hostedRemote{found: []caldavclient.Calendar{{URL: flowCalendar, Name: "Home"}, {URL: flowCalendar2, Name: "Work"}}}, nil
	})
	return b, id
}

func TestConnectionCalendarErrorKeepsWhatWasEntered(t *testing.T) {
	b, id := twoCalendarBox(t)
	h, _ := b.db.HomeHousehold()
	mia, _ := b.db.AddPerson(h.ID, store.Person{Name: "Mia", Role: store.RoleChild})
	form := url.Values{
		"calendar":               {flowCalendar, flowCalendar2},
		"name_" + flowCalendar:   {"Our home"},
		"color_" + flowCalendar:  {"#112233"},
		"people_" + flowCalendar: {mia.ID},
		"name_" + flowCalendar2:  {""}, // fails
		"color_" + flowCalendar2: {"#445566"},
		"interval":               {"15"},
	}
	r := b.do(http.MethodPost, "/connections/"+id+"/calendars", form, map[string]string{"Accept-Language": "en"})
	if r.status != http.StatusUnprocessableEntity {
		t.Fatalf("status %d\n%s", r.status, r.body)
	}
	page := formOf(t, r.body, "/connections/"+id+"/calendars")
	for _, want := range []string{
		`value="` + flowCalendar + `" checked`, `value="` + flowCalendar2 + `" checked`,
		`value="Our home"`, `value="#112233"`, `value="#445566"`,
		`<option value="15" selected>`, `value="` + mia.ID + `" checked`,
	} {
		if !strings.Contains(page, want) {
			t.Errorf("page lost %q:\n%s", want, page)
		}
	}
	if !strings.Contains(page, `aria-invalid="true"`) || !strings.Contains(page, `aria-describedby="err-name"`) {
		t.Errorf("name input not marked invalid:\n%s", page)
	}
	// Nothing is saved when one calendar is wrong.
	if cals, _ := b.db.SyncedCalendars(h.ID); len(cals) != 0 {
		t.Errorf("calendars saved despite the error: %+v", cals)
	}
}

func TestConnectionCalendarErrorSurvivesFailedRediscovery(t *testing.T) {
	b, id := twoCalendarBox(t)
	b.sync.SetDialer(func(string, string, string) (calsync.Remote, error) {
		return errRemote{err: errors.New("offline in test")}, nil
	})
	form := url.Values{"calendar": {flowCalendar}, "name_" + flowCalendar: {""}, "interval": {"60"}}
	r := b.do(http.MethodPost, "/connections/"+id+"/calendars", form, map[string]string{"Accept-Language": "en"})
	if r.status != http.StatusUnprocessableEntity || !strings.Contains(r.body, "Please enter a name.") {
		t.Fatalf("error lost: status %d\n%s", r.status, r.body)
	}
}

func TestConnectionCalendarsNothingTickedIsAnError(t *testing.T) {
	b := signedInAPI(t).b
	id := connectedBox(t, b)
	r := b.do(http.MethodPost, "/connections/"+id+"/calendars", url.Values{"interval": {"60"}}, map[string]string{"Accept-Language": "en"})
	if r.status != http.StatusUnprocessableEntity || !strings.Contains(r.body, `class="error"`) {
		t.Fatalf("status %d\n%s", r.status, r.body)
	}
}

func TestConnectionCalendarErrorAppearsAtItsCalendar(t *testing.T) {
	b := signedInAPI(t).b
	id := connectedBox(t, b)
	form := url.Values{"calendar": {flowCalendar}, "name_" + flowCalendar: {""}, "interval": {"60"}}
	r := b.do(http.MethodPost, "/connections/"+id+"/calendars", form, map[string]string{"Accept-Language": "en"})
	if r.status != http.StatusUnprocessableEntity || strings.Count(r.body, `class="ferr"`) != 1 || strings.Contains(r.body, `<p class="error"`) {
		t.Fatalf("status %d\n%s", r.status, r.body)
	}
	if i, j := strings.Index(r.body, `class="ferr"`), strings.Index(r.body, `name="calendar"`); i < j {
		t.Fatal("the error is not shown with its calendar")
	}
}

// Posting the same choice twice (double click, a second tab) hits the
// "already synced" error; the calendar is then marked added and shows no
// field, so the message has to show as a general error line.
func TestConnectionCalendarAlreadySyncedShowsItsMessage(t *testing.T) {
	b := signedInAPI(t).b
	id := connectedBox(t, b)
	form := url.Values{"calendar": {flowCalendar}, "name_" + flowCalendar: {"Home"}, "interval": {"60"}}
	if r := b.post("/connections/"+id+"/calendars", form); r.status == http.StatusUnprocessableEntity {
		t.Fatalf("first post failed: %s", r.body)
	}
	r := b.do(http.MethodPost, "/connections/"+id+"/calendars", form, map[string]string{"Accept-Language": "en"})
	if r.status != http.StatusUnprocessableEntity || !strings.Contains(r.body, "This calendar is already synced.") || !strings.Contains(r.body, `class="error"`) {
		t.Fatalf("message not shown: status %d\n%s", r.status, r.body)
	}
}
