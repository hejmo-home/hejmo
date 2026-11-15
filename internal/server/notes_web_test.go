package server_test

import (
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"testing"
)

const bin = `<svg class="icon" width="20" height="20" viewBox="0 0 24 24" aria-hidden="true"><path d="M4 7h16"/><path d="M9.5 7V4.5h5V7"/><path d="M6 7l1 13h10l1-13"/><path d="M10 11v5.5M14 11v5.5"/></svg>`

func (f *familyBox) page(lang, path string) resp {
	return f.do(http.MethodGet, path, nil, map[string]string{"Accept-Language": lang})
}

func (f *familyBox) save(path string, form url.Values, lang string) resp {
	return f.do(http.MethodPost, path, form, map[string]string{"Accept-Language": lang})
}

func mustContain(t *testing.T, page string, want ...string) {
	t.Helper()
	for _, w := range want {
		if !strings.Contains(page, w) {
			t.Errorf("missing %q in:\n%s", w, page)
		}
	}
}

func mustNotContain(t *testing.T, page string, bad ...string) {
	t.Helper()
	for _, w := range bad {
		if strings.Contains(page, w) {
			t.Errorf("unexpected %q in:\n%s", w, page)
		}
	}
}

// Design pass: the reminder says what to do, each collection has a bin icon, the note is set apart.
func TestTomorrowTileDrawsReminderPillBinsAndNote(t *testing.T) {
	f := newFamilyBox(t, "eink_color")
	f.addWaste(t, wasteEvent("p1", "Paper bin", "6"), wasteEvent("p2", "Residual waste", "6"))
	if r := f.post("/notes", url.Values{"day": {"2026-11-16"}, "text": {"Mia: swimming bag"}}); r.status != http.StatusSeeOther {
		t.Fatalf("note = %d", r.status)
	}
	eve := tomorrowTileOf(f.view(t, "en", f.at(15, 18, 0)))
	mustContain(t, eve, `class="tile alert"`, `<div class="tile-head"><span class="lab">Tomorrow</span><span class="flag">Put out tonight</span></div>`,
		`<div class="ln waste">`+bin+`<span>Paper bin</span></div>`, `<div class="ln waste">`+bin+`<span>Residual waste</span></div>`,
		`<div class="ln note">Mia: swimming bag</div>`)
	if strings.Index(eve, `ln note`) < strings.Index(eve, "Residual") {
		t.Fatal("the note is not the last line")
	}
	if de := tomorrowTileOf(f.view(t, "de", f.at(15, 18, 0))); !strings.Contains(de, `<span class="flag">Heute Abend rausstellen</span>`) {
		t.Fatalf("German pill: %s", de)
	}
	morning := tomorrowTileOf(f.view(t, "en", f.at(15, 9, 0)))
	mustNotContain(t, morning, "flag", "alert", "more")
	mustContain(t, morning, `<div class="tile-head"><span class="lab">Tomorrow</span></div>`)
}

// Design pass: today's note says "Today" next to a collection; alone, the tile is labelled "Today".
func TestTomorrowTileTodaysNote(t *testing.T) {
	f := newFamilyBox(t, "eink_color")
	if r := f.post("/notes", url.Values{"day": {"2026-11-15"}, "text": {"Football kit"}}); r.status != http.StatusSeeOther {
		t.Fatalf("note = %d", r.status)
	}
	alone := tomorrowTileOf(f.view(t, "en", f.at(15, 9, 0)))
	mustContain(t, alone, `<span class="lab">Today</span>`, `<div class="ln note">Football kit</div>`)
	mustNotContain(t, alone, "Tomorrow", `class="day"`, "flag")
	if de := tomorrowTileOf(f.view(t, "de", f.at(15, 9, 0))); !strings.Contains(de, `<span class="lab">Heute</span>`) {
		t.Fatalf("German label: %s", de)
	}
	f.addWaste(t, wasteEvent("p1", "Paper bin", "6"))
	next := tomorrowTileOf(f.view(t, "en", f.at(15, 9, 0)))
	mustContain(t, next, `<span class="lab">Tomorrow</span>`, `<div class="ln note"><span class="day">Today</span>Football kit</div>`)
}

// Design pass: four collections share one line, every title named, no "+N more"; the note stays.
func TestTomorrowTileNamesAllCollectionsOnOneLine(t *testing.T) {
	f := newFamilyBox(t, "eink_color")
	f.addWaste(t, wasteEvent("p1", "Paper bin", "6"), wasteEvent("p2", "Residual waste", "6"), wasteEvent("p3", "Organic waste", "6"), wasteEvent("p4", "Yellow bag", "6"))
	f.post("/notes", url.Values{"day": {"2026-11-16"}, "text": {"Mia: swimming bag"}})
	tile := tomorrowTileOf(f.view(t, "en", f.at(15, 18, 0)))
	mustContain(t, tile, `Mia: swimming bag`, "Paper bin", "Residual waste", "Organic waste", "Yellow bag")
	if !regexp.MustCompile(`<div class="ln waste">` + regexp.QuoteMeta(bin) + `<span>(\w+ \w+, ){3}\w+ \w+</span></div>`).MatchString(tile) {
		t.Fatalf("the four titles are not named on one line, joined by commas: %s", tile)
	}
	mustNotContain(t, tile, "more")
	if n := strings.Count(tile, `class="ln waste"`); n != 1 {
		t.Fatalf("%d waste lines", n)
	}
}

// Design pass: the night page uses the same bin icon and names.
func TestNightPageUsesTheBinIcon(t *testing.T) {
	f := newFamilyBox(t, "eink_color")
	f.quietOn(t)
	f.addWaste(t, wasteEvent("p1", "Paper bin", "5"), wasteEvent("p2", "Residual waste", "5"))
	p := f.view(t, "en", f.at(14, 22, 30))
	mustContain(t, p, `<div class="ln waste">`+bin+`<span>Paper bin</span></div>`, `<div class="ln waste">`+bin+`<span>Residual waste</span></div>`)
}

// The note card on Home: day choice with Tomorrow preselected, a text field with counter, one primary action.
func TestHomeHasTheNoteCard(t *testing.T) {
	f := newFamilyBox(t, "eink_color")
	p := f.page("de", "/").body
	mustContain(t, p, "Notiz auf dem Display", `<fieldset class="seg">`,
		`<input type="radio" name="day" value="today">Heute<small>Sa 14/11</small>`,
		`<input type="radio" name="day" value="tomorrow" checked>Morgen<small>So 15/11</small>`,
		`<input type="radio" name="day" value="other">Anderer Tag`, `name="other"`,
		`<textarea name="text" rows="2" maxlength="120"`, "0 von 120 Zeichen", "Notiz speichern", `action="/notes"`)
	if n := strings.Count(p, `type="submit"`); n != 2 { // the card's button and Sign out
		t.Fatalf("%d submit buttons", n)
	}
	en := f.page("en", "/").body
	mustContain(t, en, "Note on the display", "Sat 14/11", "Tomorrow", "Other day", "0 of 120 characters", "Save note")
	// After 18:00 the Today option is left out: today's note would no longer show.
	f.now = f.at(14, 18, 30)
	late := f.page("de", "/").body
	mustNotContain(t, late, `value="today"`)
	mustContain(t, late, `value="tomorrow" checked`)
}

// Saving says when the note appears.
func TestSavingANoteSaysWhenItAppears(t *testing.T) {
	f := newFamilyBox(t, "eink_color")
	r := f.save("/notes", url.Values{"day": {"tomorrow"}, "text": {" Mia: Schwimmsachen "}}, "de")
	if r.status != http.StatusSeeOther || r.location != "/" {
		t.Fatalf("save = %d %q", r.status, r.location)
	}
	if n, ok, _ := f.db.DayNote(f.householdID, "2026-11-15"); !ok || n != "Mia: Schwimmsachen" {
		t.Fatalf("stored %q %v", n, ok)
	}
	mustContain(t, f.page("de", "/").body, "Notiz gespeichert. Sie steht ab heute 18:00 auf dem Display.")
	f.save("/notes", url.Values{"day": {"other"}, "other": {"2026-11-19"}, "text": {"Turnbeutel"}}, "de")
	mustContain(t, f.page("de", "/").body, "Notiz gespeichert. Sie steht ab Mittwoch 18:00 auf dem Display.")
	f.save("/notes", url.Values{"day": {"other"}, "other": {"2026-11-19"}, "text": {"Gym bag"}}, "en")
	mustContain(t, f.page("en", "/").body, "Note saved. It shows on the display from Wednesday 18:00.")
	f.save("/notes", url.Values{"day": {"today"}, "text": {"Heute"}}, "de")
	mustContain(t, f.page("de", "/").body, "Notiz gespeichert. Sie steht jetzt auf dem Display.")
}

// An empty note is a field error that keeps the chosen day; so is a day in the past. Nothing is removed.
func TestEmptyOrPastNoteIsAFieldError(t *testing.T) {
	f := newFamilyBox(t, "eink_color")
	f.save("/notes", url.Values{"day": {"2026-11-16"}, "text": {"Bleibt"}}, "de")
	for _, text := range []string{"", "   \n "} {
		r := f.save("/notes", url.Values{"day": {"other"}, "other": {"2026-11-16"}, "text": {text}}, "de")
		if r.status != http.StatusUnprocessableEntity {
			t.Fatalf("empty = %d", r.status)
		}
		mustContain(t, r.body, `class="ferr"`, "Schreib eine Notiz. Zum Entfernen öffne die Notiz unten.",
			`value="other" checked`, `value="2026-11-16"`, `aria-invalid="true"`)
	}
	r := f.save("/notes", url.Values{"day": {"tomorrow"}, "text": {""}}, "en")
	mustContain(t, r.body, "Write a note. To remove one, open it below.", `value="tomorrow" checked`)
	if n, ok, _ := f.db.DayNote(f.householdID, "2026-11-16"); !ok || n != "Bleibt" {
		t.Fatalf("the note was removed: %q %v", n, ok)
	}
	past := f.save("/notes", url.Values{"day": {"other"}, "other": {"2026-11-13"}, "text": {"x"}}, "de")
	if past.status != http.StatusUnprocessableEntity || !strings.Contains(past.body, `class="ferr"`) || !strings.Contains(past.body, `value="2026-11-13"`) {
		t.Fatalf("past day = %d\n%s", past.status, past.body)
	}
	if notes, _ := f.db.DayNotes(f.householdID); len(notes) != 1 {
		t.Fatalf("notes = %v", notes)
	}
	for _, form := range []url.Values{{"day": {"other"}, "other": {""}, "text": {"x"}}, {"day": {"nonsense"}, "text": {"x"}}} {
		if r := f.save("/notes", form, "de"); r.status != http.StatusUnprocessableEntity {
			t.Fatalf("bad day %v = %d", form, r.status)
		}
	}
	long := f.save("/notes", url.Values{"day": {"tomorrow"}, "text": {strings.Repeat("a", 121)}}, "de")
	if long.status != http.StatusUnprocessableEntity || !strings.Contains(long.body, `class="ferr"`) {
		t.Fatalf("long = %d", long.status)
	}
}

// Home lists the planned notes as rows that open the note; there are no inline Remove buttons.
func TestHomeListsPlannedNotesAsRows(t *testing.T) {
	f := newFamilyBox(t, "eink_color")
	p := f.page("de", "/").body
	mustContain(t, p, "Geplante Notizen", "Noch keine Notiz geplant.")
	f.save("/notes", url.Values{"day": {"2026-11-10"}, "text": {"Vorbei"}}, "de") // a day that is over
	f.save("/notes", url.Values{"day": {"2026-11-16"}, "text": {"Mia: Schwimmsachen"}}, "de")
	p = f.page("de", "/").body
	mustContain(t, p, `<a class="note-row" href="/notes/2026-11-16"><span class="t"><b>Montag 16/11</b><span>Mia: Schwimmsachen</span></span><span class="go">Ändern</span></a>`)
	mustNotContain(t, p, "Noch keine Notiz geplant.", "Entfernen", `name="text" value=""`)
	en := f.page("en", "/").body
	mustContain(t, en, "Planned notes", "Monday 16/11", ">Change<")
}

// A planned note opens on its own page: change it, or remove it after a confirmation.
func TestNotePageChangesAndRemoves(t *testing.T) {
	f := newFamilyBox(t, "eink_color")
	f.save("/notes", url.Values{"day": {"2026-11-16"}, "text": {"Mia: Schwimmsachen"}}, "de")
	p := f.page("de", "/notes/2026-11-16").body
	mustContain(t, p, "<h1>Notiz für Montag, 16/11</h1>", `class="lead"`, "Steht ab Sonntag 18:00 in der Kachel „Morgen“.",
		"Mia: Schwimmsachen</textarea>", "18 von 120 Zeichen", ">Speichern<", `<details class="danger">`, "Notiz entfernen</summary>", "Ja, Notiz entfernen",
		`action="/notes/2026-11-16"`, `action="/notes/2026-11-16/delete"`)
	if r := f.page("de", "/notes/2026-11-17"); r.status != http.StatusNotFound {
		t.Fatalf("no such note = %d", r.status)
	}
	if r := f.page("de", "/notes/nonsense"); r.status != http.StatusNotFound {
		t.Fatalf("bad day = %d", r.status)
	}
	if r := f.save("/notes/2026-11-16", url.Values{"text": {""}}, "de"); r.status != http.StatusUnprocessableEntity || !strings.Contains(r.body, "Schreib eine Notiz.") {
		t.Fatalf("empty = %d\n%s", r.status, r.body)
	}
	if r := f.save("/notes/2026-11-16", url.Values{"text": {"Mia: Badesachen"}}, "de"); r.status != http.StatusSeeOther || r.location != "/" {
		t.Fatalf("change = %d %q", r.status, r.location)
	}
	if n, _, _ := f.db.DayNote(f.householdID, "2026-11-16"); n != "Mia: Badesachen" {
		t.Fatalf("stored %q", n)
	}
	if r := f.save("/notes/2026-11-16/delete", url.Values{}, "de"); r.status != http.StatusSeeOther || r.location != "/" {
		t.Fatalf("remove = %d %q", r.status, r.location)
	}
	if notes, _ := f.db.DayNotes(f.householdID); len(notes) != 0 {
		t.Fatalf("notes = %v", notes)
	}
	if r := f.save("/notes/2026-11-16/delete", url.Values{}, "de"); r.status != http.StatusNotFound {
		t.Fatalf("remove again = %d", r.status)
	}
}

// The calendar page (not the list, not Home) holds the waste switch and no longer holds the day note; the waste collection is a switch with its state in words.
func TestCalendarsPageHasTheWasteSwitch(t *testing.T) {
	f := newFamilyBox(t, "eink_color")
	id := f.addWaste(t, wasteEvent("p1", "Paper bin", "6"), wasteEvent("p2", "Residual waste", "6"))
	p := f.page("de", "/calendars/"+id).body
	mustNotContain(t, p, "Notiz", "Als Müllabfuhr markieren", "Keine Müllabfuhr", "data-waste-state")
	mustContain(t, p, `<form method="post" action="/calendars/`+id+`/waste"`, `role="switch" name="waste" checked aria-label="Müllabfuhr-Kalender"`,
		"An: Die Termine stehen nur in der Kachel „Morgen“ und auf der Nachtseite, ab 18:00 als Erinnerung.",
		"Aus: Die Termine erscheinen wie bei jedem Kalender.", `<noscript>`, `/static/switch-submit.js`,
		`class="waste-next"`, "Nächste Abfuhr: Montag 16/11</b> – Paper bin, Residual waste")
	if !regexp.MustCompile(`(?s)<noscript>.*?type="submit".*?</noscript>`).MatchString(p) {
		t.Fatal("no Save button in noscript")
	}
	en := f.page("en", "/calendars/"+id).body
	mustContain(t, en, "Waste collection calendar", "On: its dates show only in the Tomorrow tile and on the night page, from 18:00 as the reminder.",
		"Off: its events show like any other calendar.", "Next collection: Monday 16/11</b> – Paper bin, Residual waste")
	// Switched off, the line is gone.
	f.save("/calendars/"+id+"/waste", url.Values{}, "de")
	mustNotContain(t, f.page("de", "/calendars/"+id).body, "waste-next", ` checked aria-label="Müllabfuhr-Kalender"`)
}

// Switched on without a collection in the next 30 days, the card says so.
func TestCalendarsPageSaysWhenThereIsNoCollectionSoon(t *testing.T) {
	f := newFamilyBox(t, "eink_color")
	id := f.addWaste(t, wasteEvent("p1", "Paper bin", "6"))
	f.now = f.at(17, 12, 0) // the collection has passed
	mustContain(t, f.page("de", "/calendars/"+id).body, "Keine Abfuhr in den nächsten 30 Tagen.", "Stimmt die Adresse des Kalenders?")
	mustContain(t, f.page("en", "/calendars/"+id).body, "No collection in the next 30 days.", "Is the calendar address right?")
}

// The flash after switching says when the next collection shows; without one it keeps to the state.
func TestWasteFlashTexts(t *testing.T) {
	f := newFamilyBox(t, "eink_color")
	id := f.addWaste(t, wasteEvent("p1", "Paper bin", "6"))
	f.save("/calendars/"+id+"/waste", url.Values{"waste": {"on"}}, "de")
	mustContain(t, f.page("de", "/calendars").body, "Müllabfuhr-Kalender ist an. Die nächste Abfuhr steht ab Sonntag 15/11 18:00 auf dem Display.")
	f.save("/calendars/"+id+"/waste", url.Values{"waste": {"on"}}, "en")
	mustContain(t, f.page("en", "/calendars").body, "Waste collection calendar is on. The next collection shows on the display from Sunday 15/11 18:00.")
	f.save("/calendars/"+id+"/waste", url.Values{}, "de")
	mustContain(t, f.page("de", "/calendars").body, "Müllabfuhr-Kalender ist aus.")
	f.save("/calendars/"+id+"/waste", url.Values{}, "en")
	mustContain(t, f.page("en", "/calendars").body, "Waste collection calendar is off.")
	f.now = f.at(20, 12, 0)
	f.save("/calendars/"+id+"/waste", url.Values{"waste": {"on"}}, "de")
	p := f.page("de", "/calendars").body
	mustContain(t, p, "Müllabfuhr-Kalender ist an.")
	mustNotContain(t, p, "Die nächste Abfuhr steht")
}

// The frame grid may shrink below its content, so the German navigation does not widen a 375 px phone.
func TestFrameGridMayShrink(t *testing.T) {
	f := newFamilyBox(t, "eink_color")
	css := f.get("/static/hejmo.css").body
	if !regexp.MustCompile(`\.frame \{ grid-template-columns: minmax\(0, 1fr\); \}`).MatchString(css) {
		t.Fatal("the frame grid has no minmax(0, 1fr) column")
	}
}

// The flash names the day the reminder starts, with its date, and says so when it shows already.
func TestWasteFlashNamesTheDayAndTheDisplayNow(t *testing.T) {
	// Collection tomorrow, before 18:00: the evening before is today.
	g := newFamilyBox(t, "eink_color")
	gid := g.addWaste(t, wasteEvent("p0", "Paper bin", "5")) // Sunday 15/11, the clock is Saturday 14/11 13:00
	g.save("/calendars/"+gid+"/waste", url.Values{"waste": {"on"}}, "de")
	mustContain(t, g.page("de", "/calendars").body, "Die nächste Abfuhr steht ab heute 18:00 auf dem Display.")
	g.save("/calendars/"+gid+"/waste", url.Values{"waste": {"on"}}, "en")
	mustContain(t, g.page("en", "/calendars").body, "The next collection shows on the display from today 18:00.")

	f := newFamilyBox(t, "eink_color")
	id := f.addWaste(t, wasteEvent("p1", "Paper bin", "8")) // Wednesday 18/11
	f.save("/calendars/"+id+"/waste", url.Values{"waste": {"on"}}, "de")
	mustContain(t, f.page("de", "/calendars").body, "Die nächste Abfuhr steht ab Dienstag 17/11 18:00 auf dem Display.")
	f.save("/calendars/"+id+"/waste", url.Values{"waste": {"on"}}, "en")
	mustContain(t, f.page("en", "/calendars").body, "The next collection shows on the display from Tuesday 17/11 18:00.")
	f.now = f.at(17, 19, 0) // the evening before has come
	f.save("/calendars/"+id+"/waste", url.Values{"waste": {"on"}}, "de")
	mustContain(t, f.page("de", "/calendars").body, "Die nächste Abfuhr steht jetzt auf dem Display.")
	f.save("/calendars/"+id+"/waste", url.Values{"waste": {"on"}}, "en")
	mustContain(t, f.page("en", "/calendars").body, "The next collection shows on the display now.")
}

// A note of a past day that is not pruned yet can be neither opened nor saved.
func TestPastNoteHasNoPage(t *testing.T) {
	f := newFamilyBox(t, "eink_color")
	if err := f.db.SetDayNote(f.householdID, "2026-11-13", "Alt"); err != nil {
		t.Fatal(err)
	}
	if r := f.page("de", "/notes/2026-11-13"); r.status != http.StatusNotFound {
		t.Fatalf("GET past note = %d", r.status)
	}
	if r := f.save("/notes/2026-11-13", url.Values{"text": {"Neu"}}, "de"); r.status != http.StatusNotFound {
		t.Fatalf("POST past note = %d", r.status)
	}
	if n, _, _ := f.db.DayNote(f.householdID, "2026-11-13"); n != "Alt" {
		t.Fatalf("stored %q", n)
	}
}

// The note page belongs to Home, so Home is the current item in the navigation.
func TestNotePageMarksHomeAsCurrent(t *testing.T) {
	f := newFamilyBox(t, "eink_color")
	f.save("/notes", url.Values{"day": {"2026-11-16"}, "text": {"Mia"}}, "de")
	mustContain(t, f.page("de", "/notes/2026-11-16").body, `<a href="/" aria-current="page"`)
}
