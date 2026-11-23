package server_test

import (
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/hejmo-home/hejmo/internal/store"
	"github.com/hejmo-home/hejmo/internal/streak"
	"github.com/hejmo-home/hejmo/internal/task"
)

// The week of Mon 9 to Sun 15 November 2026 (ticket 294). Mia's morning routine
// has two steps due Monday to Friday and was created on Monday 2 November.
type weekBox struct {
	*familyBox
	routine task.Routine
	steps   []task.Task
}

func newWeekBox(t *testing.T) *weekBox {
	t.Helper()
	f := newFamilyBox(t, "eink_color")
	w := &weekBox{familyBox: f}
	var err error
	w.routine, err = f.db.CreateRoutine(f.householdID, task.Routine{PersonID: f.people["Mia"].ID, Name: "Morning routine", Part: task.Morning,
		Created: time.Date(2026, 11, 2, 6, 0, 0, 0, berlinTZ)})
	if err != nil {
		t.Fatal(err)
	}
	for _, title := range []string{"Brush teeth", "Get dressed"} {
		k, err := f.db.AddTask(f.householdID, task.Task{PersonID: f.people["Mia"].ID, Title: title, Repeat: task.Weekly, Weekdays: []string{"MO", "TU", "WE", "TH", "FR"}, Date: "2026-11-02", Stars: 1, RoutineID: w.routine.ID})
		if err != nil {
			t.Fatal(err)
		}
		w.steps = append(w.steps, k)
	}
	return w
}

// tick marks both steps (or only the first) done for a day.
func (w *weekBox) tick(t *testing.T, day string, both bool) {
	t.Helper()
	for i, k := range w.steps {
		if i == 1 && !both {
			break
		}
		if err := w.db.SetTaskDone(w.householdID, k.ID, day, true, "", w.now); err != nil {
			t.Fatal(err)
		}
	}
}

// monToTue ticks the Monday and the Tuesday.
func (w *weekBox) monToTue(t *testing.T) {
	w.tick(t, "2026-11-09", true)
	w.tick(t, "2026-11-10", true)
}

var weekRe = regexp.MustCompile(`(?s)<div class="week">.*?</p></div>`)

func weekOf(t *testing.T, page string) string {
	t.Helper()
	m := weekRe.FindString(page)
	if m == "" {
		t.Fatalf("no week on the page:\n%s", page)
	}
	return m
}

var cellRe = regexp.MustCompile(`<li data-state="(\w+)"( data-today)?>`)

// states lists the states of the seven cells, "*" marking today.
func states(t *testing.T, week string) string {
	t.Helper()
	var out []string
	for _, m := range cellRe.FindAllStringSubmatch(week, -1) {
		s := m[1]
		if m[2] != "" {
			s += "*"
		}
		out = append(out, s)
	}
	return strings.Join(out, " ")
}

// Acceptance: computed from completed tasks; the strip, the count and the best
// streak of the personal view on the display.
func TestPersonalViewShowsTheWeekOfAChildsRoutine(t *testing.T) {
	t.Parallel()
	w := newWeekBox(t)
	w.monToTue(t)
	page := w.personalPage(t, "en", "Mia", 11, 7).body
	week := weekOf(t, page)
	if got, want := states(t, week), "done done open* open open off off"; got != want {
		t.Fatalf("cells = %q, want %q\n%s", got, want, week)
	}
	for _, want := range []string{`aria-label="This week"`, `<span class="wd" aria-hidden="true">today</span>`, `<span class="wd" aria-hidden="true">Mon</span>`,
		`<span class="d w" aria-hidden="true">off</span>`, `<span class="sr">Monday: done</span>`, `<span class="sr">today, Wednesday: open</span>`, `<span class="sr">Saturday: no routine</span>`,
		`This week: <b>2 of 5 days</b> done`, `Best streak: <b>2 days in a row</b>`} {
		if !strings.Contains(week, want) {
			t.Errorf("week lacks %q\n%s", want, week)
		}
	}
	if !strings.Contains(routineBox(t, page), `class="week"`) {
		t.Error("the week is not inside the routine box")
	}
	de := weekOf(t, w.personalPage(t, "de", "Mia", 11, 7).body)
	for _, want := range []string{`aria-label="Diese Woche"`, ">heute<", `Diese Woche: <b>2 von 5 Tagen</b> geschafft`, `Bestwert: <b>2 Tage am Stück</b>`, ">frei<"} {
		if !strings.Contains(de, want) {
			t.Errorf("German week lacks %q\n%s", want, de)
		}
	}
}

// Acceptance: unticking the same day updates the count.
func TestUntickingTodayEmptiesTheCellAndLowersBothNumbers(t *testing.T) {
	t.Parallel()
	w := newWeekBox(t)
	w.monToTue(t)
	w.now = w.at(11, 7, 0)
	w.tick(t, "2026-11-11", true)
	week := weekOf(t, w.personalPage(t, "en", "Mia", 11, 7).body)
	if got := states(t, week); got != "done done done* open open off off" || !strings.Contains(week, "<b>3 of 5 days</b>") || !strings.Contains(week, "<b>3 days in a row</b>") {
		t.Fatalf("after ticking: %q\n%s", got, week)
	}
	if r := w.display.post("/display/tasks/"+w.steps[0].ID+"/done", url.Values{"done": {"0"}, "day": {"2026-11-11"}}); r.status != http.StatusSeeOther {
		t.Fatalf("untick = %d", r.status)
	}
	week = weekOf(t, w.personalPage(t, "en", "Mia", 11, 7).body)
	if got := states(t, week); got != "done done open* open open off off" || !strings.Contains(week, "<b>2 of 5 days</b>") || !strings.Contains(week, "<b>2 days in a row</b>") {
		t.Fatalf("after unticking: %q\n%s", got, week)
	}
}

// Acceptance (Y): days follow the household time zone. 00:30 in Berlin on
// Wednesday is still Tuesday in UTC.
func TestTheWeekFollowsTheHouseholdTimeZoneAtMidnight(t *testing.T) {
	t.Parallel()
	w := newWeekBox(t)
	w.monToTue(t)
	w.now = time.Date(2026, 11, 11, 0, 30, 0, 0, berlinTZ) // 23:30 UTC on the 10th
	week := weekOf(t, w.display.get("/display/person/"+w.people["Mia"].ID, "en").body)
	if got, want := states(t, week), "done done open* open open off off"; got != want {
		t.Fatalf("cells = %q, want %q", got, want)
	}
	// The routine counts from its creation day in the household zone: made at
	// 23:30 UTC on Tuesday 10 it was made on Wednesday 11 in Berlin.
	if _, err := w.db.SQL().Exec(`UPDATE routines SET created_at = '2026-11-10T23:30:00Z' WHERE id = ?`, w.routine.ID); err != nil {
		t.Fatal(err)
	}
	week = weekOf(t, w.display.get("/display/person/"+w.people["Mia"].ID, "en").body)
	if got, want := states(t, week), "before before open* open open off off"; got != want {
		t.Fatalf("cells = %q, want %q", got, want)
	}
}

// Missed and still-to-come days look alike, with no message and nothing red.
func TestAMissedDayLooksLikeADayToCome(t *testing.T) {
	t.Parallel()
	w := newWeekBox(t)
	w.tick(t, "2026-11-09", true)
	week := weekOf(t, w.personalPage(t, "en", "Mia", 13, 7).body) // Friday: Tue, Wed, Thu missed
	if got, want := states(t, week), "done open open open open* off off"; got != want {
		t.Fatalf("cells = %q, want %q", got, want)
	}
	if strings.Contains(week, "missed") || strings.Contains(week, "0 of") {
		t.Errorf("the week says something about misses:\n%s", week)
	}
	if !strings.Contains(week, "<b>1 of 5 days</b>") {
		t.Errorf("week = %s", week)
	}
}

func TestNoZeroOfFiveWhileNothingIsDoneYet(t *testing.T) {
	t.Parallel()
	w := newWeekBox(t)
	week := weekOf(t, w.personalPage(t, "en", "Mia", 9, 7).body)
	if !strings.Contains(week, "This week: <b>5 days</b> with the routine") || strings.Contains(week, "0 of") {
		t.Errorf("week = %s", week)
	}
	if strings.Contains(week, "Best streak") {
		t.Errorf("a best streak of 0 is shown:\n%s", week)
	}
	w.monToTue(t)
	w.tick(t, "2026-11-11", true)
	w.tick(t, "2026-11-12", true)
	w.tick(t, "2026-11-13", true)
	if week := weekOf(t, w.personalPage(t, "en", "Mia", 13, 18).body); !strings.Contains(week, "This week: <b>all 5 days</b> done") {
		t.Errorf("week = %s", week)
	}
}

func TestPauseDaysAreWordsAndDropOutOfTheCount(t *testing.T) {
	t.Parallel()
	w := newWeekBox(t)
	w.monToTue(t)
	if _, err := w.db.SavePause(w.householdID, streak.Pause{From: "2026-11-11", To: "2026-11-12", People: []string{w.people["Mia"].ID}}, ""); err != nil {
		t.Fatal(err)
	}
	week := weekOf(t, w.personalPage(t, "en", "Mia", 11, 7).body)
	if got, want := states(t, week), "done done pause* pause open off off"; got != want {
		t.Fatalf("cells = %q, want %q", got, want)
	}
	for _, want := range []string{`<span class="d w" aria-hidden="true">Pause</span>`, `<span class="sr">today, Wednesday: pause</span>`, "<b>2 of 3 days</b>"} {
		if !strings.Contains(week, want) {
			t.Errorf("week lacks %q\n%s", want, week)
		}
	}
	// The tasks stay due and tickable: the box still lists them.
	if page := w.personalPage(t, "en", "Mia", 11, 7).body; !strings.Contains(routineBox(t, page), "Brush teeth") {
		t.Error("the steps are gone on a pause day")
	}
	// Done anyway: the day counts like a normal day.
	w.now = w.at(11, 7, 0)
	w.tick(t, "2026-11-11", true)
	week = weekOf(t, w.personalPage(t, "en", "Mia", 11, 7).body)
	if got, want := states(t, week), "done done done* pause open off off"; got != want {
		t.Fatalf("cells = %q, want %q", got, want)
	}
	if !strings.Contains(week, "today, Wednesday: done (pause day)") {
		t.Errorf("a done pause day does not say so:\n%s", week)
	}
}

func TestEverythingPausedSaysPauseInsteadOfANumber(t *testing.T) {
	t.Parallel()
	w := newWeekBox(t)
	if _, err := w.db.SavePause(w.householdID, streak.Pause{From: "2026-11-09", To: "2026-11-15"}, ""); err != nil {
		t.Fatal(err)
	}
	week := weekOf(t, w.personalPage(t, "en", "Mia", 10, 7).body)
	if !strings.Contains(week, "This week: <b>pause</b>") {
		t.Errorf("week = %s", week)
	}
}

func TestDaysBeforeTheRoutineStartedAreEmptyCells(t *testing.T) {
	t.Parallel()
	w := newWeekBox(t)
	if _, err := w.db.SQL().Exec(`UPDATE routines SET created_at = ? WHERE id = ?`, "2026-11-11T05:00:00Z", w.routine.ID); err != nil {
		t.Fatal(err)
	}
	week := weekOf(t, w.personalPage(t, "en", "Mia", 11, 7).body)
	if got, want := states(t, week), "before before open* open open off off"; got != want {
		t.Fatalf("cells = %q, want %q", got, want)
	}
	if !strings.Contains(week, "<b>3 days</b> with the routine") || !strings.Contains(week, `<span class="d" aria-hidden="true"></span>`) {
		t.Errorf("week = %s", week)
	}
}

// Acceptance: per-person switch, defaults by role. Off hides the week on every
// surface, stars stay; counting goes on in the background.
func TestTheSwitchHidesTheWeekOnAllFourSurfaces(t *testing.T) {
	t.Parallel()
	w := newWeekBox(t)
	w.monToTue(t)
	mia := w.people["Mia"]
	kid := pairedPersonalDevice(t, w.box, mia, "Mias Handy")
	surfaces := func() map[string]string {
		w.now = w.at(11, 7, 0)
		return map[string]string{
			"display":        w.display.get("/display/person/"+mia.ID, "en").body,
			"child's tasks":  kid.get("/tasks", "en").body,
			"parent's tasks": w.page("en", "/tasks").body,
			"routine page":   w.page("en", "/routines/"+w.routine.ID).body,
		}
	}
	for name, body := range surfaces() {
		if !strings.Contains(body, "2 of 5 days") {
			t.Errorf("%s shows no week while the switch is on", name)
		}
	}
	if err := w.db.SetStreaksShown(w.householdID, mia.ID, false); err != nil {
		t.Fatal(err)
	}
	for name, body := range surfaces() {
		for _, not := range []string{"2 of 5 days", "Best streak", `class="week"`, "This week:"} {
			if strings.Contains(body, not) {
				t.Errorf("%s still shows %q with the switch off", name, not)
			}
		}
	}
	if body := w.display.get("/display/person/"+mia.ID, "en").body; !strings.Contains(body, `data-stars=`) {
		t.Error("stars disappeared with the switch")
	}
	if err := w.db.SetStreaksShown(w.householdID, mia.ID, true); err != nil {
		t.Fatal(err)
	}
	if body := w.display.get("/display/person/"+mia.ID, "en").body; !strings.Contains(body, "<b>2 days in a row</b>") {
		t.Errorf("the best streak is gone after switching back on:\n%s", body)
	}
}

func TestAnAdultsWeekIsHiddenByDefault(t *testing.T) {
	t.Parallel()
	f := newFamilyBox(t, "eink_color")
	papa := f.people["Papa"]
	r, err := f.db.CreateRoutine(f.householdID, task.Routine{PersonID: papa.ID, Name: "Coffee", Part: task.Morning, Created: time.Date(2026, 11, 2, 6, 0, 0, 0, berlinTZ)})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.AddTask(f.householdID, task.Task{PersonID: papa.ID, Title: "Make coffee", Repeat: task.Daily, Date: "2026-11-02", RoutineID: r.ID}); err != nil {
		t.Fatal(err)
	}
	if body := f.personalPage(t, "en", "Papa", 11, 7).body; strings.Contains(body, `class="week"`) {
		t.Error("an adult has a week without switching it on")
	}
	if err := f.db.SetStreaksShown(f.householdID, papa.ID, true); err != nil {
		t.Fatal(err)
	}
	if body := f.personalPage(t, "en", "Papa", 11, 7).body; !strings.Contains(body, `class="week"`) {
		t.Error("an adult who switched it on has no week")
	}
}

// The family view stays free of weeks: no comparison between children.
func TestTheFamilyViewShowsNoWeekAndNoBestStreak(t *testing.T) {
	t.Parallel()
	w := newWeekBox(t)
	w.monToTue(t)
	body := w.view(t, "en", w.at(11, 7, 0))
	for _, not := range []string{`class="week"`, "Best streak", "This week"} {
		if strings.Contains(body, not) {
			t.Errorf("family view shows %q", not)
		}
	}
}

func TestAChildSeesTheWeekOnTheirTasksPage(t *testing.T) {
	t.Parallel()
	w := newWeekBox(t)
	w.monToTue(t)
	kid := pairedPersonalDevice(t, w.box, w.people["Mia"], "Mias Handy")
	w.now = w.at(11, 7, 0)
	page := kid.get("/tasks", "en").body
	if got, want := states(t, weekOf(t, page)), "done done open* open open off off"; got != want {
		t.Fatalf("cells = %q, want %q", got, want)
	}
}

func TestParentsSeeTwoLinesOnTheRoutineCardAndNoStrip(t *testing.T) {
	t.Parallel()
	w := newWeekBox(t)
	w.monToTue(t)
	w.now = w.at(11, 7, 0)
	page := w.page("en", "/tasks").body
	if strings.Contains(page, `class="week"`) {
		t.Error("the parent's card has a strip")
	}
	for _, want := range []string{`<span class="state wline">This week: 2 of 5 days done</span>`, `<span class="state wline">Best streak: 2 days in a row</span>`} {
		if !strings.Contains(page, want) {
			t.Errorf("tasks page lacks %q\n%s", want, page)
		}
	}
	if strings.Contains(page, "Today pause day") {
		t.Error("pause tag without a pause")
	}
	if _, err := w.db.SavePause(w.householdID, streak.Pause{From: "2026-11-11", To: "2026-11-11", People: []string{w.people["Mia"].ID}}, ""); err != nil {
		t.Fatal(err)
	}
	page = w.page("en", "/tasks").body
	if !strings.Contains(page, `<span class="tag paused">`) || !strings.Contains(page, "Today pause day") {
		t.Errorf("no pause tag today\n%s", page)
	}
	if !strings.Contains(page, "today 0 of 2 done") && !strings.Contains(page, "0 of 2 done") {
		t.Errorf("the card state changed on a pause day\n%s", page)
	}
}

func TestTheRoutinePageShowsTheWeekAHintAndALinkToPauseDays(t *testing.T) {
	t.Parallel()
	w := newWeekBox(t)
	w.monToTue(t)
	w.now = w.at(11, 7, 0)
	page := w.page("en", "/routines/"+w.routine.ID).body
	if !strings.Contains(page, `<dd class="wk" data-person="`) {
		t.Fatalf("no week fact\n%s", page)
	}
	week := weekOf(t, page)
	if strings.Contains(week, "This week:") || !strings.Contains(week, "<b>2 of 5 days</b> done") {
		t.Errorf("the week on the routine page has the prefix or lacks the count:\n%s", week)
	}
	if !strings.Contains(page, `href="/tasks/pauses/new?person=`+w.people["Mia"].ID+`"`) {
		t.Errorf("no link to pause days for Mia\n%s", page)
	}
	if err := w.db.SetStreaksShown(w.householdID, w.people["Mia"].ID, false); err != nil {
		t.Fatal(err)
	}
	page = w.page("en", "/routines/"+w.routine.ID).body
	if !strings.Contains(page, "Switched off for Mia") || !strings.Contains(page, `href="/people/`+w.people["Mia"].ID+`"`) || strings.Contains(page, "/tasks/pauses/new") {
		t.Errorf("switched-off routine page:\n%s", page)
	}
}

func TestThePersonPageHasTheSwitchAndSavesIt(t *testing.T) {
	t.Parallel()
	w := newWeekBox(t)
	mia := w.people["Mia"]
	page := w.page("en", "/people/"+mia.ID).body
	if !strings.Contains(page, "Week count and badges") || !strings.Contains(page, `name="streaks" `) || !strings.Contains(page, "checked") {
		t.Fatalf("person page:\n%s", page)
	}
	form := url.Values{"name": {"Mia"}}
	r := w.do(http.MethodPost, "/people/"+mia.ID, form, map[string]string{"Accept-Language": "en"})
	if r.status != http.StatusSeeOther {
		t.Fatalf("save = %d\n%s", r.status, r.body)
	}
	got, _ := w.db.Person(w.householdID, mia.ID)
	if got.StreaksShown {
		t.Error("a form without the switch left it on")
	}
	follow := w.do(http.MethodGet, r.location, nil, map[string]string{"Accept-Language": "en"})
	if !strings.Contains(follow.body, "Mia no longer sees week counts or badges. Stars stay visible.") {
		t.Errorf("no line about the switch being off\n%s", follow.body)
	}
	form.Set("streaks", "on")
	if r := w.do(http.MethodPost, "/people/"+mia.ID, form, nil); r.status != http.StatusSeeOther {
		t.Fatalf("save = %d", r.status)
	}
	if got, _ := w.db.Person(w.householdID, mia.ID); !got.StreaksShown {
		t.Error("the switch did not turn on")
	}
}

func TestARejectedPersonFormKeepsTheSwitchTheParentSet(t *testing.T) {
	t.Parallel()
	w := newWeekBox(t)
	mia := w.people["Mia"] // the switch is on for her
	hdr := map[string]string{"Accept-Language": "en"}
	checked := func(body string) bool {
		i := strings.Index(body, `name="streaks"`)
		if i < 0 {
			t.Fatalf("no switch on the page:\n%s", body)
		}
		j := strings.Index(body[i:], ">")
		return strings.Contains(body[i:i+j], "checked")
	}
	off := w.do(http.MethodPost, "/people/"+mia.ID, url.Values{"name": {""}}, hdr)
	if off.status != http.StatusUnprocessableEntity {
		t.Fatalf("empty name = %d, want 422\n%s", off.status, off.body)
	}
	if checked(off.body) {
		t.Errorf("the parent switched it off, the 422 page shows it on")
	}
	on := w.do(http.MethodPost, "/people/"+mia.ID, url.Values{"name": {""}, "streaks": {"on"}}, hdr)
	if on.status != http.StatusUnprocessableEntity || !checked(on.body) {
		t.Errorf("422 with the switch on: status %d, checked %v", on.status, checked(on.body))
	}
	if got, _ := w.db.Person(w.householdID, mia.ID); !got.StreaksShown {
		t.Error("a rejected form changed the stored switch")
	}
}

func TestPersonsCreatedByRoleGetTheSwitchByRole(t *testing.T) {
	t.Parallel()
	f := newFamilyBox(t, "eink_color")
	for who, want := range map[string]bool{"Mama": false, "Papa": false, "Sebastian": true, "Mia": true} {
		p, _ := f.db.Person(f.householdID, f.people[who].ID)
		if p.StreaksShown != want {
			t.Errorf("%s: StreaksShown = %v, want %v", who, p.StreaksShown, want)
		}
	}
	_ = store.RoleChild
}
