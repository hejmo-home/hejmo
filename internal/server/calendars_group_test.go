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
