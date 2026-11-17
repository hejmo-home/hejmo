package server_test

import (
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/hejmo-home/hejmo/internal/list"
	"github.com/hejmo-home/hejmo/internal/store"
)

// Ticket 166 (ADR 0019, docs/design/preview-166.html): tiles on the display's
// lists view (#33). A display is not a person: it keeps its own view.

func TestADisplayShowsListsAsTilesUntilSomeoneSwitchesItWithoutAPIN(t *testing.T) {
	t.Parallel()
	f := newFamilyBox(t, "eink_color")
	id, ids := f.onlyList(t, "Shopping", []string{"Cheese"}, "Milk", "Party hats", "Cheese")
	page := f.listsPage(t, "en", "/display/lists/"+id).body
	if !strings.Contains(page, `<ul class="tiles">`) || strings.Contains(page, `class="big-check"`) {
		t.Fatalf("a display does not show tiles by default:\n%s", page)
	}
	head := page[strings.Index(page, `class="lv-head"`):strings.Index(page, `class="lv-entries"`)]
	for _, want := range []string{`<div class="view-row">`, `action="/display/list-view"`, `value="tiles" aria-pressed="true"`, `value="list" aria-pressed="false"`, `name="list" value="` + id + `"`} {
		if !strings.Contains(head, want) {
			t.Errorf("the head lacks %q:\n%s", want, head)
		}
	}
	if strings.Index(head, "view-row") > strings.Index(head, "add-entry") {
		t.Errorf("the switch does not stand with the state line, above the add row")
	}
	milk := tileOf(t, page, "Milk")
	if !strings.Contains(milk, `action="/display/lists/`+id+`/entries/`+ids["Milk"]+`/done"`) || !strings.Contains(milk, "🥛") {
		t.Errorf("Milk tile:\n%s", milk)
	}
	if !strings.Contains(tileOf(t, page, "Party hats"), `pic letter`) {
		t.Errorf("free text has no letter")
	}
	if !strings.Contains(page, `data-tick-error="Not saved. Tap again."`) {
		t.Errorf("a tile that cannot be saved has no error text for the script:\n%s", page)
	}

	// Anyone at the display may switch it: no PIN, no session.
	r := f.display.postLang("/display/list-view", url.Values{"view": {"list"}, "list": {id}}, "en")
	if r.status != http.StatusSeeOther || r.location != "/display/lists/"+id+"?view=saved" {
		t.Fatalf("switch = %d %q", r.status, r.location)
	}
	page = f.listsPage(t, "en", r.location).body
	if !strings.Contains(page, `class="big-check"`) || strings.Contains(page, `<ul class="tiles">`) || !strings.Contains(page, `value="list" aria-pressed="true"`) {
		t.Errorf("the display did not switch to the list:\n%s", page)
	}
	if !strings.Contains(page, "data-success") || !strings.Contains(page, "Saved: This display (“Hallway”) now shows lists as a list.") {
		t.Errorf("the line does not name the display:\n%s", page)
	}
	if v, _ := f.db.DisplayListView(f.householdID, f.displayID(t)); v != list.Rows {
		t.Errorf("stored %q", v)
	}
	// The line is gone without ?view=saved, and the partial view carries the new view.
	if page := f.listsPage(t, "en", "/display/lists/"+id).body; strings.Contains(page, "now shows lists") {
		t.Errorf("the line stays")
	}
	if v := f.display.get("/display/lists/"+id+"/view", "en"); !strings.Contains(v.body, `class="big-check"`) {
		t.Errorf("the partial view ignores the choice")
	}
	// German, and back to tiles.
	r = f.display.postLang("/display/list-view", url.Values{"view": {"tiles"}, "list": {id}}, "de")
	if de := f.listsPage(t, "de", r.location).body; !strings.Contains(de, "Gemerkt: Dieses Display („Hallway“) zeigt Listen ab jetzt als Kacheln.") {
		t.Errorf("German line:\n%s", de)
	}
}

func TestDisplayViewErrorsAndUnpairedScreens(t *testing.T) {
	t.Parallel()
	f := newFamilyBox(t, "eink_color")
	id, _ := f.onlyList(t, "Shopping", nil, "Milk")
	r := f.display.postLang("/display/list-view", url.Values{"view": {"grid"}, "list": {id}}, "en")
	if r.status != http.StatusUnprocessableEntity || !strings.Contains(r.body, `id="view-err"`) || !strings.Contains(r.body, `aria-invalid="true"`) || !strings.Contains(r.body, "Not saved, the view stays") {
		t.Errorf("bad view = %d\n%s", r.status, r.body)
	}
	if v, _ := f.db.DisplayListView(f.householdID, f.displayID(t)); v != list.Tiles {
		t.Errorf("stored %q", v)
	}
	loose := newScreen(f.box)
	if r := loose.post("/display/list-view", url.Values{"view": {"list"}, "list": {id}}); r.status != http.StatusSeeOther || r.location != "/display" {
		t.Errorf("unpaired = %d %q", r.status, r.location)
	}
	// A list that is gone sends the display back to the family view.
	if r := f.display.post("/display/list-view", url.Values{"view": {"list"}, "list": {"gone"}}); r.status != http.StatusSeeOther || r.location != "/display" {
		t.Errorf("gone list = %d %q", r.status, r.location)
	}
}

func TestDisplayTilesCarryWarningsPicturesAndTheFreeTextNote(t *testing.T) {
	t.Parallel()
	f := newFamilyBox(t, "eink_color")
	if err := f.db.SetPersonFood(f.householdID, f.people["Mia"].ID, store.Food{Diet: "omnivore", Allergens: []string{"milk"}, Other: "Kiwi"}); err != nil {
		t.Fatal(err)
	}
	art, err := f.db.CreateArticle(f.householdID, f.people["Mama"].ID, store.OwnArticle{Name: "Oat drink barista", Dept: "dairy", Kind: "vegan", Unit1: "l"}, &store.ArticlePicture{JPEG: testJPEG(t, 400, 400), Source: "upload"})
	if err != nil {
		t.Fatal(err)
	}
	id, _ := f.onlyList(t, "Shopping", nil, "Milk", "Oat drink barista")
	page := f.listsPage(t, "en", "/display/lists/"+id).body
	milk := tileOf(t, page, "Milk")
	if !strings.Contains(milk, `<span class="tile-warn">`) || !strings.Contains(milk, "Mia:</span>") {
		t.Errorf("Milk tile lacks the warning:\n%s", milk)
	}
	oat := tileOf(t, page, "Oat drink barista")
	if !strings.Contains(oat, `<img src="/display/articles/`+art.ID+`/picture?v=`) {
		t.Errorf("the display's picture address:\n%s", oat)
	}
	if !strings.Contains(page, `<section class="unchecked-box"`) || !strings.Contains(page, "Mia:</span> <span>Kiwi</span>") {
		t.Errorf("no free-text note on the display:\n%s", page)
	}
	// The display fetches the picture with its own token, a signed-out browser cannot.
	if r := f.display.get("/display/articles/"+art.ID+"/picture", "en"); r.status != http.StatusOK || !strings.HasPrefix(r.body, "\xff\xd8") {
		t.Errorf("display picture = %d", r.status)
	}
	if r := newScreen(f.box).get("/display/articles/"+art.ID+"/picture", "en"); r.status == http.StatusOK {
		t.Errorf("an unpaired screen gets the picture")
	}
	if r := f.display.get("/display/articles/nope/picture", "en"); r.status != http.StatusNotFound {
		t.Errorf("unknown picture = %d", r.status)
	}
}

func TestTheEntryJustAddedKeepsItsFrameInTiles(t *testing.T) {
	t.Parallel()
	f := newFamilyBox(t, "eink_color")
	id, _ := f.onlyList(t, "Shopping", nil, "Milk")
	r := f.display.post("/display/lists/"+id+"/entries", url.Values{"text": {"Pasta"}})
	if r.status != http.StatusSeeOther {
		t.Fatalf("add = %d", r.status)
	}
	page := f.listsPage(t, "en", r.location).body
	if !strings.Contains(page, `<li class="added">`) || !strings.Contains(tileOf(t, page, "Pasta"), `tile-check`) {
		t.Errorf("the new tile has no frame:\n%s", page)
	}
}
