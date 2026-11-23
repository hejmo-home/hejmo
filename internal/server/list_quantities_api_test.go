package server_test

import (
	"net/http"
	"strings"
	"testing"
)

// shopList is a list with suggestions (on for a new list).
func (lb listBox) shopList(t *testing.T) string { return lb.newList(t, "Einkauf", "manual") }

func (lb listBox) add(t *testing.T, listID string, body map[string]any) (int, map[string]any) {
	t.Helper()
	status, e, _ := lb.parent.call(http.MethodPost, "/lists/"+listID+"/entries", body)
	return status, e
}

func entryOf(t *testing.T, c *apiClient, listID string, idx int) map[string]any {
	t.Helper()
	_, l, _ := c.call(http.MethodGet, "/lists/"+listID, nil)
	return l["entries"].([]any)[idx].(map[string]any)
}

func TestANewListHasSuggestionsAndCanSwitchThemOff(t *testing.T) {
	lb := newListBox(t)
	id := lb.shopList(t)
	_, l, _ := lb.parent.call(http.MethodGet, "/lists/"+id, nil)
	if l["suggestions"] != true {
		t.Fatalf("a new list has suggestions: %v", l)
	}
	status, l, _ := lb.parent.call(http.MethodPatch, "/lists/"+id, map[string]any{"suggestions": false})
	if status != http.StatusOK || l["suggestions"] != false {
		t.Fatalf("switch off = %d %v", status, l)
	}
	status, l, _ = lb.parent.call(http.MethodPost, "/lists", map[string]any{"name": "To-dos", "color": "blue", "symbol": "circle-empty", "suggestions": false})
	if status != http.StatusCreated || l["suggestions"] != false {
		t.Fatalf("create = %d %v", status, l)
	}
}

func TestAddingAnArticleWithoutQuantityStoresTheNameInTheLanguageAndTheArticle(t *testing.T) {
	lb := newListBox(t)
	id := lb.shopList(t)
	status, e := lb.add(t, id, map[string]any{"article_id": "eggs"})
	if status != http.StatusCreated || e["article_id"] != "eggs" || e["result"] != "added" || e["text"] == "" {
		t.Fatalf("%d %v", status, e)
	}
	if _, has := e["amount"]; has {
		t.Errorf("no quantity: %v", e)
	}
	// Again: no second entry.
	status, e = lb.add(t, id, map[string]any{"article_id": "eggs"})
	if status != http.StatusOK || e["result"] != "exists" {
		t.Fatalf("second = %d %v", status, e)
	}
	if got := entryTexts(t, lb.parent, id); len(got) != 1 {
		t.Fatalf("%v", got)
	}
}

func TestAnExactArticleNameInTheTextIsTheArticle(t *testing.T) {
	lb := newListBox(t)
	id := lb.shopList(t)
	status, e := lb.add(t, id, map[string]any{"text": "eier"})
	if status != http.StatusCreated || e["article_id"] != "eggs" {
		t.Fatalf("%d %v", status, e)
	}
	status, e = lb.add(t, id, map[string]any{"text": "Quinoa-Pops"})
	if status != http.StatusCreated || e["article_id"] != nil && e["article_id"] != "" {
		t.Fatalf("free text: %d %v", status, e)
	}
}

func TestQuantitiesAddUpAcrossUnitsOfOneKind(t *testing.T) {
	lb := newListBox(t)
	id := lb.shopList(t)
	status, e := lb.add(t, id, map[string]any{"article_id": "sugar", "amount": 500, "unit": "g"})
	if status != http.StatusCreated || e["amount"] != 500.0 || e["unit"] != "g" {
		t.Fatalf("%d %v", status, e)
	}
	status, e = lb.add(t, id, map[string]any{"article_id": "sugar", "amount": 1, "unit": "kg"})
	if status != http.StatusOK || e["result"] != "added_to" || e["amount"] != 1.5 || e["unit"] != "kg" {
		t.Fatalf("500 g + 1 kg = %d %v", status, e)
	}
	prev, _ := e["previous"].(map[string]any)
	if prev["amount"] != 500.0 || prev["unit"] != "g" {
		t.Errorf("previous = %v", e["previous"])
	}
	if n := len(entryTexts(t, lb.parent, id)); n != 1 {
		t.Fatalf("%d entries", n)
	}
	// Eggs: 6 pieces and a dozen make 18 pieces, not 1.5 dozen.
	lb.add(t, id, map[string]any{"article_id": "eggs", "amount": 6, "unit": "piece"})
	_, e = lb.add(t, id, map[string]any{"article_id": "eggs", "amount": 1, "unit": "dozen"})
	if e["amount"] != 18.0 || e["unit"] != "piece" {
		t.Errorf("6 + 1 dozen = %v", e)
	}
}

func TestAnotherKindGetsItsOwnEntryDirectlyUnderTheOldOne(t *testing.T) {
	lb := newListBox(t)
	id := lb.shopList(t)
	lb.add(t, id, map[string]any{"article_id": "rolled-oats", "amount": 2, "unit": "pack"})
	lb.add(t, id, map[string]any{"article_id": "milk"})
	status, e := lb.add(t, id, map[string]any{"article_id": "rolled-oats", "amount": 500, "unit": "g"})
	if status != http.StatusCreated {
		t.Fatalf("%d %v", status, e)
	}
	if got := entryOf(t, lb.parent, id, 1); got["unit"] != "g" || got["amount"] != 500.0 {
		t.Errorf("second entry = %v", got)
	}
	if got := entryOf(t, lb.parent, id, 2); got["article_id"] != "milk" {
		t.Errorf("milk moved down: %v", got)
	}
}

func TestTheUnitIsOptionalAndDefaultsToTheArticlesFirstUnit(t *testing.T) {
	lb := newListBox(t)
	id := lb.shopList(t)
	status, e := lb.add(t, id, map[string]any{"article_id": "eggs", "amount": 1})
	if status != http.StatusCreated || e["unit"] != "dozen" {
		t.Fatalf("%d %v", status, e)
	}
	// ... or to the unit used last for it.
	lb.add(t, id, map[string]any{"article_id": "milk", "amount": 3, "unit": "pack"})
	_, e = lb.add(t, id, map[string]any{"article_id": "milk", "amount": 2})
	if e["unit"] != "pack" || e["amount"] != 5.0 {
		t.Errorf("last used unit: %v", e)
	}
}

func TestInvalidQuantitiesAreRefused(t *testing.T) {
	lb := newListBox(t)
	id := lb.shopList(t)
	for name, body := range map[string]map[string]any{
		"zero":              {"article_id": "eggs", "amount": 0, "unit": "piece"},
		"negative":          {"article_id": "eggs", "amount": -1, "unit": "piece"},
		"too much":          {"article_id": "eggs", "amount": 10000, "unit": "piece"},
		"unknown unit":      {"article_id": "eggs", "amount": 1, "unit": "stone"},
		"unit switched off": {"article_id": "eggs", "amount": 1, "unit": "lb"},
		"unknown article":   {"article_id": "unicorn", "amount": 1, "unit": "piece"},
		"no text":           {"amount": 1, "unit": "piece"},
	} {
		if status, e := lb.add(t, id, body); status != http.StatusUnprocessableEntity && status != http.StatusBadRequest {
			t.Errorf("%s = %d %v", name, status, e)
		}
	}
	if n := len(entryTexts(t, lb.parent, id)); n != 0 {
		t.Errorf("%d entries", n)
	}
}

func TestAListWithoutSuggestionsTakesPlainTextOnly(t *testing.T) {
	lb := newListBox(t)
	_, l, _ := lb.parent.call(http.MethodPost, "/lists", map[string]any{"name": "To-dos", "color": "blue", "symbol": "circle-empty", "suggestions": false})
	id := l["id"].(string)
	if status, e := lb.add(t, id, map[string]any{"text": "Call grandma", "amount": 2, "unit": "piece"}); status != http.StatusUnprocessableEntity {
		t.Errorf("quantity on a to-do list = %d %v", status, e)
	}
	for i := 0; i < 2; i++ { // as before: no merging, no library
		if status, e := lb.add(t, id, map[string]any{"text": "Eier"}); status != http.StatusCreated || e["amount"] != nil {
			t.Fatalf("plain = %d %v", status, e)
		}
	}
	if n := len(entryTexts(t, lb.parent, id)); n != 2 {
		t.Errorf("%d entries", n)
	}
}

func TestChangingAQuantityReplacesItAndSaysWhatItWas(t *testing.T) {
	lb := newListBox(t)
	id := lb.shopList(t)
	_, e := lb.add(t, id, map[string]any{"article_id": "eggs", "amount": 6, "unit": "piece"})
	eid := e["id"].(string)
	status, e, _ := lb.parent.call(http.MethodPatch, "/lists/"+id+"/entries/"+eid, map[string]any{"amount": 10, "unit": "piece"})
	if status != http.StatusOK || e["result"] != "set" || e["amount"] != 10.0 || e["unit"] != "piece" {
		t.Fatalf("%d %v", status, e)
	}
	if prev, _ := e["previous"].(map[string]any); prev["amount"] != 6.0 {
		t.Errorf("previous = %v", e["previous"])
	}
	// No quantity.
	status, e, _ = lb.parent.call(http.MethodPatch, "/lists/"+id+"/entries/"+eid, map[string]any{"amount": nil})
	if status != http.StatusOK {
		t.Fatalf("%d %v", status, e)
	}
	if _, has := e["amount"]; has {
		t.Errorf("no quantity: %v", e)
	}
	// 0 is no quantity.
	if status, _, _ = lb.parent.call(http.MethodPatch, "/lists/"+id+"/entries/"+eid, map[string]any{"amount": 0, "unit": "piece"}); status != http.StatusUnprocessableEntity {
		t.Errorf("0 = %d", status)
	}
}

func TestChangingToAnotherKindMergesIntoTheEntryOfThatKind(t *testing.T) {
	lb := newListBox(t)
	id := lb.shopList(t)
	_, a := lb.add(t, id, map[string]any{"article_id": "rolled-oats", "amount": 2, "unit": "pack"})
	lb.add(t, id, map[string]any{"article_id": "rolled-oats", "amount": 500, "unit": "g"})
	status, e, _ := lb.parent.call(http.MethodPatch, "/lists/"+id+"/entries/"+a["id"].(string), map[string]any{"amount": 250, "unit": "g"})
	if status != http.StatusOK || e["result"] != "merged" || e["amount"] != 750.0 || e["unit"] != "g" || e["id"] == a["id"] {
		t.Fatalf("%d %v", status, e)
	}
	if n := len(entryTexts(t, lb.parent, id)); n != 1 {
		t.Errorf("%d entries", n)
	}
}

func TestChangingAnEntryThatIsTickedOffOrGoneIsAConflict(t *testing.T) {
	lb := newListBox(t)
	id := lb.shopList(t)
	_, e := lb.add(t, id, map[string]any{"article_id": "eggs", "amount": 6, "unit": "piece"})
	eid := e["id"].(string)
	lb.parent.call(http.MethodPut, "/lists/"+id+"/entries/"+eid+"/done", map[string]any{"done": true})
	if status, _, _ := lb.parent.call(http.MethodPatch, "/lists/"+id+"/entries/"+eid, map[string]any{"amount": 7, "unit": "piece"}); status != http.StatusConflict {
		t.Errorf("ticked off = %d", status)
	}
	if status, _, _ := lb.parent.call(http.MethodPatch, "/lists/"+id+"/entries/"+"00000000-0000-4000-8000-000000000000", map[string]any{"amount": 7, "unit": "piece"}); status != http.StatusConflict && status != http.StatusNotFound {
		t.Errorf("gone = %d", status)
	}
}

func TestOnlyParentsChangeQuantitiesOverTheAPI(t *testing.T) {
	lb := newListBox(t)
	id := lb.shopList(t)
	_, e := lb.add(t, id, map[string]any{"article_id": "eggs", "amount": 6, "unit": "piece"})
	if status, _, _ := lb.child.call(http.MethodPatch, "/lists/"+id+"/entries/"+e["id"].(string), map[string]any{"amount": 7, "unit": "piece"}); status != http.StatusForbidden {
		t.Errorf("child = %d", status)
	}
}

func TestUnitsOverTheAPIFollowTheCountryUntilAParentChangesThem(t *testing.T) {
	c := signedInAPI(t)
	status, u, _ := c.call(http.MethodGet, "/units", nil)
	if status != http.StatusOK || u["custom"] != false || u["country"] != "DE" {
		t.Fatalf("%d %v", status, u)
	}
	active := u["active"].([]any)
	if len(active) != 8 || active[0] != "piece" {
		t.Errorf("preset for DE = %v", active)
	}
	if all := u["units"].([]any); len(all) != 18 {
		t.Errorf("units = %v", all)
	}
	status, u, _ = c.call(http.MethodPut, "/units", map[string]any{"active": []string{"piece", "g", "kg", "l", "oz"}})
	if status != http.StatusOK || u["custom"] != true || len(u["active"].([]any)) != 5 {
		t.Fatalf("%d %v", status, u)
	}
	// Eggs now offer pieces only (dozen is off): a unit that is off is refused.
	lb := listBox{taskBox{parent: c}}
	id := lb.shopList(t)
	if status, e := lb.add(t, id, map[string]any{"article_id": "eggs", "amount": 1, "unit": "dozen"}); status != http.StatusUnprocessableEntity {
		t.Errorf("dozen is off = %d %v", status, e)
	}
	// Each of weight and volume keeps one.
	status, e, _ := c.call(http.MethodPut, "/units", map[string]any{"active": []string{"piece", "g"}})
	if status != http.StatusUnprocessableEntity {
		t.Errorf("no volume = %d %v", status, e)
	}
	status, u, _ = c.call(http.MethodDelete, "/units", nil)
	if status != http.StatusOK || u["custom"] != false || len(u["active"].([]any)) != 8 {
		t.Fatalf("reset = %d %v", status, u)
	}
}

func TestAnEntryMergesOnlyWithOpenEntriesOverTheAPI(t *testing.T) {
	lb := newListBox(t)
	id := lb.shopList(t)
	_, e := lb.add(t, id, map[string]any{"article_id": "sugar", "amount": 500, "unit": "g"})
	lb.parent.call(http.MethodPut, "/lists/"+id+"/entries/"+e["id"].(string)+"/done", map[string]any{"done": true})
	status, e2 := lb.add(t, id, map[string]any{"article_id": "sugar", "amount": 1, "unit": "kg"})
	if status != http.StatusCreated || e2["id"] == e["id"] {
		t.Errorf("%d %v", status, e2)
	}
}

func TestAnAmountReadBackFromTheAPIKeepsTheTotal(t *testing.T) {
	lb := newListBox(t)
	id := lb.shopList(t)
	_, e := lb.add(t, id, map[string]any{"article_id": "eggs", "amount": 5, "unit": "piece"})
	eid := e["id"].(string)
	// A client that rounds 5 pieces to 0.42 dozen and sends that back changes nothing.
	status, e, _ := lb.parent.call(http.MethodPatch, "/lists/"+id+"/entries/"+eid, map[string]any{"amount": 0.42, "unit": "dozen"})
	if status != http.StatusOK || e["amount"] != 5.0 || e["unit"] != "piece" {
		t.Errorf("%d %v", status, e)
	}
	// Another number is the new amount.
	status, e, _ = lb.parent.call(http.MethodPatch, "/lists/"+id+"/entries/"+eid, map[string]any{"amount": 0.5, "unit": "dozen"})
	if status != http.StatusOK || e["amount"] != 6.0 {
		t.Errorf("%d %v", status, e)
	}
}

func TestTheArticleAPIOffersTheNearestActiveUnitInPlaceOfOneThatIsOff(t *testing.T) {
	c := signedInAPI(t)
	if status, u, _ := c.call(http.MethodPut, "/units", map[string]any{"active": []string{"piece", "g", "l"}}); status != http.StatusOK {
		t.Fatalf("%d %v", status, u)
	}
	// Bananas default to piece and kg; kg is off, so g takes its place, as on the add card.
	status, a, _ := c.call(http.MethodGet, "/articles/bananas", nil)
	got, _ := a["offered_units"].([]any)
	if status != http.StatusOK || len(got) != 2 || got[0] != "piece" || got[1] != "g" {
		t.Errorf("%d offered = %v", status, a["offered_units"])
	}
}

// The stored link is the article of an entry; the text names one only for an
// entry that has none, and never a hand-hidden article (as when adding).
func TestAHandHiddenArticleTypedAsTextStaysFreeTextInTheAPIAnswers(t *testing.T) {
	lb := newListBox(t)
	id := lb.shopList(t)
	if status, _, _ := lb.parent.call(http.MethodPut, "/articles/eggs/hidden", map[string]any{"hidden": true}); status != http.StatusOK {
		t.Fatalf("hide = %d", status)
	}
	status, e := lb.add(t, id, map[string]any{"text": "Eier"})
	if status != http.StatusCreated || e["article_id"] != nil || e["symbol"] != nil {
		t.Fatalf("typed = %d %v", status, e)
	}
	if got := entryOf(t, lb.parent, id, 0); got["article_id"] != nil || got["symbol"] != nil {
		t.Errorf("listed: %v", got)
	}
	// Picked by hand, the stored link stays whatever the library says now.
	other := lb.newList(t, "Wocheneinkauf", "manual")
	status, e = lb.add(t, other, map[string]any{"article_id": "eggs", "amount": 6, "unit": "piece"})
	if status != http.StatusCreated || e["article_id"] != "eggs" || e["symbol"] == nil {
		t.Fatalf("picked = %d %v", status, e)
	}
}

// Ticket 277: varieties over REST.

func TestAVarietyKeepsEntriesApartAndTheSameVarietyAddsUpOverTheAPI(t *testing.T) {
	lb := newListBox(t)
	id := lb.shopList(t)
	status, e := lb.add(t, id, map[string]any{"article_id": "wheat-flour", "amount": 500, "unit": "g"})
	if status != http.StatusCreated || e["variety"] != nil {
		t.Fatalf("%d %v", status, e)
	}
	status, e = lb.add(t, id, map[string]any{"article_id": "wheat-flour", "variety": "  Type   405 ", "amount": 1, "unit": "kg"})
	if status != http.StatusCreated || e["variety"] != "Type 405" || e["result"] != "added" {
		t.Fatalf("two entries: %d %v", status, e)
	}
	status, e = lb.add(t, id, map[string]any{"article_id": "wheat-flour", "variety": "type 405", "amount": 1, "unit": "kg"})
	if status != http.StatusOK || e["result"] != "added_to" || e["amount"] != 2.0 || e["variety"] != "Type 405" {
		t.Fatalf("they add up: %d %v", status, e)
	}
	if first := entryOf(t, lb.parent, id, 0); first["variety"] != nil || first["amount"] != 500.0 {
		t.Errorf("%v", first)
	}
	if second := entryOf(t, lb.parent, id, 1); second["variety"] != "Type 405" {
		t.Errorf("GET carries the variety: %v", second)
	}
	// Adding without a quantity exists only for article and variety both equal.
	status, e = lb.add(t, id, map[string]any{"article_id": "sugar", "variety": "braun"})
	if status != http.StatusCreated {
		t.Fatalf("%d %v", status, e)
	}
	status, e = lb.add(t, id, map[string]any{"article_id": "sugar", "variety": "Braun"})
	if status != http.StatusOK || e["result"] != "exists" {
		t.Errorf("%d %v", status, e)
	}
	status, e = lb.add(t, id, map[string]any{"article_id": "sugar"})
	if status != http.StatusCreated {
		t.Errorf("sugar next to brown sugar: %d %v", status, e)
	}
}

func TestAVarietyNeedsAnArticleAndAtMostSixtyCharactersOverTheAPI(t *testing.T) {
	lb := newListBox(t)
	id := lb.shopList(t)
	status, e := lb.add(t, id, map[string]any{"text": "Quinoa-Pops", "variety": "bunt"})
	if status != http.StatusUnprocessableEntity || e["field"] != "variety" {
		t.Errorf("free text has no variety: %d %v", status, e)
	}
	status, e = lb.add(t, id, map[string]any{"article_id": "sugar", "variety": strings.Repeat("x", 61)})
	if status != http.StatusUnprocessableEntity || e["field"] != "variety" {
		t.Errorf("too long: %d %v", status, e)
	}
	status, e = lb.add(t, id, map[string]any{"article_id": "sugar", "variety": strings.Repeat("ä", 60)})
	if status != http.StatusCreated {
		t.Errorf("60 characters are fine: %d %v", status, e)
	}
	// A list without suggestions has no articles and so no variety.
	_, l, _ := lb.parent.call(http.MethodPost, "/lists", map[string]any{"name": "To-dos", "color": "blue", "symbol": "circle-empty", "suggestions": false})
	status, e = lb.add(t, l["id"].(string), map[string]any{"text": "Anruf", "variety": "x"})
	if status != http.StatusUnprocessableEntity {
		t.Errorf("%d %v", status, e)
	}
}

func TestPatchingTheVarietyMergesAndNotesIt(t *testing.T) {
	lb := newListBox(t)
	id := lb.shopList(t)
	_, a := lb.add(t, id, map[string]any{"article_id": "wheat-flour", "amount": 500, "unit": "g"})
	lb.add(t, id, map[string]any{"article_id": "wheat-flour", "variety": "Type 405", "amount": 1, "unit": "kg"})
	path := "/lists/" + id + "/entries/" + a["id"].(string)
	status, e, _ := lb.parent.call(http.MethodPatch, path, map[string]any{"variety": "Type 550"})
	if status != http.StatusOK || e["result"] != "set" || e["variety"] != "Type 550" || e["amount"] != 500.0 {
		t.Fatalf("only the variety: %d %v", status, e)
	}
	status, e, _ = lb.parent.call(http.MethodPatch, path, map[string]any{"variety": "type 405"})
	if status != http.StatusOK || e["result"] != "merged" || e["variety"] != "Type 405" || e["amount"] != 1.5 || e["id"] == a["id"] {
		t.Fatalf("merged: %d %v", status, e)
	}
	e2 := entryOf(t, lb.parent, id, 0)
	status, e, _ = lb.parent.call(http.MethodPatch, "/lists/"+id+"/entries/"+e2["id"].(string), map[string]any{"variety": nil})
	if status != http.StatusOK || e["variety"] != nil {
		t.Errorf("null takes it away: %d %v", status, e)
	}
	status, e, _ = lb.parent.call(http.MethodPatch, "/lists/"+id+"/entries/"+e2["id"].(string), map[string]any{"variety": strings.Repeat("x", 61)})
	if status != http.StatusUnprocessableEntity || e["field"] != "variety" {
		t.Errorf("%d %v", status, e)
	}
	_, f := lb.add(t, id, map[string]any{"text": "Quinoa-Pops"})
	status, e, _ = lb.parent.call(http.MethodPatch, "/lists/"+id+"/entries/"+f["id"].(string), map[string]any{"variety": "bunt"})
	if status != http.StatusUnprocessableEntity || e["field"] != "variety" {
		t.Errorf("free text: %d %v", status, e)
	}
	if status, _, _ := lb.parent.call(http.MethodPatch, path, map[string]any{}); status != http.StatusBadRequest {
		t.Errorf("neither amount nor variety: %d", status)
	}
}

func TestTypedPluralsNameTheArticleOverTheAPI(t *testing.T) {
	lb := newListBox(t)
	id := lb.shopList(t)
	status, e := lb.add(t, id, map[string]any{"text": "Zitrone"})
	if status != http.StatusCreated || e["article_id"] != "lemons" {
		t.Errorf("%d %v", status, e)
	}
}

// A German plural is not folded onto an English name: "Beeren" is not "Beer".
func TestAGermanPluralDoesNotTurnIntoAnEnglishArticleOverTheAPI(t *testing.T) {
	lb := newListBox(t)
	id := lb.shopList(t)
	status, e := lb.add(t, id, map[string]any{"text": "Beeren"})
	if status != http.StatusCreated || e["article_id"] != nil && e["article_id"] != "" {
		t.Fatalf("free text: %d %v", status, e)
	}
}

func TestPatchingOnlyTheVarietyOfAListWithoutSuggestionsNamesTheVarietyField(t *testing.T) {
	lb := newListBox(t)
	id := lb.shopList(t)
	eid := lb.addEntry(t, id, "Mehl")
	lb.parent.call(http.MethodPatch, "/lists/"+id, map[string]any{"suggestions": false})
	for body, field := range map[string]string{`variety`: "variety", `amount`: "amount"} {
		req := map[string]any{body: "Type 405"}
		if body == "amount" {
			req = map[string]any{"amount": 2, "unit": "piece"}
		}
		status, e, _ := lb.parent.call(http.MethodPatch, "/lists/"+id+"/entries/"+eid, req)
		if status != http.StatusUnprocessableEntity || e["field"] != field {
			t.Errorf("%s: %d %v", body, status, e)
		}
	}
}
