package server_test

import (
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/hejmo-home/hejmo/internal/layout"
	"github.com/hejmo-home/hejmo/internal/store"
	"github.com/hejmo-home/hejmo/internal/task"
)

// Ticket 180 (ADR 0020, docs/design/preview-180.html): the layout of a display.

// layoutForm is the Layout section of the settings form: the people in order and
// the blocks in order, the ones named in on switched on.
func (f *familyBox) layoutForm(whole bool, people []string, on ...string) url.Values {
	form := url.Values{"layout_form": {"1"}}
	if whole {
		form.Set("whole", "1")
	}
	for _, p := range people {
		form.Add("person", f.people[p].ID)
	}
	for _, id := range []string{"clock", "weather", "events", "routines", "tasks", "stars", "meals", "tomorrow", "list"} {
		form.Add("order", id)
	}
	for _, id := range on {
		form.Set("b_"+id, "1")
	}
	return form
}

var defaultOn = []string{"clock", "weather", "events", "tasks", "stars", "meals", "tomorrow"}

func (f *familyBox) saveLayout(t *testing.T, form url.Values) resp {
	t.Helper()
	r := f.saveSettings(t, form)
	if r.status != http.StatusSeeOther {
		t.Fatalf("save = %d\n%s", r.status, r.body)
	}
	return r
}

var columnNameRe = regexp.MustCompile(`<section class="column" data-person="\d+" data-name="([^"]+)"`)

func columnNames(page string) []string {
	var out []string
	for _, m := range columnNameRe.FindAllStringSubmatch(page, -1) {
		out = append(out, m[1])
	}
	return out
}

func (f *familyBox) anotherDisplay(t *testing.T, name string) string {
	t.Helper()
	s := newScreen(f.box)
	form := pairForm(codeOn(t, s.get("/display", "en").body))
	form.Set("name", name)
	if r := f.post("/displays", form); r.status != http.StatusSeeOther {
		t.Fatalf("pair = %d", r.status)
	}
	ds, _ := f.db.Displays(f.householdID)
	for _, d := range ds {
		if d.Name == name {
			return d.ID
		}
	}
	t.Fatal("not paired")
	return ""
}

// Q87: the display shows the chosen people in the saved order (limits and
// whole-family events are the family view's).
func TestADisplayShowsTheChosenPeopleInTheSavedOrder(t *testing.T) {
	t.Parallel()
	f := newFamilyBox(t, "eink_color")
	eq(t, columnNames(f.view(t, "en", f.at(14, 9, 0))), []string{"Mama", "Papa", "Sebastian", "Mia"})
	f.saveLayout(t, f.layoutForm(false, []string{"Mia", "Mama", "Sebastian"}, defaultOn...))
	page := f.view(t, "en", f.at(14, 9, 0))
	eq(t, columnNames(page), []string{"Mia", "Mama", "Sebastian"})
	if !strings.Contains(page, `data-people="3"`) {
		t.Errorf("column count")
	}
	// "Ganze Familie": a person added later joins at the end.
	f.saveLayout(t, f.layoutForm(true, []string{"Mia", "Mama", "Papa", "Sebastian"}, defaultOn...))
	f.db.AddPerson(f.householdID, store.Person{Name: "Nora", Role: store.RoleChild})
	eq(t, columnNames(f.view(t, "en", f.at(14, 9, 0))), []string{"Mia", "Mama", "Papa", "Sebastian", "Nora"})
}

// Nobody chosen is refused at the field, and the page says so in the preview.
func TestAtLeastOnePersonMustBeChosen(t *testing.T) {
	t.Parallel()
	f := newFamilyBox(t, "eink_color")
	r := f.do(http.MethodPost, "/displays/"+f.displayID(t)+"/settings", f.layoutForm(false, nil, defaultOn...), en)
	if r.status != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d", r.status)
	}
	for _, want := range []string{"Choose at least one person.", "Nobody chosen"} {
		if !strings.Contains(r.body, want) {
			t.Errorf("page lacks %q", want)
		}
	}
	eq(t, columnNames(f.view(t, "en", f.at(14, 9, 0))), []string{"Mama", "Papa", "Sebastian", "Mia"})
}

// rotatedRight moves everyone one column on, the rightmost goes to the left.
func rotatedRight(x []string) []string {
	return append([]string{x[len(x)-1]}, x[:len(x)-1]...)
}

// Q91 a: every day each person moves one column on, at 03:00, off by default.
func TestDailyRotationMovesEveryoneOneColumnOn(t *testing.T) {
	t.Parallel()
	f := newFamilyBox(t, "eink_color")
	form := f.layoutForm(false, []string{"Mama", "Papa", "Mia"}, defaultOn...)
	form.Set("rotate", "1")
	f.saveLayout(t, form)
	// The shift counts from a fixed epoch, so only the steps are known.
	day14 := columnNames(f.view(t, "en", f.at(14, 12, 0)))
	eq(t, columnNames(f.view(t, "en", f.at(15, 2, 59))), day14)
	day15 := rotatedRight(day14)
	eq(t, columnNames(f.view(t, "en", f.at(15, 3, 0))), day15)
	day16 := rotatedRight(day15)
	eq(t, columnNames(f.view(t, "en", f.at(16, 12, 0))), day16)
	// The settings page lists today's order, and says tomorrow's in words.
	f.now = f.at(15, 12, 0)
	page := f.settingsPage(t, en)
	if want := "Tomorrow: " + strings.Join(day16, ", ") + "."; !strings.Contains(inText(page), want) {
		t.Errorf("%q missing:\n%s", want, inText(page))
	}
}

// Q91: every display with rotation on shows the same order on the same day, also
// when they were switched on on different days.
func TestDisplaysWithRotationShowTheSameOrderWhenSwitchedOnOnDifferentDays(t *testing.T) {
	t.Parallel()
	f := newFamilyBox(t, "eink_color")
	hall := f.anotherDisplay(t, "Hall")
	var kitchen string
	for _, d := range f.displays(t) {
		if d.ID != hall {
			kitchen = d.ID
		}
	}
	form := f.layoutForm(true, []string{"Mama", "Papa", "Sebastian", "Mia"}, defaultOn...)
	form.Set("rotate", "1")
	f.now = f.at(14, 12, 0)
	if r := f.post("/displays/"+kitchen+"/settings", form); r.status != http.StatusSeeOther {
		t.Fatalf("save = %d", r.status)
	}
	f.now = f.at(15, 12, 0)
	if r := f.post("/displays/"+hall+"/settings", form); r.status != http.StatusSeeOther {
		t.Fatalf("save = %d", r.status)
	}
	a, _ := f.db.DisplayLayout(f.householdID, kitchen)
	b, _ := f.db.DisplayLayout(f.householdID, hall)
	for day := 20000; day < 20008; day++ {
		eq(t, a.Order(day), b.Order(day))
	}
}

// Switching rotation on takes the order as the base order, so today's columns
// move at once by today mod n. The page must say so: the preview and the
// sentence show today's columns, the sentence about tomorrow the day after, and
// the rows keep the order they were posted in.
func TestSwitchingRotationOnShowsTodaysColumnsAndTomorrowsOrder(t *testing.T) {
	t.Parallel()
	f := newFamilyBox(t, "eink_color")
	posted := []string{"Mama", "Papa", "Mia"}
	var day int
	for d := 14; d < 17; d++ { // a day on which the rotation has a shift
		if layout.RotationDay(f.at(d, 12, 0))%3 != 0 {
			day = d
			break
		}
	}
	today := layout.RotationDay(f.at(day, 12, 0))
	f.now = f.at(day, 12, 0)
	form := f.layoutForm(false, posted, defaultOn...)
	form.Set("rotate", "1")
	form.Set("order_day", strconv.Itoa(today))
	form.Add("order", "no-such-block") // refused, so the page comes back as it was filled in
	r := f.do(http.MethodPost, "/displays/"+f.displayID(t)+"/settings", form, en)
	if r.status != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d", r.status)
	}
	rotating := layout.Layout{People: posted, Rotate: true}
	columns, next := rotating.Order(today), rotating.Order(today+1)
	if slices.Equal(columns, posted) {
		t.Fatalf("a day without a shift tests nothing")
	}
	text := inText(r.body)
	for _, want := range []string{
		"Today, left to right: " + strings.Join(columns, ", ") + ".",
		"Tomorrow: " + strings.Join(next, ", ") + ".",
		`data-was-rotating="false"`, `data-order-day="` + strconv.Itoa(today) + `"`,
	} {
		if !strings.Contains(text, want) && !strings.Contains(r.body, want) {
			t.Errorf("%q missing:\n%s", want, text)
		}
	}
	rows := regexp.MustCompile(`class="prow" data-pid="[^"]+"><span class="pos">[^<]*</span><label[^>]*>.*?<span class="an">([^<]+)<`).FindAllStringSubmatch(r.body, -1)
	var got []string
	for _, m := range rows {
		got = append(got, m[1])
	}
	eq(t, got, posted)
	// What is saved is what the page announced.
	form.Del("order")
	for _, id := range []string{"clock", "weather", "events", "routines", "tasks", "stars", "meals", "tomorrow", "list"} {
		form.Add("order", id)
	}
	f.saveLayout(t, form)
	eq(t, columnNames(f.view(t, "en", f.at(day, 12, 0))), columns)
}

// One display already rotates, another is switched on a day later with the same
// people: they agree, also after the first one is saved again from its own page.
func TestRotatingDisplayAgreesWithOneSwitchedOnADayLater(t *testing.T) {
	t.Parallel()
	f := newFamilyBox(t, "eink_color")
	hall := f.anotherDisplay(t, "Hall")
	var kitchen string
	for _, d := range f.displays(t) {
		if d.ID != hall {
			kitchen = d.ID
		}
	}
	people := []string{"Mia", "Mama", "Papa", "Sebastian"}
	form := f.layoutForm(true, people, defaultOn...)
	form.Set("rotate", "1")
	f.now = f.at(14, 12, 0)
	if r := f.post("/displays/"+kitchen+"/settings", form); r.status != http.StatusSeeOther {
		t.Fatalf("save = %d", r.status)
	}
	f.now = f.at(15, 12, 0)
	if r := f.post("/displays/"+hall+"/settings", form); r.status != http.StatusSeeOther {
		t.Fatalf("save = %d", r.status)
	}
	// The kitchen is saved again on the 16th from its page of that day: the rows
	// are the columns of the 16th, and nobody moves.
	k, _ := f.db.DisplayLayout(f.householdID, kitchen)
	f.now = f.at(16, 12, 0)
	again := f.layoutForm(true, nil, defaultOn...)
	for _, id := range k.Order(layout.RotationDay(f.now)) {
		again.Add("person", id)
	}
	again.Set("rotate", "1")
	again.Set("order_day", strconv.Itoa(layout.RotationDay(f.now)))
	if r := f.post("/displays/"+kitchen+"/settings", again); r.status != http.StatusSeeOther {
		t.Fatalf("save = %d", r.status)
	}
	a, _ := f.db.DisplayLayout(f.householdID, kitchen)
	b, _ := f.db.DisplayLayout(f.householdID, hall)
	for day := 20000; day < 20008; day++ {
		eq(t, a.Order(day), b.Order(day))
	}
}

// A form loaded before 03:00 and saved after it must not move anybody: it carries
// the day of the order it showed.
func TestAFormSavedAfterThreeKeepsTheOrderItShowed(t *testing.T) {
	t.Parallel()
	f := newFamilyBox(t, "eink_color")
	form := f.layoutForm(false, []string{"Mama", "Papa", "Mia"}, defaultOn...)
	form.Set("rotate", "1")
	f.saveLayout(t, form)
	loaded := f.at(14, 12, 0)
	shown := columnNames(f.view(t, "en", loaded))
	after := columnNames(f.view(t, "en", f.at(15, 3, 30)))
	eq(t, after, rotatedRight(shown))
	// Saved at 03:30 on the 15th, from the form of the 14th.
	again := f.layoutForm(false, shown, defaultOn...)
	again.Set("rotate", "1")
	again.Set("order_day", strconv.Itoa(layout.RotationDay(loaded)))
	f.now = f.at(15, 3, 30)
	f.saveLayout(t, again)
	eq(t, columnNames(f.view(t, "en", f.at(15, 3, 30))), after)
}

// Q88: exactly one person chosen: the display shows that person's personal view.
func TestOnePersonShowsThePersonalView(t *testing.T) {
	t.Parallel()
	f := newFamilyBox(t, "eink_color")
	f.homeWeather(t)
	f.saveLayout(t, f.layoutForm(false, []string{"Mia"}, defaultOn...))
	page := f.view(t, "en", f.at(14, 9, 0))
	if !strings.Contains(page, `data-personal="Mia"`) || strings.Contains(page, `class="fv"`) {
		t.Fatalf("not the personal view:\n%s", page)
	}
	if strings.Contains(page, `class="pv-back"`) || strings.Contains(page, `data-return-seconds`) {
		t.Errorf("the personal view of a one-person display has no way back to a family view")
	}
	if !strings.Contains(page, "data-weather") {
		t.Errorf("Q130: the personal header gains the weather chip")
	}
	// The partial update serves the same view.
	if v := f.display.get("/display/view", "en"); !strings.Contains(v.body, `data-personal="Mia"`) {
		t.Errorf("view = %s", v.body)
	}
	// With the weather switch off the chip goes.
	f.saveLayout(t, f.layoutForm(false, []string{"Mia"}, "clock", "events", "tasks"))
	if page := f.view(t, "en", f.at(14, 9, 0)); strings.Contains(page, "data-weather") {
		t.Errorf("weather chip with its block off")
	}
	// A personal view reached from a family view has the header blocks as well.
	f.saveLayout(t, f.layoutForm(true, []string{"Mama", "Papa", "Sebastian", "Mia"}, defaultOn...))
	f.homeWeather(t)
	f.now = f.at(14, 9, 0)
	if r := f.display.get("/display/person/"+f.people["Mia"].ID, "en"); !strings.Contains(r.body, "data-weather") || !strings.Contains(r.body, `class="pv-back"`) {
		t.Errorf("personal view from the family view:\n%s", r.body)
	}
}

// Q86: blocks switch their part of the display on and off.
func TestBlocksSwitchPartsOfTheDisplay(t *testing.T) {
	t.Parallel()
	f := newFamilyBox(t, "eink_color")
	f.homeWeather(t)
	f.task(t, "Mia", "Tidy up", task.Daily)
	f.event(t, "Swimming", f.at(14, 16, 0), f.at(14, 17, 0), false, "Mia")
	all := []string{"Mama", "Papa", "Sebastian", "Mia"}
	page := f.view(t, "en", f.at(14, 9, 0))
	for _, part := range []string{"data-clock", "data-weather", "Swimming", `class="pbar"`} {
		if !strings.Contains(page, part) {
			t.Fatalf("default page lacks %q", part)
		}
	}
	for _, c := range []struct{ off, gone string }{{"clock", "data-clock"}, {"weather", "data-weather"}, {"events", "Swimming"}, {"tasks", `class="pbar"`}} {
		var on []string
		for _, id := range defaultOn {
			if id != c.off {
				on = append(on, id)
			}
		}
		f.saveLayout(t, f.layoutForm(true, all, on...))
		if page := f.view(t, "en", f.at(14, 9, 0)); strings.Contains(page, c.gone) {
			t.Errorf("%s off but %q still on the page", c.off, c.gone)
		}
	}
	// The new block "Routines" shows one line per routine, above the progress.
	f.routineFor(t, "Mia", "Morning", task.Morning, "Teeth", "Dress")
	f.saveLayout(t, f.layoutForm(true, all, defaultOn...))
	if page := f.view(t, "en", f.at(14, 9, 0)); strings.Contains(page, "col-rt") {
		t.Errorf("routines are off by default")
	}
	f.saveLayout(t, f.layoutForm(true, all, append(defaultOn, "routines")...))
	page = f.view(t, "en", f.at(14, 9, 0))
	if !strings.Contains(inText(page), "Morning: 0 of 2 done") || !strings.Contains(page, `<ul class="col-rt">`) {
		t.Errorf("routine line missing:\n%s", page)
	}
}

// Q86, Q129: the block "one list" is a tile in the footer: up to 8 open entries in full.
func TestTheListBlockShowsOpenEntriesInTheFooter(t *testing.T) {
	t.Parallel()
	f := newFamilyBox(t, "eink_color")
	var texts []string
	for i := 1; i <= 10; i++ {
		texts = append(texts, fmt.Sprintf("Thing %d", i))
	}
	id, _ := f.onlyList(t, "Shopping", []string{"Thing 1"}, texts...)
	form := f.layoutForm(true, []string{"Mama", "Papa", "Sebastian", "Mia"}, append(defaultOn, "list")...)
	form.Set("list", id)
	f.saveLayout(t, form)
	page := f.view(t, "en", f.at(14, 9, 0))
	tile := regexp.MustCompile(`(?s)<a class="tile list".*?</a>`).FindString(page)
	if tile == "" || !strings.Contains(tile, `href="/display/lists/`+id+`"`) {
		t.Fatalf("no list tile:\n%s", page)
	}
	if strings.Contains(tile, "Thing 1<") || !strings.Contains(tile, "Thing 2") || !strings.Contains(tile, "Thing 9") || strings.Contains(tile, "Thing 10") {
		t.Errorf("entries (ticked ones are not open, 8 at most):\n%s", tile)
	}
	if !strings.Contains(inText(tile), "1 more. Tap here for the whole list.") {
		t.Errorf("more line:\n%s", inText(tile))
	}
	de := f.view(t, "de", f.at(14, 9, 0))
	if !strings.Contains(inText(de), "Noch 1 mehr. Tippe hier für die ganze Liste.") {
		t.Errorf("German more line missing")
	}
	// The list is deleted (another one is left): no tile, no error.
	listWith(t, f.box, "Drugstore", nil, "Soap")
	if err := f.db.DeleteList(f.householdID, id); err != nil {
		t.Fatal(err)
	}
	if page := f.view(t, "en", f.at(14, 9, 0)); strings.Contains(page, `class="tile list"`) {
		t.Errorf("a deleted list still has a tile")
	}
	// The settings page names it.
	if p := inText(f.settingsPage(t, en)); !strings.Contains(p, "The list “Shopping” no longer exists.") {
		t.Errorf("no warning about the deleted list:\n%s", p)
	}
}

// A list that was deleted stays chosen: saving the settings page posts the "no
// choice" option, which keeps it, and the warning stays until another is chosen.
func TestSavingTheSettingsKeepsAListThatWasDeleted(t *testing.T) {
	t.Parallel()
	f := newFamilyBox(t, "eink_color")
	id, _ := f.onlyList(t, "Shopping", nil, "Milk")
	form := f.layoutForm(true, []string{"Mama", "Papa", "Sebastian", "Mia"}, append(defaultOn, "list")...)
	form.Set("list", id)
	f.saveLayout(t, form)
	listWith(t, f.box, "Drugstore", nil, "Soap")
	if err := f.db.DeleteList(f.householdID, id); err != nil {
		t.Fatal(err)
	}
	again := f.layoutForm(true, []string{"Mama", "Papa", "Sebastian", "Mia"}, append(defaultOn, "list")...)
	again.Set("list", "")
	f.saveLayout(t, again)
	l, _ := f.db.DisplayLayout(f.householdID, f.displayID(t))
	if l.ListID != id || l.ListName != "Shopping" {
		t.Errorf("the choice changed: %q %q", l.ListID, l.ListName)
	}
	if page := f.view(t, "en", f.at(14, 9, 0)); strings.Contains(page, `class="tile list"`) {
		t.Errorf("a list nobody chose is shown")
	}
	if p := inText(f.settingsPage(t, en)); !strings.Contains(p, "The list “Shopping” no longer exists.") {
		t.Errorf("the warning is gone:\n%s", p)
	}
}

// Q90, designer: the section sits after "Where and what", the old tomorrow switch lives under Events.
func TestTheLayoutSectionOnTheDisplayPage(t *testing.T) {
	t.Parallel()
	f := newFamilyBox(t, "eink_color")
	f.saveLayout(t, f.layoutForm(true, []string{"Mama", "Papa", "Sebastian", "Mia"}, defaultOn...))
	page := squash(f.settingsPage(t, en))
	i, j, k := strings.Index(page, "Where and what"), strings.Index(page, `<fieldset class="layout" id="layout"`), strings.Index(page, `<legend>What it shows</legend>`)
	if !(i > 0 && j > i && k > j) {
		t.Fatalf("order of the sections: where %d layout %d shows %d", i, j, k)
	}
	section := page[j : strings.Index(page[j:], "</fieldset><fieldset")+j]
	// A new display looks like today: preview in words, people, zones, blocks.
	text := inText(section)
	for _, want := range []string{
		"Preview · landscape · saved, this is what “Hallway” shows now",
		"Left to right: Mama, Papa, Sebastian, Mia.",
		"Whole family", "Column 1", "Column 4", "Not on this display",
		"Top (header)", "In each column", "Bottom (footer)",
		"Always there: marker and name at the top of each column.",
		"Time", "Weather", "Events", "Routines", "Tasks with progress", "Meals today", "Tomorrow: waste and note", "One list",
		"Also tomorrow from 18:00", "Off: no routines in the columns",
		"Daily column swap", "Events for the whole family are always shown",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("section lacks %q", want)
		}
	}
	for _, want := range []string{`name="show_tomorrow"`, `name="b_routines"`, `name="b_list"`, `name="rotate"`, `name="whole" checked`, `<input type="hidden" name="layout_form" value="1">`} {
		if !strings.Contains(section, want) {
			t.Errorf("section lacks %s", want)
		}
	}
	if strings.Contains(strings.Replace(page, section, "", 1), `name="show_tomorrow"`) {
		t.Errorf("the tomorrow switch is still outside the section")
	}
	// A name in the preview is a full name; up arrow of the first and down arrow of the last are disabled.
	if !strings.Contains(section, `aria-label="Mama move up" disabled`) || !strings.Contains(section, `aria-label="Mia move down" disabled`) {
		t.Errorf("arrows:\n%s", section)
	}
	// Only one display: no copying.
	if strings.Contains(section, "copy_from") {
		t.Errorf("copy offered with one display")
	}
	// A household without a list: the list block is disabled with a link.
	for _, l := range f.homeLists(t) {
		_ = f.db.DeleteList(f.householdID, l.ID)
	}
	nolist := squash(f.settingsPage(t, en))
	if !strings.Contains(inText(nolist), "You have no list yet.") || !strings.Contains(nolist, `name="b_list" disabled`) {
		t.Errorf("no-list state")
	}
}

// The preview is drawn the way the display hangs, and in words for people beyond the visible columns.
func TestThePreviewFollowsHowTheDisplayHangs(t *testing.T) {
	t.Parallel()
	f := newFamilyBox(t, "eink_color")
	for _, n := range []string{"Oma", "Opa"} {
		f.db.AddPerson(f.householdID, store.Person{Name: n, Role: store.RoleChild})
	}
	if r := f.display.get("/display/state?orientation=portrait", "en"); r.status != 200 {
		t.Fatalf("state = %d", r.status)
	}
	text := inText(f.settingsPage(t, en))
	for _, want := range []string{"Preview · portrait", "4 columns fit side by side in portrait", "Oma and Opa you reach by swiping sideways.", "Column 5, by swiping"} {
		if !strings.Contains(text, want) {
			t.Errorf("page lacks %q\n%s", want, text)
		}
	}
	f.display.get("/display/state?orientation=landscape", "en")
	if text := inText(f.settingsPage(t, en)); !strings.Contains(text, "Preview · landscape") || strings.Contains(text, "by swiping") {
		t.Errorf("landscape: 6 columns fit")
	}
	if text := inText(f.settingsPage(t, map[string]string{"Accept-Language": "de"})); !strings.Contains(text, "Vorschau · quer") {
		t.Errorf("German caption")
	}
}

// Q84: copy the layout of another display, without name, brightness and quiet mode.
func TestCopyTheLayoutOfAnotherDisplay(t *testing.T) {
	t.Parallel()
	f := newFamilyBox(t, "eink_color")
	hall := f.anotherDisplay(t, "Hall")
	var kitchen string
	for _, d := range f.displays(t) {
		if d.ID != hall {
			kitchen = d.ID
		}
	}
	form := f.layoutForm(false, []string{"Sebastian", "Mia"}, "clock", "events")
	form.Set("rotate", "1")
	if r := f.post("/displays/"+kitchen+"/settings", form); r.status != http.StatusSeeOther {
		t.Fatalf("save = %d", r.status)
	}
	page := inText(f.do(http.MethodGet, "/displays/"+hall, nil, en).body)
	if !strings.Contains(page, "Apply layout") {
		t.Fatalf("copy not offered:\n%s", page)
	}
	r := f.post("/displays/"+hall+"/layout/copy", url.Values{"copy_from": {kitchen}})
	if r.status != http.StatusSeeOther {
		t.Fatalf("copy = %d\n%s", r.status, r.body)
	}
	ds := f.displays(t)
	l, err := f.db.DisplayLayout(f.householdID, hall)
	if err != nil || l.Whole || !l.Rotate || len(l.People) != 2 || l.Enabled(layout.Weather) {
		t.Fatalf("copied = %+v %v", l, err)
	}
	if len(ds) != 2 {
		t.Fatalf("displays = %d", len(ds))
	}
	follow := f.do(http.MethodGet, r.location, nil, en)
	if !strings.Contains(inText(follow.body), "Layout of “") || !strings.Contains(inText(follow.body), "applied and saved.") {
		t.Errorf("success line missing:\n%s", inText(follow.body))
	}
	// Name stays; unknown source is refused.
	for _, d := range ds {
		if d.ID == hall && d.Name != "Hall" {
			t.Errorf("name changed to %q", d.Name)
		}
	}
	if r := f.post("/displays/"+hall+"/layout/copy", url.Values{"copy_from": {"nope"}}); r.status != http.StatusNotFound && r.status != http.StatusUnprocessableEntity {
		t.Errorf("unknown source = %d", r.status)
	}
}

// The settings form without the layout fields leaves the layout alone.
func TestSavingOtherSettingsKeepsTheLayout(t *testing.T) {
	t.Parallel()
	f := newFamilyBox(t, "eink_color")
	f.saveLayout(t, f.layoutForm(false, []string{"Mia"}, "clock"))
	f.saveSettings(t, url.Values{"name": {"Renamed"}})
	l, _ := f.db.DisplayLayout(f.householdID, f.displayID(t))
	if len(l.People) != 1 || l.Enabled(layout.Events) {
		t.Errorf("layout changed: %+v", l)
	}
}
