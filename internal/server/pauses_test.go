package server_test

import (
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"testing"

	"github.com/hejmo-home/hejmo/internal/streak"
)

func streakPause(from, to string, people ...string) streak.Pause {
	return streak.Pause{From: from, To: to, People: people}
}

func (w *weekBox) postPause(t *testing.T, path string, form url.Values) resp {
	t.Helper()
	return w.do(http.MethodPost, path, form, en)
}

func TestThePauseFormPreselectsTheWholeFamilyAndTodayInTheHouseholdZone(t *testing.T) {
	t.Parallel()
	w := newWeekBox(t)
	w.now = w.at(11, 0, 30) // still the 10th in UTC
	r := w.page("en", "/tasks/pauses/new")
	if r.status != http.StatusOK {
		t.Fatalf("status = %d", r.status)
	}
	for _, want := range []string{"<h1>Add pause days</h1>", `name="whole_family" value="1" data-whole checked`, `name="from" type="date" value="2026-11-11"`, `name="to" type="date" value="2026-11-11"`,
		"Who for?", "Past days work too.", `action="/tasks/pauses/new"`, ">Add pause days</button>", `class="crumbs"`} {
		if !strings.Contains(r.body, want) {
			t.Errorf("form lacks %q\n%s", want, r.body)
		}
	}
	if strings.Contains(r.body, `class="danger"`) {
		t.Error("a new pause has a remove box")
	}
	one := w.page("en", "/tasks/pauses/new?person="+w.people["Mia"].ID).body
	if !regexp.MustCompile(`value="`+w.people["Mia"].ID+`" checked`).MatchString(one) || strings.Contains(one, "data-whole checked") {
		t.Errorf("?person= does not preselect the person\n%s", one)
	}
}

func TestParentsEnterPauseDaysForAChildAndSeeThemOnTheTasksPage(t *testing.T) {
	t.Parallel()
	w := newWeekBox(t)
	w.now = w.at(11, 7, 0)
	r := w.postPause(t, "/tasks/pauses/new", url.Values{"people": {w.people["Sebastian"].ID}, "from": {"2026-11-11"}, "to": {"2026-11-12"}})
	if r.status != http.StatusSeeOther || r.location != "/tasks" {
		t.Fatalf("save = %d %q\n%s", r.status, r.location, r.body)
	}
	page := w.do(http.MethodGet, "/tasks", nil, en).body
	for _, want := range []string{"Pause days saved for Sebastian: Wed 11.11. to Thu 12.11.", `<ul class="list-rows pauses">`, "<b>Wed 11.11. to Thu 12.11.</b>", "Sebastian", `<span class="tag paused">`, "Running now", `href="/tasks/pauses/`, "Add pause days"} {
		if !strings.Contains(page, want) {
			t.Errorf("tasks page lacks %q\n%s", want, page)
		}
	}
	// API parity.
	pauses, _ := w.db.Pauses(w.householdID)
	if len(pauses) != 1 || pauses[0].From != "2026-11-11" || len(pauses[0].People) != 1 {
		t.Fatalf("stored = %+v", pauses)
	}
}

func TestWholeFamilyPausesSayWholeFamilyNotEightNames(t *testing.T) {
	t.Parallel()
	w := newWeekBox(t)
	w.now = w.at(11, 7, 0)
	r := w.postPause(t, "/tasks/pauses/new", url.Values{"whole_family": {"1"}, "people": {w.people["Mia"].ID}, "from": {"2026-12-19"}, "to": {"2027-01-03"}})
	if r.status != http.StatusSeeOther {
		t.Fatalf("save = %d\n%s", r.status, r.body)
	}
	pauses, _ := w.db.Pauses(w.householdID)
	if len(pauses) != 1 || len(pauses[0].People) != 0 {
		t.Fatalf("stored = %+v", pauses)
	}
	page := w.do(http.MethodGet, "/tasks", nil, en).body
	if !strings.Contains(page, "Whole family") || !strings.Contains(page, "Sat 19.12. to Sun 03.01.2027") || strings.Contains(page, "Running now") {
		t.Errorf("tasks page:\n%s", page)
	}
}

func TestNoPauseDaysSaysSoAndOffersTheButton(t *testing.T) {
	t.Parallel()
	w := newWeekBox(t)
	page := w.page("en", "/tasks").body
	for _, want := range []string{`aria-labelledby="pauses"`, "<h2 id=\"pauses\">Pause days</h2>", "No pause days planned.", `href="/tasks/pauses/new"`} {
		if !strings.Contains(page, want) {
			t.Errorf("tasks page lacks %q", want)
		}
	}
	if strings.Contains(page, "Past pause days") {
		t.Error("an empty past list is shown")
	}
	if strings.Index(page, `id="pauses"`) > strings.Index(page, `id="stars"`) && strings.Contains(page, `id="stars"`) {
		t.Error("the pause section comes after the stars")
	}
}

func TestPastPausesAreUnderPastPauseDays(t *testing.T) {
	t.Parallel()
	w := newWeekBox(t)
	w.now = w.at(11, 7, 0)
	if r := w.postPause(t, "/tasks/pauses/new", url.Values{"people": {w.people["Mia"].ID}, "from": {"2026-11-02"}, "to": {"2026-11-03"}}); r.status != http.StatusSeeOther {
		t.Fatalf("past pause = %d\n%s", r.status, r.body)
	}
	page := w.do(http.MethodGet, "/tasks", nil, en).body
	if !strings.Contains(page, "Past pause days") || !strings.Contains(page, "latest Mon 02.11. to Tue 03.11.") || !strings.Contains(page, "No pause days planned.") {
		t.Errorf("tasks page:\n%s", page)
	}
}

func TestPauseFormErrorsSitAtTheToField(t *testing.T) {
	t.Parallel()
	w := newWeekBox(t)
	w.now = w.at(11, 7, 0)
	r := w.postPause(t, "/tasks/pauses/new", url.Values{"whole_family": {"1"}, "from": {"2026-11-12"}, "to": {"2026-11-11"}})
	if r.status != http.StatusUnprocessableEntity {
		t.Fatalf("order = %d", r.status)
	}
	for _, want := range []string{`id="err-to"`, "“To” is before “From”. Choose a day from 12.11.2026 on.", `name="to" type="date" value="2026-11-11" autofocus aria-invalid="true"`, `value="2026-11-12"`} {
		if !strings.Contains(r.body, want) {
			t.Errorf("order error lacks %q\n%s", want, r.body)
		}
	}
	de := w.do(http.MethodPost, "/tasks/pauses/new", url.Values{"from": {"2026-11-12"}, "to": {"2026-11-11"}}, map[string]string{"Accept-Language": "de"})
	if !strings.Contains(de.body, "„Bis“ liegt vor „Von“. Wähle einen Tag ab 12.11.2026.") {
		t.Errorf("German error:\n%s", de.body)
	}
	long := w.postPause(t, "/tasks/pauses/new", url.Values{"from": {"2026-01-01"}, "to": {"2027-01-03"}})
	if long.status != http.StatusUnprocessableEntity || !strings.Contains(long.body, `id="err-to"`) || !strings.Contains(long.body, "366 days at most") {
		t.Errorf("long = %d\n%s", long.status, long.body)
	}
	if got, _ := w.db.Pauses(w.householdID); len(got) != 0 {
		t.Errorf("a refused form stored %+v", got)
	}
}

func TestOverlappingPausesOfTheSamePeopleAreMergedOnSave(t *testing.T) {
	t.Parallel()
	w := newWeekBox(t)
	form := func(from, to string) url.Values {
		return url.Values{"people": {w.people["Mia"].ID}, "from": {from}, "to": {to}}
	}
	w.postPause(t, "/tasks/pauses/new", form("2026-11-10", "2026-11-12"))
	w.postPause(t, "/tasks/pauses/new", form("2026-11-12", "2026-11-14"))
	got, _ := w.db.Pauses(w.householdID)
	if len(got) != 1 || got[0].From != "2026-11-10" || got[0].To != "2026-11-14" {
		t.Fatalf("stored = %+v", got)
	}
}

func TestAPauseCanBeChangedAndRemovedFromItsPage(t *testing.T) {
	t.Parallel()
	w := newWeekBox(t)
	w.now = w.at(11, 7, 0)
	saved, _ := w.db.SavePause(w.householdID, streakPause("2026-11-25", "2026-11-26", w.people["Sebastian"].ID), "")
	page := w.page("en", "/tasks/pauses/"+saved.ID)
	if page.status != http.StatusOK {
		t.Fatalf("page = %d", page.status)
	}
	for _, want := range []string{"<h1>Pause for Sebastian</h1>", `action="/tasks/pauses/edit/` + saved.ID + `"`, `>Save</button>`, `<details class="danger"`, "Remove pause",
		"Remove the pause for Sebastian", "Wed 25.11. to Thu 26.11.", "stays done", `name="from" type="date" value="2026-11-25"`} {
		if !strings.Contains(page.body, want) {
			t.Errorf("edit page lacks %q\n%s", want, page.body)
		}
	}
	if !regexp.MustCompile(`value="` + w.people["Sebastian"].ID + `" checked`).MatchString(page.body) {
		t.Error("Sebastian is not chosen")
	}
	r := w.postPause(t, "/tasks/pauses/edit/"+saved.ID, url.Values{"whole_family": {"1"}, "from": {"2026-11-25"}, "to": {"2026-11-27"}})
	if r.status != http.StatusSeeOther {
		t.Fatalf("change = %d\n%s", r.status, r.body)
	}
	got, _ := w.db.Pause(w.householdID, saved.ID)
	if got.To != "2026-11-27" || len(got.People) != 0 {
		t.Fatalf("changed = %+v", got)
	}
	if bad := w.postPause(t, "/tasks/pauses/edit/"+saved.ID, url.Values{"from": {"2026-11-28"}, "to": {"2026-11-27"}}); bad.status != http.StatusUnprocessableEntity || !strings.Contains(bad.body, "Pause for the whole family") {
		t.Errorf("refused change = %d", bad.status)
	}
	if r := w.postPause(t, "/tasks/pauses/remove/"+saved.ID, url.Values{}); r.status != http.StatusSeeOther || r.location != "/tasks" {
		t.Fatalf("remove = %d", r.status)
	}
	if _, err := w.db.Pause(w.householdID, saved.ID); err == nil {
		t.Error("the pause is still there")
	}
	for _, p := range []string{"/tasks/pauses/" + saved.ID} {
		if w.page("en", p).status != http.StatusNotFound {
			t.Errorf("GET %s after removal is not 404", p)
		}
	}
	if r := w.postPause(t, "/tasks/pauses/remove/"+saved.ID, url.Values{}); r.status != http.StatusNotFound {
		t.Errorf("second remove = %d", r.status)
	}
}

func TestChildrenCannotOpenOrChangePauseDays(t *testing.T) {
	t.Parallel()
	w := newWeekBox(t)
	kid := pairedPersonalDevice(t, w.box, w.people["Mia"], "Mias Handy")
	saved, _ := w.db.SavePause(w.householdID, streakPause("2026-11-25", "2026-11-26"), "")
	if r := kid.get("/tasks/pauses/new", "en"); r.status != http.StatusForbidden {
		t.Errorf("GET new = %d", r.status)
	}
	if r := kid.get("/tasks/pauses/"+saved.ID, "en"); r.status != http.StatusForbidden {
		t.Errorf("GET edit = %d", r.status)
	}
	if r := kid.post("/tasks/pauses/new", url.Values{"from": {"2026-11-01"}, "to": {"2026-11-01"}}); r.status != http.StatusForbidden {
		t.Errorf("POST new = %d", r.status)
	}
	if r := kid.post("/tasks/pauses/remove/"+saved.ID, url.Values{}); r.status != http.StatusForbidden {
		t.Errorf("POST remove = %d", r.status)
	}
	if body := kid.get("/tasks", "en").body; strings.Contains(body, "Pause days") {
		t.Error("the child's tasks page has the pause section")
	}
}
