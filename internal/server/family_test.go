package server_test

import (
	"net/url"
	"os"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/hejmo-home/hejmo/internal/calendar"
	"github.com/hejmo-home/hejmo/internal/familyview"
	"github.com/hejmo-home/hejmo/internal/i18n"
	"github.com/hejmo-home/hejmo/internal/server"
	"github.com/hejmo-home/hejmo/internal/store"
)

var berlinTZ = func() *time.Location {
	l, err := time.LoadLocation("Europe/Berlin")
	if err != nil {
		panic(err)
	}
	return l
}()

// familyBox is a box with the example family and a paired display; the clock
// stands at Saturday 14/11/2026, 13:00 in Berlin.
type familyBox struct {
	*box
	display     *screen
	family      store.Calendar
	people      map[string]store.Person
	householdID string
}

func newFamilyBox(t *testing.T, displayType string) *familyBox {
	t.Helper()
	b := signedInBox(t)
	h, _ := b.db.HomeHousehold()
	f := &familyBox{box: b, people: map[string]store.Person{}, householdID: h.ID}
	ps, _ := b.db.People(h.ID)
	f.people["Mama"] = ps[0]
	for _, n := range []string{"Papa", "Sebastian", "Mia"} {
		role := store.RoleChild
		if n == "Papa" {
			role = store.RoleParent
		}
		p, err := b.db.AddPerson(h.ID, store.Person{Name: n, Role: role})
		if err != nil {
			t.Fatal(err)
		}
		f.people[n] = p
	}
	f.family, _ = b.db.FamilyCalendar(h.ID)
	f.display = newScreen(b)
	code := codeOn(t, f.display.get("/display", "en").body)
	form := pairForm(code)
	form.Set("type", displayType)
	if r := b.post("/displays", form); r.status != 303 {
		t.Fatalf("pair = %d", r.status)
	}
	return f
}

func (f *familyBox) event(t *testing.T, title string, start, end time.Time, allDay bool, who ...string) {
	t.Helper()
	e := calendar.Event{CalendarID: f.family.ID, Title: title, Start: start, End: end, AllDay: allDay}
	for _, w := range who {
		e.People = append(e.People, f.people[w].ID)
	}
	if _, err := f.db.CreateEvent(f.householdID, e); err != nil {
		t.Fatal(err)
	}
}

func (f *familyBox) at(day, hour, min int) time.Time {
	return time.Date(2026, 11, day, hour, min, 0, 0, berlinTZ)
}

// view fetches the family view with the clock at the given local time.
func (f *familyBox) view(t *testing.T, lang string, now time.Time) string {
	t.Helper()
	f.now = now
	r := f.display.get("/display", lang)
	if r.status != 200 {
		t.Fatalf("GET /display = %d", r.status)
	}
	return r.body
}

var (
	columnRe = regexp.MustCompile(`(?s)<section class="column"[^>]*data-name="([^"]+)".*?</section>`)
	titleRe  = regexp.MustCompile(`class="title">([^<]*)<`)
)

// columns maps each column's person to the event titles in it (the tomorrow block excluded).
func columns(t *testing.T, page string) map[string][]string {
	t.Helper()
	out := map[string][]string{}
	for _, m := range columnRe.FindAllStringSubmatch(page, -1) {
		body := m[0]
		if i := strings.Index(body, `data-tomorrow`); i >= 0 {
			body = body[:i]
		}
		var ts []string
		for _, x := range titleRe.FindAllStringSubmatch(body, -1) {
			ts = append(ts, x[1])
		}
		out[m[1]] = ts
	}
	return out
}

func strip(t *testing.T, page string) string {
	t.Helper()
	m := regexp.MustCompile(`(?s)<ul class="strip" data-strip>.*?</ul>`).FindString(page)
	return m
}

func eq(t *testing.T, got, want []string) {
	t.Helper()
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("got %q, want %q", got, want)
	}
}

// 5.3: Given events for Mama and Mia, each appears in the right column in chronological order.
func TestFamilyViewPutsEventsInTheRightColumnsInOrder(t *testing.T) {
	f := newFamilyBox(t, "eink_color")
	f.event(t, "Swimming", f.at(14, 16, 0), f.at(14, 17, 0), false, "Mia")
	f.event(t, "Dentist", f.at(14, 9, 0), f.at(14, 10, 0), false, "Mama")
	f.event(t, "School play", f.at(14, 10, 30), f.at(14, 11, 30), false, "Mia")
	cols := columns(t, f.view(t, "en", f.at(14, 8, 0)))
	eq(t, cols["Mama"], []string{"Dentist"})
	eq(t, cols["Mia"], []string{"School play", "Swimming"})
	eq(t, cols["Papa"], nil)
	if len(cols) != 4 {
		t.Fatalf("columns = %v, want the four people", cols)
	}
}

// 5.3: Given an event with audience Sebastian and Mia, it appears in both their columns.
func TestFamilyViewShowsAnEventInEachColumnOfItsAudience(t *testing.T) {
	f := newFamilyBox(t, "eink_color")
	f.event(t, "Football", f.at(14, 15, 0), f.at(14, 16, 0), false, "Sebastian", "Mia")
	cols := columns(t, f.view(t, "en", f.at(14, 8, 0)))
	eq(t, cols["Sebastian"], []string{"Football"})
	eq(t, cols["Mia"], []string{"Football"})
	eq(t, cols["Mama"], nil)
}

// 5.3: Given a public holiday in Bavaria, it is shown (and none from other states).
func TestFamilyViewShowsTheHolidaysOfTheHomeRegion(t *testing.T) {
	f := newFamilyBox(t, "eink_color")
	en := f.view(t, "en", f.at(1, 8, 0)) // All Saints' Day: Bavaria
	if s := strip(t, en); !strings.Contains(s, "All Saints") || !strings.Contains(s, `data-kind="holiday"`) {
		t.Fatalf("strip misses the holiday:\n%s", en)
	}
	if de := f.view(t, "de", f.at(1, 8, 0)); !strings.Contains(strip(t, de), "Allerheiligen") {
		t.Fatal("German strip misses Allerheiligen")
	}
	if s := strip(t, f.view(t, "en", f.at(2, 8, 0))); strings.Contains(s, "data-kind=\"holiday\"") {
		t.Fatalf("holiday on an ordinary day:\n%s", s)
	}
}

// 5.3: Given an event spanning three days, it appears on each day as part of one bar.
func TestFamilyViewShowsAThreeDayEventOnEachDayAsOneBar(t *testing.T) {
	f := newFamilyBox(t, "eink_color")
	f.event(t, "Grandparents visit", f.at(13, 0, 0), f.at(16, 0, 0), true, "Mama", "Papa")
	for day, want := range map[int]string{13: `data-bar="1/3"`, 14: `data-bar="2/3"`, 15: `data-bar="3/3"`} {
		s := strip(t, f.view(t, "en", f.at(day, 8, 0)))
		if !strings.Contains(s, "Grandparents visit") || !strings.Contains(s, want) {
			t.Errorf("day %d: strip lacks the bar %s:\n%s", day, want, s)
		}
	}
	if s := strip(t, f.view(t, "en", f.at(16, 8, 0))); strings.Contains(s, "Grandparents") {
		t.Error("bar shown after its last day")
	}
}

func TestFamilyViewHeaderShowsDateWeekAndClockWithoutLeaveTime(t *testing.T) {
	f := newFamilyBox(t, "eink_color")
	en := f.view(t, "en", f.at(14, 7, 58))
	for _, want := range []string{"Saturday", "CW 46", "14 November", `data-clock>07:58<`} {
		if !strings.Contains(en, want) {
			t.Errorf("header misses %q", want)
		}
	}
	if de := f.view(t, "de", f.at(14, 7, 58)); !strings.Contains(de, "Samstag") || !strings.Contains(de, "KW 46") || !strings.Contains(de, "14. November") {
		t.Errorf("German header wrong:\n%s", de)
	}
	for _, none := range []string{"leave", "data-weather", "data-footer", "data-progress"} {
		if strings.Contains(en, none) {
			t.Errorf("page contains %q before its data exists", none)
		}
	}
}

// showTomorrow switches the display's "show tomorrow block" setting in the web interface.
func (f *familyBox) showTomorrow(t *testing.T, on bool) {
	t.Helper()
	ds, err := f.db.Displays(f.householdID)
	if err != nil || len(ds) != 1 {
		t.Fatalf("displays = %v, %v", ds, err)
	}
	form := url.Values{}
	if on {
		form.Set("show_tomorrow", "on")
	}
	if r := f.post("/displays/"+ds[0].ID+"/settings", form); r.status != 303 {
		t.Fatalf("settings = %d", r.status)
	}
}

func TestFamilyViewTomorrowBlockAppearsFrom1800(t *testing.T) {
	f := newFamilyBox(t, "eink_color")
	f.showTomorrow(t, true)
	f.event(t, "Piano", f.at(15, 15, 0), f.at(15, 16, 0), false, "Mia")
	if p := f.view(t, "en", f.at(14, 17, 59)); strings.Contains(p, "data-tomorrow") {
		t.Fatal("tomorrow block before 18:00")
	}
	p := f.view(t, "en", f.at(14, 18, 0))
	m := regexp.MustCompile(`(?s)data-tomorrow.*?</section>`).FindString(p)
	if !strings.Contains(m, "Piano") {
		t.Fatalf("tomorrow block misses Piano:\n%s", p)
	}
}

// ADR 0016: off by default; switching it on and off is per display.
func TestFamilyViewTomorrowBlockIsOffUntilTheDisplaySettingIsOn(t *testing.T) {
	f := newFamilyBox(t, "eink_color")
	f.event(t, "Piano", f.at(15, 15, 0), f.at(15, 16, 0), false, "Mia")
	f.event(t, "Brunch", f.at(15, 10, 0), f.at(15, 11, 0), false)
	evening := f.at(14, 19, 0)
	if p := f.view(t, "en", evening); strings.Contains(p, "data-tomorrow") || strings.Contains(p, "data-strip-tomorrow") {
		t.Fatal("tomorrow shown with the setting off (the default)")
	}
	f.showTomorrow(t, true)
	if p := f.view(t, "en", evening); !strings.Contains(p, "data-tomorrow") || !strings.Contains(p, "data-strip-tomorrow") {
		t.Fatal("tomorrow not shown with the setting on")
	}
	f.showTomorrow(t, false)
	if p := f.view(t, "en", evening); strings.Contains(p, "data-tomorrow") {
		t.Fatal("tomorrow still shown after switching the setting off")
	}
}

func TestDisplaysPageShowsTheTomorrowSetting(t *testing.T) {
	f := newFamilyBox(t, "screen")
	if p := f.get("/displays/" + f.displayID(t)).body; !strings.Contains(p, `name="show_tomorrow"`) || strings.Contains(p, `name="show_tomorrow" checked`) {
		t.Fatalf("setting missing or on by default:\n%s", p)
	}
	f.showTomorrow(t, true)
	if p := f.get("/displays/" + f.displayID(t)).body; !strings.Contains(p, `name="show_tomorrow" checked`) {
		t.Fatal("setting not shown as on")
	}
	if r := f.post("/displays/unknown/settings", url.Values{}); r.status != 404 {
		t.Fatalf("unknown display = %d, want 404", r.status)
	}
}

func TestFamilyViewUsesThePaletteOfTheDisplayType(t *testing.T) {
	bw := newFamilyBox(t, "eink_bw")
	if p := bw.view(t, "en", bw.at(14, 8, 0)); !strings.Contains(p, `data-theme="bw"`) {
		t.Error("E-Ink black-and-white display lacks the bw palette")
	}
	col := newFamilyBox(t, "eink_color")
	if p := col.view(t, "en", col.at(14, 8, 0)); !strings.Contains(p, `data-theme="light"`) {
		t.Error("colour display lacks the light palette")
	}
}

// 5.7: one column per person in any number; how many are visible per
// orientation is the stylesheet's job (web/layout_test.go), so the markup
// carries the count and no view-dependent scroll flag.
func TestFamilyViewRendersOneColumnPerPersonWithoutScrollFlag(t *testing.T) {
	f := newFamilyBox(t, "eink_color")
	for _, n := range []string{"Oma", "Opa", "Tante"} {
		f.db.AddPerson(f.householdID, store.Person{Name: n, Role: store.RoleChild})
	}
	p := f.view(t, "en", f.at(14, 8, 0))
	if strings.Count(p, `class="column"`) != 7 || !strings.Contains(p, `data-people="7"`) || !strings.Contains(p, "--cols:7") {
		t.Errorf("seven people: %s", p)
	}
	if strings.Contains(p, "data-scroll") {
		t.Error("view carries a scroll flag; scrolling follows the viewport")
	}
}

// ADR 0016: the progress bar sits at the bottom of the column, after the scrolling events;
// the footer shows only the tiles that have content.
func TestRenderedViewAnchorsProgressAndHidesEmptyTiles(t *testing.T) {
	v := familyview.Build(familyview.Input{I18n: i18n.MustLoad(), Now: time.Date(2026, 11, 14, 8, 0, 0, 0, berlinTZ), Lang: "en", Region: "DE-BY", People: []familyview.Person{
		{ID: "a", Name: "Mia", Slot: 4, Shape: "square-empty"}, {ID: "b", Name: "Papa", Slot: 2, Shape: "circle-empty"}}})
	v.Columns[0].Progress = &familyview.Progress{Done: 3, Total: 5}
	out, err := server.RenderFamilyView("en", v)
	if err != nil {
		t.Fatal(err)
	}
	col := columnRe.FindString(out)
	i, j := strings.Index(col, `class="events"`), strings.Index(col, `data-progress="3/5"`)
	if i < 0 || j < i || !strings.Contains(col, "--pct:60") || !strings.Contains(col, ">3 of 5 tasks done</span>") {
		t.Fatalf("progress bar not after the events:\n%s", col)
	}
	if strings.Count(out, "data-progress") != 1 {
		t.Error("the person without tasks shows a bar")
	}
	if strings.Contains(out, "data-footer") {
		t.Error("footer shown with both tiles empty")
	}
	v.Meal = &familyview.MealTile{Label: "Dinner", Entries: []familyview.MealEntry{{Mealtime: "dinner", Dish: "Pasta bake"}}}
	one, _ := server.RenderFamilyView("en", v)
	if !strings.Contains(one, "data-footer") || !strings.Contains(one, `data-tile="meal"`) || strings.Contains(one, `data-tile="tomorrow"`) {
		t.Errorf("one tile: %s", one)
	}
	v.TomorrowTile = &familyview.Tile{Label: "Tomorrow", Lines: []familyview.Line{{Kind: familyview.LineWaste, Text: "Paper bin"}}, Alert: true}
	two, _ := server.RenderFamilyView("en", v)
	if strings.Count(two, "data-tile=") != 2 || !strings.Contains(two, "tile alert") {
		t.Errorf("two tiles: %s", two)
	}
}

// 5.3: Layout matches the spec values (snapshot of the markup, not a pixel render; the
// stylesheet values are held by web/design_test.go).
func TestFamilyViewMarkupSnapshot(t *testing.T) {
	loc := berlinTZ
	d := func(day, h, m int) time.Time { return time.Date(2026, 11, day, h, m, 0, 0, loc) }
	people := []familyview.Person{
		{ID: "mama", Name: "Mama", Slot: 1, Shape: "circle-filled"}, {ID: "papa", Name: "Papa", Slot: 2, Shape: "circle-empty"},
		{ID: "seb", Name: "Sebastian", Slot: 3, Shape: "square-filled"}, {ID: "mia", Name: "Mia", Slot: 4, Shape: "square-empty"}}
	occ := func(title string, s, e time.Time, who ...string) calendar.Occurrence {
		return calendar.Occurrence{EventID: title, CalendarID: "c", Title: title, Start: s, End: e, People: who}
	}
	v := familyview.Build(familyview.Input{I18n: i18n.MustLoad(), Now: d(14, 18, 5), Lang: "en", Region: "DE-BY", People: people, ShowTomorrow: true, Occurrences: []calendar.Occurrence{
		occ("Dentist", d(14, 9, 0), d(14, 10, 0), "mama"),
		occ("Football", d(14, 15, 0), d(14, 16, 0), "seb", "mia"),
		occ("Piano", d(15, 15, 0), d(15, 16, 0), "mia"),
		occ("Family lunch", d(14, 12, 0), d(14, 13, 30)),
		{EventID: "trip", CalendarID: "c", Title: "Grandparents visit", AllDay: true, Start: d(13, 0, 0), End: d(16, 0, 0), People: []string{"mama", "papa"}},
	}})
	v.Columns[3].Progress = &familyview.Progress{Done: 2, Total: 4}
	got, err := server.RenderFamilyView("en", v)
	if err != nil {
		t.Fatal(err)
	}
	const golden = "testdata/family_markup.html"
	if os.Getenv("HEJMO_UPDATE_GOLDEN") != "" {
		if err := os.WriteFile(golden, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(golden)
	if err != nil {
		t.Fatal(err)
	}
	if got != string(want) {
		t.Errorf("markup differs from %s (HEJMO_UPDATE_GOLDEN=1 rewrites it after a reviewed change):\n%s", golden, got)
	}
}
