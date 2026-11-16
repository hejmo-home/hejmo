package server_test

import (
	"io"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/hejmo-home/hejmo/internal/list"
)

// Ticket 6.2 (docs/design/preview-33.html): lists on the display.

// listsPage fetches a page of the lists view with the clock at 14/11/2026 09:00.
func (f *familyBox) listsPage(t *testing.T, lang, path string) resp {
	t.Helper()
	f.now = f.at(14, 9, 0)
	return f.display.get(path, lang)
}

// homeLists are the lists of the household in the order the box keeps them.
func (f *familyBox) homeLists(t *testing.T) []list.List {
	t.Helper()
	ls, err := f.db.Lists(f.householdID)
	if err != nil {
		t.Fatal(err)
	}
	return ls
}

// onlyList removes the lists a new household starts with and creates one.
func (f *familyBox) onlyList(t *testing.T, name string, ticked []string, texts ...string) (string, map[string]string) {
	t.Helper()
	for _, l := range f.homeLists(t) {
		if err := f.db.DeleteList(f.householdID, l.ID); err != nil {
			t.Fatal(err)
		}
	}
	return listWith(t, f.box, name, ticked, texts...)
}

// postLang posts a form from the display in the given language.
func (s *screen) postLang(path string, form url.Values, lang string) resp {
	s.b.t.Helper()
	req, _ := http.NewRequest(http.MethodPost, s.b.srv.URL+path, strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Origin", s.b.srv.URL)
	req.Header.Set("Accept-Language", lang)
	res, err := s.client.Do(req)
	if err != nil {
		s.b.t.Fatal(err)
	}
	defer res.Body.Close()
	raw, _ := io.ReadAll(res.Body)
	return resp{res.StatusCode, res.Header.Get("Location"), string(raw)}
}

func entryDone(t *testing.T, f *familyBox, listID, entryID string) bool {
	t.Helper()
	es, err := f.db.Entries(f.householdID, listID)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range es {
		if e.ID == entryID {
			return e.Done
		}
	}
	t.Fatalf("entry %s is gone", entryID)
	return false
}

// The way in: one "Lists" button in the header before the clock, hidden when
// the household has no list.
func TestFamilyViewHeaderHasAListsButtonBeforeTheClock(t *testing.T) {
	f := newFamilyBox(t, "eink_color")
	page := f.view(t, "en", f.at(14, 9, 0))
	at := strings.Index(page, `class="chip lists-link" href="/display/lists"`)
	if at < 0 {
		t.Fatalf("no Lists button in the header:\n%s", page)
	}
	if clock := strings.Index(page, "data-clock"); clock < at {
		t.Error("the Lists button does not come before the clock")
	}
	if !strings.Contains(page, `</svg>Lists</a>`) {
		t.Error("the button is not the list icon plus the word Lists")
	}
	if strings.Contains(page[at:at+400], "data-person") || strings.Contains(page[at:at+400], `class="marker"`) {
		t.Error("the button uses a person marker")
	}
	if de := f.view(t, "de", f.at(14, 9, 0)); !strings.Contains(de, `</svg>Listen</a>`) {
		t.Error("German button is not Listen")
	}
	for _, l := range f.homeLists(t) {
		if err := f.db.DeleteList(f.householdID, l.ID); err != nil {
			t.Fatal(err)
		}
	}
	if page := f.view(t, "en", f.at(14, 9, 0)); strings.Contains(page, "lists-link") {
		t.Error("the button shows although the household has no list")
	}
}

// "Lists" opens the first list in the household's order; the display does not
// remember a choice. Without a list there is nothing to open.
func TestListsOpensTheFirstList(t *testing.T) {
	f := newFamilyBox(t, "eink_color")
	first := f.homeLists(t)[0]
	f.now = f.at(14, 9, 0)
	if r := f.display.get("/display/lists", "en"); r.status != http.StatusSeeOther || r.location != "/display/lists/"+first.ID {
		t.Errorf("GET /display/lists = %d %q, want 303 to the first list", r.status, r.location)
	}
	for _, l := range f.homeLists(t) {
		if err := f.db.DeleteList(f.householdID, l.ID); err != nil {
			t.Fatal(err)
		}
	}
	if r := f.display.get("/display/lists", "en"); r.status != http.StatusSeeOther || r.location != "/display" {
		t.Errorf("without a list = %d %q, want 303 /display", r.status, r.location)
	}
}

// Everything on a display is for paired displays; a list that is gone sends the
// display back to the family view and never leaves a bare error page.
func TestListsPagesNeedAPairedDisplayAndAKnownList(t *testing.T) {
	f := newFamilyBox(t, "eink_color")
	id, ids := f.onlyList(t, "Shopping", nil, "Milk")
	loose := newScreen(f.box)
	for _, p := range []string{"/display/lists", "/display/lists/" + id} {
		if r := loose.get(p, "en"); r.status != http.StatusSeeOther || r.location != "/display" {
			t.Errorf("unpaired GET %s = %d %q, want 303 /display", p, r.status, r.location)
		}
	}
	if r := loose.get("/display/lists/"+id+"/view", "en"); r.status != http.StatusForbidden {
		t.Errorf("unpaired view = %d, want 403", r.status)
	}
	for _, p := range []string{"/entries/" + ids["Milk"] + "/done", "/entries", "/completed/delete"} {
		if r := loose.post("/display/lists/"+id+p, url.Values{"done": {"1"}, "text": {"x"}}); r.status != http.StatusSeeOther || r.location != "/display" {
			t.Errorf("unpaired POST %s = %d %q", p, r.status, r.location)
		}
	}
	if es, _ := f.db.Entries(f.householdID, id); len(es) != 1 || es[0].Done {
		t.Errorf("an unpaired display changed the list: %v", es)
	}
	f.now = f.at(14, 9, 0)
	gone := "00000000-0000-4000-8000-000000000000"
	if r := f.display.get("/display/lists/"+gone, "en"); r.status != http.StatusSeeOther || r.location != "/display" {
		t.Errorf("GET a gone list = %d %q, want 303 /display", r.status, r.location)
	}
	for _, p := range []string{"/entries/" + ids["Milk"] + "/done", "/entries", "/completed/delete"} {
		if r := f.display.post("/display/lists/"+gone+p, url.Values{"done": {"1"}, "text": {"x"}}); r.status != http.StatusSeeOther || r.location != "/display" {
			t.Errorf("POST on a gone list %s = %d %q, want 303 /display", p, r.status, r.location)
		}
	}
	if r := f.display.get("/display/lists/"+gone+"/view", "en"); r.status != http.StatusNotFound {
		t.Errorf("view of a gone list = %d, want 404 (the display then returns to the family view)", r.status)
	}
}

var (
	railRe = regexp.MustCompile(`(?s)<ul class="lv-rail">.*?</ul>`)
	headRe = regexp.MustCompile(`(?s)<div class="lv-head">.*?<div class="lv-entries"`)
)

// The lists view: a rail of every list (symbol, full name, state in words) and
// the chosen list, whose head stays put above the entries.
func TestListsViewShowsTheRailAndTheChosenList(t *testing.T) {
	f := newFamilyBox(t, "eink_color")
	shop, _ := f.onlyList(t, "Shopping", []string{"Oat milk", "Bread"}, "Bananas", "Coffee", "Oat milk", "Bread")
	todo, _ := listWith(t, f.box, "To-dos", nil, "Call plumber")
	gift, _ := listWith(t, f.box, "Gift ideas", nil)
	body := f.listsPage(t, "en", "/display/lists/"+shop).body

	rail := railRe.FindString(body)
	for _, want := range []string{
		`<a class="lv-tab" data-person="3" href="/display/lists/` + shop + `" aria-current="page"><span class="marker" data-shape="square-filled" aria-hidden="true"></span><span class="t"><b>Shopping</b><span class="state">2 open, 2 ticked off</span></span></a>`,
		`href="/display/lists/` + todo + `"><span class="marker" data-shape="square-filled" aria-hidden="true"></span><span class="t"><b>To-dos</b><span class="state">1 open</span>`,
		`href="/display/lists/` + gift + `"`, `<span class="state">No entries yet</span>`,
	} {
		if !strings.Contains(rail, want) {
			t.Errorf("rail lacks %q\n%s", want, rail)
		}
	}
	if strings.Count(rail, `aria-current="page"`) != 1 {
		t.Errorf("rail: want exactly one current list:\n%s", rail)
	}
	if !strings.Contains(body, `<nav aria-label="All lists">`) || !strings.Contains(body, `<a class="pv-back" href="/display">Back to the family view</a>`) {
		t.Error("no nav label or the way back")
	}
	head := headRe.FindString(body)
	for _, want := range []string{`<h2 class="list-title">`, `<span>Shopping</span>`, `<p class="list-meta">Sorted by hand. 2 open, 2 ticked off.</p>`,
		`<form class="add-entry" method="post" action="/display/lists/` + shop + `/entries">`, `>New entry<input name="text"`, `data-keep`, `maxlength="200"`, `autocomplete="off"`, `placeholder="e.g. Apples"`, `<button class="button" type="submit">Add</button>`} {
		if !strings.Contains(head, want) {
			t.Errorf("list head lacks %q\n%s", want, head)
		}
	}
	// Entries: big tick rows, open first, ticked off in a group below, then the delete action.
	before(t, body, `class="lv-entries"`, "Bananas", "Coffee", `<h2>Ticked off (2)</h2>`, "Oat milk", "Bread", `<details class="danger"><summary>Delete ticked-off entries (2)</summary>`)
	for _, want := range []string{`<button class="big-check" type="submit" aria-pressed="false" aria-label="Tick off: Bananas">`, `aria-pressed="true" aria-label="Mark as open: Oat milk"`,
		`action="/display/lists/` + shop + `/entries/`} {
		if !strings.Contains(body, want) {
			t.Errorf("entries lack %q", want)
		}
	}
	for _, not := range []string{"Hide", "Remove", "List settings", "Move up", "Delete list", "Sort or remove"} {
		if strings.Contains(body, not) {
			t.Errorf("the display offers %q (web only)", not)
		}
	}
}

// The meta line says how the list is sorted, and the order follows it.
func TestListsViewSaysHowTheListIsSorted(t *testing.T) {
	f := newFamilyBox(t, "eink_color")
	id, _ := f.onlyList(t, "Shopping", nil, "bbb", "aaa")
	l, _ := f.db.List(f.householdID, id)
	l.Sort = list.Alphabetical
	if _, err := f.db.UpdateList(f.householdID, l); err != nil {
		t.Fatal(err)
	}
	body := f.listsPage(t, "en", "/display/lists/"+id).body
	if !strings.Contains(body, `Sorted A to Z. 2 open.`) {
		t.Errorf("meta line:\n%s", headRe.FindString(body))
	}
	before(t, body, `class="lv-entries"`, ">aaa<", ">bbb<")
}

// 6.2 criterion 1: given a list opened on the display, when an entry is ticked
// off, then it is ticked off in the web interface.
func TestTickingOnTheDisplayTicksInTheWebInterface(t *testing.T) {
	f := newFamilyBox(t, "eink_color")
	id, ids := f.onlyList(t, "Shopping", nil, "Bananas", "Coffee")
	f.now = f.at(14, 9, 0)
	back := "/display/lists/" + id
	if r := f.display.post(back+"/entries/"+ids["Bananas"]+"/done", url.Values{"done": {"1"}}); r.status != http.StatusSeeOther || r.location != back {
		t.Fatalf("tick = %d %q, want 303 %s", r.status, r.location, back)
	}
	if !entryDone(t, f, id, ids["Bananas"]) || entryDone(t, f, id, ids["Coffee"]) {
		t.Fatal("the tick did not reach the list")
	}
	if page := getEN(f.box, "/lists/"+id).body; !strings.Contains(page, `<li data-done="true">`) || !strings.Contains(page, "Ticked off (1)") {
		t.Errorf("the web interface does not show the entry ticked off:\n%s", page)
	}
	if body := f.display.get(back, "en").body; !strings.Contains(body, `aria-label="Mark as open: Bananas"`) || !strings.Contains(body, "1 open, 1 ticked off") {
		t.Error("the display does not show its own tick")
	}
	if r := f.display.post(back+"/entries/"+ids["Bananas"]+"/done", url.Values{"done": {"0"}}); r.status != http.StatusSeeOther {
		t.Fatalf("untick = %d", r.status)
	}
	if entryDone(t, f, id, ids["Bananas"]) {
		t.Error("untick did not reopen the entry")
	}
	// A tick the box cannot make shows the list again, no error page.
	if r := f.display.post(back+"/entries/00000000-0000-4000-8000-000000000000/done", url.Values{"done": {"1"}}); r.status != http.StatusSeeOther || r.location != back {
		t.Errorf("unknown entry = %d %q, want 303 %s", r.status, r.location, back)
	}
	if r := f.display.post(back+"/entries/"+ids["Coffee"]+"/done", url.Values{}); r.status != http.StatusSeeOther || r.location != back {
		t.Errorf("missing done = %d %q, want 303 %s", r.status, r.location, back)
	}
	if entryDone(t, f, id, ids["Coffee"]) {
		t.Error("a tick without done changed the entry")
	}
}

// 6.2 x 5.5: a change from the web interface reaches a display with the lists
// view open within 10 seconds, as a new version of that view; the clock does
// not move it. A deleted list sends the display back.
func TestListsViewReceivesLiveUpdates(t *testing.T) {
	l := newLiveBox(t, 10*time.Millisecond)
	id, ids := l.onlyList(t, "Shopping", nil, "Bananas", "Coffee")
	_, _, page := l.get("/display/lists/" + id)
	pageVersion, events, view := versionRe.FindStringSubmatch(page), eventsURLRe.FindStringSubmatch(page), viewURLRe.FindStringSubmatch(page)
	if pageVersion == nil || events == nil || view == nil {
		t.Fatalf("lists page lacks data-version, data-events-url or data-view-url:\n%s", page)
	}
	s := l.openPath(t, strings.ReplaceAll(events[1], "&amp;", "&"))
	if v := s.first(t); v != pageVersion[1] {
		t.Fatalf("announced %q on connect, page shows %q", v, pageVersion[1])
	}
	l.clock.set(l.at(14, 8, 1)) // a new minute alone
	if v, ok := s.next(t, 400*time.Millisecond); ok {
		t.Fatalf("announced %q without a change to the list", v)
	}
	start := time.Now()
	if r := l.post("/lists/"+id+"/entries/"+ids["Coffee"]+"/done", url.Values{"done": {"1"}}); r.status != http.StatusSeeOther {
		t.Fatalf("web tick = %d", r.status)
	}
	v, ok := s.next(t, 10*time.Second)
	if !ok || v == pageVersion[1] || time.Since(start) > 10*time.Second {
		t.Fatalf("no new version within 10 seconds (got %q, ok=%v)", v, ok)
	}
	code, hdr, body := l.get(view[1])
	if code != 200 || !strings.Contains(body, `aria-label="Mark as open: Coffee"`) || strings.Contains(body, "<html") || !strings.Contains(body, "08:01") {
		t.Fatalf("GET %s = %d, want the list markup alone with the tick and the clock:\n%s", view[1], code, body)
	}
	if hdr.Get("X-Hejmo-View") != v {
		t.Errorf("view version %q, announced %q", hdr.Get("X-Hejmo-View"), v)
	}
	// An entry typed on a display shows on the others the same way.
	if err := l.display.b.db.SetEntryDone(l.householdID, id, ids["Coffee"], false); err != nil {
		t.Fatal(err)
	}
	if v2, ok := s.next(t, 2*time.Second); !ok || v2 == v {
		t.Fatalf("no new version after the entry was reopened (got %q, ok=%v)", v2, ok)
	}
	if err := l.db.DeleteList(l.householdID, id); err != nil {
		t.Fatal(err)
	}
	if v, ok := s.next(t, 2*time.Second); !ok || v != "gone" {
		t.Errorf("a deleted list announced %q (ok=%v), want gone", v, ok)
	}
}

// 6.2 criterion 2: given a new entry typed on the display, then it appears in
// the list. After adding: a success line naming the entry, the new row framed
// and the field empty again.
func TestAddingOnTheDisplayShowsTheEntryAndSaysSo(t *testing.T) {
	f := newFamilyBox(t, "eink_color")
	id, _ := f.onlyList(t, "Shopping", nil, "Bananas")
	f.now = f.at(14, 9, 0)
	r := f.display.post("/display/lists/"+id+"/entries", url.Values{"text": {"  Pasta "}})
	if r.status != http.StatusSeeOther || !strings.HasPrefix(r.location, "/display/lists/"+id+"?added=") {
		t.Fatalf("add = %d %q, want 303 to the list with the new entry named", r.status, r.location)
	}
	es, _ := f.db.Entries(f.householdID, id)
	if len(es) != 2 || es[1].Text != "Pasta" {
		t.Fatalf("entries = %v", es)
	}
	if !strings.HasSuffix(r.location, "?added="+es[1].ID) {
		t.Errorf("location %q does not name the new entry %s", r.location, es[1].ID)
	}
	body := f.display.get(r.location, "en").body
	if !strings.Contains(body, `<p class="msg ok" role="status">`) || !strings.Contains(body, `&#34;Pasta&#34; added.`) {
		t.Errorf("no success line naming the entry:\n%s", headRe.FindString(body))
	}
	if !strings.Contains(body, `<li data-done="false" class="added">`) || strings.Count(body, `class="added"`) != 1 {
		t.Error("the new entry is not framed")
	}
	if !strings.Contains(body, `value=""`) {
		t.Error("the field is not empty after adding")
	}
	if plain := f.display.get("/display/lists/"+id, "en").body; strings.Contains(plain, "added.") || strings.Contains(plain, `class="added"`) {
		t.Error("the success line and the frame outlive the page that added")
	}
	if web := getEN(f.box, "/lists/"+id).body; !strings.Contains(web, "Pasta") {
		t.Error("the entry does not show in the web interface")
	}
	if odd := f.display.get("/display/lists/"+id+"?added=nonsense", "en").body; strings.Contains(odd, "added.") || strings.Contains(odd, `class="added"`) {
		t.Error("an unknown ?added shows a success line")
	}
	// The partial view takes ?added too, so a script can swap it in.
	if v := f.display.get("/display/lists/"+id+"/view?added="+es[1].ID, "en"); v.status != 200 || !strings.Contains(v.body, `class="added"`) || strings.Contains(v.body, "<html") {
		t.Errorf("view with ?added = %d, want the markup with the frame", v.status)
	}
}

// An A-to-Z list puts the new entry in the middle: its row is the framed one.
func TestAddedEntryIsFramedInItsAlphabeticalPlace(t *testing.T) {
	f := newFamilyBox(t, "eink_color")
	id, _ := f.onlyList(t, "Shopping", nil, "Apples", "Zucchini")
	l, _ := f.db.List(f.householdID, id)
	l.Sort = list.Alphabetical
	if _, err := f.db.UpdateList(f.householdID, l); err != nil {
		t.Fatal(err)
	}
	f.now = f.at(14, 9, 0)
	r := f.display.post("/display/lists/"+id+"/entries", url.Values{"text": {"Sunscreen"}})
	body := f.display.get(r.location, "en").body
	before(t, body, ">Apples<", `class="added"`, ">Sunscreen<", ">Zucchini<")
}

// Errors at the place where they happen: the field error under the add row,
// the typed text kept.
func TestAddingWithoutTextOrTooLongShowsTheErrorAtTheField(t *testing.T) {
	f := newFamilyBox(t, "eink_color")
	id, _ := f.onlyList(t, "Shopping", nil, "Bananas")
	f.now = f.at(14, 9, 0)
	r := f.display.postLang("/display/lists/"+id+"/entries", url.Values{"text": {"   "}}, "en")
	if r.status != http.StatusUnprocessableEntity {
		t.Fatalf("empty add = %d, want 422", r.status)
	}
	head := headRe.FindString(r.body)
	for _, want := range []string{`<div class="f invalid">`, `aria-invalid="true"`, `aria-describedby="err-text"`, `id="err-text" role="alert">`, `Type the entry first, then tap Add.`} {
		if !strings.Contains(head, want) {
			t.Errorf("empty add: head lacks %q\n%s", want, head)
		}
	}
	if !strings.Contains(r.body, `data-return-seconds="30"`) || !strings.Contains(r.body, "Bananas") {
		t.Error("the error page is not the lists view")
	}
	long := strings.Repeat("a", 201)
	r = f.display.postLang("/display/lists/"+id+"/entries", url.Values{"text": {long}}, "en")
	if r.status != http.StatusUnprocessableEntity || !strings.Contains(r.body, "Please enter the entry, up to 200 characters.") || !strings.Contains(r.body, `value="`+long+`"`) {
		t.Errorf("too long: %d, text kept or message missing", r.status)
	}
	if es, _ := f.db.Entries(f.householdID, id); len(es) != 1 {
		t.Errorf("a refused add created an entry: %v", es)
	}
}

// Delete the ticked-off entries from the display: no parent PIN (ADR 0015); the
// number is named in the summary, the explanation and the button.
func TestDeletingTickedOffEntriesOnTheDisplay(t *testing.T) {
	f := newFamilyBox(t, "eink_color")
	id, _ := f.onlyList(t, "Shopping", []string{"Oat milk", "Bread"}, "Bananas", "Oat milk", "Bread")
	body := f.listsPage(t, "en", "/display/lists/"+id).body
	for _, want := range []string{`<summary>Delete ticked-off entries (2)</summary>`,
		`<p>The 2 ticked-off entries of this list are deleted for good, on every display and phone. Open entries stay.</p>`,
		`<form method="post" action="/display/lists/` + id + `/completed/delete">`, `<button class="button danger" type="submit">Yes, delete 2 entries</button>`} {
		if !strings.Contains(body, want) {
			t.Errorf("lacks %q", want)
		}
	}
	if r := f.display.post("/display/lists/"+id+"/completed/delete", url.Values{}); r.status != http.StatusSeeOther || r.location != "/display/lists/"+id {
		t.Fatalf("delete = %d %q", r.status, r.location)
	}
	es, _ := f.db.Entries(f.householdID, id)
	if len(es) != 1 || es[0].Text != "Bananas" {
		t.Errorf("entries after deleting the ticked-off ones = %v", es)
	}
	if again := f.display.get("/display/lists/"+id, "en").body; strings.Contains(again, "Ticked off (") || strings.Contains(again, "<details") {
		t.Error("nothing is ticked off any more, yet the group or the action shows")
	}
	id1, _ := listWith(t, f.box, "One", []string{"a"}, "a", "b")
	one := f.display.get("/display/lists/"+id1, "en").body
	for _, want := range []string{"Delete ticked-off entry (1)", "Yes, delete 1 entry", "The ticked-off entry of this list is deleted for good, on every display and phone. Open entries stay."} {
		if !strings.Contains(one, want) {
			t.Errorf("singular lacks %q", want)
		}
	}
}

// Empty list: one sentence on what to do. All ticked off: a calm line with a
// tick, then the ticked-off group and the delete action.
func TestEmptyAndFinishedListsHaveTheirOwnLine(t *testing.T) {
	f := newFamilyBox(t, "eink_color")
	empty, _ := f.onlyList(t, "Gift ideas", nil)
	body := f.listsPage(t, "en", "/display/lists/"+empty).body
	if !strings.Contains(body, `<div class="empty"><p>Nothing on this list yet. Type the first entry above and tap Add.</p></div>`) || !strings.Contains(body, `Sorted by hand. No entries yet.`) {
		t.Errorf("empty list:\n%s", body)
	}
	done, _ := listWith(t, f.box, "Party", []string{"Table", "Bench"}, "Table", "Bench")
	body = f.display.get("/display/lists/"+done, "en").body
	before(t, body, `class="lv-entries"`, `<div class="empty all-done">`, "Everything on this list is ticked off.", `<h2>Ticked off (2)</h2>`, "Table", "Delete ticked-off entries (2)")
	if !strings.Contains(body, "Sorted by hand. All 2 ticked off.") {
		t.Error("state of a finished list")
	}
}

// 6.2 criterion 3: the list view returns to the family view after 30 seconds
// without interaction (display.js counts; typing counts as interaction).
func TestListsViewReturnsToTheFamilyViewAfter30Seconds(t *testing.T) {
	f := newFamilyBox(t, "eink_color")
	id, _ := f.onlyList(t, "Shopping", nil, "Bananas")
	body := f.listsPage(t, "en", "/display/lists/"+id).body
	for _, want := range []string{`data-return-seconds="30"`, `data-return-url="/display"`, `data-view-url="/display/lists/` + id + `/view"`,
		`data-events-url="/display/events?list=` + id + `"`, `data-tick-error="Not saved: the box cannot be reached. Tap again."`,
		`data-add-error="Not added: the box cannot be reached. Your text is kept; tap Add again."`, `src="/static/display.js"`} {
		if !strings.Contains(body, want) {
			t.Errorf("lists page lacks %q", want)
		}
	}
	js, err := os.ReadFile("../../web/static/display.js")
	if err != nil {
		t.Fatal(err)
	}
	for _, ev := range []string{"keydown", "pointerdown", "touchstart", "scroll"} {
		if !strings.Contains(string(js), `"`+ev+`"`) {
			t.Errorf("display.js does not restart the timer on %s", ev)
		}
	}
}

// The page is German on a German box.
func TestListsViewIsGerman(t *testing.T) {
	f := newFamilyBox(t, "eink_color")
	id, _ := f.onlyList(t, "Einkauf", []string{"Brot"}, "Milch", "Brot")
	body := f.listsPage(t, "de", "/display/lists/"+id).body
	for _, want := range []string{`<html lang="de"`, `<nav aria-label="Alle Listen">`, "Zurück zur Familienansicht", "Von Hand sortiert. 1 offen, 1 abgehakt.", ">Neuer Eintrag<", "Hinzufügen", "Abgehakt (1)", "Abgehakten Eintrag löschen (1)",
		"Ja, 1 Eintrag löschen", "Der abgehakte Eintrag dieser Liste wird endgültig gelöscht, auf allen Displays und Handys. Offene Einträge bleiben."} {
		if !strings.Contains(body, want) {
			t.Errorf("German lists view lacks %q", want)
		}
	}
	r := f.display.postLang("/display/lists/"+id+"/entries", url.Values{"text": {""}}, "de")
	if !strings.Contains(r.body, "Gib zuerst den Eintrag ein und tippe dann auf Hinzufügen.") {
		t.Errorf("German empty error missing:\n%s", headRe.FindString(r.body))
	}
}

// The styles are the ones of the approved preview, word for word.
func TestListsDisplayStylesAreThePreviews(t *testing.T) {
	preview, err := os.ReadFile("../../docs/design/preview-33.html")
	if err != nil {
		t.Fatal(err)
	}
	css, err := os.ReadFile("../../web/static/hejmo.css")
	if err != nil {
		t.Fatal(err)
	}
	all := string(preview)
	start := strings.Index(all, "/* ---- Lists on the display (ticket 6.2")
	if start < 0 {
		t.Fatal("preview has no ticket 6.2 styles")
	}
	end := strings.Index(all[start:], "</template>")
	if end < 0 {
		t.Fatal("preview styles do not end")
	}
	block := strings.ReplaceAll(all[start:start+end], "&gt;", ">")
	if !strings.Contains(string(css), strings.TrimSpace(block)) {
		t.Error("web/static/hejmo.css lacks the preview's ticket 6.2 styles verbatim")
	}
}

// ADR 0016 names the way in: a Lists button in the header, no third footer tile.
func TestADR0016NamesTheListsButton(t *testing.T) {
	b, err := os.ReadFile("../../docs/adr/0016-family-view-header-and-footer.md")
	if err != nil {
		t.Fatal(err)
	}
	if s := string(b); !strings.Contains(s, "Lists button") || !strings.Contains(s, "no third footer tile") {
		t.Error("ADR 0016 does not record the Lists button in the header")
	}
}

// The display's script adds in the background (partial=1): the box answers with
// the new view markup alone (200), or with the view and the field error (422).
func TestAddingInTheBackgroundAnswersWithTheViewAlone(t *testing.T) {
	f := newFamilyBox(t, "eink_color")
	id, _ := f.onlyList(t, "Shopping", nil, "Bananas")
	f.now = f.at(14, 9, 0)
	r := f.display.postLang("/display/lists/"+id+"/entries", url.Values{"text": {"Pasta"}, "partial": {"1"}}, "en")
	es, _ := f.db.Entries(f.householdID, id)
	if r.status != http.StatusOK || strings.Contains(r.body, "<html") || !strings.Contains(r.body, `class="added"`) || !strings.Contains(r.body, `&#34;Pasta&#34; added.`) || len(es) != 2 {
		t.Errorf("partial add = %d, entries %v:\n%s", r.status, es, r.body)
	}
	bad := f.display.postLang("/display/lists/"+id+"/entries", url.Values{"text": {" "}, "partial": {"1"}}, "en")
	if bad.status != http.StatusUnprocessableEntity || strings.Contains(bad.body, "<html") || !strings.Contains(bad.body, "Type the entry first, then tap Add.") {
		t.Errorf("partial add without text = %d:\n%s", bad.status, bad.body)
	}
}
