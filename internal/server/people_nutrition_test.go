package server_test

import (
	"net/http"
	"net/url"
	"strings"
	"testing"
)

// Ticket 288: the switch "Nährwerte bei Rezepten" on the person page of a child.

func TestTheChildsPageHasTheSwitchOffAtFirstAndAdultsHaveNone(t *testing.T) {
	t.Parallel()
	rb := newRecipeBox(t)
	mia := rb.b.do(http.MethodGet, "/people/"+rb.mia.ID, nil, rcpDE).body
	rcpHas(t, "Mia's page", mia, `<fieldset id="rcp"><legend>Rezepte</legend>`, "<b>Nährwerte bei Rezepten</b>", `name="nutrition"`,
		`<span class="on-text"><b>An.</b> Mia sieht bei jedem Rezept die Nährwerte pro Portion.</span>`,
		`<span class="off-text"><b>Aus.</b> Mia sieht bei Rezepten keine Nährwerte. Zutaten, Schritte und Allergie-Warnungen bleiben.</span>`,
		"Für Kinder ist das am Anfang aus. Eltern sehen die Nährwerte immer, Wandbildschirme nie.", `aria-label="Nährwerte bei Rezepten für Mia"`)
	i := strings.Index(mia, `name="nutrition"`)
	if strings.Contains(mia[i:i+strings.Index(mia[i:], ">")], "checked") {
		t.Error("the switch must be off until a parent switches it on")
	}
	if !(strings.Index(mia, "Routinen") < strings.Index(mia, `id="rcp"`)) {
		t.Error("the fieldset comes after the routines")
	}
	mama := rb.b.do(http.MethodGet, "/people/"+rb.mama.ID, nil, rcpDE).body
	rcpLacks(t, "Mama's page", mama, `name="nutrition"`, "Nährwerte bei Rezepten")
}

func TestTheSwitchIsSavedWithTheOneSaveButton(t *testing.T) {
	t.Parallel()
	rb := newRecipeBox(t)
	form := url.Values{"name": {"Mia"}, "streaks": {"on"}, "nutrition": {"on"}}
	r := rb.b.do(http.MethodPost, "/people/"+rb.mia.ID, form, rcpDE)
	if r.status != http.StatusSeeOther {
		t.Fatalf("save = %d\n%s", r.status, r.body)
	}
	if got, _ := rb.b.db.Person(rb.hid, rb.mia.ID); !got.NutritionShown {
		t.Fatal("the switch did not turn on")
	}
	rcpHas(t, "the success line", rb.b.do(http.MethodGet, r.location, nil, rcpDE).body, "Mia sieht jetzt bei Rezepten die Nährwerte pro Portion.")
	on := rb.b.do(http.MethodGet, "/people/"+rb.mia.ID, nil, rcpDE).body
	i := strings.Index(on, `name="nutrition"`)
	if !strings.Contains(on[i:i+strings.Index(on[i:], ">")], "checked") {
		t.Error("the page does not show it on")
	}
	// Saving again without the switch turns it off.
	r = rb.b.do(http.MethodPost, "/people/"+rb.mia.ID, url.Values{"name": {"Mia"}, "streaks": {"on"}}, rcpDE)
	if got, _ := rb.b.db.Person(rb.hid, rb.mia.ID); got.NutritionShown {
		t.Fatal("the switch did not turn off")
	}
	rcpHas(t, "the success line", rb.b.do(http.MethodGet, r.location, nil, rcpDE).body, "Mia sieht bei Rezepten keine Nährwerte mehr.")
	// Saving with the switch as it was says only that the person was saved.
	r = rb.b.do(http.MethodPost, "/people/"+rb.mia.ID, url.Values{"name": {"Mia"}, "streaks": {"on"}}, enHeader)
	rcpLacks(t, "a plain save", rb.b.do(http.MethodGet, r.location, nil, enHeader).body, "nutrition", "recipes")
}

func TestAnAdultCannotGetTheSwitchThroughTheForm(t *testing.T) {
	t.Parallel()
	rb := newRecipeBox(t)
	if r := rb.b.do(http.MethodPost, "/people/"+rb.mama.ID, url.Values{"name": {"Mama"}, "nutrition": {"on"}}, rcpDE); r.status != http.StatusSeeOther {
		t.Fatalf("save = %d", r.status)
	}
	if got, _ := rb.b.db.Person(rb.hid, rb.mama.ID); got.NutritionShown {
		t.Error("adults have no switch")
	}
}

func TestARejectedPersonFormKeepsTheNutritionSwitchTheParentSet(t *testing.T) {
	t.Parallel()
	rb := newRecipeBox(t)
	checked := func(body string) bool {
		i := strings.Index(body, `name="nutrition"`)
		if i < 0 {
			t.Fatalf("no switch on the page:\n%s", body)
		}
		return strings.Contains(body[i:i+strings.Index(body[i:], ">")], "checked")
	}
	on := rb.b.do(http.MethodPost, "/people/"+rb.mia.ID, url.Values{"name": {""}, "nutrition": {"on"}}, enHeader)
	if on.status != http.StatusUnprocessableEntity || !checked(on.body) {
		t.Errorf("422 with the switch on: status %d, checked %v", on.status, checked(on.body))
	}
	off := rb.b.do(http.MethodPost, "/people/"+rb.mia.ID, url.Values{"name": {""}}, enHeader)
	if off.status != http.StatusUnprocessableEntity || checked(off.body) {
		t.Errorf("422 with the switch off: status %d, checked %v", off.status, checked(off.body))
	}
	if got, _ := rb.b.db.Person(rb.hid, rb.mia.ID); got.NutritionShown {
		t.Error("a rejected form changed the stored switch")
	}
}

func TestThePersonCarriesTheNutritionSwitchInTheAPI(t *testing.T) {
	t.Parallel()
	tb := newTaskBox(t)
	_, mia, _ := tb.parent.call(http.MethodGet, "/people/"+tb.miaID, nil)
	_, mama, _ := tb.parent.call(http.MethodGet, "/people/"+tb.mamaID, nil)
	if mia["nutrition_shown"] != false || mama["nutrition_shown"] != false {
		t.Fatalf("off for everyone at first: Mia %v, Mama %v", mia["nutrition_shown"], mama["nutrition_shown"])
	}
	status, got, _ := tb.parent.call(http.MethodPatch, "/people/"+tb.miaID, map[string]any{"nutrition_shown": true})
	if status != http.StatusOK || got["nutrition_shown"] != true || got["name"] != "Mia" {
		t.Fatalf("patch = %d %v", status, got)
	}
	_, again, _ := tb.parent.call(http.MethodGet, "/people/"+tb.miaID, nil)
	if again["nutrition_shown"] != true {
		t.Errorf("not stored: %v", again)
	}
	if status, _, _ := tb.parent.call(http.MethodPatch, "/people/"+tb.mamaID, map[string]any{"nutrition_shown": true}); status != http.StatusUnprocessableEntity {
		t.Errorf("an adult has no switch: patch = %d, want 422", status)
	}
}
