package server_test

import (
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"testing"
)

func TestTasksPageNeedsASignedInParent(t *testing.T) {
	t.Parallel()
	b, _ := configuredBox(t)
	if r := b.get("/tasks"); r.status != http.StatusSeeOther || !strings.HasPrefix(r.location, "/login") {
		t.Fatalf("signed out = %d %q", r.status, r.location)
	}
}

func TestParentCreatesAndDeletesTasksInTheWebInterface(t *testing.T) {
	t.Parallel()
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
	t.Parallel()
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
	t.Parallel()
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
	t.Parallel()
	tb := newTaskBox(t)
	r := tb.parent.b.do(http.MethodGet, "/tasks", nil, map[string]string{"Accept-Language": "de"})
	if r.status != http.StatusOK || !strings.Contains(r.body, "Aufgaben") {
		t.Fatalf("de page = %d", r.status)
	}
}

func TestTaskFormKeepsWeekdaysAndRepeatAfterAnError(t *testing.T) {
	t.Parallel()
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
	t.Parallel()
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
	t.Parallel()
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
	t.Parallel()
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
	t.Parallel()
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
	t.Parallel()
	b, _ := configuredBox(t)
	if r := b.post("/tasks/stars", url.Values{"delta": {"5"}, "note": {"x"}}); r.status != http.StatusSeeOther || !strings.HasPrefix(r.location, "/login") {
		t.Fatalf("signed out = %d %q", r.status, r.location)
	}
}

func TestStarsOnTheTasksPageAreGerman(t *testing.T) {
	t.Parallel()
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

// 7.1: a child's personal device shows neither the star accounts nor the booking form
// (ADR 0015), so a child never sees siblings' balances.
func TestChildPersonalDeviceShowsNoStarAccountsOrBookingForm(t *testing.T) {
	t.Parallel()
	cp := newChildPersonalDevice(t)
	b := cp.parent.b
	b.post("/tasks", url.Values{"person": {personNamed(t, b, "Mia").ID}, "title": {"Tidy room"}, "repeat": {"daily"}, "stars": {"3"}})
	_, _, list := cp.parent.call(http.MethodGet, "/tasks", nil)
	for _, it := range list {
		if m := it.(map[string]any); m["title"] == "Tidy room" {
			b.post("/tasks/"+m["id"].(string)+"/done", url.Values{"done": {"1"}})
		}
	}
	if parent := b.get("/tasks").body; !strings.Contains(parent, "data-balance=") {
		t.Fatalf("parent page shows no star account:\n%s", parent)
	}
	page := cp.screen.get("/tasks", "en")
	if page.status != http.StatusOK {
		t.Fatalf("child GET /tasks = %d", page.status)
	}
	for _, bad := range []string{"data-balance=", "/tasks/stars", "Star log"} {
		if strings.Contains(page.body, bad) {
			t.Errorf("child's page contains %q:\n%s", bad, page.body)
		}
	}
}

// 7.1: an invalid star value gets its own error text and the field is bounded.
func TestInvalidStarValueHasItsOwnErrorAndTheFieldIsBounded(t *testing.T) {
	t.Parallel()
	tb := newTaskBox(t)
	b := tb.parent.b
	r := b.do(http.MethodPost, "/tasks", url.Values{"person": {tb.miaID}, "title": {"Tidy"}, "repeat": {"daily"}, "stars": {"101"}}, map[string]string{"Accept-Language": "en"})
	if r.status != http.StatusUnprocessableEntity || !strings.Contains(r.body, "Please enter a whole number of stars from 0 to 100.") {
		t.Fatalf("limit = %d\n%s", r.status, r.body)
	}
	if !strings.Contains(r.body, `name="stars" type="number"`) || !strings.Contains(r.body, `min="0" max="100" step="1"`) {
		t.Errorf("star field is not bounded:\n%s", r.body)
	}
}

// 7.1: the task detail page shows the star value.
func TestTaskDetailPageShowsTheStarValue(t *testing.T) {
	t.Parallel()
	tb := newTaskBox(t)
	b := tb.parent.b
	b.post("/tasks", url.Values{"person": {tb.miaID}, "title": {"Tidy room"}, "repeat": {"daily"}, "stars": {"3"}})
	_, _, list := tb.parent.call(http.MethodGet, "/tasks", nil)
	id := list[0].(map[string]any)["id"].(string)
	if page := b.do(http.MethodGet, "/tasks/"+id, nil, map[string]string{"Accept-Language": "en"}); !strings.Contains(page.body, `data-task-stars="3"`) {
		t.Errorf("detail page lacks the star value:\n%s", page.body)
	}
}

// Acceptance (#122): a parent changes a task's star value on its detail page.
func TestParentChangesATasksStarValueOnItsDetailPage(t *testing.T) {
	t.Parallel()
	tb := newTaskBox(t)
	b := tb.parent.b
	id := tb.newDaily(t, tb.miaID)
	en := map[string]string{"Accept-Language": "en"}
	page := b.do(http.MethodGet, "/tasks/"+id, nil, en)
	for _, want := range []string{`action="/tasks/` + id + `/stars"`, `name="stars"`, `value="1"`, `max="100"`, `id="hint-stars"`, `aria-describedby="hint-stars"`,
		"Stars already credited stay as they are.", "Save stars"} {
		if !strings.Contains(page.body, want) {
			t.Fatalf("detail page lacks %q:\n%s", want, page.body)
		}
	}
	for _, c := range []struct{ stars, line string }{
		{"3", "Saved. Ticking off now earns <b>3 stars</b>."},
		{"1", "Saved. Ticking off now earns <b>1 star</b>."},
		{"0", "Saved. Ticking off now earns <b>no stars</b>."},
	} {
		r := b.post("/tasks/"+id+"/stars", url.Values{"stars": {c.stars}})
		if r.status != http.StatusSeeOther || r.location != "/tasks/"+id {
			t.Fatalf("save %s = %d %q\n%s", c.stars, r.status, r.location, r.body)
		}
		page := b.do(http.MethodGet, "/tasks/"+id, nil, en)
		if !strings.Contains(page.body, c.line) || !strings.Contains(page.body, `value="`+c.stars+`"`) {
			t.Fatalf("after saving %s the page lacks %q or the value:\n%s", c.stars, c.line, page.body)
		}
		if got := taskState(t, tb.parent, id, ""); got["stars"] != float64(mustAtoi(c.stars)) {
			t.Fatalf("API stars = %v, want %s", got["stars"], c.stars)
		}
	}
}

func mustAtoi(s string) int { n, _ := strconv.Atoi(s); return n }

func TestInvalidStarValueKeepsWhatWasTypedAndSavesNothing(t *testing.T) {
	t.Parallel()
	tb := newTaskBox(t)
	b := tb.parent.b
	id := tb.newDaily(t, tb.miaID)
	for _, bad := range []string{"", " ", "abc", "-1", "101", "2.5"} {
		r := b.do(http.MethodPost, "/tasks/"+id+"/stars", url.Values{"stars": {bad}}, map[string]string{"Accept-Language": "en"})
		if r.status != http.StatusUnprocessableEntity {
			t.Fatalf("%q = %d, want 422", bad, r.status)
		}
		for _, want := range []string{`role="alert"`, "Please enter a whole number of stars from 0 to 100.", `aria-invalid="true"`, `aria-describedby="err-stars hint-stars"`, `value="` + strings.TrimSpace(bad) + `"`} {
			if !strings.Contains(r.body, want) {
				t.Fatalf("%q: page lacks %q:\n%s", bad, want, r.body)
			}
		}
		if got := taskState(t, tb.parent, id, ""); got["stars"] != float64(1) {
			t.Fatalf("%q changed the value to %v", bad, got["stars"])
		}
	}
}

func TestStarValueOfUnknownTaskIs404AndAChildIsForbidden(t *testing.T) {
	t.Parallel()
	tb := newTaskBox(t)
	id := tb.newDaily(t, tb.miaID)
	if r := tb.parent.b.post("/tasks/00000000-0000-0000-0000-000000000000/stars", url.Values{"stars": {"2"}}); r.status != http.StatusNotFound {
		t.Fatalf("unknown = %d", r.status)
	}
	child := childSession(t, tb.parent.b)
	if r := child.post("/tasks/"+id+"/stars", url.Values{"stars": {"2"}}); r.status != http.StatusForbidden {
		t.Fatalf("child = %d", r.status)
	}
	if got := taskState(t, tb.parent, id, ""); got["stars"] != float64(1) {
		t.Fatalf("a child changed the value to %v", got["stars"])
	}
}

// Acceptance (#122): ticks already credited keep their value, said in words.
func TestDetailPageSaysWhatTodaysTickCredited(t *testing.T) {
	t.Parallel()
	tb := newTaskBox(t)
	b := tb.parent.b
	en := map[string]string{"Accept-Language": "en"}
	id := tb.newDaily(t, tb.miaID)
	if page := b.do(http.MethodGet, "/tasks/"+id, nil, en); strings.Contains(page.body, "Ticked off today already") {
		t.Fatal("notice shown for an open task")
	}
	if r := b.post("/tasks/"+id+"/done", url.Values{"done": {"1"}}); r.status != http.StatusSeeOther {
		t.Fatalf("tick = %d", r.status)
	}
	b.post("/tasks/"+id+"/stars", url.Values{"stars": {"5"}})
	page := b.do(http.MethodGet, "/tasks/"+id, nil, en)
	for _, want := range []string{"<b>Ticked off today already:</b> Mia got <b>1 star</b> for it.", "exactly this <b>1 star</b> is deducted", `value="5"`} {
		if !strings.Contains(page.body, want) {
			t.Fatalf("page lacks %q:\n%s", want, page.body)
		}
	}
	// Unticking deducts the credited star, not the new value.
	b.post("/tasks/"+id+"/done", url.Values{"done": {"0"}})
	status, stars, _ := tb.parent.call(http.MethodGet, "/people/"+tb.miaID+"/stars", nil)
	if status != http.StatusOK || stars["balance"] != float64(0) {
		t.Fatalf("balance after untick = %v", stars)
	}
	// More than one star, and none.
	b.post("/tasks/"+id+"/stars", url.Values{"stars": {"2"}})
	b.post("/tasks/"+id+"/done", url.Values{"done": {"1"}})
	page = b.do(http.MethodGet, "/tasks/"+id, nil, en)
	if want := "Mia got <b>2 stars</b> for it. They stay"; !strings.Contains(page.body, want) {
		t.Fatalf("page lacks %q:\n%s", want, page.body)
	}
	b.post("/tasks/"+id+"/done", url.Values{"done": {"0"}})
	b.post("/tasks/"+id+"/stars", url.Values{"stars": {"0"}})
	b.post("/tasks/"+id+"/done", url.Values{"done": {"1"}})
	page = b.do(http.MethodGet, "/tasks/"+id, nil, en)
	if want := "<b>Ticked off today already</b>, and that earned no stars. If the task is reopened today, nothing is deducted."; !strings.Contains(page.body, want) {
		t.Fatalf("page lacks %q:\n%s", want, page.body)
	}
}

// The notice builds trusted markup by hand, so a name with markup in it must
// come out escaped.
func TestDetailPageEscapesTheOwnerNameInTheCreditedNotice(t *testing.T) {
	t.Parallel()
	tb := newTaskBox(t)
	b := tb.parent.b
	en := map[string]string{"Accept-Language": "en"}
	id := tb.newDaily(t, tb.miaID)
	if status, got, _ := tb.parent.call(http.MethodPatch, "/people/"+tb.miaID, map[string]any{"name": `Tom & "Jerry" <b>x</b>`}); status != http.StatusOK {
		t.Fatalf("rename = %d %v", status, got)
	}
	b.post("/tasks/"+id+"/done", url.Values{"done": {"1"}})
	page := b.do(http.MethodGet, "/tasks/"+id, nil, en)
	if strings.Contains(page.body, "<b>x</b>") {
		t.Fatalf("owner name came out as markup:\n%s", page.body)
	}
	if want := "Tom &amp; &#34;Jerry&#34; &lt;b&gt;x&lt;/b&gt; got <b>1 star</b>"; !strings.Contains(page.body, want) {
		t.Fatalf("page lacks %q:\n%s", want, page.body)
	}
}

// A forged star line cookie without a number shows nothing, not a raw verb.
func TestForgedStarsFlashWithoutNumberShowsNothing(t *testing.T) {
	t.Parallel()
	tb := newTaskBox(t)
	b := tb.parent.b
	id := tb.newDaily(t, tb.miaID)
	for _, v := range []string{"tasks.stars.saved.other", "tasks.stars.saved.other:abc", "tasks.stars.saved.one", "tasks.stars.saved.bogus:3"} {
		page := b.do(http.MethodGet, "/tasks/"+id, nil, map[string]string{"Accept-Language": "en", "Cookie": "hejmo_flash=" + v})
		if strings.Contains(page.body, "%d") || strings.Contains(page.body, "MISSING") || strings.Contains(page.body, "Saved. Ticking off") {
			t.Errorf("cookie %q shows a line:\n%s", v, page.body)
		}
	}
}

// Product owner decision Q2: a booking by a person who was removed keeps their
// last name, shown as "<name> (removed)" on the page and flagged on the API.
func TestStarLogKeepsTheNameOfARemovedBooker(t *testing.T) {
	t.Parallel()
	tb := newTaskBox(t)
	b := tb.parent.b
	_, papa, _ := tb.parent.call(http.MethodPost, "/people", map[string]any{"name": "Papa", "role": "parent"})
	db := b.db.SQL()
	if _, err := db.Exec(`INSERT INTO star_log (id, person_id, delta, note, booked_by, created_at) VALUES ('11111111-1111-4111-8111-111111111111', ?, 2, 'Geschenk', ?, '2026-11-15T10:00:00Z')`, tb.miaID, papa["id"]); err != nil {
		t.Fatal(err)
	}
	entry := func() map[string]any {
		return starAccount(t, tb.parent, tb.miaID)["entries"].([]any)[0].(map[string]any)
	}
	if e := entry(); e["booked_by"] != "Papa" || e["booked_by_removed"] != false {
		t.Fatalf("before removal = %v", e)
	}
	if _, err := db.Exec(`DELETE FROM people WHERE id = ?`, papa["id"]); err != nil {
		t.Fatal(err)
	}
	if e := entry(); e["booked_by"] != "Papa" || e["booked_by_removed"] != true {
		t.Fatalf("after removal = %v", e)
	}
	for lang, want := range map[string]string{"en": "Papa (removed)", "de": "Papa (entfernt)"} {
		page := b.do(http.MethodGet, "/tasks", nil, map[string]string{"Accept-Language": lang}).body
		if !strings.Contains(page, want) {
			t.Errorf("%s page lacks %q:\n%s", lang, want, page)
		}
	}
}
