package server_test

import (
	"net/http"
	"regexp"
	"strings"
	"testing"

	"github.com/hejmo-home/hejmo/internal/store"
)

// calendarGroups reads the "Synchronised calendars" section of the page as
// ordered group heading -> calendar names.
func calendarGroups(t *testing.T, page string) (headings []string, calendars map[string][]string) {
	t.Helper()
	start := strings.Index(page, `aria-labelledby="synced"`)
	end := strings.Index(page, `aria-labelledby="add"`)
	if start < 0 || end < start {
		t.Fatalf("no synced section:\n%s", page)
	}
	calendars = map[string][]string{}
	for _, part := range strings.Split(page[start:end], `<section aria-label="`)[1:] {
		name := part[:strings.Index(part, `"`)]
		headings = append(headings, name)
		for _, m := range regexp.MustCompile(`</strong>`).FindAllStringIndex(part, -1) {
			head := part[:m[0]]
			calendars[name] = append(calendars[name], strings.TrimSpace(head[strings.LastIndex(head, ">")+1:]))
		}
	}
	return headings, calendars
}

func TestCalendarsPageGroupsSubscribedCalendarsByPerson(t *testing.T) {
	c := signedInAPI(t)
	h := mustHousehold(t, c)
	mia, _ := c.b.db.AddPerson(h.ID, store.Person{Name: "Mia", Role: store.RoleChild})
	seb, _ := c.b.db.AddPerson(h.ID, store.Person{Name: "Sebastian", Role: store.RoleChild})
	kids, err := c.b.db.CreateGroup(h.ID, "Kids", []string{mia.ID, seb.ID})
	if err != nil {
		t.Fatal(err)
	}
	c.b.feeds.set(schoolFeed, feedOf(), nil)
	two := func(people, groups []string) map[string]any {
		return map[string]any{"people": people, "groups": groups}
	}
	c.addICS(t, map[string]any{"kind": "ics", "name": "Bin collection", "url": schoolFeed})
	c.addICS(t, map[string]any{"kind": "ics", "name": "School holidays", "url": schoolFeed, "audience": two([]string{seb.ID, mia.ID}, []string{})})
	c.addICS(t, map[string]any{"kind": "ics", "name": "Swimming", "url": schoolFeed, "audience": two([]string{}, []string{kids.ID})})
	c.addICS(t, map[string]any{"kind": "ics", "name": "Football", "url": schoolFeed, "audience": two([]string{seb.ID}, []string{})})

	for lang, whole := range map[string]string{"en": "Whole household", "de": "Ganzer Haushalt"} {
		r := c.b.do(http.MethodGet, "/calendars", nil, map[string]string{"Accept-Language": lang})
		headings, cals := calendarGroups(t, r.body)
		// Household order; Mama has no calendar, so her group is not shown.
		wantHeadings := []string{whole, "Mia", "Sebastian"}
		if strings.Join(headings, "|") != strings.Join(wantHeadings, "|") {
			t.Fatalf("%s: groups = %v, want %v", lang, headings, wantHeadings)
		}
		want := map[string][]string{
			whole:       {"Bin collection"},
			"Mia":       {"School holidays", "Swimming"},
			"Sebastian": {"School holidays", "Swimming", "Football"},
		}
		for g, names := range want {
			sameList(t, cals[g], names)
		}
		if !strings.Contains(r.body, mia.Color) {
			t.Fatalf("%s: Mia's colour %q missing", lang, mia.Color)
		}
	}
}

func TestCalendarsPageShowsNoGroupForAnEmptyHousehold(t *testing.T) {
	c := signedInAPI(t)
	r := c.b.do(http.MethodGet, "/calendars", nil, map[string]string{"Accept-Language": "en"})
	if strings.Contains(r.body, "Whole household") {
		t.Fatalf("empty group shown:\n%s", r.body)
	}
}

func newGroupsPage(t *testing.T) (c *apiClient, mia, seb, mama store.Person, add func(name string, people, groups []string)) {
	t.Helper()
	c = signedInAPI(t)
	h := mustHousehold(t, c)
	people, err := c.b.db.People(h.ID)
	if err != nil || len(people) == 0 {
		t.Fatalf("people: %v %v", people, err)
	}
	mama = people[0]
	mia, _ = c.b.db.AddPerson(h.ID, store.Person{Name: "Mia", Role: store.RoleChild, Color: "#c0392b"})
	seb, _ = c.b.db.AddPerson(h.ID, store.Person{Name: "Sebastian", Role: store.RoleChild})
	c.b.feeds.set(schoolFeed, feedOf(), nil)
	add = func(name string, people, groups []string) {
		if people == nil {
			people = []string{}
		}
		if groups == nil {
			groups = []string{}
		}
		c.addICS(t, map[string]any{"kind": "ics", "name": name, "url": schoolFeed, "audience": map[string]any{"people": people, "groups": groups}})
	}
	return
}

func page(c *apiClient, lang string) string {
	return c.b.do(http.MethodGet, "/calendars", nil, map[string]string{"Accept-Language": lang}).body
}

// A calendar visible to nobody must not vanish with its buttons.
func TestCalendarNobodySeesStaysListedInALastGroup(t *testing.T) {
	c, mia, _, _, add := newGroupsPage(t)
	h := mustHousehold(t, c)
	empty, err := c.b.db.CreateGroup(h.ID, "Nobody", nil)
	if err != nil {
		t.Fatal(err)
	}
	add("Orphan", nil, []string{empty.ID})
	add("Swimming", []string{mia.ID}, nil)
	for lang, label := range map[string]string{"en": "Not assigned to anyone", "de": "Niemandem zugeordnet"} {
		body := page(c, lang)
		headings, cals := calendarGroups(t, body)
		if strings.Join(headings, "|") != "Mia|"+label {
			t.Fatalf("%s: groups = %v", lang, headings)
		}
		sameList(t, cals[label], []string{"Orphan"})
		if strings.Contains(body, "Whole household") || strings.Contains(body, "Ganzer Haushalt") {
			t.Fatalf("%s: orphan shown as whole household", lang)
		}
		i := strings.Index(body, `aria-label="`+label+`"`)
		if !strings.Contains(body[i:], "/delete") || !strings.Contains(body[i:], "/sync") {
			t.Fatalf("%s: orphan lacks sync/remove forms", lang)
		}
	}
}

func TestWholeHouseholdIsHiddenWhenEmptyAndPeopleKeepHouseholdOrder(t *testing.T) {
	c, mia, seb, mama, add := newGroupsPage(t)
	// Added in a different order than the household; Mia (middle) has none.
	add("Football", []string{seb.ID}, nil)
	add("Yoga", []string{mama.ID}, nil)
	headings, _ := calendarGroups(t, page(c, "en"))
	if strings.Join(headings, "|") != mama.Name+"|Sebastian" {
		t.Fatalf("groups = %v (mia %s)", headings, mia.Name)
	}
}

func TestCalendarInAudienceDirectlyAndViaGroupIsListedOnce(t *testing.T) {
	c, mia, seb, _, add := newGroupsPage(t)
	h := mustHousehold(t, c)
	kids, _ := c.b.db.CreateGroup(h.ID, "Kids", []string{mia.ID, seb.ID})
	add("Swimming", []string{mia.ID}, []string{kids.ID})
	_, cals := calendarGroups(t, page(c, "en"))
	sameList(t, cals["Mia"], []string{"Swimming"})
	sameList(t, cals["Sebastian"], []string{"Swimming"})
}

func TestGroupHeadingCarriesThePersonColour(t *testing.T) {
	c, mia, _, _, add := newGroupsPage(t)
	add("Swimming", []string{mia.ID}, nil)
	body := page(c, "en")
	i := strings.Index(body, `<section aria-label="Mia">`)
	if i < 0 {
		t.Fatalf("no Mia group:\n%s", body)
	}
	h3 := body[i : i+strings.Index(body[i:], "</h3>")]
	if !strings.Contains(h3, `value="`+mia.Color+`"`) {
		t.Fatalf("heading lacks colour %q: %s", mia.Color, h3)
	}
}
