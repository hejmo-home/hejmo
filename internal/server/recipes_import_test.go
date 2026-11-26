package server_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/hejmo-home/hejmo/internal/outbound"
	"github.com/hejmo-home/hejmo/internal/picture"
	"github.com/hejmo-home/hejmo/internal/recipeimport"
	"github.com/hejmo-home/hejmo/internal/store"
)

// Ticket 289 (ADR 0015: HTML routes with API parity, preview-289.html): importing a recipe from a web page.

const importPath = "/meals/recipes/import"

func loopbackRecipes() *recipeimport.Fetcher {
	return &recipeimport.Fetcher{Gate: outbound.AllowAll(), Public: func(a netip.Addr) bool { return a.IsLoopback() }, AllowIPLiteral: true, Timeout: loopbackTimeout}
}

func ldPage(ld string) string {
	return `<!doctype html><html><head><title>x</title><script type="application/ld+json">` + ld + `</script></head><body>x</body></html>`
}

const lentilLD = `{"@context":"https://schema.org","@type":"Recipe","name":"Rote Linsensuppe mit Harissa","recipeYield":"4 Portionen","totalTime":"PT35M",
"recipeCategory":"Suppe","keywords":"Suppe, vegetarisch, Linsen","inLanguage":"de","image":"/pic.jpg",
"recipeIngredient":["250 g rote Linsen","1 Zwiebel","1–2 TL Harissa (scharfe Paste)","150 g Naturjoghurt","etwas Salz"],
"recipeInstructions":[{"@type":"HowToStep","text":"Zwiebel würfeln."},{"@type":"HowToStep","text":"Alles <b>kochen</b> &amp; pürieren."}],
"nutrition":{"@type":"NutritionInformation","calories":"380 kcal","fatContent":"17 g","proteinContent":"17 g"}}`

// importBox is the example family with a web server that serves recipe pages and a picture.
type importBox struct {
	recipeBox
	web *httptest.Server
}

func newImportBox(t *testing.T, more ...boxOption) importBox {
	t.Helper()
	pages := map[string]string{}
	jpeg := testJPEG(t, 600, 400)
	var db *store.DB // set below: /offafter switches the recipes connection off once its page is being served
	web := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/pic.jpg":
			w.Header().Set("Content-Type", "image/jpeg")
			w.Write(jpeg)
		case r.URL.Path == "/pdf":
			w.Header().Set("Content-Type", "application/pdf")
			w.Write([]byte("%PDF"))
		case r.URL.Path == "/gone":
			w.WriteHeader(410)
		case r.URL.Path == "/refused":
			w.WriteHeader(403)
		case r.URL.Path == "/offafter":
			db.SetServiceEnabled(outbound.Recipes, false)
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.Write([]byte(pages["/lentils"]))
		case pages[r.URL.Path] != "":
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.Write([]byte(pages[r.URL.Path]))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(web.Close)
	pages["/lentils"] = ldPage(lentilLD)
	pages["/none"] = ldPage(`{"@type":"Article"}`)
	pages["/two"] = ldPage(`{"@graph":[` + lentilLD + `,{"@type":"Recipe","name":"Brot","recipeIngredient":["1 Ei"],"recipeInstructions":"Backen."}]}`)
	b := signedInBox(t, append([]boxOption{withRecipeFetcher(loopbackRecipes()), withFetcher(loopbackFetcher())}, more...)...)
	db = b.db
	h, _ := b.db.HomeHousehold()
	child := childSession(t, b)
	people, _ := b.db.People(h.ID)
	rb := recipeBox{b: b, child: child, hid: h.ID}
	for _, p := range people {
		if p.Name == "Mia" {
			rb.mia = p
		} else if p.Role == store.RoleParent {
			rb.mama = p
		}
	}
	if err := b.db.SetPersonFood(h.ID, rb.mia.ID, store.Food{Diet: "omnivore", Allergens: []string{"milk"}}); err != nil {
		t.Fatal(err)
	}
	return importBox{rb, web}
}

func (ib importBox) read(t *testing.T, path string, header map[string]string) resp {
	t.Helper()
	return ib.b.do(http.MethodPost, importPath, url.Values{"url": {ib.web.URL + path}}, header)
}

// draftOf reads a page and returns the address of its draft.
func (ib importBox) draftOf(t *testing.T, path string) string {
	t.Helper()
	r := ib.read(t, path, rcpDE)
	if r.status != http.StatusSeeOther || !strings.HasPrefix(r.location, importPath+"/") {
		t.Fatalf("reading %s = %d %q\n%s", path, r.status, r.location, rcpShown(r.body))
	}
	return r.location
}

func TestTheImportButtonIsForParentsAndTheImportPageForbidsChildren(t *testing.T) {
	t.Parallel()
	ib := newImportBox(t)
	parent := ib.b.do(http.MethodGet, "/meals/recipes", nil, rcpDE).body
	rcpHas(t, "the list", parent, `<a class="button" href="/meals/recipes/new">`, `<a class="button secondary" href="/meals/recipes/import">`, "Von Webseite importieren")
	if strings.Index(parent, "Neues Rezept") > strings.Index(parent, "Von Webseite importieren") {
		t.Error("the import button stands after the one primary")
	}
	rcpHas(t, "the empty card", rcpBetween(t, parent, `<div class="empty"`, `</div>`), `class="rcp-act"`, "Neues Rezept", "Von Webseite importieren")
	kid := ib.child.do(http.MethodGet, "/meals/recipes", nil, rcpDE).body
	rcpLacks(t, "the child's list", kid, "importieren", "/meals/recipes/import")
	for _, m := range []string{http.MethodGet, http.MethodPost} {
		if r := ib.child.do(m, importPath, url.Values{"url": {"https://x.de"}}, rcpDE); r.status != http.StatusForbidden {
			t.Errorf("child %s = %d, want 403", m, r.status)
		}
	}
}

func TestTheImportPageHasOneFieldAndSaysWhatHejmoTakes(t *testing.T) {
	t.Parallel()
	ib := newImportBox(t)
	r := ib.b.do(http.MethodGet, importPath, nil, rcpDE)
	rcpHas(t, "the import page", r.body, `<h1>Von Webseite importieren</h1>`, `<span aria-current="page">Von Webseite importieren</span>`, `<form class="rcp-imp" method="post"`,
		`name="url" type="url" inputmode="url"`, `placeholder="https://…"`, "Link zum Rezept", `class="imp-takes-h"`, `<ul class="imp-takes">`, "Name, Portionen und Zeit",
		`<button class="button" type="submit">`, "Rezept lesen", `class="imp-wait"`, "Hejmo ruft nur diese eine Seite und ihr Bild auf.", `/static/recipe-import.js`)
	if n := strings.Count(r.body, `<li><svg`); n < 5 {
		t.Errorf("%d tick lines", n)
	}
	en := ib.b.do(http.MethodGet, importPath, nil, enHeader).body
	rcpHas(t, "the English page", en, "Import from a web page", "Read recipe", "Link to the recipe")
}

func TestTheFieldNamesWhatWentWrongAndKeepsTheLink(t *testing.T) {
	t.Parallel()
	ib := newImportBox(t)
	cases := []struct{ name, typed, want string }{
		{"empty", "", "Füge den Link zu einem Rezept ein."},
		{"not a link", "linsensuppe", "Das ist kein Link. Ein Link beginnt mit https:// oder http://."},
		{"no recipe", ib.web.URL + "/none", "kein Rezept gefunden"},
		{"404", ib.web.URL + "/missing", "nicht (mehr)."},
		{"410", ib.web.URL + "/gone", "nicht (mehr)."},
		{"403", ib.web.URL + "/refused", "lässt Hejmo die Seite gerade nicht lesen"},
		{"a file", ib.web.URL + "/pdf", "keine Webseite, sondern eine Datei (PDF)"},
	}
	for _, c := range cases {
		r := ib.b.do(http.MethodPost, importPath, url.Values{"url": {c.typed}}, rcpDE)
		if r.status != http.StatusUnprocessableEntity {
			t.Errorf("%s: status %d", c.name, r.status)
		}
		rcpHas(t, c.name, r.body, `<div class="f invalid">`, `class="ferr"`, c.want, `aria-invalid="true"`, `value="`+c.typed+`"`)
	}
	none := ib.read(t, "/none", rcpDE).body
	rcpHas(t, "no recipe", none, `class="ferr-acts"`, "Selbst schreiben, mit diesem Link als Quelle", "/meals/recipes/new?source="+url.QueryEscape(ib.web.URL+"/none"))
	// a number address and the home network are refused by the real fetcher, with the address named
	real := signedInBox(t)
	r := real.do(http.MethodPost, importPath, url.Values{"url": {"http://192.168.178.20/rezepte/suppe"}}, rcpDE)
	rcpHas(t, "the home network", r.body, "nur von Webseiten im Internet", `<span class="host">192.168.178.20</span>`)
	r = real.do(http.MethodPost, importPath, url.Values{"url": {"http://93.184.216.34/r"}}, enHeader)
	rcpHas(t, "a number address", r.body, "bare number addresses", "93.184.216.34")
}

func TestASwitchedOffConnectionAndOfflineModeReplaceTheForm(t *testing.T) {
	t.Parallel()
	ib := newImportBox(t)
	if err := ib.b.db.SetServiceEnabled(outbound.Recipes, false); err != nil {
		t.Fatal(err)
	}
	for _, r := range []resp{ib.b.do(http.MethodGet, importPath, nil, rcpDE), ib.read(t, "/lentils", rcpDE)} {
		rcpHas(t, "the switched off page", r.body, `class="notice imp-off"`, "Der Rezept-Import ist ausgeschaltet.", `href="/settings/connections"`, `class="imp-after"`, "Rezept selbst schreiben", `href="/meals/recipes/new"`)
		rcpLacks(t, "the switched off page", r.body, `class="rcp-imp"`)
	}
	ib.b.db.SetServiceEnabled(outbound.Recipes, true)
	if err := ib.b.db.SetOffline(true, ib.b.now); err != nil {
		t.Fatal(err)
	}
	r := ib.b.do(http.MethodGet, importPath, nil, rcpDE)
	rcpHas(t, "offline", r.body, `class="notice imp-off"`, "Die Box ist im Offline-Modus.", `href="/settings/connections"`)
}

func TestReadingAPageGivesADraftToCheckWithWhatThePageSaid(t *testing.T) {
	t.Parallel()
	ib := newImportBox(t)
	ib.b.db.SetPersonFood(ib.hid, ib.mia.ID, store.Food{Diet: "omnivore", Allergens: []string{"milk"}})
	to := ib.draftOf(t, "/lentils")
	r := ib.b.do(http.MethodGet, to, nil, rcpDE)
	if r.status != http.StatusOK {
		t.Fatalf("GET draft = %d", r.status)
	}
	rcpHas(t, "the review", r.body, `<h1>Rezept prüfen</h1>`, `<div class="imp-sum">`, `<b class="fname">127.0.0.1</b>`, ib.web.URL+"/lentils", "Gelesen von ", `class="steps rcp-form from-web"`,
		`value="Rote Linsensuppe mit Harissa"`, `value="4"`, `value="35"`, "Die Webseite nennt „Suppe“. Das ist keine der sechs Kategorien",
		`<p class="ing-src">`, "Auf der Webseite: <q>250 g rote Linsen</q>", "<q>1–2 TL Harissa (scharfe Paste)</q>", `<span class="chg">Hejmo hat die kleinere Zahl 1 eingesetzt.</span>`, `Hejmo lässt die Menge leer.`,
		`class="ing needs" id="ing1"`, `class="flag warn need-open"`, `class="flag done need-done"`, "Hejmo kennt „rote Linsen“ nicht als Artikel.",
		`<fieldset class="choice imp-pick ing-pick">`, "Was soll Hejmo mit „rote Linsen“ machen?", `name="pick_0"`, "Eigenen Artikel „rote Linsen“ anlegen", "Als freien Text lassen",
		"Zwiebeln", `class="ing-state ok"`, "Zwiebel würfeln.", "Alles kochen &amp; pürieren.",
		`id="pic"`, `class="imp-pic-card"`, `role="switch" name="picture" checked`, `class="imp-pic"`, "Von 127.0.0.1, verkleinert auf höchstens 1600 Pixel",
		`id="nut"`, `class="nut-na web"`, "nicht angegeben", `class="imp-srcline"`, `class="ext"`, `<span class="dirty"`, "Bei 3 Zutaten fehlt deine Wahl.", "Abbrechen, nichts speichern", "Rezept speichern")
	rcpLacks(t, "the review", r.body, `name="source"`, `name="pick_1" value="`+"checked")
	// the proposals with their warnings: Naturjoghurt is Joghurt · Natur, and Mia is allergic to milk
	yog := rcpBetween(t, r.body, `Was soll Hejmo mit „Naturjoghurt“`, `</fieldset>`)
	rcpHas(t, "the joghurt choice", yog, "Joghurt<span class=\"variety\"> · Natur</span>", `class="allergen-warn"`, "Enthält Milch", "Mia</span> ist allergisch")
	for _, m := range regexp.MustCompile(`name="pick_\d+"[^>]*checked`).FindAllString(r.body, -1) {
		t.Errorf("nothing is preselected: %s", m)
	}
	rcpHas(t, "the jump list", r.body, `<span class="need-line">`, `href="#ing1"`, `href="#ing3"`, `href="#ing4"`)
	// another parent cannot open it, and an unknown id is not found
	if r := ib.b.do(http.MethodGet, importPath+"/unknown", nil, rcpDE); r.status != http.StatusNotFound {
		t.Errorf("unknown draft = %d", r.status)
	}
}

func TestSavingNeedsAChoiceForEveryLineHejmoDoesNotKnow(t *testing.T) {
	t.Parallel()
	ib := newImportBox(t)
	to := ib.draftOf(t, "/lentils")
	form := reviewForm(t, ib.b.do(http.MethodGet, to, nil, rcpDE).body)
	r := ib.b.do(http.MethodPost, to, form, rcpDE)
	if r.status != http.StatusUnprocessableEntity {
		t.Fatalf("saving without a choice = %d", r.status)
	}
	rcpHas(t, "the errors", r.body, `class="ing needs invalid" id="ing1"`, "Wähle, was Hejmo mit „rote Linsen“ machen soll.", `class="error form-error"`, `href="#ing1"`, `value="250"`)
	if recs, _ := ib.b.db.Recipes(ib.hid); len(recs) != 0 {
		t.Errorf("a recipe was saved without the choices: %d", len(recs))
	}
}

// reviewForm reads the fields of the review form the way a browser sends them: the typed values and the checked options.
func reviewForm(t *testing.T, body string) url.Values {
	t.Helper()
	body = rcpLive(body)
	form := url.Values{}
	f := rcpBetween(t, body, `<form method="post" action="/meals/recipes/import/`, `</form>`)
	for _, m := range regexp.MustCompile(`<input[^>]*>`).FindAllString(f, -1) {
		name := attr(m, "name")
		if name == "" || attr(m, "type") == "radio" && !strings.Contains(m, " checked") || attr(m, "type") == "checkbox" && !strings.Contains(m, " checked") {
			continue
		}
		val := attr(m, "value")
		if attr(m, "type") == "checkbox" && val == "" {
			val = "on"
		}
		form.Add(name, htmlUnescape(val))
	}
	for _, m := range regexp.MustCompile(`(?s)<select[^>]*name="([^"]+)"[^>]*>(.*?)</select>`).FindAllStringSubmatch(f, -1) {
		sel := regexp.MustCompile(`<option value="([^"]*)" selected`).FindStringSubmatch(m[2])
		if sel != nil {
			form.Add(m[1], sel[1])
		} else {
			form.Add(m[1], "")
		}
	}
	for _, m := range regexp.MustCompile(`(?s)<textarea[^>]*name="([^"]+)"[^>]*>(.*?)</textarea>`).FindAllStringSubmatch(f, -1) {
		form.Add(m[1], htmlUnescape(m[2]))
	}
	return form
}

func attr(tag, name string) string {
	m := regexp.MustCompile(`\s` + name + `="([^"]*)"`).FindStringSubmatch(tag)
	if m == nil {
		return ""
	}
	return m[1]
}

func htmlUnescape(s string) string {
	return strings.NewReplacer("&amp;", "&", "&lt;", "<", "&gt;", ">", "&#34;", `"`, "&#39;", "'", "&quot;", `"`).Replace(s)
}

func TestSavingTheDraftMakesTheRecipeWithItsCoverSourceNutritionAndOwnArticles(t *testing.T) {
	t.Parallel()
	ib := newImportBox(t)
	to := ib.draftOf(t, "/lentils")
	body := ib.b.do(http.MethodGet, to, nil, rcpDE).body
	form := reviewForm(t, body)
	form.Set("pick_0", art(t, body, "Was soll Hejmo mit „rote Linsen“", "Linsen")) // an article of the library
	form.Set("pick_2", "own")                                                      // Harissa becomes an own article
	form.Set("pick_3", "free")                                                     // Naturjoghurt stays free text
	r := ib.b.do(http.MethodPost, to, form, rcpDE)
	if r.status != http.StatusSeeOther || !strings.HasPrefix(r.location, "/meals/recipes/") {
		t.Fatalf("saving = %d %q\n%s", r.status, r.location, rcpShown(r.body))
	}
	recs, _ := ib.b.db.Recipes(ib.hid)
	if len(recs) != 1 {
		t.Fatalf("%d recipes", len(recs))
	}
	got := recs[0]
	if got.Name != "Rote Linsensuppe mit Harissa" || got.Source != ib.web.URL+"/lentils" || got.Portions != 4 || got.Minutes != 35 || len(got.Steps) != 2 || len(got.Tags) != 0 {
		t.Errorf("recipe = %+v", got)
	}
	by := map[string]string{}
	for _, in := range got.Ingredients {
		by[in.Text] = in.ArticleID
	}
	if by["Linsen"] != "lentils" || by["Zwiebeln"] != "onions" || by["Naturjoghurt"] != "" || by["Harissa"] == "" || by["Salz"] != "salt" {
		t.Errorf("ingredients = %+v", got.Ingredients)
	}
	if own, err := ib.b.db.Article(ib.hid, by["Harissa"]); err != nil || own.Name != "Harissa" || len(own.Allergens) != 0 || own.Dept != "other" {
		t.Errorf("own article = %+v %v", own, err)
	}
	if photos, err := ib.b.db.RecipePhotos(ib.hid, got.ID); err != nil || len(photos) != 1 || !photos[0].IsCover || photos[0].Origin != store.PhotoFromWeb {
		t.Errorf("the picture of the page is the cover photo of origin web: %+v %v", photos, err)
	}
	n, ok, _ := ib.b.db.RecipePageNutrition(ib.hid, got.ID)
	if !ok || n.KCal == nil || *n.KCal != 380 || n.Salt != nil || n.Carbs != nil {
		t.Errorf("page nutrition = %+v", n)
	}
	page := ib.b.do(http.MethodGet, r.location, nil, rcpDE)
	rcpHas(t, "the recipe page", page.body, `class="msg ok"`, "„Rote Linsensuppe mit Harissa“ von 127.0.0.1 importiert und gespeichert.",
		`class="notice imp-new-art"`, "Neu in eurer Bibliothek, noch ohne Allergene:", `class="rcp-cover"`, `class="src-when"`, "Importiert am ",
		"Von der Webseite importiert von ", "Richtwerte von der Webseite, für eine Portion angegeben.", `class="nut-gap"`, "Unvollständig", "Quelle: ", "Hejmo hat sie nicht nachgerechnet.", `class="nut-na web"`)
	if strings.Index(page.body, "rcp-cover") > strings.Index(page.body, "rcp-act") || strings.Index(page.body, "rcp-act") > strings.Index(page.body, "rcp-facts") {
		t.Error("the cover stands in the head, before .rcp-act and .rcp-facts")
	}
	if again := ib.b.do(http.MethodGet, r.location, nil, rcpDE); strings.Contains(again.body, "imp-new-art") || strings.Contains(again.body, "importiert und gespeichert") {
		t.Error("the notice and the success line show once")
	}
	if kid := ib.child.do(http.MethodGet, r.location, nil, rcpDE); !strings.Contains(kid.body, "rcp-cover") || strings.Contains(kid.body, "imp-new-art") {
		t.Errorf("a child sees the cover and no notice about articles")
	}
	if photos, _ := ib.b.db.RecipePhotos(ib.hid, got.ID); len(photos) == 1 {
		if cover := ib.child.do(http.MethodGet, r.location+"/photos/"+photos[0].ID, nil, nil); cover.status != http.StatusOK || !strings.HasPrefix(cover.body, "\xff\xd8") {
			t.Errorf("cover = %d", cover.status)
		}
	}
	if _, err := ib.b.db.RecipeDraft(ib.hid, strings.TrimPrefix(to, importPath+"/")); err == nil {
		t.Error("the draft stays after saving")
	}
	// changing the portions drops the page's values, with a word about who changed what
	edit := ib.b.do(http.MethodGet, r.location+"/edit", nil, rcpDE).body
	rcpHas(t, "the edit page", edit, `class="imp-srcline"`, "Importiert von ", "lässt sich nicht ändern")
	rcpLacks(t, "the edit page", edit, `name="source"`)
	ef := reviewFormOf(t, edit, "form.steps.rcp-form")
	ef.Set("portions", "6")
	ef.Set("source", "https://elsewhere.example/")
	if r2 := ib.b.do(http.MethodPost, r.location, ef, rcpDE); r2.status != http.StatusSeeOther {
		t.Fatalf("update = %d\n%s", r2.status, rcpShown(r2.body))
	}
	if now, _ := ib.b.db.Recipe(ib.hid, got.ID); now.Source != ib.web.URL+"/lentils" {
		t.Errorf("the source of an imported recipe changed to %q", now.Source)
	}
	after := ib.b.do(http.MethodGet, r.location, nil, rcpDE).body
	rcpHas(t, "after the change", after, "Die Werte von 127.0.0.1 gelten nicht mehr", "die Portionen von 4 auf 6 geändert hat", "Darum rechnet Hejmo jetzt selbst.")
	rcpLacks(t, "after the change", after, "Richtwerte von der Webseite")
}

func reviewFormOf(t *testing.T, body, _ string) url.Values {
	t.Helper()
	body = rcpLive(body)
	form := url.Values{}
	f := rcpBetween(t, body, `<form method="post" action="/meals/recipes/`, `</form>`)
	for _, m := range regexp.MustCompile(`<input[^>]*>`).FindAllString(f, -1) {
		name := attr(m, "name")
		if name == "" || (attr(m, "type") == "radio" || attr(m, "type") == "checkbox") && !strings.Contains(m, " checked") {
			continue
		}
		form.Add(name, htmlUnescape(attr(m, "value")))
	}
	for _, m := range regexp.MustCompile(`(?s)<select[^>]*name="([^"]+)"[^>]*>(.*?)</select>`).FindAllStringSubmatch(f, -1) {
		sel := regexp.MustCompile(`<option value="([^"]*)" selected`).FindStringSubmatch(m[2])
		if sel != nil {
			form.Add(m[1], sel[1])
		} else {
			form.Add(m[1], "")
		}
	}
	for _, m := range regexp.MustCompile(`(?s)<textarea[^>]*name="([^"]+)"[^>]*>(.*?)</textarea>`).FindAllStringSubmatch(f, -1) {
		form.Add(m[1], htmlUnescape(m[2]))
	}
	return form
}

// art finds the value of the radio of a proposal for the article named in the fieldset of a line.
func art(t *testing.T, body, legend, name string) string {
	t.Helper()
	set := rcpBetween(t, body, legend, `</fieldset>`)
	for _, m := range regexp.MustCompile(`<input type="radio" name="pick_\d+" value="(art:[^"]*)"[^>]*><b>`+name).FindAllStringSubmatch(set, -1) {
		return htmlUnescape(m[1])
	}
	t.Fatalf("no proposal %q in %s", name, set)
	return ""
}

func TestSeveralRecipesOnOnePageAskWhichOne(t *testing.T) {
	t.Parallel()
	ib := newImportBox(t)
	to := ib.draftOf(t, "/two")
	r := ib.b.do(http.MethodGet, to, nil, rcpDE)
	rcpHas(t, "the choice", r.body, `<h1>Welches Rezept?</h1>`, `<fieldset class="choice imp-which">`, `name="which" value="0" checked`, "Rote Linsensuppe mit Harissa", "<small>5 Zutaten · 2 Schritte · 35 Minuten · mit Bild</small>", "<small>1 Zutat · 1 Schritt</small>", "Weiter zum Prüfen")
	second := ib.b.do(http.MethodGet, to+"?which=1", nil, rcpDE)
	rcpHas(t, "the second", second.body, `<h1>Rezept prüfen</h1>`, `value="Brot"`)
}

func TestADraftExpiresAfterAnHourAndBelongsToTheParentWhoReadIt(t *testing.T) {
	t.Parallel()
	ib := newImportBox(t)
	to := ib.draftOf(t, "/lentils")
	other, err := ib.b.db.AddPerson(ib.hid, store.Person{Name: "Papa", Role: store.RoleParent})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ib.b.db.AddRecipeDraft(ib.hid, other.ID, "https://x.de/a", "x.de", []byte(`{}`), ib.b.now); err != nil {
		t.Fatal(err)
	}
	ib.b.now = ib.b.now.Add(61 * time.Minute)
	r := ib.b.do(http.MethodGet, to, nil, rcpDE)
	rcpHas(t, "expired", r.body, `class="empty"`, "Das ist zu lange her.", "Gespeichert wurde nichts.", "Link noch einmal lesen", `class="button secondary"`)
	if p := ib.b.do(http.MethodPost, to, url.Values{"name": {"x"}}, rcpDE); p.status != http.StatusOK || !strings.Contains(p.body, "Das ist zu lange her.") {
		t.Errorf("saving an expired draft = %d", p.status)
	}
	// a draft of another parent is not found
	var id string
	if err := ib.b.db.SQL().QueryRow(`SELECT id FROM recipe_drafts WHERE read_by = ?`, other.ID).Scan(&id); err != nil {
		t.Fatal(err)
	}
	if r := ib.b.do(http.MethodGet, importPath+"/"+id, nil, rcpDE); r.status != http.StatusNotFound {
		t.Errorf("another parent's draft = %d", r.status)
	}
}

func TestCancellingDiscardsTheDraft(t *testing.T) {
	t.Parallel()
	ib := newImportBox(t)
	to := ib.draftOf(t, "/lentils")
	if r := ib.b.do(http.MethodGet, to+"/cancel", nil, rcpDE); r.status != http.StatusSeeOther || r.location != "/meals/recipes" {
		t.Errorf("cancel = %d %q", r.status, r.location)
	}
	if r := ib.b.do(http.MethodGet, to, nil, rcpDE); r.status != http.StatusNotFound {
		t.Errorf("the draft after cancelling = %d", r.status)
	}
}

var _ = json.Marshal
var _ = picture.RecipeMaxSide

func TestAYieldThatIsNoPortionCountAndAMissingOneAreMarkedToCheck(t *testing.T) {
	t.Parallel()
	ib := newImportBox(t)
	pages := map[string]string{
		"/tray":    `{"@type":"Recipe","name":"Apfelkuchen vom Blech","recipeYield":"1 Blech (12 Stücke)","recipeIngredient":["250 g Mehl"],"recipeInstructions":"Backen."}`,
		"/noyield": `{"@type":"Recipe","name":"Suppe","recipeIngredient":["1 Zwiebel"]}`,
	}
	web := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.Write([]byte(ldPage(pages[r.URL.Path])))
	}))
	t.Cleanup(web.Close)
	read := func(path string) string {
		r := ib.b.do(http.MethodPost, importPath, url.Values{"url": {web.URL + path}}, rcpDE)
		return ib.b.do(http.MethodGet, r.location, nil, rcpDE).body
	}
	tray := read("/tray")
	rcpHas(t, "the tray", tray, `value="12"`, `<span class="flag warn">`, "Bitte prüfen", "Die Webseite schreibt „1 Blech (12 Stücke)“. Hejmo hat 12 Portionen eingesetzt")
	none := read("/noyield")
	rcpHas(t, "no yield", none, `<span class="flag warn">`, "Die Webseite nennt keine Portionen.", "Hejmo hat alle 1 Zutaten", `class="ing-state ok"`)
	rcpLacks(t, "no yield", none, `id="nut"`, `id="pic"`)
	rcpHas(t, "no yield", none, "Keine Nährwerte auf der Webseite")
}

func TestTheSamePageTwiceShowsANoticeAndATakenNameGetsAProposal(t *testing.T) {
	t.Parallel()
	ib := newImportBox(t)
	save := func() resp {
		to := ib.draftOf(t, "/lentils")
		body := ib.b.do(http.MethodGet, to, nil, rcpDE).body
		form := reviewForm(t, body)
		form.Set("pick_0", "free")
		form.Set("pick_2", "free")
		form.Set("pick_3", "free")
		return ib.b.do(http.MethodPost, to, form, rcpDE)
	}
	if r := save(); r.status != http.StatusSeeOther {
		t.Fatalf("the first save = %d", r.status)
	}
	to := ib.draftOf(t, "/lentils")
	r := ib.b.do(http.MethodGet, to, nil, rcpDE)
	rcpHas(t, "the notice", r.body, `class="notice imp-again"`, "Von dieser Seite habt ihr schon", "„Rote Linsensuppe mit Harissa“", "importiert von Mama am", "gibt es das Rezept zweimal")
	form := reviewForm(t, r.body)
	for _, k := range []string{"pick_0", "pick_2", "pick_3"} {
		form.Set(k, "free")
	}
	second := ib.b.do(http.MethodPost, to, form, rcpDE)
	if second.status != http.StatusUnprocessableEntity {
		t.Fatalf("a taken name = %d", second.status)
	}
	rcpHas(t, "the name error", second.body, `class="ferr"`, "gibt es schon", "Gib diesem Rezept einen eigenen Namen", "öffne das vorhandene</a>", `class="notice imp-again"`)
}

// The picture of a page counts as the connection "recipes" like the page itself: the switch of the web
// images does not stop it, and a picture at a number address is not downloaded (ADR 0021).
func TestThePictureOfAPageFollowsTheRecipesSwitchNotTheImagesSwitch(t *testing.T) {
	t.Parallel()
	ib := newImportBox(t)
	if err := ib.b.db.SetServiceEnabled(outbound.Images, false); err != nil {
		t.Fatal(err)
	}
	rcpHas(t, "the review with the web images off", ib.b.do(http.MethodGet, ib.draftOf(t, "/lentils"), nil, rcpDE).body, `class="imp-pic-card"`)
}

func TestThePictureIsNotDownloadedWhenTheRecipesSwitchIsOffByThen(t *testing.T) {
	t.Parallel()
	ib := newImportBox(t)
	body := ib.b.do(http.MethodGet, ib.draftOf(t, "/offafter"), nil, rcpDE).body
	rcpHas(t, "the review", body, `<h1>Rezept prüfen</h1>`)
	rcpLacks(t, "the review", body, `class="imp-pic-card"`)
}

func TestAPictureAtANumberAddressIsNotDownloaded(t *testing.T) {
	t.Parallel()
	pics := loopbackFetcher()
	pics.NoIPLiteral = true
	ib := newImportBox(t, withFetcher(pics))
	body := ib.b.do(http.MethodGet, ib.draftOf(t, "/lentils"), nil, rcpDE).body
	rcpLacks(t, "the review", body, `class="imp-pic-card"`, "Von 127.0.0.1, verkleinert")
}

func TestWithTheSwitchOffTheRecipeHasNoCover(t *testing.T) {
	t.Parallel()
	ib := newImportBox(t)
	to := ib.draftOf(t, "/lentils")
	form := reviewForm(t, ib.b.do(http.MethodGet, to, nil, rcpDE).body)
	for _, k := range []string{"pick_0", "pick_2", "pick_3"} {
		form.Set(k, "free")
	}
	form.Del("picture")
	r := ib.b.do(http.MethodPost, to, form, rcpDE)
	if r.status != http.StatusSeeOther {
		t.Fatalf("save = %d", r.status)
	}
	rcpLacks(t, "the recipe page", ib.b.do(http.MethodGet, r.location, nil, rcpDE).body, "rcp-cover")
	if photos, _ := ib.b.db.RecipePhotos(ib.hid, strings.TrimPrefix(r.location, "/meals/recipes/")); len(photos) != 0 {
		t.Errorf("photos = %+v, want none: the picture was switched off", photos)
	}
}

func TestEveryTextOfTheImportPagesExistsInBothLanguages(t *testing.T) {
	t.Parallel()
	ib := newImportBox(t)
	for _, h := range []map[string]string{rcpDE, enHeader} {
		to := ib.draftOf(t, "/lentils")
		pages := map[string]string{"import": ib.b.do(http.MethodGet, importPath, nil, h).body, "review": ib.b.do(http.MethodGet, to, nil, h).body,
			"errors": ib.b.do(http.MethodPost, to, url.Values{"name": {""}, "which": {"0"}, "ing_name": {"Harissa"}, "ing_from": {"2"}, "ing_amount": {"1"}, "ing_unit": {""}, "ing_article": {""}, "ing_variety": {""}}, h).body,
			"two":    ib.b.do(http.MethodGet, ib.draftOf(t, "/two"), nil, h).body, "expired": ib.b.do(http.MethodGet, "/meals/recipes/import/unknown", nil, h).body}
		for name, body := range pages {
			if m := regexp.MustCompile(`recipes\.import\.[a-z.]+|flash\.recipe_imported`).FindString(body); m != "" {
				t.Errorf("%s page (%v) shows the key %s", name, h, m)
			}
		}
	}
	en := ib.b.do(http.MethodGet, ib.draftOf(t, "/lentils"), nil, enHeader).body
	rcpHas(t, "English review", en, "<h1>Check recipe</h1>", "Please choose", "Create own article “rote Linsen”", "Keep as free text", "Save recipe", "Cancel, save nothing")
}
