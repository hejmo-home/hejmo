package server_test

import (
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"testing"
)

const wasteFeed = "https://example.org/waste.ics"

func wasteEvent(uid, title, day string) string {
	return "UID:" + uid + "\r\nSUMMARY:" + title + "\r\nDTSTART;VALUE=DATE:2026111" + day + "\r\nDTEND;VALUE=DATE:2026111" + nextDay(day)
}

func nextDay(d string) string { return string(rune(d[0] + 1)) } // the days used here are 5 to 7

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
	f.addWaste(t, wasteEvent("p1", "Paper bin", "5"))
	p := f.view(t, "en", f.at(15, 9, 0))
	if strings.Contains(strip(t, p), "Paper bin") || strings.Contains(strings.Join(columns(t, p)["Mama"], ""), "Paper") {
		t.Fatalf("waste event in the strip or a column:\n%s", p)
	}
	if r := f.get("/calendars"); !strings.Contains(r.body, `/waste"`) {
		t.Fatal("calendars page has no waste collection switch")
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
	if status, _, _ := c.call(http.MethodPut, "/day-notes/tomorrow", map[string]any{"text": "x"}); status != http.StatusUnprocessableEntity {
		t.Fatalf("bad day = %d", status)
	}
	if status, _, _ := c.call(http.MethodPut, "/day-notes/2026-11-17", map[string]any{"text": ""}); status != http.StatusUnprocessableEntity {
		t.Fatalf("empty text = %d", status)
	}
	if status, _, _ := c.call(http.MethodDelete, "/day-notes/2026-11-16", nil); status != http.StatusNoContent {
		t.Fatalf("delete = %d", status)
	}
	if _, _, list := c.call(http.MethodGet, "/day-notes", nil); len(list) != 0 {
		t.Fatalf("list after delete = %v", list)
	}
}
