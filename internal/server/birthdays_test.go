package server_test

import (
	"net/http"
	"strings"
	"testing"
)

// Ticket 302: the generated, read-only calendar Birthdays, switched per contact.

// birthdayCalendarOf returns the calendar of kind birthdays from the API, nil when there is none.
func birthdayCalendarOf(t *testing.T, c ctBox) map[string]any {
	t.Helper()
	status, _, list := c.api.call(http.MethodGet, "/calendars", nil)
	if status != http.StatusOK {
		t.Fatalf("calendars = %d", status)
	}
	for _, x := range list {
		if m := x.(map[string]any); m["kind"] == "birthdays" {
			return m
		}
	}
	return nil
}

func birthdayTitles(t *testing.T, c ctBox, from, to string) []string {
	t.Helper()
	status, _, list := c.api.call(http.MethodGet, "/events?from="+from+"&to="+to, nil)
	if status != http.StatusOK {
		t.Fatalf("events = %d", status)
	}
	cal := birthdayCalendarOf(t, c)
	var out []string
	for _, x := range list {
		m := x.(map[string]any)
		if cal != nil && m["calendar_id"] == cal["id"] {
			out = append(out, m["title"].(string))
		}
	}
	return out
}

func TestBirthdaysCalendarIsGeneratedReadOnlyAndShowsAgeOnlyWithAKnownYear(t *testing.T) {
	t.Parallel()
	c := newCtBox(t)
	if birthdayCalendarOf(t, c) != nil {
		t.Fatal("a Birthdays calendar before any birthday")
	}
	status, _, _ := c.api.call(http.MethodPost, "/contacts", map[string]any{"name": "Oma Gerda", "birthday": map[string]any{"day": 3, "month": 5}})
	status2, _, _ := c.api.call(http.MethodPost, "/contacts", map[string]any{"name": "Tante Ines", "birthday": map[string]any{"day": 4, "month": 5, "year": 1980}})
	if status != http.StatusCreated || status2 != http.StatusCreated {
		t.Fatalf("create = %d %d", status, status2)
	}
	cal := birthdayCalendarOf(t, c)
	if cal == nil || cal["read_only"] != true || cal["share_through_box"] != true {
		t.Fatalf("calendar = %v, want read-only and shared through the box", cal)
	}
	got := birthdayTitles(t, c, "2028-05-01T00:00:00Z", "2028-06-01T00:00:00Z")
	if strings.Join(got, "|") != "Oma Gerda|Tante Ines (48)" {
		t.Errorf("May 2028 = %v", got)
	}
	// A birthday cannot be edited like an event.
	status, _, list := c.api.call(http.MethodGet, "/events?from=2028-05-01T00:00:00Z&to=2028-06-01T00:00:00Z", nil)
	if status != http.StatusOK || len(list) == 0 {
		t.Fatal("no events")
	}
	id := list[0].(map[string]any)["event_id"].(string)
	if status, _, _ := c.api.call(http.MethodDelete, "/events/"+id, nil); status == http.StatusNoContent {
		t.Errorf("a birthday was deleted as an event")
	}
}

func TestBirthdayShowsInTheCalendarUnlessItsContactIsSwitchedOff(t *testing.T) {
	t.Parallel()
	c := newCtBox(t)
	_, got, _ := c.api.call(http.MethodPost, "/contacts", map[string]any{"name": "Oma Gerda", "birthday": map[string]any{"day": 3, "month": 5}})
	id := got["id"].(string)
	if got["show_in_birthdays"] != true {
		t.Fatalf("show_in_birthdays = %v, want true by default", got["show_in_birthdays"])
	}
	status, got, _ := c.api.call(http.MethodPut, "/contacts/"+id, map[string]any{"name": "Oma Gerda", "birthday": map[string]any{"day": 3, "month": 5}, "show_in_birthdays": false})
	if status != http.StatusOK || got["show_in_birthdays"] != false {
		t.Fatalf("switch off = %d %v", status, got)
	}
	if got := birthdayTitles(t, c, "2028-05-01T00:00:00Z", "2028-06-01T00:00:00Z"); len(got) != 0 {
		t.Errorf("switched off, but shown: %v", got)
	}
	if _, got, _ = c.api.call(http.MethodPut, "/contacts/"+id, map[string]any{"name": "Oma Gerda", "birthday": map[string]any{"day": 3, "month": 5}}); got["show_in_birthdays"] != true {
		t.Errorf("a PUT without the field switches it on again: %v", got["show_in_birthdays"])
	}
}

func TestContactFormSwitchesTheBirthdayOutOfTheCalendarAndKeepsTheSwitch(t *testing.T) {
	t.Parallel()
	c := newCtBox(t)
	f := contactForm("Oma Gerda", "", nil, nil)
	f.Set("bday_day", "3")
	f.Set("bday_month", "5")
	id := c.add(t, f)
	ctHas(t, "new form", c.page("/contacts/new", "en").body, `name="hide_birthday"`, "Birthdays calendar")
	if got := birthdayTitles(t, c, "2028-05-01T00:00:00Z", "2028-06-01T00:00:00Z"); len(got) != 1 {
		t.Fatalf("shown by default: %v", got)
	}
	if strings.Contains(c.page("/contacts/"+id+"/edit", "en").body, `name="hide_birthday" value="1" checked`) {
		t.Error("the switch is off on a new contact")
	}
	f.Set("hide_birthday", "1")
	if r := c.send("/contacts/"+id+"/edit", f); r.status != http.StatusSeeOther {
		t.Fatalf("save = %d\n%s", r.status, r.body)
	}
	if got := birthdayTitles(t, c, "2028-05-01T00:00:00Z", "2028-06-01T00:00:00Z"); len(got) != 0 {
		t.Errorf("switched off, but shown: %v", got)
	}
	ctHas(t, "edit form", c.page("/contacts/"+id+"/edit", "en").body, `name="hide_birthday" value="1" checked`)
	ctHas(t, "German form", c.page("/contacts/new", "de").body, "Geburtstagskalender")
}

func TestBirthdaysCalendarIsNamedInTheLanguageOfWhoMadeIt(t *testing.T) {
	t.Parallel()
	for lang, want := range map[string]string{"en": "Birthdays", "de": "Geburtstage"} {
		c := newCtBox(t)
		f := contactForm("Oma Gerda", "", nil, nil)
		f.Set("bday_day", "3")
		f.Set("bday_month", "5")
		if r := c.do(http.MethodPost, "/contacts/new", f, map[string]string{"Accept-Language": lang}); r.status != http.StatusSeeOther {
			t.Fatalf("%s: add = %d", lang, r.status)
		}
		if cal := birthdayCalendarOf(t, c); cal == nil || cal["name"] != want {
			t.Errorf("%s: calendar = %v, want %s", lang, cal, want)
		}
	}
}

func TestBirthdaysShowInTheWeekOfTheCalendarPageWithTheirAge(t *testing.T) {
	t.Parallel()
	c := newCtBox(t)
	f := contactForm("Mia B", "", nil, nil)
	f.Set("bday_day", "17")
	f.Set("bday_month", "11")
	f.Set("bday_year", "2019")
	c.add(t, f)
	g := contactForm("Oma Gerda", "", nil, nil)
	g.Set("bday_day", "18")
	g.Set("bday_month", "11")
	c.add(t, g)
	page := c.page("/calendar?week=2026-11-16", "en").body
	ctHas(t, "calendar week", page, "Mia B (7)", "Oma Gerda")
	ctHasNot(t, "calendar week", page, "Oma Gerda (")
}
