package server_test

import (
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"testing"
)

// listBox is a signed-in parent (Mama) with a child, Mia, who has her own token.
type listBox struct {
	taskBox
}

func newListBox(t *testing.T) listBox { return listBox{newTaskBox(t)} }

func (lb listBox) newList(t *testing.T, name, sort string) string {
	t.Helper()
	status, l, _ := lb.parent.call(http.MethodPost, "/lists", map[string]any{"name": name, "color": "blue", "symbol": "circle-empty", "sort": sort})
	if status != http.StatusCreated {
		t.Fatalf("create list = %d %v", status, l)
	}
	return l["id"].(string)
}

func (lb listBox) addEntry(t *testing.T, listID, text string) string {
	t.Helper()
	status, e, _ := lb.parent.call(http.MethodPost, "/lists/"+listID+"/entries", map[string]any{"text": text})
	if status != http.StatusCreated {
		t.Fatalf("add entry %q = %d %v", text, status, e)
	}
	return e["id"].(string)
}

func entryTexts(t *testing.T, c *apiClient, listID string) []string {
	t.Helper()
	status, l, _ := c.call(http.MethodGet, "/lists/"+listID, nil)
	if status != http.StatusOK {
		t.Fatalf("get list = %d %v", status, l)
	}
	var out []string
	for _, e := range l["entries"].([]any) {
		out = append(out, e.(map[string]any)["text"].(string))
	}
	return out
}

func sameStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// The default lists are named in the language the setup is shown in: the
// hejmo_lang cookie beats Accept-Language.
func TestSetupNamesTheListsInTheChosenSetupLanguage(t *testing.T) {
	b := newBox(t)
	r := b.do(http.MethodPost, "/setup", validSetupForm(), map[string]string{"Accept-Language": "de-DE", "Cookie": "hejmo_lang=en"})
	if r.status != http.StatusSeeOther {
		t.Fatalf("setup = %d", r.status)
	}
	secret := secretOn(t, b.get("/setup/totp").body)
	b.post("/setup/totp", url.Values{"code": {totpNow(b, secret)}})
	h, _ := b.db.HomeHousehold()
	lists, err := b.db.Lists(h.ID)
	if err != nil || len(lists) != 2 || lists[0].Name != "Shopping" || lists[1].Name != "To-dos" {
		t.Fatalf("lists = %v, %v; want Shopping and To-dos", lists, err)
	}
}

// Acceptance: Given a new box, when setup is complete, then the lists
// "Shopping" and "To-dos" exist.
func TestNewBoxHasTheListsShoppingAndTodos(t *testing.T) {
	b := newBox(t)
	r := b.do(http.MethodPost, "/setup", validSetupForm(), map[string]string{"Accept-Language": "en"})
	if r.status != http.StatusSeeOther {
		t.Fatalf("setup = %d", r.status)
	}
	secret := secretOn(t, b.get("/setup/totp").body)
	b.post("/setup/totp", url.Values{"code": {totpNow(b, secret)}})
	c := newAPIClient(t, b)
	c.cookie = true
	status, _, lists := c.call(http.MethodGet, "/lists", nil)
	if status != http.StatusOK || len(lists) != 2 {
		t.Fatalf("lists = %d %v", status, lists)
	}
	if lists[0].(map[string]any)["name"] != "Shopping" || lists[1].(map[string]any)["name"] != "To-dos" {
		t.Fatalf("lists = %v", lists)
	}
}

func TestNewBoxListsAreGermanForAGermanSetup(t *testing.T) {
	c := signedInAPI(t)
	_, _, lists := c.call(http.MethodGet, "/lists", nil)
	if len(lists) != 2 || lists[0].(map[string]any)["name"] != "Einkaufen" || lists[1].(map[string]any)["name"] != "To-dos" {
		t.Fatalf("lists = %v", lists)
	}
}

func TestParentCreatesUpdatesAndDeletesLists(t *testing.T) {
	lb := newListBox(t)
	id := lb.newList(t, "Packing", "manual")
	status, l, _ := lb.parent.call(http.MethodPatch, "/lists/"+id, map[string]any{"name": "Holiday packing", "color": "plum", "sort": "alphabetical"})
	if status != http.StatusOK || l["name"] != "Holiday packing" || l["color"] != "plum" || l["sort"] != "alphabetical" || l["symbol"] != "circle-empty" {
		t.Fatalf("patch = %d %v", status, l)
	}
	if status, _, _ := lb.parent.call(http.MethodDelete, "/lists/"+id, nil); status != http.StatusNoContent {
		t.Fatalf("delete = %d", status)
	}
	if status, _, _ := lb.parent.call(http.MethodGet, "/lists/"+id, nil); status != http.StatusNotFound {
		t.Fatalf("get after delete = %d", status)
	}
	if status, _, _ := lb.parent.call(http.MethodDelete, "/lists/"+id, nil); status != http.StatusNotFound {
		t.Fatalf("second delete = %d", status)
	}
}

func TestListInputIsValidated(t *testing.T) {
	lb := newListBox(t)
	for name, body := range map[string]map[string]any{
		"no name":   {"name": " ", "color": "blue", "symbol": "circle-empty"},
		"no such":   {"name": "x", "color": "pink", "symbol": "circle-empty"},
		"no symbol": {"name": "x", "color": "blue", "symbol": "star"},
		"bad sort":  {"name": "x", "color": "blue", "symbol": "circle-empty", "sort": "random"},
		"long name": {"name": strings.Repeat("x", 61), "color": "blue", "symbol": "circle-empty"},
	} {
		// The contract client refuses what the schema forbids before it is sent.
		if got := rawCall(t, lb.parent, http.MethodPost, "/lists", mustJSON(body)); got != http.StatusUnprocessableEntity {
			t.Errorf("%s = %d, want 422", name, got)
		}
	}
	if got := rawCall(t, lb.parent, http.MethodPost, "/lists", "{"); got != http.StatusBadRequest {
		t.Errorf("malformed = %d, want 400", got)
	}
}

// Acceptance: Given a list with alphabetical sorting, when an entry is added,
// then it appears in the correct alphabetical position.
func TestAlphabeticalListShowsNewEntriesInPlace(t *testing.T) {
	lb := newListBox(t)
	id := lb.newList(t, "Shopping 2", "alphabetical")
	for _, s := range []string{"Milk", "Apples", "Zucchini", "Bread"} {
		lb.addEntry(t, id, s)
	}
	if got := entryTexts(t, lb.parent, id); !sameStrings(got, []string{"Apples", "Bread", "Milk", "Zucchini"}) {
		t.Fatalf("order = %v", got)
	}
}

func TestManualListMovesEntriesAndRefusesMovingInAlphabeticalLists(t *testing.T) {
	lb := newListBox(t)
	id := lb.newList(t, "Chores", "manual")
	lb.addEntry(t, id, "a")
	lb.addEntry(t, id, "b")
	c := lb.addEntry(t, id, "c")
	if status, _, _ := lb.parent.call(http.MethodPut, "/lists/"+id+"/entries/"+c+"/position", map[string]any{"position": 0}); status != http.StatusOK {
		t.Fatalf("move = %d", status)
	}
	if got := entryTexts(t, lb.parent, id); !sameStrings(got, []string{"c", "a", "b"}) {
		t.Fatalf("order = %v", got)
	}
	lb.parent.call(http.MethodPatch, "/lists/"+id, map[string]any{"sort": "alphabetical"})
	if status, _, _ := lb.parent.call(http.MethodPut, "/lists/"+id+"/entries/"+c+"/position", map[string]any{"position": 2}); status != http.StatusUnprocessableEntity {
		t.Fatalf("move in alphabetical list = %d, want 422", status)
	}
}

// Acceptance: Given a checked-off entry, when "Delete completed" is selected,
// then only the checked-off entries disappear.
func TestDeleteCompletedRemovesOnlyCheckedOffEntries(t *testing.T) {
	lb := newListBox(t)
	id := lb.newList(t, "Shopping 2", "manual")
	milk, bread := lb.addEntry(t, id, "Milk"), lb.addEntry(t, id, "Bread")
	lb.addEntry(t, id, "Eggs")
	for _, e := range []string{milk, bread} {
		if status, k, _ := lb.parent.call(http.MethodPut, "/lists/"+id+"/entries/"+e+"/done", map[string]any{"done": true}); status != http.StatusOK || k["done"] != true {
			t.Fatalf("tick = %d %v", status, k)
		}
	}
	status, k, _ := lb.parent.call(http.MethodDelete, "/lists/"+id+"/completed-entries", nil)
	if status != http.StatusOK || k["deleted"] != float64(2) {
		t.Fatalf("delete completed = %d %v", status, k)
	}
	if got := entryTexts(t, lb.parent, id); !sameStrings(got, []string{"Eggs"}) {
		t.Fatalf("left = %v", got)
	}
}

func TestParentDeletesASingleEntry(t *testing.T) {
	lb := newListBox(t)
	id := lb.newList(t, "Shopping 2", "manual")
	e := lb.addEntry(t, id, "Milk")
	if status, _, _ := lb.parent.call(http.MethodDelete, "/lists/"+id+"/entries/"+e, nil); status != http.StatusNoContent {
		t.Fatalf("delete = %d", status)
	}
	if status, _, _ := lb.parent.call(http.MethodDelete, "/lists/"+id+"/entries/"+e, nil); status != http.StatusNotFound {
		t.Fatalf("second delete = %d", status)
	}
	if status, _, _ := lb.parent.call(http.MethodPost, "/lists/"+id+"/entries", map[string]any{"text": " "}); status != http.StatusUnprocessableEntity {
		t.Fatalf("blank entry = %d", status)
	}
}

// Acceptance: Given a child, when they try to delete a list, then this is refused.
func TestChildMayTickOffButNotDeleteListsOrAddEntries(t *testing.T) {
	lb := newListBox(t)
	id := lb.newList(t, "Shopping 2", "manual")
	e := lb.addEntry(t, id, "Milk")
	if status, _, _ := lb.child.call(http.MethodDelete, "/lists/"+id, nil); status != http.StatusForbidden {
		t.Fatalf("child deletes list = %d, want 403", status)
	}
	if status, _, _ := lb.child.call(http.MethodGet, "/lists/"+id, nil); status != http.StatusOK {
		t.Fatalf("child reads list = %d", status)
	}
	if status, k, _ := lb.child.call(http.MethodPut, "/lists/"+id+"/entries/"+e+"/done", map[string]any{"done": true}); status != http.StatusOK || k["done"] != true {
		t.Fatalf("child ticks = %d %v", status, k)
	}
	if status, _, _ := lb.child.call(http.MethodPut, "/lists/"+id+"/entries/"+e+"/done", map[string]any{"done": false}); status != http.StatusOK {
		t.Fatalf("child unticks = %d", status)
	}
	for name, status := range map[string]int{
		"add entry":        statusOf(lb.child.call(http.MethodPost, "/lists/"+id+"/entries", map[string]any{"text": "x"})),
		"delete entry":     statusOf(lb.child.call(http.MethodDelete, "/lists/"+id+"/entries/"+e, nil)),
		"delete completed": statusOf(lb.child.call(http.MethodDelete, "/lists/"+id+"/completed-entries", nil)),
		"create list":      statusOf(lb.child.call(http.MethodPost, "/lists", map[string]any{"name": "x", "color": "blue", "symbol": "circle-empty"})),
		"change list":      statusOf(lb.child.call(http.MethodPatch, "/lists/"+id, map[string]any{"name": "x"})),
	} {
		if status != http.StatusForbidden {
			t.Errorf("child %s = %d, want 403", name, status)
		}
	}
	if got := entryTexts(t, lb.parent, id); !sameStrings(got, []string{"Milk"}) {
		t.Fatalf("a refused request changed the list: %v", got)
	}
}

func statusOf(status int, _ map[string]any, _ []any) int { return status }

// Acceptance: token scope `lists` works.
func TestTokenScopeListsGivesAccessToListsOnly(t *testing.T) {
	lb := newListBox(t)
	id := lb.newList(t, "Shopping 2", "manual")
	lists := lb.parent.withToken(lb.parent.newToken("lists"))
	if status, _, _ := lists.call(http.MethodPost, "/lists/"+id+"/entries", map[string]any{"text": "Milk"}); status != http.StatusCreated {
		t.Fatalf("lists token adds = %d", status)
	}
	if status, _, _ := lists.call(http.MethodDelete, "/lists/"+id, nil); status != http.StatusNoContent {
		t.Fatalf("lists token deletes = %d", status)
	}
	if status, _, _ := lists.call(http.MethodGet, "/tasks", nil); status != http.StatusForbidden {
		t.Fatalf("lists token reads tasks = %d, want 403", status)
	}
	tasks := lb.parent.withToken(lb.parent.newToken("tasks"))
	if status, _, _ := tasks.call(http.MethodGet, "/lists", nil); status != http.StatusForbidden {
		t.Fatalf("tasks token reads lists = %d, want 403", status)
	}
	ro := lb.parent.withToken(lb.parent.newToken("lists:read"))
	if status, _, _ := ro.call(http.MethodGet, "/lists", nil); status != http.StatusOK {
		t.Fatalf("lists:read reads = %d", status)
	}
	if status, _, _ := ro.call(http.MethodPost, "/lists", map[string]any{"name": "x", "color": "blue", "symbol": "circle-empty"}); status != http.StatusForbidden {
		t.Fatalf("lists:read writes = %d, want 403", status)
	}
}

func mustJSON(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}
