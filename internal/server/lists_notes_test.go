package server_test

import (
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/hejmo-home/hejmo/internal/list"
)

// Ticket 167 (Q42): notes on entries, in the list, the tiles, the display and
// the editor, and "Auch in Apple Erinnerungen" in the state line.

type noteBox struct {
	sharingBox
	shop, todos list.List
}

func newNoteBox(t *testing.T) noteBox {
	t.Helper()
	s := newSharingBox(t)
	nb := noteBox{sharingBox: s}
	for _, l := range s.lists {
		if l.Suggestions {
			nb.shop = l
		} else {
			nb.todos = l
		}
	}
	if nb.shop.ID == "" || nb.todos.ID == "" {
		t.Fatalf("lists %+v", s.lists)
	}
	return nb
}

func (n noteBox) entry(l list.List, text, note string) string {
	n.t.Helper()
	e, err := n.db.AddEntry(n.h.ID, l.ID, text)
	if err != nil {
		n.t.Fatal(err)
	}
	if note != "" {
		if err := n.db.SetEntryNote(n.h.ID, l.ID, e.ID, note); err != nil {
			n.t.Fatal(err)
		}
	}
	return e.ID
}

func TestANoteStandsSmallUnderTheNameInsideTheTickTargetAndStaysWhenTicked(t *testing.T) {
	t.Parallel()
	n := newNoteBox(t)
	id := n.entry(n.shop, "Milch", "laktosefrei,\ndie blaue Packung")
	done := n.entry(n.shop, "Käse", "Gouda")
	_ = n.db.SetEntryDone(n.h.ID, n.shop.ID, done, true)
	page := n.en("/lists/" + n.shop.ID)
	for _, want := range []string{
		`aria-describedby="n-e-` + id + `"`, `<span class="tt"><span class="title">Milch</span><span class="note" id="n-e-` + id + `">laktosefrei,` + "\n" + `die blaue Packung</span></span>`,
		`<span class="tt"><span class="title"><s>Käse</s></span><span class="note" id="n-e-` + done + `">Gouda</span>`,
	} {
		if !strings.Contains(page.body, want) {
			t.Errorf("missing %q\n%s", want, page.body)
		}
	}
	// The note sits inside the tick button.
	if i, j := strings.Index(page.body, `class="big-check"`), strings.Index(page.body, `class="note"`); i < 0 || j < i || j > i+700 {
		t.Error("the note is not in the tick button")
	}
}

func TestTilesShowTheNoteAfterTheQuantity(t *testing.T) {
	t.Parallel()
	n := newNoteBox(t)
	n.entry(n.shop, "Milch", "die blaue Packung")
	_ = n.db.SetPersonListView(n.h.ID, n.mama.ID, list.Tiles)
	page := n.en("/lists/" + n.shop.ID)
	if !strings.Contains(page.body, `<span class="note">die blaue Packung</span>`) || !strings.Contains(page.body, `class="tile-check"`) {
		t.Errorf("\n%s", page.body)
	}
}

func TestTheStateLineSaysAlsoInAppleRemindersWithALinkForParentsAndPlainForChildren(t *testing.T) {
	t.Parallel()
	b := signedInBox(t)
	h, _ := b.db.HomeHousehold()
	ls, _ := b.db.Lists(h.ID)
	if page := getEN(b, "/lists/"+ls[0].ID); strings.Contains(page.body, "Also in Apple Reminders") {
		t.Error("an unshared list says it")
	}
	_ = b.db.SetListsShared(h.ID, map[string]bool{ls[0].ID: true})
	page := getEN(b, "/lists/"+ls[0].ID)
	if !strings.Contains(page.body, `Also in Apple Reminders: <a href="/settings/lists/sharing">Share with other apps</a>.`) {
		t.Errorf("parent\n%s", page.body)
	}
	kid := childSession(t, b)
	page = getEN(kid, "/lists/"+ls[0].ID)
	if !strings.Contains(page.body, "Also in Apple Reminders.") || strings.Contains(page.body, `href="/settings/lists/sharing"`) {
		t.Errorf("child\n%s", page.body)
	}
}

func TestTheQuantityEditorAlsoHoldsTheNoteAndSavingItKeepsTheQuantity(t *testing.T) {
	t.Parallel()
	n := newNoteBox(t)
	id := n.entry(n.shop, "Milch", "alt")
	page := n.en("/lists/" + n.shop.ID + "?qty=" + id)
	for _, want := range []string{"Quantity and note", `<textarea id="qn-` + id + `" name="note" rows="2" maxlength="500"`, ">alt</textarea>", "Shown small under the entry and in Reminders. Leave it empty to remove the note.", `data-format="%d of 500 characters"`, `aria-label="Add a quantity or note: Milch"`} {
		if !strings.Contains(page.body, want) {
			t.Errorf("missing %q\n%s", want, page.body)
		}
	}
	f := url.Values{"qmode": {"none"}, "note": {"  neu\r\nzeile "}}
	r := n.postEN("/lists/"+n.shop.ID+"/entries/"+id+"/quantity", f)
	if r.status != http.StatusSeeOther {
		t.Fatalf("%d\n%s", r.status, r.body)
	}
	es, _ := n.db.Entries(n.h.ID, n.shop.ID)
	if es[0].Note != "neu\nzeile" {
		t.Errorf("note %q", es[0].Note)
	}
	if line := n.en(r.location); !strings.Contains(line.body, "Saved: the note of “Milch”.") {
		t.Errorf("only the note changed:\n%s", line.body)
	}
	// Empty removes it.
	n.postEN("/lists/"+n.shop.ID+"/entries/"+id+"/quantity", url.Values{"qmode": {"none"}, "note": {""}})
	if es, _ = n.db.Entries(n.h.ID, n.shop.ID); es[0].Note != "" {
		t.Errorf("note %q", es[0].Note)
	}
	// Too long: refused at the field, nothing stored.
	r = n.postEN("/lists/"+n.shop.ID+"/entries/"+id+"/quantity", url.Values{"qmode": {"none"}, "note": {strings.Repeat("x", 501)}})
	if r.status != http.StatusUnprocessableEntity || !strings.Contains(r.body, "at most 500 characters") {
		t.Errorf("%d", r.status)
	}
}

func TestAListWithoutSuggestionsHasANoteButtonAndANoteOnlyEditor(t *testing.T) {
	t.Parallel()
	n := newNoteBox(t)
	with := n.entry(n.todos, "Zettel unterschreiben", "bis Donnerstag")
	without := n.entry(n.todos, "Winterreifen wechseln", "")
	page := n.en("/lists/" + n.todos.ID)
	for _, want := range []string{
		`<a class="qty-open" href="?qty=` + with + `#e-` + with + `" aria-label="Change note: Zettel unterschreiben">Note`,
		`<a class="qty-open none" href="?qty=` + without + `#e-` + without + `" aria-label="Write a note: Winterreifen wechseln">Note`,
	} {
		if !strings.Contains(page.body, want) {
			t.Errorf("missing %q\n%s", want, page.body)
		}
	}
	ed := n.en("/lists/" + n.todos.ID + "?qty=" + with)
	if !strings.Contains(ed.body, `action="/lists/`+n.todos.ID+`/entries/`+with+`/note"`) || !strings.Contains(ed.body, `name="note"`) || strings.Contains(ed.body, `name="amount"`) || strings.Contains(ed.body, `name="qmode"`) {
		t.Errorf("a note-only editor\n%s", ed.body)
	}
	_ = n.db.SetListsShared(n.h.ID, map[string]bool{n.todos.ID: true})
	r := n.postEN("/lists/"+n.todos.ID+"/entries/"+without+"/note", url.Values{"note": {"Werkstatt anrufen"}})
	if r.status != http.StatusSeeOther {
		t.Fatalf("%d", r.status)
	}
	if line := n.en(r.location); !strings.Contains(line.body, "Saved: the note of “Winterreifen wechseln”. It is in Reminders at the next sync.") {
		t.Errorf("\n%s", line.body)
	}
	es, _ := n.db.Entries(n.h.ID, n.todos.ID)
	if es[1].Note != "Werkstatt anrufen" {
		t.Errorf("%+v", es)
	}
	if r = n.postEN("/lists/"+n.todos.ID+"/entries/"+without+"/note", url.Values{"note": {strings.Repeat("y", 501)}}); r.status != http.StatusUnprocessableEntity {
		t.Errorf("too long = %d", r.status)
	}
}

func TestChildrenGetNoNoteButton(t *testing.T) {
	t.Parallel()
	b := signedInBox(t)
	h, _ := b.db.HomeHousehold()
	ls, _ := b.db.Lists(h.ID)
	var todos list.List
	for _, l := range ls {
		if !l.Suggestions {
			todos = l
		}
	}
	e, _ := b.db.AddEntry(h.ID, todos.ID, "Winterreifen")
	_ = b.db.SetEntryNote(h.ID, todos.ID, e.ID, "bald")
	kid := childSession(t, b)
	_ = kid
	page := getEN(kid, "/lists/"+todos.ID)
	if strings.Contains(page.body, "qty-open") || !strings.Contains(page.body, `class="note"`) {
		t.Errorf("a child reads the note but cannot change it\n%s", page.body)
	}
	if r := kid.post("/lists/"+todos.ID+"/entries/"+e.ID+"/note", url.Values{"note": {"x"}}); r.status != http.StatusForbidden {
		t.Errorf("child post = %d", r.status)
	}
}

func TestTheAPIWritesAndReadsNotes(t *testing.T) {
	t.Parallel()
	c := signedInAPI(t)
	h, _ := familyOf(t, c)
	ls, _ := c.b.db.Lists(h.ID)
	e, _ := c.b.db.AddEntry(h.ID, ls[1].ID, "Winterreifen") // To-dos: no quantities
	status, body, _ := c.call(http.MethodPatch, "/lists/"+ls[1].ID+"/entries/"+e.ID, map[string]any{"note": " bald "})
	if status != http.StatusOK || body["note"] != "bald" || body["result"] != "set" {
		t.Fatalf("%d %v", status, body)
	}
	_ = c.b.db.SetEntryDone(h.ID, ls[1].ID, e.ID, true)
	status, body, _ = c.call(http.MethodGet, "/lists/"+ls[1].ID, nil)
	entries, _ := body["entries"].([]any)
	if status != http.StatusOK || len(entries) != 1 || entries[0].(map[string]any)["note"] != "bald" {
		t.Fatalf("a ticked entry keeps its note: %d %v", status, body)
	}
	if status, _, _ = c.call(http.MethodPatch, "/lists/"+ls[1].ID+"/entries/"+e.ID, map[string]any{"note": ""}); status != http.StatusOK {
		t.Errorf("removing = %d", status)
	}
	if es, _ := c.b.db.Entries(h.ID, ls[1].ID); es[0].Note != "" {
		t.Error("not removed")
	}
	// A quantity on a list without quantities is still refused.
	if status, _, _ = c.call(http.MethodPatch, "/lists/"+ls[1].ID+"/entries/"+e.ID, map[string]any{"amount": 2}); status != http.StatusUnprocessableEntity {
		t.Errorf("amount on To-dos = %d", status)
	}
}

func TestTheDisplayShowsTheNoteInTheTickTargetAndSaysAlsoInAppleReminders(t *testing.T) {
	t.Parallel()
	f := newFamilyBox(t, "eink_color")
	id, ids := f.onlyList(t, "Shopping", nil, "Milk")
	if err := f.db.SetEntryNote(f.householdID, id, ids["Milk"], "the blue pack"); err != nil {
		t.Fatal(err)
	}
	page := f.listsPage(t, "en", "/display/lists/"+id)
	if strings.Contains(page.body, "Also in Apple Reminders") {
		t.Error("not shared yet")
	}
	_ = f.db.SetListsShared(f.householdID, map[string]bool{id: true})
	if page = f.listsPage(t, "en", "/display/lists/"+id); !strings.Contains(page.body, `<span class="note">the blue pack</span>`) {
		t.Errorf("tiles\n%s", page.body)
	}
	if err := f.db.SetDisplayListView(f.householdID, f.displayID(t), list.Rows); err != nil {
		t.Fatal(err)
	}
	page = f.listsPage(t, "en", "/display/lists/"+id)
	for _, want := range []string{`aria-describedby="n-e-` + ids["Milk"] + `"`, `<span class="tt"><span class="title">Milk</span><span class="note" id="n-e-` + ids["Milk"] + `">the blue pack</span></span>`, "Also in Apple Reminders."} {
		if !strings.Contains(page.body, want) {
			t.Errorf("missing %q\n%s", want, page.body)
		}
	}
	if strings.Contains(page.body, `/settings/lists/sharing`) {
		t.Error("a display has no link into Settings")
	}
}
