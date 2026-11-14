package server_test

import (
	"net/http"
	"net/url"
	"strings"
	"testing"
)

func TestTasksPageNeedsASignedInParent(t *testing.T) {
	b, _ := configuredBox(t)
	if r := b.get("/tasks"); r.status != http.StatusSeeOther || !strings.HasPrefix(r.location, "/login") {
		t.Fatalf("signed out = %d %q", r.status, r.location)
	}
}

func TestParentCreatesAndDeletesTasksInTheWebInterface(t *testing.T) {
	tb := newTaskBox(t)
	b := tb.parent.b
	r := b.post("/tasks", url.Values{"person": {tb.miaID}, "title": {"Brush teeth"}, "emoji": {"🪥"}, "repeat": {"weekly"}, "weekdays": {"MO", "TU"}, "part": {"morning"}})
	if r.status != http.StatusSeeOther || r.location != "/tasks" {
		t.Fatalf("create = %d %q\n%s", r.status, r.location, r.body)
	}
	page := b.do(http.MethodGet, "/tasks", nil, map[string]string{"Accept-Language": "en"})
	if page.status != http.StatusOK {
		t.Fatalf("page = %d", page.status)
	}
	for _, want := range []string{"Brush teeth", "Every", "Mon, Tue", "Morning", "🪥", "Mia"} {
		if !strings.Contains(page.body, want) {
			t.Fatalf("page lacks %q:\n%s", want, page.body)
		}
	}
	// API parity: the same task is there.
	_, _, list := tb.parent.call(http.MethodGet, "/tasks", nil)
	if len(list) != 1 || list[0].(map[string]any)["repeat"] != "weekly" || list[0].(map[string]any)["part_of_day"] != "morning" {
		t.Fatalf("API list = %v", list)
	}
	id := list[0].(map[string]any)["id"].(string)
	if r := b.post("/tasks/"+id+"/delete", url.Values{}); r.status != http.StatusSeeOther {
		t.Fatalf("delete = %d", r.status)
	}
	if _, _, list := tb.parent.call(http.MethodGet, "/tasks", nil); len(list) != 0 {
		t.Fatalf("left over: %v", list)
	}
	if r := b.post("/tasks/"+id+"/delete", url.Values{}); r.status != http.StatusNotFound {
		t.Fatalf("second delete = %d", r.status)
	}
}

func TestInvalidTaskFormShowsAnError(t *testing.T) {
	tb := newTaskBox(t)
	b := tb.parent.b
	r := b.do(http.MethodPost, "/tasks", url.Values{"person": {tb.miaID}, "title": {" "}, "repeat": {"daily"}}, map[string]string{"Accept-Language": "en"})
	if r.status != http.StatusUnprocessableEntity || !strings.Contains(r.body, `role="alert"`) {
		t.Fatalf("blank title = %d\n%s", r.status, r.body)
	}
	r = b.post("/tasks", url.Values{"person": {tb.miaID}, "title": {"x"}, "repeat": {"once"}})
	if r.status != http.StatusUnprocessableEntity {
		t.Fatalf("once without date = %d", r.status)
	}
}

// Acceptance: Given a parent on the go, when they confirm a child's task in
// the web interface, then it is stored as done.
func TestParentConfirmsAChildsTaskInTheWebInterface(t *testing.T) {
	tb := newTaskBox(t)
	b := tb.parent.b
	id := tb.newDaily(t, tb.miaID)
	if r := b.post("/tasks/"+id+"/done", url.Values{"done": {"1"}}); r.status != http.StatusSeeOther || r.location != "/tasks" {
		t.Fatalf("confirm = %d %q", r.status, r.location)
	}
	if got := taskState(t, tb.child, id, ""); got["done"] != true {
		t.Fatalf("stored state = %v", got)
	}
	if page := b.get("/tasks"); page.status != http.StatusOK || !strings.Contains(page.body, "data-done") || !strings.Contains(page.body, "<s>Brush teeth</s>") {
		t.Fatalf("page does not show the task as done (%d)", page.status)
	}
	if r := b.post("/tasks/"+id+"/done", url.Values{"done": {"0"}}); r.status != http.StatusSeeOther {
		t.Fatalf("reopen = %d", r.status)
	}
	if got := taskState(t, tb.child, id, ""); got["done"] != false {
		t.Fatalf("after reopen = %v", got)
	}
	if r := b.post("/tasks/00000000-0000-4000-8000-000000000000/done", url.Values{"done": {"1"}}); r.status != http.StatusNotFound {
		t.Fatalf("unknown = %d", r.status)
	}
}

func TestTasksPageIsGerman(t *testing.T) {
	tb := newTaskBox(t)
	r := tb.parent.b.do(http.MethodGet, "/tasks", nil, map[string]string{"Accept-Language": "de"})
	if r.status != http.StatusOK || !strings.Contains(r.body, "Aufgaben") {
		t.Fatalf("de page = %d", r.status)
	}
}

func TestTaskFormKeepsWeekdaysAndRepeatAfterAnError(t *testing.T) {
	tb := newTaskBox(t)
	r := tb.parent.b.post("/tasks", url.Values{"person": {tb.miaID}, "title": {" "}, "repeat": {"weekly"}, "weekdays": {"TU", "FR"}})
	if r.status != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d", r.status)
	}
	for _, want := range []string{`value="TU" checked`, `value="FR" checked`} {
		if !strings.Contains(r.body, want) {
			t.Errorf("form lacks %q:\n%s", want, r.body)
		}
	}
	if strings.Contains(r.body, `value="MO" checked`) {
		t.Error("Monday must stay unchecked")
	}
}

// 7.1: the star value is a field of the new-task form (empty: the default of
// the person) and shows on the task; the API sees the same value.
func TestParentSetsAStarValueInTheWebInterface(t *testing.T) {
	tb := newTaskBox(t)
	b := tb.parent.b
	b.post("/tasks", url.Values{"person": {tb.miaID}, "title": {"Tidy room"}, "repeat": {"daily"}, "stars": {"3"}})
	b.post("/tasks", url.Values{"person": {tb.miaID}, "title": {"Feed cat"}, "repeat": {"daily"}})
	b.post("/tasks", url.Values{"person": {tb.mamaID}, "title": {"Bins"}, "repeat": {"daily"}})
	_, _, list := tb.parent.call(http.MethodGet, "/tasks", nil)
	got := map[string]float64{}
	for _, x := range list {
		m := x.(map[string]any)
		got[m["title"].(string)] = m["stars"].(float64)
	}
	if got["Tidy room"] != 3 || got["Feed cat"] != 1 || got["Bins"] != 0 {
		t.Fatalf("stars = %v, want Tidy room 3, Feed cat 1 (child default), Bins 0 (adult default)", got)
	}
	page := b.do(http.MethodGet, "/tasks", nil, map[string]string{"Accept-Language": "en"})
	if !strings.Contains(page.body, `data-task-stars="3"`) || !strings.Contains(page.body, `name="stars"`) {
		t.Fatalf("page lacks the star value or the field:\n%s", page.body)
	}
	if r := b.post("/tasks", url.Values{"person": {tb.miaID}, "title": {"x"}, "repeat": {"daily"}, "stars": {"-4"}}); r.status != http.StatusUnprocessableEntity {
		t.Errorf("negative stars = %d, want 422", r.status)
	}
	if r := b.post("/tasks", url.Values{"person": {tb.miaID}, "title": {"x"}, "repeat": {"daily"}, "stars": {"lots"}}); r.status != http.StatusUnprocessableEntity {
		t.Errorf("non-numeric stars = %d, want 422", r.status)
	}
}

// 7.1: ticking in the web interface credits stars, and the page shows the
// person's balance and star log.
func TestWebTickCreditsStarsAndThePageShowsBalanceAndLog(t *testing.T) {
	tb := newTaskBox(t)
	b := tb.parent.b
	b.post("/tasks", url.Values{"person": {tb.miaID}, "title": {"Tidy room"}, "repeat": {"daily"}, "stars": {"3"}})
	_, _, list := tb.parent.call(http.MethodGet, "/tasks", nil)
	id := list[0].(map[string]any)["id"].(string)
	b.post("/tasks/"+id+"/done", url.Values{"done": {"1"}})
	page := b.do(http.MethodGet, "/tasks", nil, map[string]string{"Accept-Language": "en"}).body
	for _, want := range []string{`data-balance="3"`, "Star log", "Tidy room", "&#43;3"} { // html/template writes + as &#43;
		if !strings.Contains(page, want) {
			t.Errorf("page lacks %q:\n%s", want, page)
		}
	}
}

// 7.1 acceptance: Given a parent books -5 with note "redeemed: ice cream",
// then the balance and the log show it (web interface and API alike).
func TestParentBooksStarsByHandInTheWebInterface(t *testing.T) {
	tb := newTaskBox(t)
	b := tb.parent.b
	r := b.post("/tasks/stars", url.Values{"person": {tb.miaID}, "delta": {"-5"}, "note": {"redeemed: ice cream"}})
	if r.status != http.StatusSeeOther || r.location != "/tasks" {
		t.Fatalf("book = %d %q\n%s", r.status, r.location, r.body)
	}
	page := b.do(http.MethodGet, "/tasks", nil, map[string]string{"Accept-Language": "en"}).body
	for _, want := range []string{`data-balance="-5"`, "redeemed: ice cream", "-5", "Mama"} {
		if !strings.Contains(page, want) {
			t.Errorf("page lacks %q:\n%s", want, page)
		}
	}
	// API parity.
	_, acc, _ := tb.parent.call(http.MethodGet, "/people/"+tb.miaID+"/stars", nil)
	if acc["balance"] != float64(-5) {
		t.Errorf("API balance = %v", acc["balance"])
	}
}

func TestInvalidStarBookingShowsAnErrorAndKeepsTheForm(t *testing.T) {
	tb := newTaskBox(t)
	b := tb.parent.b
	for name, form := range map[string]url.Values{
		"zero":       {"person": {tb.miaID}, "delta": {"0"}, "note": {"x"}},
		"no note":    {"person": {tb.miaID}, "delta": {"2"}, "note": {" "}},
		"not number": {"person": {tb.miaID}, "delta": {"many"}, "note": {"x"}},
	} {
		r := b.do(http.MethodPost, "/tasks/stars", form, map[string]string{"Accept-Language": "en"})
		if r.status != http.StatusUnprocessableEntity || !strings.Contains(r.body, `role="alert"`) {
			t.Errorf("%s = %d\n%s", name, r.status, r.body)
		}
	}
	r := b.do(http.MethodPost, "/tasks/stars", url.Values{"person": {tb.miaID}, "delta": {"2"}, "note": {" "}}, map[string]string{"Accept-Language": "en"})
	if !strings.Contains(r.body, `value="2"`) {
		t.Errorf("the amount typed is lost:\n%s", r.body)
	}
	if r := b.post("/tasks/stars", url.Values{"person": {"00000000-0000-4000-8000-000000000000"}, "delta": {"1"}, "note": {"x"}}); r.status != http.StatusUnprocessableEntity {
		t.Errorf("unknown person = %d, want 422", r.status)
	}
}

func TestStarBookingPageIsForSignedInParentsOnly(t *testing.T) {
	b, _ := configuredBox(t)
	if r := b.post("/tasks/stars", url.Values{"delta": {"5"}, "note": {"x"}}); r.status != http.StatusSeeOther || !strings.HasPrefix(r.location, "/login") {
		t.Fatalf("signed out = %d %q", r.status, r.location)
	}
}

func TestStarsOnTheTasksPageAreGerman(t *testing.T) {
	tb := newTaskBox(t)
	b := tb.parent.b
	b.post("/tasks/stars", url.Values{"person": {tb.miaID}, "delta": {"2"}, "note": {"Geschenk"}})
	page := b.do(http.MethodGet, "/tasks", nil, map[string]string{"Accept-Language": "de"}).body
	for _, want := range []string{"Sternekonto", "Geschenk"} {
		if !strings.Contains(page, want) {
			t.Errorf("German page lacks %q", want)
		}
	}
}
