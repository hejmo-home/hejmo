package server_test

import (
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/hejmo-home/hejmo/internal/caldavclient/caldavtest"
	"github.com/hejmo-home/hejmo/internal/store"
)

const wasteFeed = "https://example.org/waste.ics"

func wasteEvent(uid, title, day string) string {
	return "UID:" + uid + "\r\nSUMMARY:" + title + "\r\nDTSTART;VALUE=DATE:2026111" + day + "\r\nDTEND;VALUE=DATE:2026111" + nextDay(day)
}

func nextDay(d string) string { n, _ := strconv.Atoi(d); return strconv.Itoa(n + 1) } // single digits, 1 to 8

// addWaste subscribes the waste ICS feed and marks it as waste collection through the web interface.
func (f *familyBox) addWaste(t *testing.T, events ...string) string {
	t.Helper()
	f.feeds.set(wasteFeed, feedOf(events...), nil)
	if r := f.post("/calendars", url.Values{"name": {"Waste"}, "url": {wasteFeed}, "interval": {"60"}}); r.status != http.StatusSeeOther {
		t.Fatalf("add waste calendar = %d\n%s", r.status, r.body)
	}
	cals, _ := f.db.Calendars(f.householdID)
	id := ""
	for _, c := range cals {
		if c.Kind == "ics" {
			id = c.ID
		}
	}
	if r := f.post("/calendars/"+id+"/waste", url.Values{"waste": {"on"}}); r.status != http.StatusSeeOther {
		t.Fatalf("mark waste = %d", r.status)
	}
	return id
}

var tomorrowTileRe = regexp.MustCompile(`(?s)<div class="tile( alert)?" data-tile="tomorrow">.*?</div></div>`)

func tomorrowTileOf(page string) string { return tomorrowTileRe.FindString(page) }

// 6.7 AC1: a waste collection tomorrow appears in the tomorrow tile and is highlighted from 18:00.
func TestWasteReminderAppearsTheEveningBefore(t *testing.T) {
	f := newFamilyBox(t, "eink_color")
	f.addWaste(t, wasteEvent("p1", "Paper bin", "6"))
	morning := tomorrowTileOf(f.view(t, "en", f.at(15, 9, 0)))
	if !strings.Contains(morning, "Paper bin") || strings.Contains(morning, "alert") {
		t.Fatalf("morning tile = %q", morning)
	}
	evening := tomorrowTileOf(f.view(t, "en", f.at(15, 18, 0)))
	if !strings.Contains(evening, "Paper bin") || !strings.Contains(evening, `class="tile alert"`) {
		t.Fatalf("18:00 tile = %q", evening)
	}
	if de := tomorrowTileOf(f.view(t, "de", f.at(15, 18, 0))); !strings.Contains(de, "Morgen") {
		t.Fatalf("German tile = %q", de)
	}
}

// 6.7 AC2 and AC3: no collection tomorrow, or the collection day has come: no tile.
func TestWasteReminderIsGoneWithoutACollectionTomorrow(t *testing.T) {
	f := newFamilyBox(t, "eink_color")
	f.addWaste(t, wasteEvent("p1", "Paper bin", "6"))
	for _, now := range []int{14, 16, 17} {
		if p := f.view(t, "en", f.at(now, 19, 0)); strings.Contains(p, `data-tile="tomorrow"`) || strings.Contains(p, "data-footer") {
			t.Errorf("tile shown on %d/11 (collection on 16/11)", now)
		}
	}
}

// ADR 0015: waste events appear only in the tomorrow tile, the reminder and the night page.
func TestWasteEventsStayOutOfTheStripAndColumns(t *testing.T) {
	f := newFamilyBox(t, "eink_color")
	wid := f.addWaste(t, wasteEvent("p1", "Paper bin", "5"))
	p := f.view(t, "en", f.at(15, 9, 0))
	if strings.Contains(strip(t, p), "Paper bin") || strings.Contains(strings.Join(columns(t, p)["Mama"], ""), "Paper") {
		t.Fatalf("waste event in the strip or a column:\n%s", p)
	}
	if r := f.get("/calendars/" + wid); !strings.Contains(r.body, `/waste"`) {
		t.Fatal("calendars page has no waste collection switch")
	}
	// ADR 0015/0016: nor does the personal view show it.
	if r := f.personalPage(t, "en", "Mama", 15, 9); strings.Contains(r.body, "Paper bin") {
		t.Fatalf("waste event in the personal view:\n%s", r.body)
	}
}

// Only read-only calendars can be waste collection (ADR 0015/0016). Every synced calendar is read-only for now
// (CalDAV write-back has its own ticket), so the calendars page offers the switch on each of them; a writable
// calendar is refused by the web form as it is by the API, and stays unmarked.
func TestOnlyReadOnlyCalendarsCanBeWasteCollection(t *testing.T) {
	c, srv := radicaleBox(t)
	family := srv.MakeCalendar(t, "family", "Family")
	srv.Put(t, family, "swim.ics", caldavtest.Event("swim@test", "Swimming", "20261120T150000", "20261120T160000"))
	conn := c.connect(t, davCreds(srv))
	pick := calendarNamed(t, conn, "Family")
	status, cal, _ := c.call(http.MethodPost, "/calendar-connections/"+conn["id"].(string)+"/calendars", map[string]any{"url": pick["url"], "name": "Family"})
	if status != http.StatusCreated {
		t.Fatalf("add calendar = %d %v", status, cal)
	}
	if cal["read_only"] != true {
		t.Fatalf("synced CalDAV calendar is not read-only: %v", cal)
	}
	c.b.feeds.set(wasteFeed, feedOf(wasteEvent("p1", "Paper bin", "5")), nil)
	if r := c.b.post("/calendars", url.Values{"name": {"Waste"}, "url": {wasteFeed}, "interval": {"60"}}); r.status != http.StatusSeeOther {
		t.Fatalf("add waste calendar = %d\n%s", r.status, r.body)
	}
	// The switch sits on the page of a subscribed calendar; the calendars of a connection have no page of
	// their own (ADR 0017) and are marked through the API, where the read-only gate is the same.
	synced, err := c.b.db.SyncedCalendars(mustHousehold(t, c).ID)
	if err != nil {
		t.Fatal(err)
	}
	var feedID string
	for _, sc := range synced {
		if sc.Kind == "ics" {
			feedID = sc.ID
		}
	}
	page := c.b.get("/calendars/" + feedID).body
	if n := strings.Count(page, `/waste"`); n != 1 {
		t.Fatalf("%d waste forms, want 1\n%s", n, page)
	}
	if n := strings.Count(page, `role="switch" name="waste"`); n != 1 {
		t.Fatalf("%d waste switches, want 1\n%s", n, page)
	}

	home, err := c.b.db.CreateCalendar(store.Calendar{HouseholdID: mustHousehold(t, c).ID, Name: "Home", Kind: "local", Color: "#2F5E7A"})
	if err != nil {
		t.Fatal(err)
	}
	if r := c.b.post("/calendars/"+home.ID+"/waste", url.Values{"waste": {"on"}}); r.status != http.StatusUnprocessableEntity {
		t.Fatalf("marking a writable calendar = %d, want 422", r.status)
	}
	if c.calendarJSON(t, home.ID)["waste_collection"] == true {
		t.Fatal("the writable calendar was marked as waste collection")
	}
}

// ADR 0015: unmarking turns the calendar back into an ordinary one.
func TestUnmarkedCalendarShowsItsEventsAgain(t *testing.T) {
	f := newFamilyBox(t, "eink_color")
	id := f.addWaste(t, wasteEvent("p1", "Paper bin", "5"))
	if r := f.post("/calendars/"+id+"/waste", url.Values{"waste": {"off"}}); r.status != http.StatusSeeOther {
		t.Fatalf("unmark = %d", r.status)
	}
	p := f.view(t, "en", f.at(15, 9, 0))
	if !strings.Contains(strip(t, p), "Paper bin") || strings.Contains(p, `data-tile="tomorrow"`) {
		t.Fatalf("after unmarking:\n%s", p)
	}
}

// ADR 0016: the note is the last line; from 18:00 tomorrow's note replaces today's.
func TestNoteForTomorrowIsTheLastLineAndReplacesTodaysFrom1800(t *testing.T) {
	f := newFamilyBox(t, "eink_color")
	f.addWaste(t, wasteEvent("p1", "Paper bin", "6"))
	for day, text := range map[string]string{"2026-11-15": "Swimming bags", "2026-11-16": "Parents' evening"} {
		if r := f.post("/notes", url.Values{"day": {day}, "text": {text}}); r.status != http.StatusSeeOther {
			t.Fatalf("note = %d", r.status)
		}
	}
	day := tomorrowTileOf(f.view(t, "en", f.at(15, 12, 0)))
	if !strings.Contains(day, "Swimming bags") || strings.Contains(day, "Parents") || strings.Index(day, "Paper bin") > strings.Index(day, "Swimming") {
		t.Fatalf("daytime tile = %q", day)
	}
	eve := tomorrowTileOf(f.view(t, "en", f.at(15, 18, 0)))
	if !strings.Contains(eve, "Parents&#39; evening") || strings.Contains(eve, "Swimming") || strings.Index(eve, "Paper bin") > strings.Index(eve, "Parents") {
		t.Fatalf("evening tile = %q", eve)
	}
	f.view(t, "en", f.at(17, 8, 0)) // the days are over: notes are deleted
	if notes, _ := f.db.DayNotes(f.householdID); len(notes) != 0 {
		t.Fatalf("notes kept: %v", notes)
	}
}

// ADR 0015: the night page names the waste collection of the coming morning.
func TestNightPageShowsTheWasteCollectionOfTheMorning(t *testing.T) {
	f := newFamilyBox(t, "eink_color")
	f.quietOn(t)
	f.addWaste(t, wasteEvent("p1", "Paper bin", "5"), wasteEvent("p2", "Yellow bag", "6"))
	p := f.view(t, "en", f.at(14, 22, 30))
	if !strings.Contains(p, "data-waste") || !strings.Contains(p, "Paper bin") || strings.Contains(p, "Yellow bag") {
		t.Fatalf("night page:\n%s", p)
	}
	if strings.Contains(p, "data-no-event") && !strings.Contains(p, "data-waste") {
		t.Fatal("waste missing")
	}
}

// ADR 0015 (web parity): the API marks a calendar as waste collection; only read-only calendars qualify.
func TestWasteCollectionThroughTheAPI(t *testing.T) {
	c := signedInAPI(t)
	_, family := familyOf(t, c)
	c.b.feeds.set(schoolFeed, feedOf(), nil)
	cal := c.addICS(t, map[string]any{"kind": "ics", "name": "Waste", "url": schoolFeed})
	id := cal["id"].(string)
	if cal["waste_collection"] != false {
		t.Fatalf("default = %v", cal["waste_collection"])
	}
	if status, got, _ := c.call(http.MethodPatch, "/calendars/"+id, map[string]any{"waste_collection": true}); status != http.StatusOK || got["waste_collection"] != true {
		t.Fatalf("mark = %d %v", status, got)
	}
	if c.calendarJSON(t, id)["waste_collection"] != true {
		t.Fatal("not listed as waste collection")
	}
	if status, _, _ := c.call(http.MethodPatch, "/calendars/"+family.ID, map[string]any{"waste_collection": true}); status != http.StatusUnprocessableEntity {
		t.Fatalf("family calendar = %d, want 422", status)
	}
}

// ADR 0015 (web parity): a parent sets, lists and removes a note for a day through the API.
func TestDayNotesThroughTheAPI(t *testing.T) {
	c := signedInAPI(t)
	if status, got, _ := c.call(http.MethodPut, "/day-notes/2026-11-16", map[string]any{"text": "Swimming bags"}); status != http.StatusOK || got["text"] != "Swimming bags" {
		t.Fatalf("set = %d %v", status, got)
	}
	if status, got, _ := c.call(http.MethodPut, "/day-notes/2026-11-16", map[string]any{"text": "Football kit"}); status != http.StatusOK || got["text"] != "Football kit" {
		t.Fatalf("replace = %d %v", status, got)
	}
	if _, _, list := c.call(http.MethodGet, "/day-notes", nil); len(list) != 1 || list[0].(map[string]any)["day"] != "2026-11-16" {
		t.Fatalf("list = %v", list)
	}
	if status, _, _ := c.call(http.MethodPut, "/day-notes/2026-11-16", map[string]any{"text": "   "}); status != http.StatusUnprocessableEntity {
		t.Fatalf("whitespace text = %d, want 422", status)
	}
	if _, _, list := c.call(http.MethodGet, "/day-notes", nil); len(list) != 1 {
		t.Fatalf("a refused note changed the list: %v", list)
	}
	if status, _, _ := c.call(http.MethodDelete, "/day-notes/2026-11-16", nil); status != http.StatusNoContent {
		t.Fatalf("delete = %d", status)
	}
	if _, _, list := c.call(http.MethodGet, "/day-notes", nil); len(list) != 0 {
		t.Fatalf("list after delete = %v", list)
	}
}

// The API lists only today and later: notes of days that are over are gone when the list is read.
func TestDayNoteListLeavesOutPastDays(t *testing.T) {
	c := signedInAPI(t)
	for _, day := range []string{"2020-01-01", "2999-01-01"} {
		if status, _, _ := c.call(http.MethodPut, "/day-notes/"+day, map[string]any{"text": "x"}); status != http.StatusOK {
			t.Fatalf("set %s = %d", day, status)
		}
	}
	if _, _, list := c.call(http.MethodGet, "/day-notes", nil); len(list) != 1 || list[0].(map[string]any)["day"] != "2999-01-01" {
		t.Fatalf("list = %v", list)
	}
}

// A PATCH that cannot be applied as a whole changes nothing: marking a writable calendar as waste collection
// is refused before the sharing change in the same request is saved.
func TestWritableCalendarCannotBeWasteCollection(t *testing.T) {
	c := signedInAPI(t)
	familyOf(t, c) // the family calendar comes first, so Home is an ordinary local calendar
	cal, err := c.b.db.CreateCalendar(store.Calendar{HouseholdID: mustHousehold(t, c).ID, Name: "Home", Kind: "local", Color: "#2F5E7A"})
	if err != nil {
		t.Fatal(err)
	}
	was := c.calendarJSON(t, cal.ID)["share_through_box"]
	if status, _, _ := c.call(http.MethodPatch, "/calendars/"+cal.ID, map[string]any{"share_through_box": was != true, "waste_collection": true}); status != http.StatusUnprocessableEntity {
		t.Fatalf("patch = %d, want 422", status)
	}
	if c.calendarJSON(t, cal.ID)["share_through_box"] != was {
		t.Fatal("the sharing change was saved although the request was refused")
	}
}

// The web form counts the trimmed text, like the API.
func TestNoteWithSurroundingSpaceCountsTrimmed(t *testing.T) {
	f := newFamilyBox(t, "eink_color")
	text := " " + strings.Repeat("a", 120) + " "
	if r := f.post("/notes", url.Values{"day": {"2026-11-16"}, "text": {text}}); r.status != http.StatusSeeOther {
		t.Fatalf("note of 120 characters with spaces = %d", r.status)
	}
}
