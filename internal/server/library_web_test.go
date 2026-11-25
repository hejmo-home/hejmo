package server_test

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	"image/jpeg"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/textproto"
	"net/url"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/hejmo-home/hejmo/internal/list"
	"github.com/hejmo-home/hejmo/internal/outbound"
	"github.com/hejmo-home/hejmo/internal/picture"
	"github.com/hejmo-home/hejmo/internal/store"
)

// Ticket 164 (ADR 0019, preview-164.html): the article library on the web.

// libBox is a signed-in box with the example family: Mama (the parent who set
// the box up), and whoever a test adds with a diet and allergies.
type libBox struct {
	*box
	h    store.Household
	mama store.Person
}

func newLibBox(t *testing.T, opts ...boxOption) libBox {
	t.Helper()
	b := signedInBox(t, opts...)
	h, err := b.db.HomeHousehold()
	if err != nil {
		t.Fatal(err)
	}
	people, _ := b.db.People(h.ID)
	return libBox{box: b, h: h, mama: people[0]}
}

func (l libBox) person(name, role, diet string, allergens ...string) store.Person {
	l.t.Helper()
	p, err := l.db.AddPerson(l.h.ID, store.Person{Name: name, Role: store.Role(role)})
	if err != nil {
		l.t.Fatal(err)
	}
	if err := l.db.SetPersonFood(l.h.ID, p.ID, store.Food{Diet: diet, Allergens: allergens}); err != nil {
		l.t.Fatal(err)
	}
	return p
}

func (l libBox) en(path string) resp { return getEN(l.box, path) }

func (l libBox) de(path string) resp {
	return l.do(http.MethodGet, path, nil, map[string]string{"Accept-Language": "de"})
}

func (l libBox) postEN(path string, f url.Values) resp {
	return l.do(http.MethodPost, path, f, map[string]string{"Accept-Language": "en"})
}

type upload struct {
	field, name string
	data        []byte
}

// postMultipart posts a form with files, as a browser does with a photo.
func (l libBox) postMultipart(path string, f url.Values, files ...upload) resp {
	l.t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	for k, vs := range f {
		for _, v := range vs {
			_ = mw.WriteField(k, v)
		}
	}
	for _, u := range files {
		h := textproto.MIMEHeader{}
		h.Set("Content-Disposition", fmt.Sprintf(`form-data; name=%q; filename=%q`, u.field, u.name))
		h.Set("Content-Type", "application/octet-stream")
		part, _ := mw.CreatePart(h)
		_, _ = part.Write(u.data)
	}
	_ = mw.Close()
	req, _ := http.NewRequest(http.MethodPost, l.srv.URL+path, &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req.Header.Set("Origin", l.srv.URL)
	req.Header.Set("Accept-Language", "en")
	res, err := l.client.Do(req)
	if err != nil {
		l.t.Fatal(err)
	}
	defer res.Body.Close()
	var out bytes.Buffer
	_, _ = out.ReadFrom(res.Body)
	return resp{res.StatusCode, res.Header.Get("Location"), out.String()}
}

func testJPEG(t *testing.T, w, h int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.Set(x, y, color.RGBA{uint8(x * 255 / w), uint8(y * 255 / h), 90, 255})
		}
	}
	var b bytes.Buffer
	if err := jpeg.Encode(&b, img, nil); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

// loopbackFetcher lets the box reach the test's own server, and nothing else.
func loopbackFetcher() *picture.Fetcher {
	return &picture.Fetcher{Gate: outbound.AllowAll(), Public: func(a netip.Addr) bool { return a == netip.MustParseAddr("127.0.0.1") }, Timeout: 3 * time.Second}
}

func imageServer(t *testing.T, w, h int) *httptest.Server {
	t.Helper()
	body := testJPEG(t, w, h)
	srv := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/page":
			rw.Header().Set("Content-Type", "text/html")
			_, _ = rw.Write([]byte("<!doctype html><title>Shop</title>"))
		default:
			rw.Header().Set("Content-Type", "image/jpeg")
			_, _ = rw.Write(body)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

// articleForm is a valid form of a new own article.
func articleForm() url.Values {
	return url.Values{"name": {"Katzenfutter Huhn"}, "dept": {"other"}, "kind": {"nonfood"}, "unit1": {"pack"}, "unit2": {""}}
}

var idRe = regexp.MustCompile(`/settings/lists/library/([0-9a-f-]{36})`)

func libHas(t *testing.T, what, page string, want ...string) {
	t.Helper()
	for _, w := range want {
		if !strings.Contains(page, w) {
			t.Errorf("%s lacks %q\n%s", what, w, page)
		}
	}
}

func libHasNot(t *testing.T, what, page string, bad ...string) {
	t.Helper()
	for _, w := range bad {
		if strings.Contains(page, w) {
			t.Errorf("%s holds %q", what, w)
		}
	}
}

func (l libBox) createArticle(f url.Values) string {
	l.t.Helper()
	r := l.postEN("/settings/lists/library", f)
	if r.status != http.StatusSeeOther {
		l.t.Fatalf("create = %d\n%s", r.status, r.body)
	}
	for _, a := range func() []store.OwnArticle { as, _ := l.db.Articles(l.h.ID); return as }() {
		if a.Name == strings.Join(strings.Fields(f.Get("name")), " ") {
			return a.ID
		}
	}
	l.t.Fatal("the created article is not stored")
	return ""
}

func TestSettingsHubHasAListsRowAfterPeopleForParentsOnly(t *testing.T) {
	t.Parallel()
	l := newLibBox(t)
	hub := l.en("/settings").body
	libHas(t, "hub", hub, `href="/settings/lists"`, "<strong>Lists</strong>")
	if at, people := strings.Index(hub, `href="/settings/lists"`), strings.Index(hub, `href="/people"`); people < 0 || at < people {
		t.Errorf("the Lists row must come after People: %d %d", at, people)
	}
	child := childSession(t, l.box)
	for _, page := range []string{"/settings/lists", "/settings/lists/library", "/settings/lists/library/new", "/settings/lists/library/apples"} {
		if r := child.get(page); r.status != http.StatusForbidden {
			t.Errorf("child GET %s = %d, want 403", page, r.status)
		}
	}
	if r := child.post("/settings/lists", url.Values{"hide": {"on"}}); r.status != http.StatusForbidden {
		t.Errorf("child POST settings = %d", r.status)
	}
}

func TestListsPageStatesTheLibraryInWordsAndSaysThereAreNoAllergiesYet(t *testing.T) {
	t.Parallel()
	l := newLibBox(t)
	r := l.en("/settings/lists")
	if r.status != http.StatusOK {
		t.Fatalf("GET = %d", r.status)
	}
	libHas(t, "lists page", r.body, "<h1>Lists</h1>", `class="list-rows"`, `href="/settings/lists/library"`, "<b>Article library</b>", "254 articles from Hejmo", "No own articles yet",
		`<h2 id="al">Allergies</h2>`, `class="empty"`, "No allergies are entered yet", `href="/people"`, "Go to people")
	libHasNot(t, "lists page without allergies", r.body, `name="hide"`, "How Hejmo warns")
	l.postEN("/settings/lists/library/apples/hidden", url.Values{"hidden": {"on"}})
	l.createArticle(articleForm())
	libHas(t, "lists page", l.en("/settings/lists").body, "254 articles from Hejmo, 1 hidden by hand", "1 own article")
}

func TestListsPageWarnsInWordsListsAllergiesAndOffersTheExperimentalHidingOffByDefault(t *testing.T) {
	t.Parallel()
	l := newLibBox(t)
	mia := l.person("Mia", "child", "vegetarian", "peanuts", "nuts")
	l.db.SetPersonFood(l.h.ID, mia.ID, store.Food{Diet: "vegetarian", Allergens: []string{"peanuts", "nuts"}, Other: "Kiwi"})
	l.person("Papa", "parent", "omnivore")
	r := l.en("/settings/lists")
	libHas(t, "lists page", r.body, "How Hejmo warns", "This is always on.", `<span class="allergen-warn">`, "Contains Peanuts: ", `<span class="who"><span class="marker"`, "Mia</span> is allergic",
		"Experimental, no guarantee", "Allergies entered", `<ul class="allergy-people">`, "Mia:</span>", "Peanuts and Tree nuts", "Not checked automatically, not filterable: Kiwi",
		"Mama and Papa have none entered.", `class="tag exp">Experimental</span>`, `name="hide"`, "Off. Hejmo only warns and hides nothing.", `class="liability"`, "Experimental: no guarantee, no liability",
		`name="liability"`, "Hejmo does not guarantee that the information is complete or correct and takes no liability.", "What Hejmo does not hide", "Kiwi")
	if strings.Contains(r.body, `name="hide" checked`) || strings.Contains(r.body, `role="switch" name="hide" checked`) {
		t.Error("hiding is on by default")
	}
	libHasNot(t, "lists page", r.body, "<b>Experimental, no guarantee</b>")
	// The name sits on the text baseline: an inline span, never inline-flex (found in ticket 163).
	if strings.Contains(r.body, `<span class="who" style`) {
		t.Error("a person inside a sentence carries layout styles")
	}
	// Whole family is the choice when nobody is picked; only people with ticked allergens can be chosen.
	libHas(t, "chips", r.body, `name="whole_family" value="1" data-whole checked`, `name="people" value="`+mia.ID+`"`)
	libHasNot(t, "chips", r.body, `name="people" value="`+l.mama.ID+`"`)
}

func TestSwitchingHidingOnNeedsTheTickAndTheErrorSitsAtTheTick(t *testing.T) {
	t.Parallel()
	l := newLibBox(t)
	l.person("Mia", "child", "omnivore", "peanuts")
	r := l.postEN("/settings/lists", url.Values{"hide": {"on"}, "whole_family": {"1"}})
	if r.status != http.StatusUnprocessableEntity {
		t.Fatalf("without the tick = %d", r.status)
	}
	libHas(t, "refused page", r.body, `id="err-liability"`, "Please confirm the notice. Until then hiding stays off and Hejmo only warns.", `name="liability" autofocus aria-invalid="true"`)
	// The error is the line right under the tick, inside the same group.
	if !regexp.MustCompile(`(?s)class="confirm-field">.*?name="liability".*?id="err-liability".*?</div>`).MatchString(r.body) {
		t.Error("the error does not sit with the tick in .confirm-field")
	}
	set, _ := l.db.LibrarySettings(l.h.ID)
	if set.HideAllergen {
		t.Fatal("hiding was switched on without the notice being confirmed")
	}
	// The form comes back as it was sent: hiding on.
	libHas(t, "refused page", r.body, `name="hide" checked aria-label="Hide articles with allergens"`)
}

func TestConfirmedHidingIsStoredWhoAndWhenAndHidesFromTheSuggestions(t *testing.T) {
	t.Parallel()
	l := newLibBox(t)
	mia := l.person("Mia", "child", "omnivore", "peanuts")
	r := l.postEN("/settings/lists", url.Values{"hide": {"on"}, "whole_family": {"1"}, "liability": {"on"}})
	if r.status != http.StatusSeeOther || r.location != "/settings/lists" {
		t.Fatalf("save = %d %q\n%s", r.status, r.location, r.body)
	}
	page := l.en("/settings/lists")
	libHas(t, "after saving", page.body, "Saved. Hejmo now hides articles with an allergen from the suggestions.", "Confirmed by Mama on ", `name="hide" checked aria-label="Hide articles with allergens"`, `name="liability" checked`)
	set, _ := l.db.LibrarySettings(l.h.ID)
	if !set.HideAllergen || !set.Everyone || set.ConfirmedBy != l.mama.ID {
		t.Errorf("stored = %+v", set)
	}
	art := l.en("/settings/lists/library/peanut-butter").body
	libHas(t, "peanut butter", art, "No, because of the allergy of", "Mia", "Hiding for allergies is on (experimental).", `href="/settings/lists"`, "because of the allergy of")
	row := l.en("/settings/lists/library?q=peanut+butter").body
	libHas(t, "search", row, "Hidden because of allergy: Mia", `class="tag exp"`)

	// Only for Mia: others keep their suggestions.
	papa := l.person("Papa", "parent", "omnivore", "milk")
	l.postEN("/settings/lists", url.Values{"hide": {"on"}, "people": {mia.ID}, "liability": {"on"}})
	if s := l.en("/settings/lists/library/butter").body; strings.Contains(s, "No, because of the allergy") {
		t.Errorf("butter is hidden for %s although only Mia was chosen", papa.Name)
	}
	// Off again: warns only.
	l.postEN("/settings/lists", url.Values{"liability": {"on"}})
	set, _ = l.db.LibrarySettings(l.h.ID)
	if set.HideAllergen {
		t.Error("hiding stayed on")
	}
	libHas(t, "off", l.en("/settings/lists").body, "Saved. Hejmo only warns and hides nothing.", "Off. Hejmo only warns and hides nothing.")
}

func TestLibraryShowsOwnArticlesFirstAndShippedOnesInClosedDepartmentCardsWithCountsInWords(t *testing.T) {
	t.Parallel()
	l := newLibBox(t)
	mia := l.person("Mia", "child", "omnivore", "nuts")
	l.db.SetPersonFood(l.h.ID, mia.ID, store.Food{Diet: "omnivore", Allergens: []string{"nuts"}, Other: "Kiwi"})
	l.postEN("/settings/lists/library/walnuts/hidden", url.Values{"hidden": {"on"}})
	l.createArticle(articleForm())
	r := l.en("/settings/lists/library")
	if r.status != http.StatusOK {
		t.Fatalf("GET = %d", r.status)
	}
	libHas(t, "library", r.body, `<a href="/settings/lists">Lists</a>`, "<h1>Article library</h1>", `class="button" href="/settings/lists/library/new">Add own article`,
		`class="lib-search"`, `role="search"`, `class="lib-filter"`, `aria-current="true">All</a>`, "With allergy warning", ">Hidden</a>",
		`<h2 id="own">Own articles</h2>`, "Katzenfutter Huhn", `<h2 id="hej">Articles from Hejmo</h2>`, "254 articles in German and English.",
		`<details class="dept"><summary>`, "Fruit and vegetables", "47 articles", "Sweets and snacks", "17 articles · 1 hidden by hand · ", "with allergy warning",
		`class="unchecked-box"`, "Not checked automatically, cannot be filtered", "Mia:</span> <span>Kiwi")
	if strings.Contains(r.body, `<details class="dept" open`) {
		t.Error("a department starts open")
	}
	if n := strings.Count(r.body, `<details class="dept">`); n != 13 {
		t.Errorf("%d departments, want 13", n)
	}
	if strings.Index(r.body, "Katzenfutter Huhn") > strings.Index(r.body, "Articles from Hejmo") {
		t.Error("own articles come first")
	}
	// A count never replaces a name: Mia is named on each warning, never "+2".
	if regexp.MustCompile(`\+\d`).MatchString(strings.ReplaceAll(strings.ReplaceAll(r.body, "<style", ""), "+0", "")) {
		t.Errorf("the library shows a count in place of a name")
	}
	de := l.de("/settings/lists/library").body
	libHas(t, "German library", de, `<a href="/settings/lists">Listen</a>`, "Eigenen Artikel anlegen", "Obst und Gemüse", "47 Artikel", "Artikel von Hejmo")
}

func TestOwnArticlesStartWithAnEmptyStateSentence(t *testing.T) {
	t.Parallel()
	l := newLibBox(t)
	libHas(t, "library", l.en("/settings/lists/library").body, `class="empty" data-empty`, "No own articles yet. Add what you often buy")
}

func TestLibrarySearchIgnoresUmlautsFindsOwnAndShippedAndNamesEachPersonInTheWarnings(t *testing.T) {
	t.Parallel()
	l := newLibBox(t)
	l.person("Mia", "child", "omnivore", "nuts")
	l.person("Konstantin", "child", "omnivore", "milk")
	l.person("Charlotte-Sophie", "child", "vegan", "soy")
	l.createArticle(url.Values{"name": {"Haselnuss-Mandel-Knusperflocken mit Schokostückchen"}, "dept": {"pantry"}, "kind": {"veg"}, "unit1": {"pack"}, "allergens": {"nuts", "milk", "soy"}})
	r := l.en("/settings/lists/library?q=NUSS")
	libHas(t, "search", r.body, `<h2 id="res">Search result</h2>`, `class="results-head"`, "articles with “NUSS”", "Clear search", "Peanut butter", "Walnuts", "Hazelnut chocolate spread", "Haselnuss-Mandel-Knusperflocken",
		"Pantry and breakfast", "Vegetarian", "own article", "from Hejmo")
	// One line per group of people, each person named.
	for _, want := range []string{
		`Contains Milk and lactose: <span class="who"><span class="marker" data-person="`,
		`Mia</span> is allergic</span>`, `Konstantin</span> is allergic</span>`, `Charlotte-Sophie</span> is allergic</span>`,
	} {
		libHas(t, "warnings", r.body, want)
	}
	if strings.Contains(r.body, "+2") || strings.Contains(r.body, "+1") {
		t.Error("a warning counts people")
	}
	// ü = u in German too.
	if de := l.de("/settings/lists/library?q=Muesli").body; !strings.Contains(de, "Müsli") {
		t.Errorf("\"Muesli\" does not find Müsli\n%s", de)
	}
	if de := l.de("/settings/lists/library?q=musli").body; !strings.Contains(de, "Müsli") {
		t.Error("\"musli\" does not find Müsli")
	}
}

func TestTwoPeopleWithTheSameAllergenShareOneWarningLineThatNamesBoth(t *testing.T) {
	t.Parallel()
	l := newLibBox(t)
	l.person("Konstantin", "child", "omnivore", "milk")
	l.person("Papa", "parent", "omnivore", "milk")
	body := l.en("/settings/lists/library?q=butter").body
	re := regexp.MustCompile(`Contains Milk and lactose: (<span class="who">.*?Konstantin</span>) and (<span class="who">.*?Papa</span>) are allergic`)
	if !re.MatchString(body) {
		t.Errorf("one line naming both people expected\n%s", body)
	}
}

func TestNoMatchOffersTheNameAsAnOwnArticle(t *testing.T) {
	t.Parallel()
	l := newLibBox(t)
	r := l.en("/settings/lists/library?q=Quinoa-Pops").body
	libHas(t, "no match", r, `class="empty" data-empty`, "No article is called “Quinoa-Pops” or contains the word.", `href="/settings/lists/library/new?name=Quinoa-Pops"`, "Add “Quinoa-Pops” as own article")
	form := l.en("/settings/lists/library/new?name=Quinoa-Pops").body
	libHas(t, "prefilled form", form, ">Quinoa-Pops</textarea>")
}

func TestFiltersShowOnlyWarnedOrHiddenArticles(t *testing.T) {
	t.Parallel()
	l := newLibBox(t)
	l.person("Mia", "child", "omnivore", "peanuts")
	l.postEN("/settings/lists/library/walnuts/hidden", url.Values{"hidden": {"on"}})
	l.en("/settings/lists/library/walnuts") // the success line is shown once
	warn := l.en("/settings/lists/library?f=warn").body
	libHas(t, "warn filter", warn, "articles with an allergy warning", "Peanut butter", `aria-current="true">With allergy warning`)
	libHasNot(t, "warn filter", warn, "Walnuts", "Apples")
	hidden := l.en("/settings/lists/library?f=hidden").body
	libHas(t, "hidden filter", hidden, "1 article hidden", "Walnuts", "Hidden by hand")
	libHasNot(t, "hidden filter", hidden, "Peanut butter")
	l.postEN("/settings/lists/library/walnuts/hidden", url.Values{})
	libHas(t, "empty hidden filter", l.en("/settings/lists/library?f=hidden").body, "Nothing is hidden.")
}

func TestShippedArticlePageSaysEverythingInWordsAndCannotBeEdited(t *testing.T) {
	t.Parallel()
	l := newLibBox(t)
	mia := l.person("Mia", "child", "omnivore", "peanuts")
	l.person("Sebastian", "child", "vegetarian")
	l.db.SetPersonFood(l.h.ID, mia.ID, store.Food{Diet: "omnivore", Allergens: []string{"peanuts"}})
	r := l.en("/settings/lists/library/peanut-butter")
	if r.status != http.StatusOK {
		t.Fatalf("GET = %d", r.status)
	}
	libHas(t, "article page", r.body, `<a href="/settings/lists/library">Article library</a>`, "Article library", `<h1 class="art-title"><span class="art-sym big" aria-hidden="true">🥜</span><span>Peanut butter</span></h1>`,
		"Comes from", "Hejmo · German: Erdnussbutter", "Pantry and breakfast", "piece and g", "When adding, piece is preselected until you use another unit.", "Vegan: nothing from animals",
		"everyone at your home", "Peanuts", `Contains Peanuts: `, "Mia</span> is allergic", "Hide by hand", "Off. Hejmo suggests Peanut butter.", "Make own version", "Copy as own article")
	libHasNot(t, "shipped page", r.body, `name="name"`, "<textarea", "Delete article")
	// The German page names the English name.
	libHas(t, "German article", l.de("/settings/lists/library/peanut-butter").body, "Hejmo · Englisch: Peanut butter", "Stück und g", "Beim Hinzufügen ist Stück vorgewählt", "Erdnussbutter")
	if r := l.get("/settings/lists/library/no-such-article"); r.status != http.StatusNotFound {
		t.Errorf("unknown article = %d", r.status)
	}
	// Edit is not possible: the form post is refused.
	if r := l.postEN("/settings/lists/library/peanut-butter", articleForm()); r.status != http.StatusNotFound {
		t.Errorf("saving a shipped article = %d, want 404", r.status)
	}
}

func TestWhoAnArticleSuitsIsNamedByDietAndNeverCounted(t *testing.T) {
	t.Parallel()
	l := newLibBox(t)
	l.person("Mia", "child", "vegetarian")
	l.person("Charlotte-Sophie", "child", "vegan")
	l.person("Papa", "parent", "omnivore")
	body := l.en("/settings/lists/library/salmon-fillet").body
	libHas(t, "salmon", body, "Suits", "Does not suit", `<span class="pgrp"><span class="diet">Eats everything:</span>`, `<span class="diet">Vegetarian:</span>`, `<span class="diet">Vegan:</span>`)
	if !regexp.MustCompile(`(?s)Does not suit.*?Vegetarian:.*?Mia</span>.*?Vegan:.*?Charlotte-Sophie</span>`).MatchString(body) {
		t.Errorf("not suiting: Mia (vegetarian) and Charlotte-Sophie (vegan)\n%s", body)
	}
	vegan := l.person("Konstantin", "child", "vegan")
	_ = vegan
	steak := l.en("/settings/lists/library/beef-steak").body
	libHas(t, "steak", steak, "Does not suit")
	libHasNot(t, "steak", steak, "everyone at your home")
}

func TestAnArticleNobodySuitsIsNotSuggestedAndSaysWhy(t *testing.T) {
	t.Parallel()
	l := newLibBox(t)
	l.db.SetPersonFood(l.h.ID, l.mama.ID, store.Food{Diet: "vegan"})
	body := l.en("/settings/lists/library/beef-steak").body
	libHas(t, "steak", body, "nobody at your home", "No, suits nobody at your home", "Off. Beef steak suits nobody at your home, so Hejmo does not suggest it.")
}

func TestHidingByHandIsStoredShownAsAWordAndUndone(t *testing.T) {
	t.Parallel()
	l := newLibBox(t)
	r := l.postEN("/settings/lists/library/walnuts/hidden", url.Values{"hidden": {"on"}})
	if r.status != http.StatusSeeOther || r.location != "/settings/lists/library/walnuts" {
		t.Fatalf("hide = %d %q", r.status, r.location)
	}
	page := l.en("/settings/lists/library/walnuts").body
	libHas(t, "after hiding", page, "“Walnuts” is hidden. Hejmo no longer suggests it.", `name="hidden" checked aria-label="Hide Walnuts by hand"`, "No, hidden by hand")
	libHas(t, "library", l.en("/settings/lists/library?q=walnuts").body, `class="tag hidden-tag">Hidden by hand`)
	r = l.postEN("/settings/lists/library/walnuts/hidden", url.Values{})
	libHas(t, "after showing", l.en(r.location).body, "“Walnuts” is suggested again.")
	if r := l.postEN("/settings/lists/library/nothing/hidden", url.Values{"hidden": {"on"}}); r.status != http.StatusNotFound {
		t.Errorf("hiding an unknown article = %d", r.status)
	}
}

func TestCopyingAShippedArticleMakesAnOwnOneWithItsUnitsAndHidesTheOriginal(t *testing.T) {
	t.Parallel()
	l := newLibBox(t)
	r := l.postEN("/settings/lists/library/peanut-butter/copy", url.Values{})
	if r.status != http.StatusSeeOther {
		t.Fatalf("copy = %d", r.status)
	}
	m := idRe.FindStringSubmatch(r.location)
	if m == nil {
		t.Fatalf("location %q", r.location)
	}
	own, err := l.db.Article(l.h.ID, m[1])
	if err != nil || own.Name != "Peanut butter" || own.Dept != "pantry" || own.Kind != "vegan" || own.Unit1 != "piece" || own.Unit2 != "g" || strings.Join(own.Allergens, ",") != "peanuts" {
		t.Errorf("copy = %+v, %v", own, err)
	}
	if hidden, _ := l.db.HiddenArticles(l.h.ID); !hidden["peanut-butter"] {
		t.Error("the original is not hidden")
	}
	libHas(t, "after copy", l.en(r.location).body, "Own version of “Peanut butter” added. The original from Hejmo is hidden.")
}

func TestOwnArticleFormFollowsThePreviewOrderAndHasNoPreselectedKindOrPicture(t *testing.T) {
	t.Parallel()
	l := newLibBox(t)
	mia := l.person("Mia", "child", "omnivore", "peanuts")
	_ = mia
	r := l.en("/settings/lists/library/new")
	if r.status != http.StatusOK {
		t.Fatalf("GET = %d", r.status)
	}
	libHas(t, "form", r.body, `<h1>Own article</h1>`, `enctype="multipart/form-data" class="steps food-form article-form lib-form"`, "What is the article called?", `<textarea name="name" rows="2" maxlength="60" required`,
		"Department", `name="dept" value="other" checked`, `<span class="dept-sym" aria-hidden="true">📦</span>Other`, "Picture", `class="tag hidden-tag">optional`,
		`<fieldset class="seg">`, `name="pic_source" value="symbol">Symbol`, `name="pic_source" value="upload">Photo`, `name="pic_source" value="web">Picture from the web`,
		`class="pic-fallback"`, "Nothing chosen yet: Hejmo shows the symbol of the department “Other”.", `type="file" name="photo" accept="image/jpeg,image/png,image/webp,image/gif"`, "up to 15 MB",
		`name="url"`, `name="fetch" value="1"`, `name="symq"`, `name="symsearch" value="1" formnovalidate`, "Matching the department “Other”",
		"Units", "First unit", "Second unit", `name="unit1" value="piece" checked`, `name="unit2" value="" checked`, "Dozen (12 pieces)", "Gram (g)", "Piece: already first", "Only units that are switched on are listed here.",
		"What is in it?", "With meat", "Suits: eats everything", "With fish or seafood", `name="kind" value="vegan"`, "Not food",
		"Which allergens does it contain?", `class="hint caveat"`, "Experimental, no guarantee:", `<small class="aff">`, "Mia</span> is allergic", "Add article")
	// Nothing preselected: no kind, no picture source.
	libHasNot(t, "form", r.body, `name="kind" value="meat" checked`, `name="kind" value="vegan" checked`, `name="kind" value="veg" checked`, `name="pic_source" value="symbol" checked`, `name="pic_source" value="web" checked`)
	// The people who are allergic are named on the allergen cards, and only there.
	if !regexp.MustCompile(`(?s)value="peanuts"><b>Peanuts</b><small>[^<]*</small><small class="aff"><span class="who"><span class="marker"[^>]*></span>Mia</span> is allergic</small>`).MatchString(r.body) {
		t.Errorf("the peanuts card does not name Mia\n%s", r.body)
	}
	if strings.Count(r.body, `class="aff"`) != 1 {
		t.Error("only the peanut card names someone")
	}
	// The units are the units in words, the symbols carry their names.
	if strings.Contains(r.body, ">l<") && !strings.Contains(r.body, "Litre (l)") {
		t.Error("a lone l is not enough")
	}
	for _, want := range []string{"Shipped symbols of the department come first"} {
		_ = want
	}
	libHas(t, "symbols with names", r.body, `class="sym-opt"`, `<span class="n">package</span>`)
}

func TestSymbolSearchFindsGermanAndEnglishNamesAndSaysWhenNothingMatches(t *testing.T) {
	t.Parallel()
	l := newLibBox(t)
	r := l.postEN("/settings/lists/library", url.Values{"name": {"Katzenfutter Huhn"}, "dept": {"other"}, "pic_source": {"symbol"}, "symq": {"katze"}, "symsearch": {"1"}, "unit1": {"piece"}})
	if r.status != http.StatusOK {
		t.Fatalf("search = %d", r.status)
	}
	libHas(t, "search", r.body, `name="pic_source" value="symbol" checked`, `class="results-head"`, "symbols for “katze”", "cat", "black cat", `value="🐈"`, ">Katzenfutter Huhn</textarea>")
	en := l.postEN("/settings/lists/library", url.Values{"name": {"x"}, "pic_source": {"symbol"}, "symq": {"cat"}, "symsearch": {"1"}}).body
	libHas(t, "english search", en, "symbols for “cat”", "cat", `value="🐈"`)
	umlaut := l.postEN("/settings/lists/library", url.Values{"name": {"x"}, "pic_source": {"symbol"}, "symq": {"schussel"}, "symsearch": {"1"}}).body
	libHas(t, "ü = u", umlaut, "bowl with spoon")
	none := l.postEN("/settings/lists/library", url.Values{"name": {"x"}, "pic_source": {"symbol"}, "symq": {"Quinoa"}, "symsearch": {"1"}})
	libHas(t, "no symbol", none.body, `class="empty" data-empty`, "No symbol matches “Quinoa”.", "grain", "photo of the packet")
	libHasNot(t, "no symbol", none.body, `class="sym-opt"`)
	// Without a search the symbols of the department come first, the department's own first.
	pantry := l.postEN("/settings/lists/library", url.Values{"name": {"x"}, "dept": {"pantry"}, "pic_source": {"symbol"}, "symsearch": {"1"}}).body
	libHas(t, "pantry symbols", pantry, "Matching the department “Pantry and breakfast”")
	if strings.Index(pantry, `value="🥫"`) > strings.Index(pantry, `value="🥣"`) {
		t.Error("the department's own symbol comes first")
	}
}

func TestOwnArticleWithAChosenSymbolIsCreatedAndShownWithIt(t *testing.T) {
	t.Parallel()
	l := newLibBox(t)
	f := articleForm()
	f.Set("pic_source", "symbol")
	f.Set("symbol", "🐈")
	id := l.createArticle(f)
	a, _ := l.db.Article(l.h.ID, id)
	if a.PicSource != "symbol" || a.Symbol != "🐈" || a.HasPicture {
		t.Errorf("stored = %+v", a)
	}
	r := l.en("/settings/lists/library")
	libHas(t, "library", r.body, "“Katzenfutter Huhn” is now in the library. Hejmo suggests it when adding.", `<span class="art-sym" aria-hidden="true">🐈</span>`, "<span>Symbol</span>", "<span>Not food</span>")
	// The chosen symbol is announced in words on the edit page.
	libHas(t, "edit page", l.en("/settings/lists/library/"+id).body, `class="sym-chosen"`, "Chosen: cat", "This is how “Katzenfutter Huhn” appears in the list and on the tile.", `name="pic_source" value="symbol" checked`, `value="🐈" checked`)
	// Without any choice the department's symbol stays the fallback.
	g := articleForm()
	g.Set("name", "Windeln Größe 5")
	g.Set("dept", "baby")
	gid := l.createArticle(g)
	row := l.en("/settings/lists/library").body
	libHas(t, "fallback row", row, `<span class="art-sym" aria-hidden="true">🍼</span>`, "No picture, with the symbol of the department")
	_ = gid
}

func TestOwnArticleKindHasNoDefaultAndTheErrorSitsAtTheGroup(t *testing.T) {
	t.Parallel()
	l := newLibBox(t)
	f := articleForm()
	f.Del("kind")
	r := l.postEN("/settings/lists/library", f)
	if r.status != http.StatusUnprocessableEntity {
		t.Fatalf("no kind = %d", r.status)
	}
	libHas(t, "refused", r.body, `<fieldset class="choice kind" aria-invalid="true" aria-describedby="err-kind">`, `id="err-kind"`, "Choose what is in it. Hejmo does not guess")
	if as, _ := l.db.Articles(l.h.ID); len(as) != 0 {
		t.Error("an article without a kind was stored")
	}
}

func TestOwnArticleNeedsANameAndKeepsWhatWasTyped(t *testing.T) {
	t.Parallel()
	l := newLibBox(t)
	f := articleForm()
	f.Set("name", "   ")
	f.Set("dept", "dairy")
	f.Set("allergens", "milk")
	f.Set("kind", "veg")
	r := l.postEN("/settings/lists/library", f)
	if r.status != http.StatusUnprocessableEntity {
		t.Fatalf("no name = %d", r.status)
	}
	libHas(t, "refused", r.body, "Give the article a name.", `id="err-name"`, `name="dept" value="dairy" checked`, `name="kind" value="veg" checked`, `name="allergens" value="milk" checked`)
	long := articleForm()
	long.Set("name", strings.Repeat("a", 61))
	if r := l.postEN("/settings/lists/library", long); r.status != http.StatusUnprocessableEntity || !strings.Contains(r.body, "The name is too long. At most 60 characters.") {
		t.Errorf("long name = %d", r.status)
	}
}

func TestNonFoodArticlesDropTheirAllergens(t *testing.T) {
	t.Parallel()
	l := newLibBox(t)
	f := articleForm()
	f.Set("allergens", "milk")
	id := l.createArticle(f)
	a, _ := l.db.Article(l.h.ID, id)
	if len(a.Allergens) != 0 {
		t.Errorf("allergens = %v", a.Allergens)
	}
}

func TestUnitsOnlyOfferedOnesAreAcceptedAndTheSecondDiffersFromTheFirst(t *testing.T) {
	t.Parallel()
	l := newLibBox(t)
	body := l.en("/settings/lists/library/new").body
	libHasNot(t, "units", body, `value="oz"`, `value="lb"`, `value="gallon_us"`)
	f := articleForm()
	f.Set("unit1", "lb")
	if r := l.postEN("/settings/lists/library", f); r.status != http.StatusUnprocessableEntity || !strings.Contains(r.body, "This unit is switched off.") {
		t.Errorf("a switched-off unit = %d", r.status)
	}
	f.Set("unit1", "g")
	f.Set("unit2", "g")
	if r := l.postEN("/settings/lists/library", f); r.status != http.StatusUnprocessableEntity || !strings.Contains(r.body, "The second unit must differ from the first.") {
		t.Errorf("the same unit twice = %d", r.status)
	}
	f.Set("unit2", "kg")
	id := l.createArticle(f)
	a, _ := l.db.Article(l.h.ID, id)
	if a.Unit1 != "g" || a.Unit2 != "kg" {
		t.Errorf("units = %q %q", a.Unit1, a.Unit2)
	}
	// On the edit page the second row shows the first unit dashed, with the reason in words.
	libHas(t, "edit", l.en("/settings/lists/library/"+id).body, `<label class="pchip plain same"><input type="radio" name="unit2" value="g" disabled>Gram (g): already first</label>`)
}

func TestAPhotoIsScaledDownAndStoredWithoutMetadataAndServedToEveryone(t *testing.T) {
	t.Parallel()
	l := newLibBox(t)
	f := articleForm()
	f.Set("pic_source", "upload")
	photo := testJPEG(t, 2000, 1000)
	r := l.postMultipart("/settings/lists/library", f, upload{"photo", "IMG_4711.JPG", photo})
	if r.status != http.StatusSeeOther {
		t.Fatalf("create with photo = %d\n%s", r.status, r.body)
	}
	as, _ := l.db.Articles(l.h.ID)
	if len(as) != 1 || !as[0].HasPicture || as[0].PicSource != "upload" || as[0].PictureFromW != 2000 || as[0].PictureFromH != 1000 {
		t.Fatalf("stored = %+v", as)
	}
	stored, _ := l.db.ArticlePicture(l.h.ID, as[0].ID)
	img, err := jpeg.Decode(bytes.NewReader(stored))
	if err != nil || img.Bounds().Dx() != 512 || img.Bounds().Dy() != 256 {
		t.Errorf("stored picture is %v, %v", img.Bounds(), err)
	}
	if bytes.Contains(stored, []byte("Exif")) {
		t.Error("metadata survived")
	}
	// The picture is served to a signed-in child (the tile view) and not to nobody.
	pic := l.get("/articles/" + as[0].ID + "/picture")
	if pic.status != http.StatusOK || !strings.HasPrefix(pic.body, "\xff\xd8") {
		t.Errorf("picture = %d", pic.status)
	}
	child := childSession(t, l.box)
	if r := child.get("/articles/" + as[0].ID + "/picture"); r.status != http.StatusOK {
		t.Errorf("child picture = %d", r.status)
	}
	anon := newBox(t)
	if r := anon.get("/articles/" + as[0].ID + "/picture"); r.status == http.StatusOK {
		t.Error("a picture is served without a session")
	}
	libHas(t, "library", l.en("/settings/lists/library").body, `<img src="/articles/`+as[0].ID+`/picture?v=`, "<span>Photo</span>")
	// Edit page: the stored picture is the current one, it can be replaced or removed.
	edit := l.en("/settings/lists/library/" + as[0].ID).body
	libHas(t, "edit", edit, `class="pic-result"`, "Current picture", "Uploaded on ", "512 × 256 pixels", `name="picture" value="replace"`, `name="picture" value="remove"`, `name="pic_source" value="upload" checked`)
	libHasNot(t, "edit", edit, `class="pic-fallback"`)
}

func TestAPhotoStaysWhileTheRestOfTheFormIsCorrected(t *testing.T) {
	t.Parallel()
	l := newLibBox(t)
	f := articleForm()
	f.Set("pic_source", "upload")
	f.Del("kind")
	r := l.postMultipart("/settings/lists/library", f, upload{"photo", "IMG_4711.JPG", testJPEG(t, 800, 600)})
	if r.status != http.StatusUnprocessableEntity {
		t.Fatalf("= %d", r.status)
	}
	libHas(t, "kept photo", r.body, `class="pic-result"`, "Photo loaded and scaled down", "IMG_4711.JPG: from 800 × 600 to 512 × 384 pixels", "Location and camera data removed.", `name="picture_token"`, "/settings/lists/library/picture/")
	token := regexp.MustCompile(`name="picture_token" value="([0-9a-f]+)"`).FindStringSubmatch(r.body)[1]
	if p := l.get("/settings/lists/library/picture/" + token); p.status != http.StatusOK || !strings.HasPrefix(p.body, "\xff\xd8") {
		t.Errorf("pending picture = %d", p.status)
	}
	f.Set("kind", "nonfood")
	f.Set("picture_token", token)
	if r := l.postEN("/settings/lists/library", f); r.status != http.StatusSeeOther {
		t.Fatalf("second try = %d\n%s", r.status, r.body)
	}
	as, _ := l.db.Articles(l.h.ID)
	if len(as) != 1 || !as[0].HasPicture {
		t.Errorf("the photo was lost: %+v", as)
	}
	if p := l.get("/settings/lists/library/picture/" + token); p.status != http.StatusNotFound {
		t.Error("the pending picture is still there after saving")
	}
	if p := l.get("/settings/lists/library/picture/nothing"); p.status != http.StatusNotFound {
		t.Errorf("unknown token = %d", p.status)
	}
}

func TestPhotoErrorsStandAtTheFieldAndSaveNothing(t *testing.T) {
	t.Parallel()
	l := newLibBox(t)
	f := articleForm()
	f.Set("pic_source", "upload")
	big := bytes.Repeat([]byte{0xff}, 16<<20+1<<20)
	r := l.postMultipart("/settings/lists/library", f, upload{"photo", "IMG_4711.DNG", big})
	if r.status != http.StatusUnprocessableEntity {
		t.Fatalf("too big = %d", r.status)
	}
	libHas(t, "too big", r.body, `id="err-photo"`, "“IMG_4711.DNG” is 17.0 MB", "Hejmo takes photos up to 15 MB.", `name="photo"`)
	// The name of the file, its size and the limit are said in German too.
	text := l.postMultipart("/settings/lists/library", f, upload{"photo", "notes.txt", []byte("not a picture")})
	libHas(t, "wrong type", text.body, `id="err-photo"`, "“notes.txt” is not a picture Hejmo can read.", "JPEG, PNG, WebP and GIF")
	page := l.postMultipart("/settings/lists/library", f, upload{"photo", "shop.html", []byte("<!doctype html><title>x</title>")})
	libHas(t, "web page", page.body, "“shop.html” is a web page, not a picture.")
	if as, _ := l.db.Articles(l.h.ID); len(as) != 0 {
		t.Errorf("an article with a refused picture was stored: %v", as)
	}
	// The article form without a picture at all still works as the same form posts it.
	f.Set("pic_source", "upload")
	if r := l.postMultipart("/settings/lists/library", f); r.status != http.StatusSeeOther {
		t.Errorf("source photo without a file = %d, want the article saved without a picture", r.status)
	}
}

func TestAnImageFromTheWebIsDownloadedOnceStoredScaledAndTheAddressForgotten(t *testing.T) {
	t.Parallel()
	srv := imageServer(t, 1200, 1200)
	l := newLibBox(t, withFetcher(loopbackFetcher()))
	f := articleForm()
	f.Set("pic_source", "web")
	f.Set("url", srv.URL+"/media/hafer.jpg")
	f.Set("fetch", "1")
	r := l.postEN("/settings/lists/library", f)
	if r.status != http.StatusOK {
		t.Fatalf("fetch = %d\n%s", r.status, r.body)
	}
	libHas(t, "fetched", r.body, `class="pic-result"`, "Picture fetched and scaled down", "From 127.0.0.1: 1200 × 1200 to 512 × 512 pixels", "the website is not asked again", `name="picture" value="replace"`, `name="pic_source" value="web" checked`)
	libHasNot(t, "fetched", r.body, "Nothing chosen yet", srv.URL, "Remove picture")
	if as, _ := l.db.Articles(l.h.ID); len(as) != 0 {
		t.Error("fetching saves nothing yet")
	}
	token := regexp.MustCompile(`name="picture_token" value="([0-9a-f]+)"`).FindStringSubmatch(r.body)[1]
	save := articleForm()
	save.Set("pic_source", "web")
	save.Set("picture_token", token)
	if r := l.postEN("/settings/lists/library", save); r.status != http.StatusSeeOther {
		t.Fatalf("save = %d\n%s", r.status, r.body)
	}
	as, _ := l.db.Articles(l.h.ID)
	if len(as) != 1 || as[0].PicSource != "web" || as[0].PictureHost != "127.0.0.1" || !as[0].HasPicture {
		t.Fatalf("stored = %+v", as)
	}
	stored, _ := l.db.ArticlePicture(l.h.ID, as[0].ID)
	if img, err := jpeg.Decode(bytes.NewReader(stored)); err != nil || img.Bounds().Dx() != 512 {
		t.Errorf("stored picture: %v %v", img, err)
	}
	// Nothing links to the web address any more.
	for _, page := range []string{"/settings/lists/library", "/settings/lists/library/" + as[0].ID} {
		libHasNot(t, page, l.en(page).body, srv.URL, "/media/hafer.jpg")
	}
}

func TestSavingWithAWebAddressFetchesItWithoutPressingGetPicture(t *testing.T) {
	t.Parallel()
	srv := imageServer(t, 600, 400)
	l := newLibBox(t, withFetcher(loopbackFetcher()))
	f := articleForm()
	f.Set("pic_source", "web")
	f.Set("url", srv.URL+"/a.jpg")
	if r := l.postEN("/settings/lists/library", f); r.status != http.StatusSeeOther {
		t.Fatalf("= %d\n%s", r.status, r.body)
	}
	if as, _ := l.db.Articles(l.h.ID); len(as) != 1 || !as[0].HasPicture {
		t.Errorf("stored = %+v", as)
	}
}

func TestWebAddressErrorsStandAtTheFieldAndNameWhatIsWrong(t *testing.T) {
	t.Parallel()
	srv := imageServer(t, 100, 100)
	l := newLibBox(t, withFetcher(loopbackFetcher()))
	post := func(u string) resp {
		f := articleForm()
		f.Set("pic_source", "web")
		f.Set("url", u)
		f.Set("fetch", "1")
		return l.postEN("/settings/lists/library", f)
	}
	r := post(srv.URL + "/page")
	if r.status != http.StatusUnprocessableEntity {
		t.Fatalf("web page = %d", r.status)
	}
	libHas(t, "web page", r.body, `id="err-url"`, `name="url" value="`+srv.URL+`/page"`, "a web page. Open the picture itself", "copy its address", `name="url"`, `aria-invalid="true"`)
	// An address in the own network is refused and named, and the box never asked it.
	l2 := newLibBox(t)
	f := articleForm()
	f.Set("pic_source", "web")
	f.Set("url", "http://192.168.178.20/bilder/birne.jpg")
	f.Set("fetch", "1")
	own := l2.postEN("/settings/lists/library", f)
	if own.status != http.StatusUnprocessableEntity {
		t.Fatalf("own network = %d", own.status)
	}
	libHas(t, "own network", own.body, `id="err-url"`, "leads into your own network (192.168.178.20), not to the internet.", "only loads pictures from public websites", "upload it as a photo")
	local := l2.postEN("/settings/lists/library", url.Values{"name": {"x"}, "pic_source": {"web"}, "url": {srv.URL + "/a.jpg"}, "fetch": {"1"}})
	libHas(t, "loopback", local.body, "leads into your own network (127.0.0.1)")
	for u, want := range map[string]string{
		"ftp://example.org/a.jpg": "must start with https:// or http://",
		"":                        "Enter the address of the picture.",
	} {
		if got := post2(l2, u); !strings.Contains(got, want) {
			t.Errorf("%q: %s", u, got)
		}
	}
	if as, _ := l2.db.Articles(l2.h.ID); len(as) != 0 {
		t.Error("an article was stored")
	}
}

func post2(l libBox, u string) string {
	return l.postEN("/settings/lists/library", url.Values{"name": {"x"}, "pic_source": {"web"}, "url": {u}, "fetch": {"1"}}).body
}

func TestAnotherPictureStartsOverAndRemovingGoesBackToTheDepartmentSymbol(t *testing.T) {
	t.Parallel()
	srv := imageServer(t, 300, 300)
	l := newLibBox(t, withFetcher(loopbackFetcher()))
	f := articleForm()
	f.Set("pic_source", "web")
	f.Set("url", srv.URL+"/a.jpg")
	id := l.createArticle(f)
	// Another picture: the address field comes back, the stored picture stays until a new one is saved.
	again := l.postEN("/settings/lists/library/"+id, url.Values{"name": {"Katzenfutter Huhn"}, "dept": {"other"}, "kind": {"nonfood"}, "unit1": {"pack"}, "pic_source": {"web"}, "picture": {"replace"}})
	if again.status != http.StatusOK {
		t.Fatalf("replace = %d", again.status)
	}
	libHas(t, "replace", again.body, `name="url"`, `name="replacing" value="1"`)
	libHasNot(t, "replace", again.body, `class="pic-result"`)
	if a, _ := l.db.Article(l.h.ID, id); !a.HasPicture {
		t.Error("the picture is gone before a new one is saved")
	}
	// Saving the changed name without a new picture keeps the old picture.
	save := url.Values{"name": {"Katzenfutter Rind"}, "dept": {"other"}, "kind": {"nonfood"}, "unit1": {"pack"}, "pic_source": {"web"}, "replacing": {"1"}}
	if r := l.postEN("/settings/lists/library/"+id, save); r.status != http.StatusSeeOther || r.location != "/settings/lists/library/"+id {
		t.Fatalf("save = %d %q\n%s", r.status, r.location, r.body)
	}
	a, _ := l.db.Article(l.h.ID, id)
	if a.Name != "Katzenfutter Rind" || !a.HasPicture {
		t.Errorf("after save = %+v", a)
	}
	libHas(t, "after save", l.en("/settings/lists/library/"+id).body, "“Katzenfutter Rind” saved.")
	// Remove: the tile shows the symbol of the department again.
	rm := url.Values{"name": {"Katzenfutter Rind"}, "dept": {"other"}, "kind": {"nonfood"}, "unit1": {"pack"}, "picture": {"remove"}}
	if r := l.postEN("/settings/lists/library/"+id, rm); r.status != http.StatusSeeOther {
		t.Fatalf("remove = %d\n%s", r.status, r.body)
	}
	a, _ = l.db.Article(l.h.ID, id)
	if a.HasPicture || a.PicSource != "" {
		t.Errorf("after remove = %+v", a)
	}
	libHas(t, "after remove", l.en("/settings/lists/library/"+id).body, `class="pic-fallback"`)
}

func TestChangingAnOwnArticleAndDeletingItKeepsTheEntriesOnTheListsAsText(t *testing.T) {
	t.Parallel()
	l := newLibBox(t)
	id := l.createArticle(articleForm())
	lst, err := l.db.CreateList(l.h.ID, listFor("Einkauf"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := l.db.AddEntry(l.h.ID, lst.ID, "Katzenfutter Huhn"); err != nil {
		t.Fatal(err)
	}
	edit := l.en("/settings/lists/library/" + id).body
	libHas(t, "edit page", edit, `<h1 class="art-title">`, "Own article, added by Mama on ", "Currently on", `href="/lists/`+lst.ID+`"`, "Einkauf",
		`<details class="danger">`, "Delete article", "On these lists it stays as text, without picture and without warning:", "<strong>Einkauf</strong>", `Yes, delete “Katzenfutter Huhn”`, `action="/settings/lists/library/`+id+`/delete"`)
	r := l.postEN("/settings/lists/library/"+id+"/delete", url.Values{})
	if r.status != http.StatusSeeOther || r.location != "/settings/lists/library" {
		t.Fatalf("delete = %d %q", r.status, r.location)
	}
	libHas(t, "after delete", l.en(r.location).body, "“Katzenfutter Huhn” is deleted.")
	es, _ := l.db.Entries(l.h.ID, lst.ID)
	if len(es) != 1 || es[0].Text != "Katzenfutter Huhn" {
		t.Errorf("the entry changed: %+v", es)
	}
	if r := l.get("/settings/lists/library/" + id); r.status != http.StatusNotFound {
		t.Errorf("deleted article = %d", r.status)
	}
}

func TestAnOwnArticleWarnsByNameInEveryPlaceItShows(t *testing.T) {
	t.Parallel()
	l := newLibBox(t)
	l.person("Charlotte-Sophie", "child", "omnivore", "gluten", "soy")
	f := articleForm()
	f.Set("name", "Hafer-Drink Barista")
	f.Set("dept", "drinks")
	f.Set("kind", "vegan")
	f.Add("allergens", "gluten")
	f.Add("allergens", "soy")
	id := l.createArticle(f)
	line := `Contains Cereals containing gluten and Soy: <span class="who"><span class="marker" data-person="`
	for _, page := range []string{"/settings/lists/library", "/settings/lists/library?q=hafer", "/settings/lists/library/" + id} {
		libHas(t, page, l.en(page).body, line, "Charlotte-Sophie</span> is allergic")
	}
}

func TestNameSuggestionsListUpToFiveSimilarArticlesWithoutTheArticleItself(t *testing.T) {
	t.Parallel()
	l := newLibBox(t)
	f := articleForm()
	f.Set("name", "Hafer-Drink Barista")
	f.Set("dept", "drinks")
	f.Set("kind", "vegan")
	id := l.createArticle(f)
	r := l.get("/settings/lists/library/suggest?q=Hafer")
	if r.status != http.StatusOK {
		t.Fatalf("suggest = %d", r.status)
	}
	libHas(t, "suggestions", r.body, `"Name":"Hafer-Drink Barista"`, `"Name":"Haferflocken"`, `"Href":"/settings/lists/library/`+id+`"`, "eigener Artikel", "von Hejmo")
	if n := strings.Count(r.body, `"Href"`); n > 5 || n < 3 {
		t.Errorf("%d suggestions", n)
	}
	if r := l.get("/settings/lists/library/suggest?q=H"); strings.Contains(r.body, `"Href"`) {
		t.Error("one letter suggests articles")
	}
	if r := childSession(t, l.box).get("/settings/lists/library/suggest?q=Hafer"); r.status != http.StatusForbidden {
		t.Errorf("child = %d", r.status)
	}
}

func TestNamesAndTextsAreEscapedEverywhere(t *testing.T) {
	t.Parallel()
	l := newLibBox(t)
	p := l.person(`<b>Mia</b>`, "child", "omnivore", "milk")
	_ = p
	f := articleForm()
	f.Set("name", `<script>alert(1)</script> Müsli`)
	f.Set("kind", "veg")
	f.Add("allergens", "milk")
	id := l.createArticle(f)
	for _, page := range []string{"/settings/lists", "/settings/lists/library", "/settings/lists/library/" + id, "/settings/lists/library/new", "/settings/lists/library?q=%3Cscript%3E"} {
		body := l.en(page).body
		if strings.Contains(body, "<script>alert(1)") || strings.Contains(body, "<b>Mia</b>") {
			t.Errorf("%s does not escape names", page)
		}
	}
}

// Ticket 182 replaced the back links of the pages below Settings by a trail
// of links inside main; Settings stays marked as the current section.
func TestEveryLibraryPageHasATrailBackToSettingsInsideMain(t *testing.T) {
	t.Parallel()
	l := newLibBox(t)
	l.person("Mia", "child", "omnivore", "milk")
	id := l.createArticle(articleForm())
	for page, up := range map[string]string{
		"/settings/lists":                `<a href="/settings">Settings</a>`,
		"/settings/lists/library":        `<a href="/settings/lists">Lists</a>`,
		"/settings/lists/library/new":    `<a href="/settings/lists/library">Article library</a>`,
		"/settings/lists/library/apples": `<a href="/settings/lists/library">Article library</a>`,
		"/settings/lists/library/" + id:  `<a href="/settings/lists/library">Article library</a>`,
	} {
		body := l.en(page).body
		nav, main := strings.Index(body, `<nav class="crumbs"`), strings.Index(body, "<main")
		at := strings.Index(body, up)
		if nav < main || at < nav {
			t.Errorf("%s: the trail %s is missing or outside main", page, up)
		}
		if !strings.Contains(body, `<a href="/settings" aria-current="true"`) {
			t.Errorf("%s does not mark Settings as the current section", page)
		}
	}
}

func listFor(name string) list.List {
	return list.List{Name: name, Color: "green", Symbol: "square-filled", Sort: list.Manual}
}

// Ticket 278 (ADR 0021): the pantry mark on the web.

func TestAParentSwitchesThePantryMarkOfAShippedArticleAndTheLibraryShowsIt(t *testing.T) {
	t.Parallel()
	l := newLibBox(t)
	libHas(t, "salt", l.en("/settings/lists/library/salt").body, "Pantry article", `name="pantry" checked`, "On. Recipes leave Salt out of the shopping list.")
	libHasNot(t, "apples", l.en("/settings/lists/library/apples").body, `name="pantry" checked`)
	libHas(t, "library row", l.en("/settings/lists/library?q=salt").body, `class="tag">Pantry article`)
	libHasNot(t, "library row", l.en("/settings/lists/library?q=apples").body, "Pantry article")

	r := l.postEN("/settings/lists/library/apples/pantry", url.Values{"pantry": {"on"}})
	if r.status != http.StatusSeeOther || r.location != "/settings/lists/library/apples" {
		t.Fatalf("mark = %d %q", r.status, r.location)
	}
	libHas(t, "after marking", l.en(r.location).body, "“Apples” is now a pantry article. Recipes leave it out of the shopping list.", `name="pantry" checked`)
	libHas(t, "library row", l.en("/settings/lists/library?q=apples").body, `class="tag">Pantry article`)
	libHas(t, "German page", l.de("/settings/lists/library/apples").body, "Vorratsartikel")

	r = l.postEN("/settings/lists/library/salt/pantry", url.Values{})
	libHas(t, "after unmarking", l.en(r.location).body, "“Salt” is no longer a pantry article. Recipes put it on the shopping list.")
	libHasNot(t, "after unmarking", l.en("/settings/lists/library/salt").body, `name="pantry" checked`)
	if r := l.postEN("/settings/lists/library/nothing/pantry", url.Values{"pantry": {"on"}}); r.status != http.StatusNotFound {
		t.Errorf("an unknown article = %d", r.status)
	}
}

func TestTheOwnArticleFormHasAPantrySwitchThatIsStored(t *testing.T) {
	t.Parallel()
	l := newLibBox(t)
	libHas(t, "new form", l.en("/settings/lists/library/new").body, "Pantry article", `name="pantry"`)
	libHasNot(t, "new form", l.en("/settings/lists/library/new").body, `name="pantry" checked`)
	f := articleForm()
	f.Set("name", "Kräutersalz")
	f.Set("pantry", "on")
	id := l.createArticle(f)
	if a, _ := l.db.Article(l.h.ID, id); !a.Pantry {
		t.Fatal("the mark was not stored")
	}
	libHas(t, "edit form", l.en("/settings/lists/library/"+id).body, `name="pantry" checked`)
	f.Del("pantry")
	if r := l.postEN("/settings/lists/library/"+id, f); r.status != http.StatusSeeOther {
		t.Fatalf("save = %d\n%s", r.status, r.body)
	}
	if a, _ := l.db.Article(l.h.ID, id); a.Pantry {
		t.Error("the mark was not removed")
	}
}

func TestACopyOfAShippedArticleKeepsItsPantryMarkAndTheOwnArticleCanBeMarkedOnThePantryRoute(t *testing.T) {
	t.Parallel()
	l := newLibBox(t)
	// pepper is shipped as a pantry article, salt is unmarked by the household first
	l.postEN("/settings/lists/library/salt/pantry", url.Values{})
	for key, want := range map[string]bool{"salt": false, "pepper": true} {
		r := l.postEN("/settings/lists/library/"+key+"/copy", url.Values{})
		m := idRe.FindStringSubmatch(r.location)
		if r.status != http.StatusSeeOther || m == nil {
			t.Fatalf("copy %s = %d %q", key, r.status, r.location)
		}
		if own, err := l.db.Article(l.h.ID, m[1]); err != nil || own.Pantry != want {
			t.Errorf("copy of %s: pantry = %v (%v), want %v", key, own.Pantry, err, want)
		}
	}
	f := articleForm()
	f.Set("name", "Kräutersalz")
	id := l.createArticle(f)
	r := l.postEN("/settings/lists/library/"+id+"/pantry", url.Values{"pantry": {"on"}})
	if r.status != http.StatusSeeOther || r.location != "/settings/lists/library/"+id {
		t.Fatalf("mark own = %d %q", r.status, r.location)
	}
	if a, _ := l.db.Article(l.h.ID, id); !a.Pantry {
		t.Error("the own article was not marked")
	}
	l.postEN("/settings/lists/library/"+id+"/pantry", url.Values{})
	if a, _ := l.db.Article(l.h.ID, id); a.Pantry {
		t.Error("the own article was not unmarked")
	}
}
