package server_test

import (
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/hejmo-home/hejmo/internal/library"
	"github.com/hejmo-home/hejmo/internal/list"
	"github.com/hejmo-home/hejmo/internal/store"
	"github.com/hejmo-home/hejmo/internal/unit"
)

// Ticket 277 (ADR 0019, preview-277.html): varieties on list entries on the
// web. The shipped article "Wheat flour" / "Weizenmehl" stands for "Mehl".

const flour = "wheat-flour"

// vitem is the add card's post for an article with a variety chosen as "new variety".
func (s shop) vitem(art, variety string, qty ...string) url.Values {
	f := s.item(art, qty...)
	if variety != "" {
		f.Set("variety_pick", "new")
		f.Set("variety", variety)
	}
	return f
}

// vlines are the entries as "Text · Variety quantity".
func (s shop) vlines() []string {
	s.t.Helper()
	es, err := s.db.Entries(s.h.ID, s.id)
	if err != nil {
		s.t.Fatal(err)
	}
	var out []string
	for _, e := range es {
		l := e.Text
		if e.Variety != "" {
			l += " · " + e.Variety
		}
		if !e.Qty.None() {
			l += " " + unit.Format(e.Qty.Amount(), e.Qty.Unit, "en") + " " + e.Qty.Unit
		}
		out = append(out, l)
	}
	return out
}

func (s shop) seedVariety(art, text, variety string, amount float64, u string) string {
	s.t.Helper()
	it := store.Item{Text: text, ArticleID: art, Variety: variety}
	if u != "" {
		it.Qty = list.NewQuantity(amount, u)
	}
	r, err := s.db.AddItem(s.h.ID, s.id, it, func(b float64, k unit.Kind) string { return u })
	if err != nil {
		s.t.Fatal(err)
	}
	return r.Entry.ID
}

func TestAddingAVarietyNextToTheArticleKeepsTwoEntriesAndTheLineSaysWhy(t *testing.T) {
	s := newShop(t)
	s.add(s.vitem(flour, "", "500", "g"))
	_, page := s.add(s.vitem(flour, "Type 405", "1", "kg"))
	if got := s.vlines(); !same(got, []string{"Wheat flour 500 g", "Wheat flour · Type 405 1 kg"}) {
		t.Fatalf("%v", got)
	}
	for _, want := range []string{"“Wheat flour · Type 405” added. It stands next to “Wheat flour”, because Hejmo only adds up the same variety.", `<span class="variety"> · Type 405</span>`} {
		if !strings.Contains(page.body, want) {
			t.Errorf("page lacks %q:\n%s", want, page.body)
		}
	}
	// The same variety adds up, ignoring case and spaces.
	_, page = s.add(s.vitem(flour, " type   405 ", "1", "kg"))
	if got := s.vlines(); !same(got, []string{"Wheat flour 500 g", "Wheat flour · Type 405 2 kg"}) {
		t.Fatalf("%v", got)
	}
	if !strings.Contains(page.body, "added to <b>1 kg</b>: now <b>2 kg</b>.") || !strings.Contains(page.body, "Wheat flour · Type 405") {
		t.Errorf("the line:\n%s", page.body)
	}
}

func TestWithoutQuantityTheEntryExistsOnlyForTheSameArticleAndVariety(t *testing.T) {
	s := newShop(t)
	s.add(s.vitem(flour, "Type 405"))
	_, page := s.add(s.vitem(flour, "Type 405"))
	if !strings.Contains(page.body, "is already on the list. No second entry.") || len(s.vlines()) != 1 {
		t.Errorf("%v\n%s", s.vlines(), page.body)
	}
	s.add(s.vitem(flour, ""))
	s.add(s.vitem(flour, "Type 550"))
	if got := s.vlines(); !same(got, []string{"Wheat flour · Type 405", "Wheat flour", "Wheat flour · Type 550"}) {
		t.Errorf("%v", got)
	}
}

func TestTheSentenceUnderTheQuantitySaysWhyAnotherVarietyGetsItsOwnEntry(t *testing.T) {
	s := newShop(t)
	s.seedVariety(flour, "Wheat flour", "", 500, "g")
	body := s.en(s.path("/quantity?part=merge&article=" + flour + "&variety=Type+405&text=Wheat+flour&amount=1&unit=kg")).body
	want := "Already on the list: <b>Wheat flour</b> without variety, <b>500 g</b>. “Type 405” gets its own entry, because Hejmo only adds up the same variety."
	if !strings.Contains(body, want) {
		t.Errorf("sentence:\n%s", body)
	}
	if body := s.en(s.path("/quantity?part=merge&article=" + flour + "&text=Wheat+flour&amount=1&unit=kg")).body; !strings.Contains(body, "Now <b>500 g</b>") && !strings.Contains(body, "<b>1.5 kg</b>") {
		t.Errorf("no variety still adds up:\n%s", body)
	}
	s.seedVariety(flour, "Wheat flour", "Type 550", 1, "kg")
	body = s.en(s.path("/quantity?part=merge&article=" + flour + "&variety=Type+550&text=Wheat+flour&amount=1&unit=kg")).body
	if !strings.Contains(body, "<b>2 kg</b>") {
		t.Errorf("the same variety adds up:\n%s", body)
	}
}

func TestTypingTheArticleAndAVarietyOffersTheVarietyRowFirst(t *testing.T) {
	s := newShop(t)
	s.seedVariety(flour, "Wheat flour", "Type 550", 0, "")
	s.seedVariety(flour, "Wheat flour", "", 500, "g")
	body := s.en(s.path("/suggest?q=Wheat+flour+Type+405&mode=quick")).body
	first := strings.Index(body, `data-article="wheat-flour"`)
	for _, want := range []string{
		`data-variety="Type 405"`, `<b>Wheat flour<span class="variety"> · Type 405</span></b>`, "New variety of Wheat flour · Baking and spices",
		"Without variety · Baking and spices", "Variety of Wheat flour, used before · Baking and spices", "Already on the list: 500 g",
		"3 suggestions match “Wheat flour Type 405”. A tap puts the entry on the list at once.",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("suggestions lack %q:\n%s", want, body)
		}
	}
	if first < 0 || strings.Index(body, `data-variety="Type 405"`) > strings.Index(body, `<b>Wheat flour</b>`) {
		t.Errorf("the typed variety comes first:\n%s", body)
	}
	if strings.Index(body, `<b>Wheat flour</b>`) > strings.Index(body, `data-variety="Type 550"`) || strings.Index(body, `class="free"`) < strings.Index(body, `data-variety="Type 550"`) {
		t.Errorf("order: variety typed, plain article, known varieties, free text:\n%s", body)
	}
	for _, sep := range []string{"Wheat flour, Type 405", "wheat flour · Type 405", "Wheat flour: Type 405", "Wheat flour-Type 405"} {
		if b := s.en(s.path("/suggest?q=" + url.QueryEscape(sep) + "&mode=quick")).body; !strings.Contains(b, `data-variety="Type 405"`) {
			t.Errorf("%q: %s", sep, b)
		}
	}
	de := s.de(s.path("/suggest?q=Weizenmehl+Type+405&mode=qty")).body
	if !strings.Contains(de, "Neue Sorte von Weizenmehl · Backen und Gewürze") || !strings.Contains(de, "Vorschläge passen zu „Weizenmehl Type 405“. Antippen wählt den Artikel nur aus.") {
		t.Errorf("German:\n%s", de)
	}
}

func TestKnownVarietiesAreOfferedAfterTheArticleOnListOnesFirstAtMostFourRows(t *testing.T) {
	s := newShop(t)
	s.seedVariety("apples", "Apples", "Boskop", 0, "")
	s.seedVariety("apples", "Apples", "Elstar", 6, "piece")
	body := s.en(s.path("/suggest?q=Apple&mode=quick")).body
	for _, want := range []string{"<b>Apples</b>", `<b>Apples<span class="variety"> · Elstar</span></b>`, "Variety of Apples, used before", "Already on the list: 6 pieces", `<b>Apples<span class="variety"> · Boskop</span></b>`} {
		if !strings.Contains(body, want) {
			t.Errorf("lacks %q:\n%s", want, body)
		}
	}
	if strings.Index(body, "Elstar") > strings.Index(body, "Boskop") {
		t.Errorf("the varieties on the list come first:\n%s", body)
	}
	if n := strings.Count(body, `role="option"`); n > 5 {
		t.Errorf("up to 4 article rows and the free row: %d", n)
	}
	// Without a known variety the small line is the department alone, as before.
	if b := s.en(s.path("/suggest?q=Egg&mode=quick")).body; strings.Contains(b, "Without variety") {
		t.Errorf("no variety rows, no 'Without variety':\n%s", b)
	}
}

func TestPickingTheTypedVarietyRowAddsTheArticleWithTheVariety(t *testing.T) {
	s := newShop(t)
	f := url.Values{"article": {flour}, "mode": {"quick"}, "text": {"Wheat flour Type 405"}, "variety_pick": {"new"}, "variety": {"Type 405"}}
	r, _ := s.add(f)
	if r.status != http.StatusSeeOther || !same(s.vlines(), []string{"Wheat flour · Type 405"}) {
		t.Fatalf("%d %v\n%s", r.status, s.vlines(), r.body)
	}
	// The typed text is not stale: a picked article stays picked when the text starts with its name.
	if es, _ := s.db.Entries(s.h.ID, s.id); es[0].ArticleID != flour {
		t.Errorf("%+v", es)
	}
}

func TestInQuantityModeTheChosenArticleShowsItsVarietyAndTheSorteRow(t *testing.T) {
	s := newShop(t)
	s.seedVariety(flour, "Wheat flour", "Type 550", 0, "")
	body := s.en(s.path("/quantity?article=" + flour + "&variety=Type+405&text=Wheat+flour+Type+405&amount=1")).body
	for _, want := range []string{
		`<span>Wheat flour<span class="variety"> · Type 405</span></span>`,
		`<details class="more variety-more"`, `<span>Variety:</span><span class="v-state chosen">Type 405</span>`,
		`name="variety_pick" value=""`, `name="variety_pick" value="Type 550"`, `name="variety_pick" value="new" checked`, `<legend>Which variety? Optional</legend>`,
		`<div class="f variety-new">`, `id="variety" name="variety" type="text" maxlength="60"`, `value="Type 405"`, `placeholder="for example Type 1050"`,
		"Varieties used before are listed first. Allergen warnings come from the article Wheat flour, not from the variety.",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("lacks %q:\n%s", want, body)
		}
	}
	if strings.Contains(body, `variety-more" open`) {
		t.Errorf("closed by default")
	}
	// A known variety is a chip, "No variety" is the default.
	body = s.en(s.path("/quantity?article=" + flour + "&variety=type+550&text=Wheat+flour&amount=1")).body
	if !strings.Contains(body, `name="variety_pick" value="Type 550" checked`) || !strings.Contains(body, `<span class="v-state chosen">Type 550</span>`) {
		t.Errorf("a known variety is chosen as its chip:\n%s", body)
	}
	body = s.en(s.path("/quantity?article=" + flour + "&text=Wheat+flour&amount=1")).body
	if !strings.Contains(body, `name="variety_pick" value="" checked`) || !strings.Contains(body, `<span class="v-state">none</span>`) {
		t.Errorf("no variety is the default:\n%s", body)
	}
	// Free text has no variety row.
	if b := s.en(s.path("/quantity?article=&text=Quinoa&amount=1")).body; strings.Contains(b, "variety-more") {
		t.Errorf("free text has no variety:\n%s", b)
	}
	if de := s.de(s.path("/quantity?article=" + flour + "&text=x&amount=1")).body; !strings.Contains(de, "<span>Sorte:</span><span class=\"v-state\">keine</span>") || !strings.Contains(de, "Ohne Sorte") {
		t.Errorf("German:\n%s", de)
	}
}

func TestAnEmptyNewVarietyAndATooLongOneAreErrorsAtTheFieldWithTheRowOpen(t *testing.T) {
	s := newShop(t)
	r := s.postEN(s.path("/entries"), url.Values{"article": {flour}, "text": {"Wheat flour"}, "mode": {"qty"}, "amount": {"1"}, "unit": {"kg"}, "variety_pick": {"new"}, "variety": {"  "}})
	for _, want := range []string{`id="err-variety"`, "Which variety? Type it in or choose “No variety”.", `<details class="more variety-more" open`, `<span class="v-state">new variety, still empty</span>`, `variety-new invalid`, `aria-invalid="true" aria-describedby="err-variety"`, `value="Wheat flour"`} {
		if r.status != http.StatusUnprocessableEntity || !strings.Contains(r.body, want) {
			t.Errorf("empty (%d) lacks %q:\n%s", r.status, want, r.body)
		}
	}
	long := strings.Repeat("ä", 61)
	r = s.postEN(s.path("/entries"), url.Values{"article": {flour}, "text": {"Wheat flour"}, "mode": {"quick"}, "variety_pick": {"new"}, "variety": {long}})
	for _, want := range []string{`id="err-variety"`, "The variety is too long: at most 60 characters.", `variety-more" open`, `value="` + long + `"`} {
		if r.status != http.StatusUnprocessableEntity || !strings.Contains(r.body, want) {
			t.Errorf("too long (%d) lacks %q", r.status, want)
		}
	}
	if len(s.vlines()) != 0 {
		t.Errorf("nothing was added: %v", s.vlines())
	}
}

func TestTheListShowsTheVarietyAfterTheNameInRowsAndLabels(t *testing.T) {
	s := newShop(t)
	id := s.seedVariety(flour, "Wheat flour", "Type 405", 1, "kg")
	s.seedVariety("apples", "Apples", "Elstar", 0, "")
	done := s.seedVariety("milk", "Milk", "Whole 3.5 %", 2, "l")
	s.db.SetEntryDone(s.h.ID, s.id, done, true)
	body := s.en(s.path("")).body
	for _, want := range []string{
		`<span class="title">Wheat flour<span class="variety"> · Type 405</span></span>`,
		`aria-label="Tick off: Wheat flour · Type 405, 1 kg"`,
		`aria-label="Change quantity or variety: Wheat flour · Type 405, 1 kg"`,
		`aria-label="Add a quantity or variety: Apples · Elstar"`,
		`<span class="title"><s>Milk<span class="variety"> · Whole 3.5 %</span></s></span>`,
		`aria-label="Mark as open: Milk · Whole 3.5 %, 2 l"`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("lacks %q:\n%s", want, body)
		}
	}
	_ = id
	// The sort-or-remove view names the variety too.
	edit := s.en(s.path("?edit=1")).body
	if !strings.Contains(edit, `<span class="title">Wheat flour<span class="variety"> · Type 405</span>`) || !strings.Contains(edit, `aria-label="Remove: Wheat flour · Type 405"`) {
		t.Errorf("edit view:\n%s", edit)
	}
}

func TestFreeTextKeepsTheShortEditorTitleAndHasNoVarietyRow(t *testing.T) {
	s := newShop(t)
	e, _ := s.db.AddItem(s.h.ID, s.id, store.Item{Text: "Wrapping paper"}, func(float64, unit.Kind) string { return "piece" })
	art := s.seedVariety(flour, "Wheat flour", "Type 405", 1, "kg")
	free := s.en(s.path("?qty=" + e.Entry.ID)).body
	if !strings.Contains(free, `>Change quantity</h3>`) || strings.Contains(free, "qvariety") || !strings.Contains(free, `aria-label="Add a quantity: Wrapping paper"`) {
		t.Errorf("free text:\n%s", free)
	}
	body := s.en(s.path("?qty=" + art)).body
	for _, want := range []string{`>Change quantity or variety</h3>`, `name="qvariety_pick" value="Type 405" checked`, `<span class="v-state chosen">Type 405</span>`, `id="qvariety" name="qvariety"`} {
		if !strings.Contains(body, want) {
			t.Errorf("article editor lacks %q:\n%s", want, body)
		}
	}
	i := strings.Index(body, `class="qe-top"`)
	if j := strings.Index(body[i:], "variety-more"); i < 0 || j < 0 || j > strings.Index(body[i:], `<fieldset class="qty"`) {
		t.Errorf("the Sorte row sits between the head and the quantity")
	}
}

func TestChangingTheVarietyInTheEditorMergesIntoTheEntryOfThatVarietyAndSaysBoth(t *testing.T) {
	s := newShop(t)
	a := s.seedVariety(flour, "Wheat flour", "", 500, "g")
	s.seedVariety(flour, "Wheat flour", "Type 405", 1, "kg")
	// The sentence before saving.
	sent := s.en(s.path("/quantity?entry=" + a + "&amount=500&unit=g&variety_pick=Type+405")).body
	if !strings.Contains(sent, "Wheat flour · Type 405") {
		t.Errorf("sentence names the merge:\n%s", sent)
	}
	r := s.postEN(s.path("/entries/"+a+"/quantity"), url.Values{"qmode": {"qty"}, "amount": {"500"}, "qunit": {"g"}, "qvariety_pick": {"Type 405"}})
	if r.status != http.StatusSeeOther {
		t.Fatalf("%d\n%s", r.status, r.body)
	}
	if got := s.vlines(); !same(got, []string{"Wheat flour · Type 405 1.5 kg"}) {
		t.Errorf("%v", got)
	}
	if body := s.en(r.location).body; !strings.Contains(body, "<b>Wheat flour · Type 405</b>") {
		t.Errorf("the line names the entry with its variety:\n%s", body)
	}
	// A new variety on the only entry just sets it.
	e := s.vlines()
	_ = e
	es, _ := s.db.Entries(s.h.ID, s.id)
	r = s.postEN(s.path("/entries/"+es[0].ID+"/quantity"), url.Values{"qmode": {"qty"}, "amount": {"1.5"}, "qunit": {"kg"}, "qvariety_pick": {"new"}, "qvariety": {"Type 1050"}})
	if r.status != http.StatusSeeOther || !same(s.vlines(), []string{"Wheat flour · Type 1050 1.5 kg"}) {
		t.Errorf("%d %v", r.status, s.vlines())
	}
	// Errors stay at the field with the row open.
	r = s.postEN(s.path("/entries/"+es[0].ID+"/quantity"), url.Values{"qmode": {"qty"}, "amount": {"1.5"}, "qunit": {"kg"}, "qvariety_pick": {"new"}, "qvariety": {""}})
	if r.status != http.StatusUnprocessableEntity || !strings.Contains(r.body, `id="err-qvariety"`) || !strings.Contains(r.body, "Which variety? Type it in or choose “No variety”.") || !strings.Contains(r.body, `variety-more" open`) {
		t.Errorf("%d\n%s", r.status, r.body)
	}
	// Taking the variety away.
	r = s.postEN(s.path("/entries/"+es[0].ID+"/quantity"), url.Values{"qmode": {"qty"}, "amount": {"1.5"}, "qunit": {"kg"}, "qvariety_pick": {""}})
	if r.status != http.StatusSeeOther || !same(s.vlines(), []string{"Wheat flour 1.5 kg"}) {
		t.Errorf("%d %v", r.status, s.vlines())
	}
}

func TestAllergenWarningsComeFromTheArticleAndStayWithAVariety(t *testing.T) {
	s := newShop(t)
	s.person("Mia", "child", "omnivore", "peanuts")
	_, page := s.add(s.vitem("peanut-butter", "creamy"))
	if n := strings.Count(page.body, "Contains Peanuts"); n < 2 || !strings.Contains(page.body, "Peanut butter · creamy") {
		t.Errorf("the line and the entry both warn (%d):\n%s", n, page.body)
	}
	if body := s.en(s.path("/quantity?article=peanut-butter&variety=creamy&text=Peanut+butter&amount=1")).body; !strings.Contains(body, "Allergen warnings come from the article Peanut butter, not from the variety.") {
		t.Errorf("the hint says it once:\n%s", body)
	}
}

func TestTypedPluralsAndUmlautsFindTheArticleAndAddItsName(t *testing.T) {
	s := newShop(t)
	for _, c := range []struct{ typed, art, name string }{{"Zitrone", "lemons", "Lemons"}, {"Apfel", "apples", "Apples"}, {"Tomate", "tomatoes", "Tomatoes"}, {"Kartoffel", "potatoes", "Potatoes"}, {"lemon", "lemons", "Lemons"}} {
		if body := s.en(s.path("/suggest?q=" + c.typed + "&mode=quick")).body; !strings.Contains(body, `data-article="`+c.art+`"`) {
			t.Errorf("%q suggests no %s:\n%s", c.typed, c.art, body)
		}
	}
	if r := s.do(http.MethodPost, s.path("/entries"), url.Values{"text": {"Zitrone"}, "mode": {"quick"}}, map[string]string{"Accept-Language": "de"}); r.status != http.StatusSeeOther {
		t.Fatalf("%d", r.status)
	}
	es, _ := s.db.Entries(s.h.ID, s.id)
	if len(es) != 1 || es[0].ArticleID != "lemons" {
		t.Fatalf("the typed singular is the article: %+v", es)
	}
	if es[0].Text != "Zitronen" {
		t.Errorf("the entry carries the library's name: %q", es[0].Text)
	}
}

func TestATextEntryThatNamesAnArticleInTheSingularHasItsTile(t *testing.T) {
	s := newShop(t)
	for _, text := range []string{"Zitrone", "Apfel", "Nuss"} {
		if _, err := s.db.AddEntry(s.h.ID, s.id, text); err != nil {
			t.Fatal(err)
		}
	}
	kid := childSession(t, s.box)
	page := kid.do(http.MethodGet, s.path(""), nil, map[string]string{"Accept-Language": "en"}).body
	for _, want := range []string{"🍋", "🍎"} {
		if !strings.Contains(page, want) {
			t.Errorf("the tile lacks %s:\n%s", want, page)
		}
	}
	if tile := tileOf(t, page, "Nuss"); !strings.Contains(tile, `class="pic letter"`) && !strings.Contains(tile, "🌰") {
		t.Errorf("a name no article has stays a letter: %s", tile)
	}
}

func TestTilesShowTheVarietyOnItsOwnLineBetweenNameAndQuantity(t *testing.T) {
	s := newShop(t)
	s.seedVariety(flour, "Wheat flour", "Dinkel-Vollkorn Type 1050 aus der Mühle Schäftlarn", 1, "kg")
	kid := childSession(t, s.box)
	page := kid.do(http.MethodGet, s.path(""), nil, map[string]string{"Accept-Language": "en"}).body
	re := regexp.MustCompile(`<span class="title">Wheat flour</span><span class="variety"><span class="vh">Variety: </span>Dinkel-Vollkorn Type 1050 aus der Mühle Schäftlarn</span><span class="qty">1 kg</span>`)
	if !re.MatchString(page) {
		t.Errorf("tile:\n%s", page)
	}
}

func TestTheListOnTheArticlePageFindsAnEntryOfThePluralOrSingularName(t *testing.T) {
	s := newShop(t)
	if _, err := s.db.AddEntry(s.h.ID, s.id, "Zitrone"); err != nil {
		t.Fatal(err)
	}
	if body := s.de("/settings/lists/library/lemons").body; !strings.Contains(body, "Einkaufen") {
		t.Errorf("the article page lists the list it stands on:\n%s", body)
	}
}

// firstArticles are the articles of the suggestion rows, in order.
func firstArticles(body string) []string {
	var out []string
	for _, m := range regexp.MustCompile(`data-article="([^"]+)"`).FindAllStringSubmatch(body, -1) {
		out = append(out, m[1])
	}
	return out
}

func TestTypingAShippedNameOrAnyStartOfItStillSuggestsThatArticle(t *testing.T) {
	s := newShop(t)
	all := library.Shipped()
	for _, a := range all {
		for lang, name := range map[string]string{"en": a.En, "de": a.De} {
			if !strings.ContainsAny(name, " ,·-:") {
				continue // only names with a separator can be split into article and variety
			}
			rs := []rune(name)
			for n := 2; n <= len(rs); n++ {
				typed := strings.TrimSpace(string(rs[:n]))
				path := s.path("/suggest?q=" + url.QueryEscape(typed) + "&mode=quick")
				body := s.en(path).body
				if lang == "de" {
					body = s.de(path).body
				}
				got := firstArticles(body)
				want := library.Complete(typed, all, 1)
				if len(want) == 0 {
					continue
				}
				if len(got) == 0 || got[0] != want[0].ID {
					t.Fatalf("%q suggests %v first, not %s", typed, got, want[0].ID)
				}
				if n == len(rs) && !slices.Contains(got, a.ID) {
					t.Fatalf("the whole name %q does not suggest %s: %v", typed, a.ID, got)
				}
			}
		}
	}
}

func TestTypingTheNameOfAnArticleThatStartsWithAnotherOneAddsNoMadeUpVariety(t *testing.T) {
	s := newShop(t)
	for _, typed := range []string{"Milk chocolate", "Apple juice", "Sesame seeds", "Berries (frozen)"} {
		body := s.en(s.path("/suggest?q=" + url.QueryEscape(typed) + "&mode=quick")).body
		if strings.Contains(body, `class="variety"`) && strings.Contains(body, "New variety of") {
			t.Errorf("%q: a variety was made up:\n%s", typed, body)
		}
	}
}

// A page names its lists by the article itself: the English Beer page is not on
// a list holding "Beeren", the German Eis page not on one holding "Ei".
func TestTheListOnTheArticlePageDoesNotMixThePluralEndingsOfTwoLanguages(t *testing.T) {
	s := newShop(t)
	for _, text := range []string{"Beeren", "Ei"} {
		if _, err := s.db.AddEntry(s.h.ID, s.id, text); err != nil {
			t.Fatal(err)
		}
	}
	for _, c := range []struct{ id, lang, name string }{{"beer", "en", "Beer"}, {"ice-cream", "de", "Eis"}} {
		r := s.en("/settings/lists/library/" + c.id)
		if c.lang == "de" {
			r = s.de("/settings/lists/library/" + c.id)
		}
		body := r.body
		if r.status != 200 || !strings.Contains(body, c.name) {
			t.Fatalf("%s page: status %d, name %q missing:\n%s", c.id, r.status, c.name, body)
		}
		if strings.Contains(body, "Einkaufen") || strings.Contains(body, "Shopping") {
			t.Errorf("%s page claims a list:\n%s", c.id, body)
		}
	}
}

// A name typed as text never finds an article hidden by hand (as when adding),
// so the page of the hidden article does not claim the list either.
func TestTheListOnTheArticlePageIgnoresAFreeTextEntryOfAnArticleHiddenByHand(t *testing.T) {
	s := newShop(t)
	if _, err := s.db.AddEntry(s.h.ID, s.id, "Äpfel"); err != nil {
		t.Fatal(err)
	}
	if body := s.de("/settings/lists/library/apples").body; !strings.Contains(body, "Einkaufen") {
		t.Fatalf("before hiding, the Apples page names the list:\n%s", body)
	}
	if err := s.db.SetArticleHidden(s.h.ID, "apples", true); err != nil {
		t.Fatal(err)
	}
	r := s.de("/settings/lists/library/apples")
	if r.status != 200 || strings.Contains(r.body, "Einkaufen") {
		t.Errorf("hidden Apples page: status %d, names the list:\n%s", r.status, r.body)
	}
}

// The typed variety keeps its row when completions and known varieties would fill them all.
func TestTheTypedVarietyKeepsItsRowWhenCompletionsFillTheOthers(t *testing.T) {
	s := newShop(t)
	for _, v := range []string{"Seeds", "Wholemeal", "Spelt", "Rye"} {
		s.seedVariety("bread-rolls", "Bread rolls", v, 0, "")
	}
	body := s.en(s.path("/suggest?q=Bread+r&mode=quick")).body
	if !strings.Contains(body, `data-article="bread"`) || !strings.Contains(body, `data-variety="r"`) {
		t.Errorf("the row for Bread with the typed variety is gone:\n%s", body)
	}
	if n := len(firstArticles(body)); n > 4 {
		t.Errorf("%d rows, at most 4", n)
	}
}
