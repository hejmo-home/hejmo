package server_test

import (
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"testing"
)

// Ticket 163 (ADR 0019, preview-163.html): the web pages of diet and allergies.

func (p peopleBox) foodPage(path, lang string) resp {
	return p.do(http.MethodGet, path, nil, map[string]string{"Accept-Language": lang})
}

func (p peopleBox) saveFood(id string, f url.Values) resp {
	return p.do(http.MethodPost, "/people/"+id+"/food", f, map[string]string{"Accept-Language": "en"})
}

func foodHas(t *testing.T, what, page string, want ...string) {
	t.Helper()
	for _, w := range want {
		if !strings.Contains(page, w) {
			t.Errorf("%s lacks %q\n%s", what, w, page)
		}
	}
}

func TestPersonPageHasTheCalmFoodRowWhichOpensItsOwnPage(t *testing.T) {
	p := newPeopleBox(t)
	mia := p.add(t, "Mia", "child")["id"].(string)
	page := p.en("/people/" + mia).body
	foodHas(t, "person page", page, `<h2 class="rows-head">Diet and allergies</h2>`, `<ul class="list-rows food-row">`, `href="/people/`+mia+`/food"`, "<b>Eats everything</b>", "No allergies entered. Tap here")
	papa := p.add(t, "Papa", "parent")["id"].(string)
	foodHas(t, "adult page", p.en("/people/"+papa).body, `href="/people/`+papa+`/food"`)
}

func TestFoodPageOffersFourDietsAndFourteenAllergensInTheOrderOfThePageLanguage(t *testing.T) {
	p := newPeopleBox(t)
	mia := p.add(t, "Mia", "child")["id"].(string)
	de := p.foodPage("/people/"+mia+"/food", "de")
	if de.status != http.StatusOK {
		t.Fatalf("GET = %d", de.status)
	}
	foodHas(t, "food page", de.body, "<h1>Essen und Allergien</h1>", `<a href="/people/`+mia+`">Mia</a>`, "Bleibt in eurer Box", `class="why health"`,
		`<form method="post" action="/people/`+mia+`/food" class="steps food-form">`, "Wie isst Mia?",
		`value="omnivore" checked`, "Experimentell, ohne Gewähr:", `class="hint caveat"`, `name="other"`, `maxlength="200"`, "Das prüft Hejmo nicht automatisch und kann nicht danach filtern.")
	if n := strings.Count(de.body, `type="radio" name="diet"`); n != 4 {
		t.Errorf("%d diets", n)
	}
	re := regexp.MustCompile(`name="allergens" value="[a-z]+"><b>([^<]*)</b>`)
	var got []string
	for _, m := range re.FindAllStringSubmatch(de.body, -1) {
		got = append(got, m[1])
	}
	want := "Eier, Erdnüsse, Fisch, Glutenhaltiges Getreide, Krebstiere, Lupinen, Milch und Laktose, Schalenfrüchte (Nüsse), Schwefeldioxid und Sulfite, Sellerie, Senf, Sesam, Soja, Weichtiere"
	if strings.Join(got, ", ") != want {
		t.Errorf("German order = %q", strings.Join(got, ", "))
	}
	en := p.foodPage("/people/"+mia+"/food", "en")
	got = nil
	for _, m := range re.FindAllStringSubmatch(en.body, -1) {
		got = append(got, m[1])
	}
	want = "Celery, Cereals containing gluten, Crustaceans, Eggs, Fish, Lupin, Milk and lactose, Molluscs, Mustard, Peanuts, Sesame, Soy, Sulphur dioxide and sulphites, Tree nuts"
	if strings.Join(got, ", ") != want {
		t.Errorf("English order = %q", strings.Join(got, ", "))
	}
	foodHas(t, "English page", en.body, "Experimental, no guarantee:", "Stays in your box")
}

func TestSavingFoodReturnsToThePersonPageAndShowsWhatNowApplies(t *testing.T) {
	p := newPeopleBox(t)
	mia := p.add(t, "Mia", "child")["id"].(string)
	r := p.saveFood(mia, url.Values{"diet": {"vegetarian"}, "allergens": {"nuts", "peanuts"}, "other": {" Kiwi ,, Erdbeeren"}})
	if r.status != http.StatusSeeOther || r.location != "/people/"+mia {
		t.Fatalf("save = %d %q\n%s", r.status, r.location, r.body)
	}
	page := p.en("/people/" + mia).body
	foodHas(t, "person page", page, "Diet and allergies of Mia saved.", "<b>Vegetarian</b>", "Allergies: Peanuts, Tree nuts",
		`class="unchecked"`, "Not checked automatically, not filterable: Kiwi, Erdbeeren")
	if got := p.foodPage("/people/"+mia+"/food", "en").body; !strings.Contains(got, `value="vegetarian" checked`) || !strings.Contains(got, `value="peanuts" checked`) || !strings.Contains(got, `value="Kiwi, Erdbeeren"`) {
		t.Errorf("the form does not show what is stored\n%s", got)
	}
	// Through the API too: the same data.
	_, f, _ := p.api.call(http.MethodGet, "/people/"+mia+"/food", nil)
	if f["diet"] != "vegetarian" {
		t.Errorf("api = %v", f)
	}
}

func peopleRow(page, name string) string {
	for _, r := range regexp.MustCompile(`(?s)<li data-person-row="[^"]*">.*?</li>`).FindAllString(page, -1) {
		if strings.Contains(r, ">"+name+"</a>") {
			return r
		}
	}
	return ""
}

func TestPeopleOverviewNamesDietAndAllergiesOnlyForThoseWhoNeedIt(t *testing.T) {
	p := newPeopleBox(t)
	mia := p.add(t, "Mia", "child")["id"].(string)
	p.add(t, "Sebastian", "child")
	p.saveFood(mia, url.Values{"diet": {"vegan"}, "allergens": {"soy", "eggs", "milk"}, "other": {"Kiwi"}})
	page := p.en("/people").body
	foodHas(t, "Mia's row", peopleRow(page, "Mia"), `<p class="food">`, `<span class="diet">Vegan</span>`, "Allergies: Eggs, Milk and lactose, Soy", "Not checked automatically, not filterable: Kiwi")
	for _, name := range []string{"Sebastian", "Mama"} {
		if strings.Contains(peopleRow(page, name), `class="food"`) {
			t.Errorf("%s eats everything without allergies and gets no line", name)
		}
	}
	// Only the diet is given for someone with no allergies.
	p.saveFood(mia, url.Values{"diet": {"pescatarian"}})
	if row := peopleRow(p.en("/people").body, "Mia"); !strings.Contains(row, `<span class="diet">Pescatarian</span>`) || strings.Contains(row, "Allergies") {
		t.Errorf("row = %s", row)
	}
}

func TestFreeTextNamingOneOfTheFourteenIsRefusedAtTheFieldAndNothingIsStored(t *testing.T) {
	p := newPeopleBox(t)
	mia := p.add(t, "Mia", "child")["id"].(string)
	r := p.saveFood(mia, url.Values{"diet": {"vegetarian"}, "allergens": {"peanuts"}, "other": {"Laktose, Kiwi"}})
	if r.status != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d", r.status)
	}
	foodHas(t, "refusal", r.body, `class="f invalid"`, `aria-invalid="true"`, `id="err-other"`, "“Laktose” is already in the list above: tick “Milk and lactose”", `value="Laktose, Kiwi"`, `value="peanuts" checked`)
	if _, f, _ := p.api.call(http.MethodGet, "/people/"+mia+"/food", nil); f["diet"] != "omnivore" {
		t.Errorf("a refused form changed something: %v", f)
	}
	for _, ok := range []string{"Haselnuss", "Weizen"} {
		if r := p.saveFood(mia, url.Values{"diet": {"vegan"}, "other": {ok}}); r.status != http.StatusSeeOther {
			t.Errorf("%q = %d", ok, r.status)
		}
	}
	long := p.saveFood(mia, url.Values{"diet": {"vegan"}, "other": {strings.Repeat("a", 201)}})
	if long.status != http.StatusUnprocessableEntity || !strings.Contains(long.body, "At most 200 characters") {
		t.Errorf("too long = %d", long.status)
	}
	// The limit counts the stored text, as the API does: duplicates and blanks do not use it up.
	if r := p.saveFood(mia, url.Values{"diet": {"vegan"}, "other": {strings.Repeat("Kiwi  ,  ,", 30)}}); r.status != http.StatusSeeOther {
		t.Errorf("text that fits once normalised = %d", r.status)
	}
	if bad := p.saveFood(mia, url.Values{"diet": {"keto"}}); bad.status != http.StatusUnprocessableEntity {
		t.Errorf("unknown diet = %d", bad.status)
	}
	if bad := p.saveFood(mia, url.Values{"diet": {"vegan"}, "allergens": {"kiwi"}}); bad.status != http.StatusUnprocessableEntity {
		t.Errorf("unknown allergen = %d", bad.status)
	}
}

func TestChildrenCannotOpenOrSaveTheFoodPage(t *testing.T) {
	b := signedInBox(t)
	child := childSession(t, b)
	h, _ := b.db.HomeHousehold()
	people, _ := b.db.People(h.ID)
	var mia string
	for _, x := range people {
		if x.Name == "Mia" {
			mia = x.ID
		}
	}
	if r := child.get("/people/" + mia + "/food"); r.status != http.StatusForbidden {
		t.Errorf("child GET = %d", r.status)
	}
	if r := child.post("/people/"+mia+"/food", url.Values{"diet": {"vegan"}}); r.status != http.StatusForbidden {
		t.Errorf("child POST = %d", r.status)
	}
	if f, _ := b.db.PersonFood(h.ID, mia); f.Diet != "omnivore" {
		t.Errorf("a child changed their diet: %+v", f)
	}
}

func TestDisplaysNeverShowDietOrAllergies(t *testing.T) {
	p := newPeopleBox(t)
	mia := p.add(t, "Mia", "child")["id"].(string)
	p.saveFood(mia, url.Values{"diet": {"vegan"}, "allergens": {"peanuts"}, "other": {"Kiwi"}})
	for _, path := range []string{"/display", "/display/person/" + mia} {
		page := p.display.get(path, "en").body
		for _, leak := range []string{"Vegan", "Peanuts", "Kiwi", "llerg"} {
			if strings.Contains(page, leak) {
				t.Errorf("%s shows %q", path, leak)
			}
		}
	}
}

func TestMCPTokenFormSetsHealthApartAndOffersNoChange(t *testing.T) {
	b := signedInBox(t)
	switchMCPOnPage(t, b, true)
	page := getEN(b, "/mcp/tokens/new").body
	block := regexp.MustCompile(`(?s)<div class="scope health">.*?</div>`).FindString(page)
	if block == "" {
		t.Fatalf("no health area\n%s", page)
	}
	foodHas(t, "health area", block, "Diet and allergies", `<svg class="icon"`, `name="health" value="none" checked`, `name="health" value="read"`)
	if strings.Contains(block, `value="write"`) {
		t.Error("health data offers a change level")
	}
	foodHas(t, "form", page, "not checked automatically, not filterable")
}
