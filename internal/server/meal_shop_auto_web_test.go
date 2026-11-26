package server_test

import (
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/hejmo-home/hejmo/internal/store"
)

// Ticket 285, the web page "Einkaufsliste aus dem Essensplan" in Settings,
// Lists (ADR 0015: the same setting as meal_shopping of the API).

const shopAutoPath = "/settings/lists/meal-shopping"

func TestTheListsSettingsLinkToTheAutomaticShoppingListAndTheDefaultIsByHand(t *testing.T) {
	t.Parallel()
	sb := newShopBox(t)
	hub := sb.b.do(http.MethodGet, "/settings/lists", nil, enHeader).body
	rcpHas(t, "the lists settings", hub, `href="`+shopAutoPath+`"`, "Shopping list from the meal plan", "By hand")
	r := sb.b.do(http.MethodGet, shopAutoPath, nil, enHeader)
	if r.status != http.StatusOK {
		t.Fatalf("GET = %d", r.status)
	}
	rcpHas(t, "the page", r.body, "<h1>Shopping list from the meal plan</h1>", `role="switch" name="auto"`, `name="days"`, `value="7"`, `name="list"`, "Every day at 3:00")
	rcpLacks(t, "the page", r.body, "checked")
}

// Acceptance: the household chooses automatic, the days and the list; the page says so after saving.
func TestSavingTheAutomaticShoppingListStoresTheHouseholdSetting(t *testing.T) {
	t.Parallel()
	sb := newShopBox(t)
	r := sb.b.do(http.MethodPost, shopAutoPath, url.Values{"auto": {"on"}, "days": {"3"}, "list": {sb.list.ID}}, enHeader)
	if r.status != http.StatusSeeOther || r.location != shopAutoPath {
		t.Fatalf("POST = %d %q", r.status, r.location)
	}
	got, err := sb.b.db.MealShopAuto(sb.hid)
	if err != nil || !got.On || got.Days != 3 || got.ListID != sb.list.ID {
		t.Fatalf("stored = %+v %v", got, err)
	}
	if got.Lang != "en" {
		t.Errorf("the web form stores the language of the parent: %q, want en", got.Lang)
	}
	page := sb.b.do(http.MethodGet, shopAutoPath, nil, enHeader).body
	rcpHas(t, "the page after saving", page, "Saved", `name="auto"`, "checked", `value="3"`, `value="`+sb.list.ID+`" selected`)
	// switching it off keeps the days and the list
	sb.b.do(http.MethodPost, shopAutoPath, url.Values{"days": {"3"}, "list": {sb.list.ID}}, enHeader)
	if got, _ := sb.b.db.MealShopAuto(sb.hid); got.On || got.Days != 3 {
		t.Errorf("by hand again = %+v", got)
	}
}

func TestTheAutomaticShoppingListRefusesWrongDaysAndSavesNothing(t *testing.T) {
	t.Parallel()
	sb := newShopBox(t)
	for _, days := range []string{"0", "15", "x", ""} {
		r := sb.b.do(http.MethodPost, shopAutoPath, url.Values{"auto": {"on"}, "days": {days}}, enHeader)
		if r.status != http.StatusUnprocessableEntity || !strings.Contains(r.body, `aria-invalid="true"`) || !strings.Contains(r.body, "1 to 14") {
			t.Errorf("days %q = %d", days, r.status)
		}
	}
	if got, _ := sb.b.db.MealShopAuto(sb.hid); got.On {
		t.Errorf("a refused form saved: %+v", got)
	}
	if r := sb.b.do(http.MethodPost, shopAutoPath, url.Values{"auto": {"on"}, "days": {"7"}, "list": {"nope"}}, enHeader); r.status != http.StatusUnprocessableEntity {
		t.Errorf("an unknown list = %d", r.status)
	}
}

func TestOnlyParentsSeeTheAutomaticShoppingListSetting(t *testing.T) {
	t.Parallel()
	sb := newShopBox(t)
	if r := sb.child.do(http.MethodGet, shopAutoPath, nil, enHeader); r.status != http.StatusForbidden {
		t.Errorf("a child's GET = %d", r.status)
	}
	if r := sb.child.do(http.MethodPost, shopAutoPath, url.Values{"auto": {"on"}, "days": {"7"}}, enHeader); r.status != http.StatusForbidden {
		t.Errorf("a child's POST = %d", r.status)
	}
	if got, _ := sb.b.db.MealShopAuto(sb.hid); got != (store.MealShopAuto{Days: 7}) {
		t.Errorf("stored = %+v", got)
	}
}

// A list that lost its suggestions is no longer offered; the page says so
// instead of silently showing another choice.
func TestThePageSaysWhenTheChosenListLostItsSuggestions(t *testing.T) {
	t.Parallel()
	sb := newShopBox(t)
	if err := sb.b.db.SetMealShopAuto(sb.hid, store.MealShopAuto{On: true, Days: 7, ListID: sb.list.ID, Lang: "en"}); err != nil {
		t.Fatal(err)
	}
	rcpLacks(t, "the page while the list is fine", sb.b.do(http.MethodGet, shopAutoPath, nil, enHeader).body, "no longer offers suggestions")
	l := sb.list
	l.Suggestions = false
	if _, err := sb.b.db.UpdateList(sb.hid, l); err != nil {
		t.Fatal(err)
	}
	body := sb.b.do(http.MethodGet, shopAutoPath, nil, enHeader).body
	rcpHas(t, "the page", body, "no longer offers suggestions", "“"+sb.list.Name+"”")
}
