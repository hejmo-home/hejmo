package server_test

import (
	"html"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"testing"
)

// calendarBox is the example family with a paired display and the parent signed in.
func calendarBox(t *testing.T) *familyBox {
	t.Helper()
	return newFamilyBox(t, "screen")
}

func (f *familyBox) postEvent(t *testing.T, form url.Values) resp {
	t.Helper()
	r := f.post("/calendar/events", form)
	if r.status != http.StatusSeeOther {
		t.Fatalf("create event = %d\n%s", r.status, r.body)
	}
	return r
}

func eventForm(title, date, start, end string) url.Values {
	return url.Values{"title": {title}, "start_date": {date}, "start_time": {start}, "end_date": {date}, "end_time": {end}, "repeat": {"none"}, "whole_family": {"1"}}
}

// Ticket 5.12: Given a parent, when they create a weekly event for Mia, then it
// appears in the week view and on the display.
func TestParentCreatesAWeeklyEventForMiaAndItShowsInTheWeekAndOnTheDisplay(t *testing.T) {
	f := calendarBox(t)
	form := eventForm("Swimming", "2026-11-14", "15:00", "16:00")
	form.Set("repeat", "weekly")
	form.Del("whole_family")
	form["people"] = []string{f.people["Mia"].ID}
	if r := f.postEvent(t, form); r.location != "/calendar?week=2026-11-09" {
		t.Fatalf("redirect = %q", r.location)
	}
	week := getEN(f.box, "/calendar?week=2026-11-09")
	if week.status != http.StatusOK || !strings.Contains(week.body, "Swimming") || !strings.Contains(week.body, "15:00") {
		t.Fatalf("week view lacks the event (%d):\n%s", week.status, week.body)
	}
	next := getEN(f.box, "/calendar?week=2026-11-16")
	if !strings.Contains(next.body, "Swimming") {
		t.Fatalf("next week lacks the weekly repeat:\n%s", next.body)
	}
	f.now = f.at(14, 13, 0)
	if cols := columns(t, f.view(t, "en", f.at(14, 13, 0))); strings.Join(cols["Mia"], "|") != "Swimming" {
		t.Fatalf("Mia's column = %v", cols)
	}
}

var eventLinkRe = regexp.MustCompile(`href="(/calendar/events/[^"]+)"`)

// eventLinks returns the addresses of the event pages a page links to.
func eventLinks(page string) []string {
	var out []string
	for _, m := range eventLinkRe.FindAllStringSubmatch(page, -1) {
		out = append(out, html.UnescapeString(m[1]))
	}
	return out
}

// linkTo finds the address of the event page for title on a page.
func linkTo(t *testing.T, page, title string) string {
	t.Helper()
	re := regexp.MustCompile(`href="(/calendar/events/[^"]+)">` + regexp.QuoteMeta(title) + `<`)
	m := re.FindStringSubmatch(page)
	if m == nil {
		t.Fatalf("no link to %q on the page:\n%s", title, page)
	}
	return html.UnescapeString(m[1])
}

func occurrenceOf(t *testing.T, link string) (path, occurrence string) {
	t.Helper()
	u, err := url.Parse(link)
	if err != nil {
		t.Fatal(err)
	}
	return u.Path, u.Query().Get("occurrence")
}

// weekdayTimes returns the times shown for an event title on a page, one per appearance.
func timesOf(page, title string) []string {
	re := regexp.MustCompile(`>` + regexp.QuoteMeta(title) + `</a> <span class="meta">([^<]*)</span>`)
	var out []string
	for _, m := range re.FindAllStringSubmatch(page, -1) {
		out = append(out, m[1])
	}
	return out
}

// Ticket 5.12: Given a series, when "this and following" is changed, then
// earlier occurrences remain unchanged.
func TestChangingThisAndFollowingLeavesEarlierOccurrencesUnchanged(t *testing.T) {
	f := calendarBox(t)
	form := eventForm("Choir", "2026-11-02", "17:00", "18:00")
	form.Set("repeat", "weekly")
	f.postEvent(t, form)
	week := getEN(f.box, "/calendar?week=2026-11-16").body
	path, occ := occurrenceOf(t, linkTo(t, week, "Choir"))

	// The edit page asks for the scope.
	page := getEN(f.box, path+"?occurrence="+url.QueryEscape(occ))
	if page.status != http.StatusOK {
		t.Fatalf("edit page = %d", page.status)
	}
	for _, want := range []string{"Only this event", "This and following events", "All events of the series", `name="scope" value="this" checked`} {
		if !strings.Contains(page.body, want) {
			t.Errorf("edit page lacks %q", want)
		}
	}
	edit := eventForm("Choir (big hall)", "2026-11-16", "18:00", "19:00")
	edit.Set("repeat", "weekly")
	edit.Set("occurrence", occ)
	edit.Set("scope", "following")
	if r := f.post(path, edit); r.status != http.StatusSeeOther || r.location != "/calendar?week=2026-11-16" {
		t.Fatalf("edit = %d %q\n%s", r.status, r.location, r.body)
	}
	earlier := getEN(f.box, "/calendar?week=2026-11-09").body
	if got := timesOf(earlier, "Choir"); len(got) != 1 || got[0] != "17:00" || strings.Contains(earlier, "big hall") {
		t.Fatalf("earlier week changed: %v\n%s", got, earlier)
	}
	first := getEN(f.box, "/calendar?week=2026-11-02").body
	if got := timesOf(first, "Choir"); len(got) != 1 || got[0] != "17:00" {
		t.Fatalf("first week changed: %v", got)
	}
	for _, w := range []string{"2026-11-16", "2026-11-23"} {
		if got := timesOf(getEN(f.box, "/calendar?week="+w).body, "Choir (big hall)"); len(got) != 1 || got[0] != "18:00" {
			t.Fatalf("week %s: %v", w, got)
		}
	}
}

func TestChangingOnlyThisOccurrenceAndAllOfASeries(t *testing.T) {
	f := calendarBox(t)
	form := eventForm("Piano", "2026-11-02", "16:00", "17:00")
	form.Set("repeat", "weekly")
	f.postEvent(t, form)
	path, occ := occurrenceOf(t, linkTo(t, getEN(f.box, "/calendar?week=2026-11-09").body, "Piano"))
	edit := eventForm("Piano (exam)", "2026-11-09", "16:00", "17:00")
	edit.Set("repeat", "weekly")
	edit.Set("occurrence", occ)
	edit.Set("scope", "this")
	f.post(path, edit)
	if got := timesOf(getEN(f.box, "/calendar?week=2026-11-09").body, "Piano (exam)"); len(got) != 1 {
		t.Fatalf("changed occurrence missing: %v", got)
	}
	if body := getEN(f.box, "/calendar?week=2026-11-16").body; !strings.Contains(body, "Piano") || strings.Contains(body, "exam") {
		t.Fatalf("only the chosen occurrence must change:\n%s", body)
	}
	path, occ = occurrenceOf(t, linkTo(t, getEN(f.box, "/calendar?week=2026-11-16").body, "Piano"))
	all := eventForm("Piano lessons", "2026-11-16", "16:30", "17:30")
	all.Set("repeat", "weekly")
	all.Set("occurrence", occ)
	all.Set("scope", "all")
	f.post(path, all)
	for _, w := range []string{"2026-11-02", "2026-11-23"} {
		if got := timesOf(getEN(f.box, "/calendar?week="+w).body, "Piano lessons"); len(got) != 1 || got[0] != "16:30" {
			t.Fatalf("week %s: %v", w, got)
		}
	}
}

// Ticket 5.12: Given an event spanning three days, when the week view is
// shown, then it appears as one continuous bar.
func TestAnEventOfThreeDaysIsOneContinuousBar(t *testing.T) {
	f := calendarBox(t)
	form := url.Values{"title": {"Hut weekend"}, "all_day": {"on"}, "start_date": {"2026-11-17"}, "end_date": {"2026-11-19"}, "repeat": {"none"}, "whole_family": {"1"}}
	f.postEvent(t, form)
	body := getEN(f.box, "/calendar?week=2026-11-16").body
	if n := strings.Count(body, `<li class="bar `); n != 1 {
		t.Fatalf("%d bars, want 1:\n%s", n, body)
	}
	if !strings.Contains(body, `data-col="2" data-span="3" data-lane="1"`) {
		t.Fatalf("bar does not span Tue to Thu:\n%s", body)
	}
	// Stored with an exclusive end, as all-day events are.
	h, _ := f.db.HomeHousehold()
	events, _ := f.db.CalendarEvents(h.ID, f.family.ID)
	if len(events) != 1 || events[0].End.Format("2006-01-02") != "2026-11-20" || !events[0].AllDay {
		t.Fatalf("stored = %+v", events)
	}
	// A week that only shows the last day carries the bar on with a flag.
	form.Set("start_date", "2026-11-22")
	form.Set("end_date", "2026-11-24")
	form.Set("title", "Trip")
	f.postEvent(t, form)
	if next := getEN(f.box, "/calendar?week=2026-11-23").body; !strings.Contains(next, `class="bar c1 s2 before"`) || !strings.Contains(next, `data-col="1" data-span="2"`) {
		t.Fatalf("continued bar missing:\n%s", next)
	}
}

// Ticket 5.12: Given a read-only calendar, then its events offer no edit actions.
func TestEventsOfReadOnlyCalendarsOfferNoEditActions(t *testing.T) {
	f := calendarBox(t)
	f.feeds.set("https://example.org/school.ics", feedOf("UID:trip\r\nSUMMARY:School trip\r\nDTSTART:20261117T080000Z\r\nDTEND:20261117T120000Z"), nil)
	if r := f.post("/calendars", url.Values{"name": {"School"}, "url": {"https://example.org/school.ics"}, "color": {"#2F5E7A"}, "interval": {"60"}, "whole_family": {"1"}}); r.status != http.StatusSeeOther {
		t.Fatalf("add calendar = %d", r.status)
	}
	f.postEvent(t, eventForm("Dentist", "2026-11-17", "10:00", "11:00"))
	week := getEN(f.box, "/calendar?week=2026-11-16").body
	if !strings.Contains(week, "School trip") || !strings.Contains(week, "Read-only") {
		t.Fatalf("read-only event not shown as such:\n%s", week)
	}
	links := eventLinks(week)
	if len(links) != 1 {
		t.Fatalf("links = %v, want only the family event", links)
	}
	linkTo(t, week, "Dentist")
	if s := getEN(f.box, "/calendar/search?q=school"); !strings.Contains(s.body, "School trip") || len(eventLinks(s.body)) != 0 {
		t.Fatalf("search offers an edit link for a read-only event:\n%s", s.body)
	}
	h, _ := f.db.HomeHousehold()
	cals, _ := f.db.SyncedCalendars(h.ID)
	events, _ := f.db.CalendarEvents(h.ID, cals[0].ID)
	if len(events) != 1 {
		t.Fatalf("events = %v", events)
	}
	id := events[0].ID
	// Typing the address by hand shows the event without a form.
	page := getEN(f.box, "/calendar/events/"+id)
	if page.status != http.StatusOK || strings.Contains(page.body, "<form method=\"post\"") && strings.Contains(page.body, "/calendar/events/"+id) || !strings.Contains(page.body, "data-readonly") {
		t.Fatalf("read-only page offers a form (%d):\n%s", page.status, page.body)
	}
	if r := f.post("/calendar/events/"+id, eventForm("Hacked", "2026-11-17", "08:00", "09:00")); r.status != http.StatusUnprocessableEntity {
		t.Fatalf("edit = %d", r.status)
	}
	if r := f.post("/calendar/events/"+id+"/delete", url.Values{}); r.status != http.StatusUnprocessableEntity {
		t.Fatalf("delete = %d", r.status)
	}
	create := eventForm("New", "2026-11-17", "08:00", "09:00")
	create.Set("calendar", cals[0].ID)
	if r := f.post("/calendar/events", create); r.status != http.StatusUnprocessableEntity {
		t.Fatalf("create in a read-only calendar = %d", r.status)
	}
	if after, _ := f.db.CalendarEvents(h.ID, cals[0].ID); len(after) != 1 || after[0].Title != "School trip" {
		t.Fatalf("read-only calendar changed: %+v", after)
	}
}

// Ticket 5.12: Given a search for "schwimm", then "Schwimmen" is found (prefix,
// accent-insensitive).
func TestSearchFindsEventsByPrefixIgnoringAccents(t *testing.T) {
	f := calendarBox(t)
	f.postEvent(t, eventForm("Schwimmen", "2026-11-18", "15:00", "16:00"))
	f.postEvent(t, eventForm("Café Treff", "2026-11-19", "15:00", "16:00"))
	f.postEvent(t, eventForm("Dentist", "2026-11-19", "09:00", "10:00"))
	r := getEN(f.box, "/calendar/search?q=schwimm")
	if r.status != http.StatusOK || !strings.Contains(r.body, "Schwimmen") || strings.Contains(r.body, "Dentist") || len(eventLinks(r.body)) != 1 {
		t.Fatalf("search = %d\n%s", r.status, r.body)
	}
	if r := getEN(f.box, "/calendar/search?q=cafe"); !strings.Contains(r.body, "Café Treff") {
		t.Fatalf("accent-insensitive search failed:\n%s", r.body)
	}
	if r := getEN(f.box, "/calendar/search?q=zzz"); !strings.Contains(r.body, "Nothing found") {
		t.Fatalf("empty result lacks the message:\n%s", r.body)
	}
}

func TestParentDeletesEventsBehindAConfirmationWithAScopeForSeries(t *testing.T) {
	f := calendarBox(t)
	f.postEvent(t, eventForm("Dentist", "2026-11-17", "10:00", "11:00"))
	series := eventForm("Choir", "2026-11-02", "17:00", "18:00")
	series.Set("repeat", "weekly")
	f.postEvent(t, series)

	path, occ := occurrenceOf(t, linkTo(t, getEN(f.box, "/calendar?week=2026-11-16").body, "Dentist"))
	page := getEN(f.box, path+"?occurrence="+url.QueryEscape(occ)).body
	if !strings.Contains(page, `<details class="danger">`) || !strings.Contains(page, "Delete for good") {
		t.Fatalf("delete is not behind a confirmation:\n%s", page)
	}
	if strings.Contains(strings.SplitAfter(page, `<details class="danger">`)[1], `name="scope"`) {
		t.Error("a single event asks for a scope")
	}
	if r := f.post(path+"/delete", url.Values{"occurrence": {occ}}); r.status != http.StatusSeeOther || r.location != "/calendar?week=2026-11-16" {
		t.Fatalf("delete = %d %q", r.status, r.location)
	}
	if strings.Contains(getEN(f.box, "/calendar?week=2026-11-16").body, "Dentist") {
		t.Fatal("event is still there")
	}

	path, occ = occurrenceOf(t, linkTo(t, getEN(f.box, "/calendar?week=2026-11-16").body, "Choir"))
	page = getEN(f.box, path+"?occurrence="+url.QueryEscape(occ)).body
	if danger := strings.SplitAfter(page, `<details class="danger">`)[1]; !strings.Contains(danger, "This and following events") {
		t.Fatalf("a series asks no scope when deleting:\n%s", danger)
	}
	f.post(path+"/delete", url.Values{"occurrence": {occ}, "scope": {"following"}})
	if strings.Contains(getEN(f.box, "/calendar?week=2026-11-16").body, "Choir") || !strings.Contains(getEN(f.box, "/calendar?week=2026-11-09").body, "Choir") {
		t.Fatal("deleting this and following must keep earlier events and remove later ones")
	}
	if r := f.post("/calendar/events/00000000-0000-4000-8000-000000000000/delete", url.Values{}); r.status != http.StatusNotFound {
		t.Fatalf("unknown = %d", r.status)
	}
}

func TestEventFormShowsErrorsAtTheFieldAndKeepsEntries(t *testing.T) {
	f := calendarBox(t)
	for _, c := range []struct {
		change func(url.Values)
		field  string
	}{
		{func(v url.Values) { v.Set("title", " ") }, `name="title"`},
		{func(v url.Values) { v.Set("end_time", "14:00") }, `name="end_date"`},
		{func(v url.Values) { v.Set("start_date", "soon") }, `name="start_date"`},
		{func(v url.Values) { v.Set("timezone", "Mars/Olympus") }, `name="timezone"`},
		{func(v url.Values) { v.Set("repeat", "weekly"); v.Set("until", "2026-01-01") }, `name="until"`},
	} {
		v := eventForm("Choir", "2026-11-17", "15:00", "16:00")
		c.change(v)
		r := f.do(http.MethodPost, "/calendar/events", v, map[string]string{"Accept-Language": "en"})
		if r.status != http.StatusUnprocessableEntity || !strings.Contains(r.body, `role="alert"`) || !strings.Contains(r.body, `aria-invalid="true"`) {
			t.Errorf("%s: status %d\n%s", c.field, r.status, r.body)
		}
		if !strings.Contains(r.body, `value="15:00"`) && c.field != `name="start_date"` {
			t.Errorf("%s: entries are lost", c.field)
		}
	}
}

func TestCalendarScreenIsForParentsAndGerman(t *testing.T) {
	b, _ := configuredBox(t)
	for _, path := range []string{"/calendar", "/calendar/new", "/calendar/search?q=a"} {
		if r := b.get(path); r.status != http.StatusSeeOther || !strings.HasPrefix(r.location, "/login") {
			t.Errorf("signed out %s = %d %q", path, r.status, r.location)
		}
	}
	f := calendarBox(t)
	r := f.do(http.MethodGet, "/calendar?week=2026-11-16", nil, map[string]string{"Accept-Language": "de"})
	if r.status != http.StatusOK || !strings.Contains(r.body, "Neuer Termin") || !strings.Contains(r.body, "Woche 47") {
		t.Fatalf("de page = %d\n%s", r.status, r.body)
	}
	if nav := getEN(f.box, "/").body; !strings.Contains(nav, `href="/calendar"`) {
		t.Error("the navigation has no calendar entry")
	}
}

// A saved event is the same through the API (ADR 0015: parity).
func TestEventsMadeInTheWebInterfaceAreVisibleThroughTheAPI(t *testing.T) {
	f := calendarBox(t)
	form := eventForm("Swimming", "2026-11-14", "15:00", "16:00")
	form.Set("repeat", "weekly")
	form.Set("timezone", "Europe/London")
	form.Del("whole_family")
	form["people"] = []string{f.people["Mia"].ID}
	f.postEvent(t, form)
	c := newAPIClient(t, f.box)
	c.cookie = true
	occs := c.occurrences(t, november)
	if len(occs) != 3 {
		t.Fatalf("occurrences in November = %v", occTitles(occs))
	}
	o := occs[0]
	people, _ := o["people"].([]any)
	if o["title"] != "Swimming" || o["start"] != "2026-11-14T16:00:00+01:00" || len(people) != 1 || people[0] != f.people["Mia"].ID {
		t.Fatalf("occurrence = %v", o)
	}
	h, _ := f.db.HomeHousehold()
	events, _ := f.db.CalendarEvents(h.ID, f.family.ID)
	if len(events) != 1 || events[0].RRule != "FREQ=WEEKLY" || events[0].Start.Location().String() != "Europe/London" {
		t.Fatalf("stored = %+v", events)
	}
}

// The earlier half of a split series ends the day before the split; editing it
// without touching repeat or end must not bring the split-off occurrence back.
func TestEditingTheEarlierHalfOfASplitSeriesKeepsItsEnd(t *testing.T) {
	f := calendarBox(t)
	form := eventForm("Choir", "2026-11-02", "17:00", "18:00")
	form.Set("repeat", "weekly")
	f.postEvent(t, form)
	path, occ := occurrenceOf(t, linkTo(t, getEN(f.box, "/calendar?week=2026-11-16").body, "Choir"))
	split := eventForm("Choir (big hall)", "2026-11-16", "17:00", "18:00")
	split.Set("repeat", "weekly")
	split.Set("occurrence", occ)
	split.Set("scope", "following")
	f.post(path, split)

	path, occ = occurrenceOf(t, linkTo(t, getEN(f.box, "/calendar?week=2026-11-09").body, "Choir"))
	page := getEN(f.box, path+"?occurrence="+url.QueryEscape(occ)).body
	m := regexp.MustCompile(`name="until"[^>]*value="([^"]*)"|value="([^"]*)"[^>]*name="until"`).FindStringSubmatch(page)
	if m == nil {
		t.Fatalf("no end date on the edit page:\n%s", page)
	}
	until := m[1] + m[2]
	for _, scope := range []string{"all", "following"} {
		edit := eventForm("Choir (small hall)", "2026-11-09", "17:00", "18:00")
		edit.Set("repeat", "weekly")
		edit.Set("until", until)
		edit.Set("occurrence", occ)
		edit.Set("scope", scope)
		if r := f.post(path, edit); r.status != http.StatusSeeOther {
			t.Fatalf("%s: edit = %d\n%s", scope, r.status, r.body)
		}
		week := getEN(f.box, "/calendar?week=2026-11-16").body
		if strings.Contains(week, "small hall") || !strings.Contains(week, "big hall") {
			t.Fatalf("%s: the split-off occurrence changed or came back:\n%s", scope, week)
		}
		if !strings.Contains(getEN(f.box, "/calendar?week=2026-11-09").body, "small hall") {
			t.Fatalf("%s: the earlier half was not changed", scope)
		}
	}
}

// An event page opened without ?occurrence= must still save when the first
// occurrence of its series was moved.
func TestEventPageOfASeriesWhoseFirstOccurrenceWasMovedStillSaves(t *testing.T) {
	f := calendarBox(t)
	form := eventForm("Piano", "2026-11-02", "16:00", "17:00")
	form.Set("repeat", "weekly")
	f.postEvent(t, form)
	path, occ := occurrenceOf(t, linkTo(t, getEN(f.box, "/calendar?week=2026-11-02").body, "Piano"))
	move := eventForm("Piano", "2026-11-02", "18:00", "19:00")
	move.Set("repeat", "weekly")
	move.Set("occurrence", occ)
	move.Set("scope", "this")
	f.post(path, move)

	page := getEN(f.box, path).body
	m := regexp.MustCompile(`name="occurrence" value="([^"]*)"`).FindStringSubmatch(page)
	if m == nil {
		t.Fatalf("no occurrence on the page:\n%s", page)
	}
	save := eventForm("Piano (moved)", "2026-11-02", "18:00", "19:00")
	save.Set("repeat", "weekly")
	save.Set("occurrence", html.UnescapeString(m[1]))
	save.Set("scope", "this")
	if r := f.post(path, save); r.status != http.StatusSeeOther {
		t.Fatalf("save = %d\n%s", r.status, r.body)
	}
}

// ADR 0015: the zone of an event is not changed afterwards; the API says so.
func TestAPIRefusesATimezoneWhenChangingAnEvent(t *testing.T) {
	c := signedInAPI(t)
	ev := c.createEvent(t, map[string]any{"title": "Call", "start": "2026-11-14T09:00:00+01:00", "end": "2026-11-14T10:00:00+01:00"})
	status, _, _ := c.call(http.MethodPatch, "/events/"+ev["id"].(string), map[string]any{"timezone": "Europe/London"})
	if status != http.StatusUnprocessableEntity {
		t.Fatalf("patch with timezone = %d", status)
	}
}
