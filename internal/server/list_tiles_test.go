package server_test

import (
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"testing"

	"github.com/hejmo-home/hejmo/internal/list"
	"github.com/hejmo-home/hejmo/internal/store"
)

// Ticket 166 (ADR 0019, docs/design/preview-166.html): the tile view of lists.

// tileList makes a list with the entries and returns its id and the ids by text.
func tileList(t *testing.T, b *box, ticked []string, texts ...string) (string, map[string]string) {
	t.Helper()
	return listWith(t, b, "Groceries", ticked, texts...)
}

func personID(t *testing.T, b *box, name string) string { return personNamed(t, b, name).ID }

func setFood(t *testing.T, b *box, who string, f store.Food) {
	t.Helper()
	h, _ := b.db.HomeHousehold()
	if err := b.db.SetPersonFood(h.ID, personID(t, b, who), f); err != nil {
		t.Fatal(err)
	}
}

func TestAdultsSeeTheListAndChildrenSeeTilesUntilTheyChoose(t *testing.T) {
	cp := newChildPhone(t)
	b := cp.parent.b
	id, _ := tileList(t, b, nil, "Milk", "Bananas")

	adult := getEN(b, "/lists/"+id)
	if strings.Contains(adult.body, `class="tiles"`) || !strings.Contains(adult.body, `class="entries"`) {
		t.Errorf("an adult sees tiles by default:\n%s", adult.body)
	}
	if !strings.Contains(adult.body, `class="view-switch"`) || !strings.Contains(adult.body, `value="list" aria-pressed="true"`) || !strings.Contains(adult.body, `value="tiles" aria-pressed="false"`) {
		t.Errorf("the switch does not show the list as chosen:\n%s", adult.body)
	}

	child := cp.screen.get("/lists/"+id, "en")
	if !strings.Contains(child.body, `<ul class="tiles"`) || strings.Contains(child.body, `class="entries"`) {
		t.Errorf("a child does not see tiles by default:\n%s", child.body)
	}
	if !strings.Contains(child.body, `value="tiles" aria-pressed="true"`) || !strings.Contains(child.body, `value="list" aria-pressed="false"`) {
		t.Errorf("the switch does not show the tiles as chosen")
	}
	// The labels are words next to the icons, and the group is named.
	for _, want := range []string{`aria-label="View"`, "View:", "Tiles", "List"} {
		if !strings.Contains(child.body, want) {
			t.Errorf("switch lacks %q", want)
		}
	}
	de := cp.screen.get("/lists/"+id, "de")
	for _, want := range []string{`aria-label="Ansicht"`, "Ansicht:", "Kacheln", "Liste"} {
		if !strings.Contains(de.body, want) {
			t.Errorf("German switch lacks %q", want)
		}
	}
}

func TestChoosingTilesIsRememberedForEveryListAndOnlyForThePerson(t *testing.T) {
	cp := newChildPhone(t)
	b := cp.parent.b
	id, _ := tileList(t, b, nil, "Milk")
	other, _ := listWith(t, b, "Other", nil, "Eggs")

	r := b.do(http.MethodPost, "/me/list-view", url.Values{"view": {"tiles"}, "list": {id}}, map[string]string{"Accept-Language": "en"})
	if r.status != http.StatusSeeOther || r.location != "/lists/"+id {
		t.Fatalf("switch = %d %q", r.status, r.location)
	}
	page := getEN(b, "/lists/"+id)
	if !strings.Contains(page.body, "data-success") || !strings.Contains(page.body, "From now on you see lists as tiles, on every device. Nothing changes for the others.") {
		t.Errorf("no success line that says what is remembered and for whom:\n%s", page.body)
	}
	if !strings.Contains(page.body, `<ul class="tiles"`) {
		t.Errorf("tiles not shown after choosing them")
	}
	// All lists, not this one only.
	if p := getEN(b, "/lists/"+other); !strings.Contains(p.body, `<ul class="tiles"`) || strings.Contains(p.body, "data-success") {
		t.Errorf("the choice does not hold for the other list or the line stays:\n%s", p.body)
	}
	// Not for the child, whose default stays.
	h, _ := b.db.HomeHousehold()
	if v, _ := b.db.PersonListView(h.ID, personID(t, b, "Mia")); v != "" {
		t.Errorf("Mia's choice = %q, want none", v)
	}
	// The child chooses the list for herself.
	r = cp.screen.post("/me/list-view", url.Values{"view": {"list"}, "list": {id}})
	if r.status != http.StatusSeeOther {
		t.Fatalf("child switch = %d", r.status)
	}
	if p := cp.screen.get("/lists/"+id, "en"); !strings.Contains(p.body, `class="entries"`) || strings.Contains(p.body, `<ul class="tiles"`) || !strings.Contains(p.body, "From now on you see lists as a list, on every device.") {
		t.Errorf("child's list view:\n%s", p.body)
	}
	if v, _ := b.db.PersonListView(h.ID, personID(t, b, "Papa")); v != "" {
		t.Errorf("Papa's choice = %q, want none", v)
	}
	// German line.
	r = b.do(http.MethodPost, "/me/list-view", url.Values{"view": {"tiles"}, "list": {id}}, map[string]string{"Accept-Language": "de"})
	de := b.do(http.MethodGet, r.location, nil, map[string]string{"Accept-Language": "de"})
	if !strings.Contains(de.body, "Gemerkt: Du siehst Listen ab jetzt als Kacheln, auf jedem Gerät. Für die anderen ändert sich nichts.") {
		t.Errorf("German success line:\n%s", de.body)
	}
}

func TestAViewThatCannotBeSavedShowsTheErrorUnderTheSwitchAndKeepsTheOldView(t *testing.T) {
	b := signedInBox(t)
	id, _ := tileList(t, b, nil, "Milk")
	r := b.do(http.MethodPost, "/me/list-view", url.Values{"view": {"grid"}, "list": {id}}, map[string]string{"Accept-Language": "en"})
	if r.status != http.StatusUnprocessableEntity {
		t.Fatalf("unknown view = %d", r.status)
	}
	if !strings.Contains(r.body, `aria-invalid="true"`) || !strings.Contains(r.body, `id="view-err"`) || !strings.Contains(r.body, "Not saved, the view stays") {
		t.Errorf("no error under the switch:\n%s", r.body)
	}
	if !strings.Contains(r.body, `value="list" aria-pressed="true"`) {
		t.Errorf("the old view is not kept")
	}
	h, _ := b.db.HomeHousehold()
	if v, _ := b.db.PersonListView(h.ID, personID(t, b, "Mama")); v != "" {
		t.Errorf("stored %q", v)
	}
	// Someone signed out cannot choose.
	b.logout()
	if r := b.post("/me/list-view", url.Values{"view": {"tiles"}}); r.status != http.StatusSeeOther || !strings.HasPrefix(r.location, "/login") {
		t.Errorf("signed out = %d %q", r.status, r.location)
	}
}

// tilesOf returns the markup of the <li> of the tile with that name.
func tileOf(t *testing.T, page, name string) string {
	t.Helper()
	for _, m := range regexp.MustCompile(`(?s)<li[^>]*>\s*<form.*?</form>(?:<p class="ferr".*?</p>)?\s*</li>`).FindAllString(page, -1) {
		if strings.Contains(m, `<span class="title">`+name+`</span>`) {
			return m
		}
	}
	t.Fatalf("no tile %q in\n%s", name, page)
	return ""
}

func TestATileIsTheWholeTickButtonWithPictureOrSymbolOrLetter(t *testing.T) {
	cp := newChildPhone(t)
	b := cp.parent.b
	h, _ := b.db.HomeHousehold()
	art, err := b.db.CreateArticle(h.ID, personID(t, b, "Mama"), store.OwnArticle{Name: "Oat drink barista", Dept: "dairy", Kind: "vegan", Unit1: "l"},
		&store.ArticlePicture{JPEG: testJPEG(t, 400, 400), Source: "upload"})
	if err != nil {
		t.Fatal(err)
	}
	id, ids := tileList(t, b, []string{"Cheese"}, "Milk", "Gift wrap for Sebastian's party", "Oat drink barista", "Cheese")
	page := cp.screen.get("/lists/"+id, "en").body

	milk := tileOf(t, page, "Milk")
	for _, want := range []string{
		`class="tile-check"`, `type="submit"`, `aria-pressed="false"`, `action="/lists/` + id + `/entries/` + ids["Milk"] + `/done"`,
		`name="done" value="1"`, `<span class="vh">Tick off: </span>`, `<span class="pic" aria-hidden="true">🥛</span>`, `<span class="box" aria-hidden="true">`,
	} {
		if !strings.Contains(milk, want) {
			t.Errorf("Milk tile lacks %q:\n%s", want, milk)
		}
	}
	if strings.Contains(milk, "tile-warn") {
		t.Errorf("Milk warns without an allergy")
	}
	free := tileOf(t, page, "Gift wrap for Sebastian&#39;s party")
	if !strings.Contains(free, `<span class="pic letter" aria-hidden="true">G</span>`) {
		t.Errorf("free text has no letter on a ring:\n%s", free)
	}
	own := tileOf(t, page, "Oat drink barista")
	if !strings.Contains(own, `<img src="/articles/`+art.ID+`/picture?v=`) || strings.Contains(own, "letter") {
		t.Errorf("own article shows no picture:\n%s", own)
	}
	// Ticked-off tiles sit under "Ticked off (1)", filled, and untick on a tap.
	i := strings.Index(page, "Ticked off (1)")
	if i < 0 {
		t.Fatalf("no group of ticked-off tiles:\n%s", page)
	}
	cheese := tileOf(t, page[i:], "Cheese")
	if !strings.Contains(cheese, `data-done="true"`) && !strings.Contains(page[i:], `<li data-done="true">`) {
		t.Errorf("ticked tile not marked")
	}
	for _, want := range []string{`aria-pressed="true"`, `name="done" value="0"`, `Mark as open: `} {
		if !strings.Contains(cheese, want) {
			t.Errorf("ticked tile lacks %q:\n%s", want, cheese)
		}
	}
	if !strings.Contains(page, `<ul class="tiles" data-person="`) {
		t.Errorf("tiles do not carry the list colour")
	}
	// Children cannot add or sort.
	for _, forbidden := range []string{"add-entry", "?edit=1"} {
		if strings.Contains(page, forbidden) {
			t.Errorf("a child's tiles offer %q", forbidden)
		}
	}
}

func TestATileWarnsWithOneLinePerPersonNeverACount(t *testing.T) {
	b := signedInBox(t)
	setFood(t, b, "Mia", store.Food{Diet: "omnivore", Allergens: []string{"milk", "nuts"}})
	setFood(t, b, "Sebastian", store.Food{Diet: "omnivore", Allergens: []string{"milk"}})
	setFood(t, b, "Papa", store.Food{Diet: "omnivore", Allergens: []string{"soy"}})
	id, _ := tileList(t, b, nil, "Milk", "Bananas")
	b.post("/me/list-view", url.Values{"view": {"tiles"}, "list": {id}})
	page := getEN(b, "/lists/"+id).body
	milk := tileOf(t, page, "Milk")
	if !strings.Contains(milk, `<span class="tile-warn"><span class="h">`) || !strings.Contains(milk, "Allergy warning</span>") {
		t.Fatalf("no warning block:\n%s", milk)
	}
	if n := strings.Count(milk, `<li><span class="who">`); n != 2 {
		t.Errorf("%d lines, want one for Mia and one for Sebastian:\n%s", n, milk)
	}
	for _, want := range []string{"Mia:</span>", "Sebastian:</span>", "Milk and lactose"} {
		if !strings.Contains(milk, want) {
			t.Errorf("warning lacks %q:\n%s", want, milk)
		}
	}
	if strings.Contains(milk, "Papa") || strings.Contains(milk, "+") {
		t.Errorf("warns about someone it does not concern, or counts:\n%s", milk)
	}
	if strings.Contains(tileOf(t, page, "Bananas"), "tile-warn") {
		t.Errorf("Bananas warn")
	}
}

func TestTheListViewShowsTheSameWarningAsTheLongSentence(t *testing.T) {
	b := signedInBox(t)
	setFood(t, b, "Mia", store.Food{Diet: "omnivore", Allergens: []string{"milk"}})
	id, _ := tileList(t, b, nil, "Milk", "Bananas")
	page := getEN(b, "/lists/"+id).body
	if !strings.Contains(page, `<span class="allergen-warn">`) || !strings.Contains(page, "Contains Milk and lactose") {
		t.Errorf("no warning under the entry in the list view:\n%s", page)
	}
	if strings.Count(page, `class="allergen-warn"`) != 1 {
		t.Errorf("a warning under an entry without the allergen")
	}
}

func TestFreeTextAllergiesEndEveryListWithFoodInBothViews(t *testing.T) {
	b := signedInBox(t)
	setFood(t, b, "Mia", store.Food{Diet: "omnivore", Other: "Kiwi"})
	food, _ := tileList(t, b, nil, "Milk")
	nonfood, _ := listWith(t, b, "Home", nil, "Gift wrap")
	empty, _ := listWith(t, b, "Nothing", nil)
	for _, view := range []string{"list", "tiles"} {
		b.post("/me/list-view", url.Values{"view": {view}})
		page := getEN(b, "/lists/"+food).body
		if !strings.Contains(page, `<section class="unchecked-box"`) || !strings.Contains(page, "Mia:</span> <span>Kiwi</span>") {
			t.Errorf("%s: no note about the free-text allergy:\n%s", view, page)
		}
		if strings.Index(page, "unchecked-box") < strings.Index(page, "Milk") {
			t.Errorf("%s: the note does not end the list", view)
		}
		for _, id := range []string{nonfood, empty} {
			if strings.Contains(getEN(b, "/lists/"+id).body, "unchecked-box") {
				t.Errorf("%s: note on a list without a food article", view)
			}
		}
	}
	// Nobody with a free-text allergy: no note.
	setFood(t, b, "Mia", store.Food{Diet: "omnivore"})
	if strings.Contains(getEN(b, "/lists/"+food).body, "unchecked-box") {
		t.Errorf("note without anyone's free-text allergy")
	}
}

func TestTilesOfAnEmptyOrFinishedListSayWhatThatMeansToAChild(t *testing.T) {
	cp := newChildPhone(t)
	b := cp.parent.b
	empty, _ := listWith(t, b, "Nothing", nil)
	page := cp.screen.get("/lists/"+empty, "en").body
	if !strings.Contains(page, `data-empty`) || !strings.Contains(page, "Mama can add something.") || strings.Contains(page, `<ul class="tiles"`) {
		t.Errorf("empty list for a child:\n%s", page)
	}
	done, _ := listWith(t, b, "Done", []string{"Milk"}, "Milk")
	page = cp.screen.get("/lists/"+done, "en").body
	if !strings.Contains(page, "All 1 ticked off. Tap a tile to open it again.") || !strings.Contains(page, `class="empty all-done"`) || !strings.Contains(page, "Everything is ticked off.") {
		t.Errorf("finished list for a child:\n%s", page)
	}
}

func TestTheSortOrRemoveViewStaysRowsAndHidingTickedWorksInTiles(t *testing.T) {
	b := signedInBox(t)
	id, _ := tileList(t, b, []string{"Cheese"}, "Milk", "Cheese")
	b.post("/me/list-view", url.Values{"view": {"tiles"}, "list": {id}})
	if edit := getEN(b, "/lists/"+id+"?edit=1").body; strings.Contains(edit, `class="tiles"`) || !strings.Contains(edit, `class="entries"`) {
		t.Errorf("the edit view is not rows:\n%s", edit)
	}
	hidden := getEN(b, "/lists/"+id+"?ticked=hide").body
	if strings.Contains(hidden, "Cheese") || !strings.Contains(hidden, "Milk") {
		t.Errorf("hiding ticked-off tiles:\n%s", hidden)
	}
	if !strings.Contains(hidden, `name="ticked" value="hide"`) {
		t.Errorf("the choice to hide ticked-off entries does not survive a tick")
	}
	// The switch keeps it too.
	if r := b.post("/me/list-view", url.Values{"view": {"list"}, "list": {id}, "ticked": {"hide"}}); r.location != "/lists/"+id+"?ticked=hide" {
		t.Errorf("switch redirect = %q", r.location)
	}
}

func TestListViewHasAPIParity(t *testing.T) {
	c := signedInAPI(t)
	mia := personNamed(t, c.b, "Mia")
	mama := personNamed(t, c.b, "Mama")
	status, got, _ := c.call(http.MethodGet, "/people/"+mia.ID+"/list-view", nil)
	if status != http.StatusOK || got["view"] != "tiles" || got["default"] != true {
		t.Errorf("child default = %d %v", status, got)
	}
	status, got, _ = c.call(http.MethodGet, "/people/"+mama.ID+"/list-view", nil)
	if status != http.StatusOK || got["view"] != "list" || got["default"] != true {
		t.Errorf("adult default = %d %v", status, got)
	}
	status, got, _ = c.call(http.MethodPut, "/people/"+mama.ID+"/list-view", map[string]any{"view": "tiles"})
	if status != http.StatusOK || got["view"] != "tiles" || got["default"] != false {
		t.Errorf("put = %d %v", status, got)
	}
	// The same choice is what the web page shows.
	id, _ := tileList(t, c.b, nil, "Milk")
	if page := getEN(c.b, "/lists/"+id).body; !strings.Contains(page, `<ul class="tiles"`) {
		t.Errorf("web page ignores the API choice")
	}
	for _, bad := range []string{`{"view":"grid"}`, `{}`, `{"view":""}`} { // the contract refuses them, so they go raw
		if status := rawCall(t, c, http.MethodPut, "/people/"+mama.ID+"/list-view", bad); status != http.StatusUnprocessableEntity {
			t.Errorf("put %s = %d, want 422", bad, status)
		}
	}
	// Only your own: a parent does not change someone else's view.
	if status, _, _ := c.call(http.MethodPut, "/people/"+mia.ID+"/list-view", map[string]any{"view": "list"}); status != http.StatusForbidden {
		t.Errorf("changing another person's view = %d, want 403", status)
	}
	if status, _, _ := c.call(http.MethodGet, "/people/nobody/list-view", nil); status != http.StatusNotFound {
		t.Errorf("stranger = %d", status)
	}
}

func TestEntriesOfTheAPICarryTheirSymbolOrPicture(t *testing.T) {
	c := signedInAPI(t)
	h, _ := c.b.db.HomeHousehold()
	art, _ := c.b.db.CreateArticle(h.ID, personNamed(t, c.b, "Mama").ID, store.OwnArticle{Name: "Oat drink barista", Dept: "dairy", Kind: "vegan", Unit1: "l"}, &store.ArticlePicture{JPEG: testJPEG(t, 400, 400), Source: "upload"})
	id, _ := tileList(t, c.b, nil, "Milk", "Oat drink barista", "Party hats")
	_, got, _ := c.call(http.MethodGet, "/lists/"+id, nil)
	byText := map[string]map[string]any{}
	for _, e := range got["entries"].([]any) {
		m := e.(map[string]any)
		byText[m["text"].(string)] = m
	}
	if m := byText["Milk"]; m["article_id"] != "milk" || m["symbol"] != "🥛" || m["picture"] != nil {
		t.Errorf("Milk = %v", m)
	}
	if m := byText["Oat drink barista"]; m["article_id"] != art.ID || m["picture"] == nil || !strings.HasPrefix(m["picture"].(string), "/articles/"+art.ID+"/picture") {
		t.Errorf("own = %v", m)
	}
	if m := byText["Party hats"]; m["article_id"] != nil || m["symbol"] != nil || m["picture"] != nil {
		t.Errorf("free text = %v", m)
	}
}

var _ = list.Tiles

func TestADisplaysViewIsReadAndChangedThroughTheAPI(t *testing.T) {
	c := signedInAPI(t)
	code := codeOn(t, newScreen(c.b).get("/display", "en").body)
	_, shown, _ := c.call(http.MethodPost, "/displays", map[string]any{"code": code, "name": "Kitchen", "type": "screen"})
	id := shown["id"].(string)
	if shown["list_view"] != "tiles" {
		t.Errorf("a new display shows %v, want tiles", shown["list_view"])
	}
	status, got, _ := c.call(http.MethodPatch, "/displays/"+id, map[string]any{"list_view": "list"})
	if status != http.StatusOK || got["list_view"] != "list" {
		t.Fatalf("patch = %d %v", status, got)
	}
	h, _ := c.b.db.HomeHousehold()
	if v, _ := c.b.db.DisplayListView(h.ID, id); v != list.Rows {
		t.Errorf("stored %q", v)
	}
	// Other fields leave the view alone.
	if _, got, _ := c.call(http.MethodPatch, "/displays/"+id, map[string]any{"name": "Hall"}); got["list_view"] != "list" {
		t.Errorf("renaming changed the view: %v", got)
	}
	if status := rawCall(t, c, http.MethodPatch, "/displays/"+id, `{"list_view":"grid"}`); status != http.StatusUnprocessableEntity {
		t.Errorf("unknown view = %d, want 422", status)
	}
}

func TestOnlyTilesLoadTheScriptThatShowsAnUnsavedTick(t *testing.T) {
	b := signedInBox(t)
	id, _ := tileList(t, b, nil, "Milk")
	if page := getEN(b, "/lists/"+id).body; strings.Contains(page, "tiles.js") {
		t.Errorf("the list view loads the tile script")
	}
	b.post("/me/list-view", url.Values{"view": {"tiles"}, "list": {id}})
	if page := getEN(b, "/lists/"+id).body; !strings.Contains(page, `<script src="/static/tiles.js" defer data-error="Not saved. Tap again."></script>`) {
		t.Errorf("tiles lack the script:\n%s", page)
	}
}
