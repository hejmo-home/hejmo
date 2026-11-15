package server_test

import (
	"net/http"
	"net/url"
	"strings"
	"testing"
)

// listPage opens the detail page of the list with the given name (English).
func listIDByName(t *testing.T, c *apiClient, name string) string {
	t.Helper()
	_, _, lists := c.call(http.MethodGet, "/lists", nil)
	for _, l := range lists {
		if m := l.(map[string]any); m["name"] == name {
			return m["id"].(string)
		}
	}
	t.Fatalf("no list %q in %v", name, lists)
	return ""
}

func TestListsPagesNeedASignedInMember(t *testing.T) {
	b, _ := configuredBox(t)
	for _, path := range []string{"/lists", "/lists/x"} {
		if r := b.get(path); r.status != http.StatusSeeOther || !strings.HasPrefix(r.location, "/login") {
			t.Fatalf("signed out %s = %d %q", path, r.status, r.location)
		}
	}
}

func TestParentCreatesAListAndAddsEntriesInTheWebInterface(t *testing.T) {
	c := signedInAPI(t)
	b := c.b
	r := b.post("/lists", url.Values{"name": {"Packing"}, "color": {"plum"}, "symbol": {"triangle-filled"}, "sort": {"manual"}})
	if r.status != http.StatusSeeOther || !strings.HasPrefix(r.location, "/lists/") {
		t.Fatalf("create = %d %q\n%s", r.status, r.location, r.body)
	}
	page := b.do(http.MethodGet, r.location, nil, map[string]string{"Accept-Language": "en"})
	if page.status != http.StatusOK || !strings.Contains(page.body, "Packing") || !strings.Contains(page.body, `data-shape="triangle-filled"`) || !strings.Contains(page.body, `data-person="5"`) {
		t.Fatalf("detail = %d\n%s", page.status, page.body)
	}
	if !strings.Contains(page.body, "data-success") {
		t.Errorf("no success line after creating")
	}
	id := strings.TrimPrefix(r.location, "/lists/")
	if r := b.post("/lists/"+id+"/entries", url.Values{"text": {"Sunscreen"}}); r.status != http.StatusSeeOther || r.location != "/lists/"+id {
		t.Fatalf("add = %d %q", r.status, r.location)
	}
	// API parity: the same entry is there.
	if got := entryTexts(t, c, id); !sameStrings(got, []string{"Sunscreen"}) {
		t.Fatalf("API entries = %v", got)
	}
	overview := getEN(b, "/lists")
	if overview.status != http.StatusOK || !strings.Contains(overview.body, "Packing") || !strings.Contains(overview.body, "Einkaufen") || !strings.Contains(overview.body, "To-dos") {
		t.Fatalf("overview = %d\n%s", overview.status, overview.body)
	}
}

func TestInvalidListFormsShowErrorsAtTheFieldAndKeepWhatWasTyped(t *testing.T) {
	b := signedInBox(t)
	r := b.do(http.MethodPost, "/lists", url.Values{"name": {" "}, "color": {"blue"}, "symbol": {"circle-empty"}, "sort": {"alphabetical"}}, map[string]string{"Accept-Language": "en"})
	if r.status != http.StatusUnprocessableEntity || !strings.Contains(r.body, `id="err-name"`) {
		t.Fatalf("blank name = %d\n%s", r.status, r.body)
	}
	if !strings.Contains(r.body, `value="alphabetical" checked`) {
		t.Errorf("chosen sorting lost after an error")
	}
	id := firstListID(t, b)
	r = b.do(http.MethodPost, "/lists/"+id+"/entries", url.Values{"text": {""}}, map[string]string{"Accept-Language": "en"})
	if r.status != http.StatusUnprocessableEntity || !strings.Contains(r.body, `id="err-text"`) {
		t.Fatalf("blank entry = %d\n%s", r.status, r.body)
	}
}

func firstListID(t *testing.T, b *box) string {
	t.Helper()
	h, _ := b.db.HomeHousehold()
	ls, err := b.db.Lists(h.ID)
	if err != nil || len(ls) == 0 {
		t.Fatalf("no lists: %v", err)
	}
	return ls[0].ID
}

// Acceptance (web): an alphabetical list shows a new entry in its place.
func TestAlphabeticalListPageShowsEntriesInOrder(t *testing.T) {
	b := signedInBox(t)
	r := b.post("/lists", url.Values{"name": {"Drinks"}, "color": {"teal"}, "symbol": {"diamond-filled"}, "sort": {"alphabetical"}})
	id := strings.TrimPrefix(r.location, "/lists/")
	for _, s := range []string{"Water", "Apple juice", "Milk"} {
		b.post("/lists/"+id+"/entries", url.Values{"text": {s}})
	}
	body := getEN(b, "/lists/"+id).body
	a, m, w := strings.Index(body, "Apple juice"), strings.Index(body, "Milk"), strings.Index(body, "Water")
	if a < 0 || !(a < m && m < w) {
		t.Fatalf("order on page: apple %d milk %d water %d", a, m, w)
	}
	if strings.Contains(body, "/move") {
		t.Errorf("alphabetical list offers moving entries")
	}
}

func TestManualListPageMovesEntries(t *testing.T) {
	b := signedInBox(t)
	id := firstListID(t, b)
	h, _ := b.db.HomeHousehold()
	var ids []string
	for _, s := range []string{"a-one", "b-two", "c-three"} {
		e, _ := b.db.AddEntry(h.ID, id, s)
		ids = append(ids, e.ID)
	}
	if r := b.post("/lists/"+id+"/entries/"+ids[2]+"/move", url.Values{"direction": {"up"}}); r.status != http.StatusSeeOther {
		t.Fatalf("move = %d", r.status)
	}
	body := getEN(b, "/lists/"+id).body
	if !(strings.Index(body, "c-three") < strings.Index(body, "b-two")) || !(strings.Index(body, "a-one") < strings.Index(body, "c-three")) {
		t.Fatalf("order after move wrong:\n%s", body)
	}
	if r := b.post("/lists/"+id+"/entries/"+ids[0]+"/move", url.Values{"direction": {"sideways"}}); r.status != http.StatusUnprocessableEntity {
		t.Fatalf("bad direction = %d", r.status)
	}
}

// Acceptance (web): "Delete completed" removes only checked-off entries; they
// can also be hidden.
func TestDeleteCompletedAndHideCompletedInTheWebInterface(t *testing.T) {
	b := signedInBox(t)
	id := firstListID(t, b)
	h, _ := b.db.HomeHousehold()
	milk, _ := b.db.AddEntry(h.ID, id, "Milk")
	b.db.AddEntry(h.ID, id, "Eggs")
	if r := b.post("/lists/"+id+"/entries/"+milk.ID+"/done", url.Values{"done": {"1"}}); r.status != http.StatusSeeOther || r.location != "/lists/"+id {
		t.Fatalf("tick = %d %q", r.status, r.location)
	}
	page := getEN(b, "/lists/"+id)
	if !strings.Contains(page.body, "<s>Milk</s>") || !strings.Contains(page.body, "Eggs") {
		t.Fatalf("ticked entry not shown struck through:\n%s", page.body)
	}
	hidden := getEN(b, "/lists/"+id+"?completed=hide")
	if strings.Contains(hidden.body, "Milk") || !strings.Contains(hidden.body, "Eggs") {
		t.Fatalf("hide completed:\n%s", hidden.body)
	}
	if r := b.post("/lists/"+id+"/completed/delete", url.Values{}); r.status != http.StatusSeeOther {
		t.Fatalf("delete completed = %d", r.status)
	}
	after := getEN(b, "/lists/"+id).body
	if strings.Contains(after, "Milk") || !strings.Contains(after, "Eggs") {
		t.Fatalf("after delete completed:\n%s", after)
	}
	eggs, _ := b.db.Entries(h.ID, id)
	if r := b.post("/lists/"+id+"/entries/"+eggs[0].ID+"/delete", url.Values{}); r.status != http.StatusSeeOther {
		t.Fatalf("delete entry = %d", r.status)
	}
	if r := b.post("/lists/"+id+"/entries/"+eggs[0].ID+"/delete", url.Values{}); r.status != http.StatusNotFound {
		t.Fatalf("second delete = %d", r.status)
	}
}

func TestParentChangesAndDeletesAListInTheWebInterface(t *testing.T) {
	b := signedInBox(t)
	id := firstListID(t, b)
	if r := b.post("/lists/"+id+"/settings", url.Values{"name": {"Weekly shop"}, "color": {"ochre"}, "symbol": {"square-empty"}, "sort": {"alphabetical"}}); r.status != http.StatusSeeOther || r.location != "/lists/"+id {
		t.Fatalf("settings = %d %q", r.status, r.location)
	}
	h, _ := b.db.HomeHousehold()
	l, _ := b.db.List(h.ID, id)
	if l.Name != "Weekly shop" || l.Color != "ochre" || l.Sort != "alphabetical" {
		t.Fatalf("stored = %+v", l)
	}
	if r := b.do(http.MethodPost, "/lists/"+id+"/settings", url.Values{"name": {""}, "color": {"ochre"}, "symbol": {"square-empty"}, "sort": {"manual"}}, nil); r.status != http.StatusUnprocessableEntity {
		t.Fatalf("blank rename = %d", r.status)
	}
	page := getEN(b, "/lists/"+id).body
	if !strings.Contains(page, `<details class="danger">`) || !strings.Contains(page, `action="/lists/`+id+`/delete"`) {
		t.Fatalf("delete is not behind a confirmation:\n%s", page)
	}
	if r := b.post("/lists/"+id+"/delete", url.Values{}); r.status != http.StatusSeeOther || r.location != "/lists" {
		t.Fatalf("delete = %d %q", r.status, r.location)
	}
	if r := b.get("/lists/" + id); r.status != http.StatusNotFound {
		t.Fatalf("deleted list = %d", r.status)
	}
}

// Acceptance (web): a child ticks off, but cannot delete a list or add entries.
func TestChildPhoneTicksOffListEntriesButDeletesNothing(t *testing.T) {
	cp := newChildPhone(t)
	b := cp.parent.b
	id := firstListID(t, b)
	h, _ := b.db.HomeHousehold()
	e, _ := b.db.AddEntry(h.ID, id, "Milk")
	page := cp.screen.get("/lists/"+id, "en")
	if page.status != http.StatusOK || !strings.Contains(page.body, "Milk") {
		t.Fatalf("child GET list = %d\n%s", page.status, page.body)
	}
	for _, forbidden := range []string{"/delete", "/settings", `action="/lists/` + id + `/entries"`, "/move"} {
		if strings.Contains(page.body, forbidden) {
			t.Errorf("child's list page offers %s", forbidden)
		}
	}
	if r := cp.screen.post("/lists/"+id+"/entries/"+e.ID+"/done", url.Values{"done": {"1"}}); r.status != http.StatusSeeOther || r.location != "/lists/"+id {
		t.Fatalf("child tick = %d %q", r.status, r.location)
	}
	if es, _ := b.db.Entries(h.ID, id); !es[0].Done {
		t.Fatal("tick not stored")
	}
	for name, r := range map[string]resp{
		"delete list":      cp.screen.post("/lists/"+id+"/delete", url.Values{}),
		"create list":      cp.screen.post("/lists", url.Values{"name": {"x"}, "color": {"blue"}, "symbol": {"circle-empty"}}),
		"add entry":        cp.screen.post("/lists/"+id+"/entries", url.Values{"text": {"x"}}),
		"delete entry":     cp.screen.post("/lists/"+id+"/entries/"+e.ID+"/delete", url.Values{}),
		"delete completed": cp.screen.post("/lists/"+id+"/completed/delete", url.Values{}),
		"change list":      cp.screen.post("/lists/"+id+"/settings", url.Values{"name": {"x"}, "color": {"blue"}, "symbol": {"circle-empty"}, "sort": {"manual"}}),
		"move entry":       cp.screen.post("/lists/"+id+"/entries/"+e.ID+"/move", url.Values{"direction": {"up"}}),
	} {
		if r.status != http.StatusForbidden {
			t.Errorf("child %s = %d, want 403", name, r.status)
		}
	}
	if _, err := b.db.List(h.ID, id); err != nil {
		t.Fatalf("list gone: %v", err)
	}
	overview := cp.screen.get("/lists", "en")
	if overview.status != http.StatusOK || strings.Contains(overview.body, `action="/lists"`) {
		t.Errorf("child overview = %d, offers creating a list", overview.status)
	}
	// API parity.
	if status, _, _ := cp.phone.call(http.MethodDelete, "/lists/"+id, nil); status != http.StatusForbidden {
		t.Errorf("API child deletes list = %d, want 403", status)
	}
}

func TestListsPageIsGermanAndEscapesText(t *testing.T) {
	b := signedInBox(t)
	id := firstListID(t, b)
	b.post("/lists/"+id+"/entries", url.Values{"text": {hostileTitle}})
	de := b.do(http.MethodGet, "/lists/"+id, nil, map[string]string{"Accept-Language": "de"})
	requireEscaped(t, "GET /lists/{id}", de.body)
	if !strings.Contains(de.body, "Neuer Eintrag") {
		t.Errorf("German page lacks German text:\n%s", de.body)
	}
	if o := b.do(http.MethodGet, "/lists", nil, map[string]string{"Accept-Language": "de"}); !strings.Contains(o.body, "Neue Liste") {
		t.Errorf("German overview lacks German text")
	}
}
