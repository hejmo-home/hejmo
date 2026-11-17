package server_test

import (
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"testing"

	"github.com/hejmo-home/hejmo/internal/list"
	"github.com/hejmo-home/hejmo/internal/store"
	"github.com/hejmo-home/hejmo/internal/unit"
)

// Ticket 165 (ADR 0019, preview-165.html): adding to lists on the web.

// shop is a box with the example family and the shopping list, which has suggestions.
type shop struct {
	libBox
	id string
}

func newShop(t *testing.T) shop {
	t.Helper()
	l := newLibBox(t)
	ls, _ := l.db.Lists(l.h.ID)
	return shop{l, ls[0].ID}
}

func (s shop) path(rest string) string { return "/lists/" + s.id + rest }

// add posts the add form as the browser does and follows the redirect.
func (s shop) add(f url.Values) (resp, resp) {
	s.t.Helper()
	r := s.postEN(s.path("/entries"), f)
	if r.status != http.StatusSeeOther {
		return r, r
	}
	return r, s.en(r.location)
}

func (s shop) item(art string, qty ...string) url.Values {
	f := url.Values{"article": {art}, "mode": {"quick"}}
	if len(qty) == 2 {
		f.Set("mode", "qty")
		f.Set("amount", qty[0])
		f.Set("unit", qty[1])
	}
	return f
}

func (s shop) lines() []string {
	s.t.Helper()
	es, err := s.db.Entries(s.h.ID, s.id)
	if err != nil {
		s.t.Fatal(err)
	}
	var out []string
	for _, e := range es {
		l := e.Text
		if !e.Qty.None() {
			l += " " + unit.Format(e.Qty.Amount(), e.Qty.Unit, "en") + " " + e.Qty.Unit
		}
		out = append(out, l)
	}
	return out
}

func (s shop) seed(art, text string, amount float64, u string) string {
	s.t.Helper()
	it := store.Item{Text: text, ArticleID: art}
	if u != "" {
		it.Qty = list.NewQuantity(amount, u)
	}
	r, err := s.db.AddItem(s.h.ID, s.id, it, func(b float64, k unit.Kind) string { return u })
	if err != nil {
		s.t.Fatal(err)
	}
	return r.Entry.ID
}

func optionCount(body string) int { return strings.Count(body, `role="option"`) }

func same(a, b []string) bool { return sameStrings(a, b) }

func TestTheAddCardHasTwoModesInWordsAndOnePrimaryButton(t *testing.T) {
	s := newShop(t)
	page := s.en(s.path("")).body
	for _, want := range []string{`class="add-entry adder"`, `name="mode" value="quick" checked`, `name="mode" value="qty"`, "No quantity", "With quantity", `role="combobox"`} {
		if !strings.Contains(page, want) {
			t.Errorf("the add card lacks %q", want)
		}
	}
	if n := strings.Count(page, `class="button adder-go"`); n != 1 {
		t.Errorf("%d primary add buttons", n)
	}
	de := s.de(s.path("")).body
	for _, want := range []string{"Ohne Menge", "Mit Menge", "Hinzufügen"} {
		if !strings.Contains(de, want) {
			t.Errorf("German: %q missing", want)
		}
	}
}

func TestAListWithoutSuggestionsKeepsThePlainAddRow(t *testing.T) {
	s := newShop(t)
	ls, _ := s.db.Lists(s.h.ID)
	todo := ls[1]
	if todo.Suggestions {
		t.Fatal("the To-dos list is created without suggestions")
	}
	page := s.en("/lists/" + todo.ID).body
	if strings.Contains(page, `adder`) || strings.Contains(page, "qty-open") || strings.Contains(page, `value="qty"`) {
		t.Errorf("a to-do list has no quantities:\n%s", page)
	}
	if r := s.postEN("/lists/"+todo.ID+"/entries", url.Values{"text": {"Eier"}}); r.status != http.StatusSeeOther {
		t.Fatalf("add = %d", r.status)
	}
	es, _ := s.db.Entries(s.h.ID, todo.ID)
	if len(es) != 1 || es[0].ArticleID != "" {
		t.Errorf("plain text only: %+v", es)
	}
}

func TestSuggestionsComeFromTwoLettersWithSymbolNameDepartmentAndWhatIsOnTheList(t *testing.T) {
	s := newShop(t)
	s.person("Mia", "child", "omnivore", "peanuts")
	s.seed("eggs", "Eggs", 6, "piece")
	if body := s.en(s.path("/suggest?q=e&mode=quick")).body; optionCount(body) != 0 || strings.TrimSpace(body) != "" {
		t.Errorf("one letter suggests nothing: %q", body)
	}
	body := s.en(s.path("/suggest?q=Egg&mode=quick")).body
	for _, want := range []string{`role="listbox"`, "Eggs", "Already on the list: 6 pieces", `data-article="eggs"`, "A tap puts it on the list at once.", "Add “Egg” as text", "Hejmo does not know it then and does not warn about allergens."} {
		if !strings.Contains(body, want) {
			t.Errorf("suggestions lack %q:\n%s", want, body)
		}
	}
	if n := optionCount(s.en(s.path("/suggest?q=an&mode=quick")).body); n < 2 || n > 5 {
		t.Errorf("up to 4 articles and the free text row: %d", n)
	}
	last := s.en(s.path("/suggest?q=an&mode=quick")).body
	if i := strings.LastIndex(last, `role="option"`); !strings.Contains(last[i:], `class="free"`) && !strings.Contains(last[i-60:], `free`) {
		t.Errorf("the free text row comes last")
	}
}

func TestSuggestionsWarnPerAffectedPersonAndDeAndEnNamesMatch(t *testing.T) {
	s := newShop(t)
	s.person("Mia", "child", "omnivore", "peanuts")
	body := s.en(s.path("/suggest?q=peanut&mode=quick")).body
	if !strings.Contains(body, "Peanut butter") || !strings.Contains(body, "allergen-warn") || !strings.Contains(body, "Mia") || !strings.Contains(body, "is allergic") {
		t.Errorf("no warning for Mia:\n%s", body)
	}
	if de := s.de(s.path("/suggest?q=erdnussb&mode=quick")).body; !strings.Contains(de, "Erdnussbutter") {
		t.Errorf("German name:\n%s", de)
	}
	if u := s.en(s.path("/suggest?q=brotchen&mode=quick")).body; !strings.Contains(u, "Brötchen") && !strings.Contains(u, "Bread rolls") {
		t.Errorf("ö = o:\n%s", u)
	}
}

func TestTheTextOfTheTapChangesWithTheMode(t *testing.T) {
	s := newShop(t)
	if b := s.en(s.path("/suggest?q=Egg&mode=qty")).body; !strings.Contains(b, "A tap only chooses the article.") {
		t.Errorf("with quantity:\n%s", b)
	}
}

func TestFreeTextAllergiesAreSaidWhereAnArticleIsChosen(t *testing.T) {
	s := newShop(t)
	s.person("Mia", "child", "omnivore")
	mia, _ := s.db.People(s.h.ID)
	for _, p := range mia {
		if p.Name == "Mia" {
			s.db.SetPersonFood(s.h.ID, p.ID, store.Food{Diet: "omnivore", Other: "Kiwi"})
		}
	}
	if b := s.en(s.path("/suggest?q=Egg&mode=quick")).body; !strings.Contains(b, "Not checked automatically") || !strings.Contains(b, "Kiwi") {
		t.Errorf("suggestions:\n%s", b)
	}
	page := s.en(s.path("")).body
	if !strings.Contains(page, `class="unchecked-box"`) || !strings.Contains(page, "Kiwi") || !strings.Contains(page, "not on text") {
		t.Errorf("the list shows the note too:\n%s", page)
	}
}

func TestHiddenArticlesAreNamedNotOfferedAndTheExactNameStillAdds(t *testing.T) {
	s := newShop(t)
	s.person("Mia", "child", "omnivore", "peanuts")
	if err := s.db.SetLibrarySettings(s.h.ID, store.LibrarySettings{HideAllergen: true, Everyone: true, People: []string{}, ConfirmedBy: s.mama.ID}); err != nil {
		t.Fatal(err)
	}
	body := s.en(s.path("/suggest?q=Peanut&mode=quick")).body
	if !strings.Contains(body, "sugg-hidden") || !strings.Contains(body, "Experimental") || !strings.Contains(body, "Peanut butter") {
		t.Errorf("the hidden articles are named:\n%s", body)
	}
	if strings.Contains(body, `data-article="peanut-butter"`) {
		t.Errorf("a hidden article is not offered")
	}
	if !strings.Contains(body, "as text") {
		t.Errorf("the free text row stays")
	}
	_, page := s.add(url.Values{"text": {"Peanut butter"}, "mode": {"quick"}})
	if !strings.Contains(page.body, "Contains Peanuts") || !strings.Contains(page.body, "Mia") {
		t.Errorf("typing the exact name adds the article, with its warning:\n%s", page.body)
	}
}

func TestAddingWithoutQuantityPutsTheArticleOnTheListAndASecondTimeSaysSo(t *testing.T) {
	s := newShop(t)
	r, page := s.add(s.item("bananas"))
	if r.status != http.StatusSeeOther {
		t.Fatalf("add = %d\n%s", r.status, r.body)
	}
	if !strings.Contains(page.body, "Bananas") || !strings.Contains(page.body, "added") || !strings.Contains(page.body, `class="qty-open none"`) {
		t.Errorf("page:\n%s", page.body)
	}
	_, page = s.add(s.item("bananas"))
	if !strings.Contains(page.body, "is already on the list. No second entry.") {
		t.Errorf("no second entry:\n%s", page.body)
	}
	if got := s.lines(); !same(got, []string{"Bananas"}) {
		t.Errorf("%v", got)
	}
}

func TestAddingWithAQuantityAddsUpAndTheLineSaysWhatWasAddedToWhat(t *testing.T) {
	s := newShop(t)
	s.seed("eggs", "Eggs", 6, "piece")
	before := s.en(s.path("")).body
	if !strings.Contains(before, "6 pieces") {
		t.Errorf("an entry shows its quantity in words:\n%s", before)
	}
	_, page := s.add(s.item("eggs", "1", "dozen"))
	if !strings.Contains(page.body, "<b>1 dozen</b> Eggs added to <b>6 pieces</b>: now <b>18 pieces</b>.") {
		t.Errorf("the line:\n%s", page.body)
	}
	if got := s.lines(); !same(got, []string{"Eggs 18 piece"}) {
		t.Errorf("%v", got)
	}
	if !strings.Contains(page.body, `class="added"`) {
		t.Errorf("the entry keeps a frame until the next action")
	}
	// A unit of the same kind: 500 g and 1 kg.
	s.add(s.item("sugar", "500", "g"))
	_, page = s.add(s.item("sugar", "1", "kg"))
	if !strings.Contains(page.body, "now <b>1.5 kg</b>") {
		t.Errorf("500 g + 1 kg:\n%s", page.body)
	}
	if got := s.de(s.path("")).body; !strings.Contains(got, "1,5 kg") {
		t.Errorf("a decimal comma in German:\n%s", got)
	}
}

func TestAnotherKindStandsNextToTheOldEntryAndTheLineSaysWhy(t *testing.T) {
	s := newShop(t)
	s.add(s.item("rolled-oats", "2", "pack"))
	_, page := s.add(s.item("rolled-oats", "500", "g"))
	if !strings.Contains(page.body, "<b>500 g</b> Rolled oats added. They stand next to <b>2 packs</b>, because Hejmo cannot convert packs to grams.") {
		t.Errorf("the line:\n%s", page.body)
	}
	if got := s.lines(); !same(got, []string{"Rolled oats 2 pack", "Rolled oats 500 g"}) {
		t.Errorf("%v", got)
	}
	_, page = s.add(s.item("rolled-oats", "250", "g"))
	if !strings.Contains(page.body, "now <b>750 g</b>") || len(s.lines()) != 2 {
		t.Errorf("250 g join the 500 g: %v\n%s", s.lines(), page.body)
	}
}

func TestFreeTextIsAddedWithTheNoteThatHejmoCannotWarnAndTheOfferToAddItToTheLibrary(t *testing.T) {
	s := newShop(t)
	_, page := s.add(url.Values{"text": {"Quinoa-Pops"}, "mode": {"quick"}})
	for _, want := range []string{"“Quinoa-Pops” added.", "Hejmo does not know this yet and so cannot warn about allergens.", `class="msg-act"`, "Add to the library", "/settings/lists/library/new?"} {
		if !strings.Contains(page.body, want) {
			t.Errorf("the line lacks %q:\n%s", want, page.body)
		}
	}
	if !strings.Contains(page.body, "Quinoa-Pops") || !strings.Contains(page.body, "entry=") {
		t.Errorf("the link carries the name and the entry")
	}
}

func TestAnArticleWithAnAllergenCarriesItsWarningInTheLineAndOnTheEntry(t *testing.T) {
	s := newShop(t)
	s.person("Mia", "child", "omnivore", "peanuts")
	_, page := s.add(s.item("peanut-butter"))
	if n := strings.Count(page.body, "Contains Peanuts"); n < 2 {
		t.Errorf("the line and the entry both warn (%d):\n%s", n, page.body)
	}
}

func TestAMissingOrBadAmountIsAnErrorAtTheNumberAndTheTextStays(t *testing.T) {
	s := newShop(t)
	for _, amount := range []string{"", "0", "abc", "10000", "-2"} {
		r := s.postEN(s.path("/entries"), url.Values{"text": {"Eggs"}, "article": {"eggs"}, "mode": {"qty"}, "amount": {amount}, "unit": {"piece"}})
		if r.status != http.StatusUnprocessableEntity || !strings.Contains(r.body, `id="err-amount"`) || !strings.Contains(r.body, "How much? Enter a number above 0, for example 2 or 0.5. Or choose “No quantity” above.") {
			t.Errorf("amount %q = %d\n%s", amount, r.status, r.body)
			continue
		}
		if !strings.Contains(r.body, `value="Eggs"`) || !strings.Contains(r.body, `data-mode="qty"`) || !strings.Contains(r.body, `value="`+amount+`"`) {
			t.Errorf("amount %q: the typed text and the mode stay", amount)
		}
	}
	if n := len(s.lines()); n != 0 {
		t.Errorf("%d entries", n)
	}
	if r := s.postEN(s.path("/entries"), url.Values{"text": {""}, "mode": {"quick"}}); r.status != http.StatusUnprocessableEntity || !strings.Contains(r.body, `id="err-text"`) {
		t.Errorf("empty text = %d", r.status)
	}
}

func TestACommaOrAPointTakesHalves(t *testing.T) {
	s := newShop(t)
	s.add(s.item("sugar", "0,5", "kg"))
	s.add(s.item("rolled-oats", "0.5", "pack"))
	if got := s.lines(); !same(got, []string{"Sugar 500 g", "Rolled oats 0.5 pack"}) {
		t.Errorf("%v", got)
	}
}

func TestAnEntryShowsItsQuantityAsAButtonAndNothingIsTickedOffByIt(t *testing.T) {
	s := newShop(t)
	id := s.seed("eggs", "Eggs", 6, "piece")
	body := s.en(s.path("")).body
	re := regexp.MustCompile(`<a class="qty-open" href="\?qty=` + id + `#e-` + id + `"[^>]*aria-label="Change quantity: Eggs, 6 pieces">6 pieces`)
	if !re.MatchString(body) {
		t.Errorf("the quantity button:\n%s", body)
	}
	if !strings.Contains(body, `id="e-`+id+`"`) {
		t.Errorf("the entry has an anchor")
	}
	// A ticked-off entry only shows its quantity.
	s.postEN(s.path("/entries/"+id+"/done"), url.Values{"done": {"1"}})
	if b := s.en(s.path("")).body; strings.Contains(b, `class="qty-open"`) || !strings.Contains(b, `class="qty"`) {
		t.Errorf("ticked off: only the pill\n%s", b)
	}
}

func TestOpeningAQuantityShowsTheEditorDirectlyUnderTheEntry(t *testing.T) {
	s := newShop(t)
	id := s.seed("eggs", "Eggs", 6, "piece")
	other := s.seed("bananas", "Bananas", 0, "")
	body := s.en(s.path("?qty=" + id)).body
	for _, want := range []string{`class="qty-edit"`, `name="qmode" value="qty" checked`, `name="qmode" value="none"`, `name="amount"`, `value="6"`, "Now <b>6 pieces</b>, after that <b>6 pieces</b>.", ">Save<", ">Cancel<", `aria-expanded="true"`, `class="editing"`} {
		if !strings.Contains(body, want) {
			t.Errorf("the editor lacks %q:\n%s", want, body)
		}
	}
	if strings.Count(body, `class="qty-edit"`) != 1 || strings.Count(body, `class="button adder-go"`) != 0 || !strings.Contains(body, `class="button secondary adder-go"`) {
		t.Errorf("only one editor, and the add button is secondary while it is open")
	}
	// The editor of an entry without quantity opens with "With quantity", 1 and the first default unit.
	body = s.en(s.path("?qty=" + other)).body
	if !strings.Contains(body, `name="qmode" value="qty" checked`) && !strings.Contains(body, `value="qty" checked`) {
		t.Errorf("opens with quantity:\n%s", body)
	}
	if !strings.Contains(body, `value="1"`) || !strings.Contains(body, `name="qunit" value="piece" checked`) {
		t.Errorf("1 and the first default unit:\n%s", body)
	}
}

func TestChangingAQuantityReplacesItAndTheLineNamesOldAndNew(t *testing.T) {
	s := newShop(t)
	id := s.seed("eggs", "Eggs", 6, "piece")
	r := s.postEN(s.path("/entries/"+id+"/quantity"), url.Values{"qmode": {"qty"}, "amount": {"10"}, "qunit": {"piece"}})
	if r.status != http.StatusSeeOther {
		t.Fatalf("save = %d\n%s", r.status, r.body)
	}
	page := s.en(r.location).body
	if !strings.Contains(page, "<b>Eggs</b>: now <b>10 pieces</b>, before 6 pieces.") || !strings.Contains(page, `class="added"`) {
		t.Errorf("the line:\n%s", page)
	}
	if got := s.lines(); !same(got, []string{"Eggs 10 piece"}) {
		t.Errorf("%v", got)
	}
}

func TestChangingToNoQuantityAndBack(t *testing.T) {
	s := newShop(t)
	id := s.seed("sugar", "Sugar", 500, "g")
	r := s.postEN(s.path("/entries/"+id+"/quantity"), url.Values{"qmode": {"none"}})
	if r.status != http.StatusSeeOther {
		t.Fatalf("%d", r.status)
	}
	if got := s.lines(); !same(got, []string{"Sugar"}) {
		t.Errorf("%v", got)
	}
	if page := s.en(r.location).body; !strings.Contains(page, "now without quantity") {
		t.Errorf("the line:\n%s", page)
	}
}

func TestChangingToAnotherKindMergesIntoTheEntryOfThatKindAndSaysWhichWentAway(t *testing.T) {
	s := newShop(t)
	a := s.seed("rolled-oats", "Rolled oats", 2, "pack")
	s.seed("rolled-oats", "Rolled oats", 500, "g")
	r := s.postEN(s.path("/entries/"+a+"/quantity"), url.Values{"qmode": {"qty"}, "amount": {"250"}, "qunit": {"g"}})
	page := s.en(r.location).body
	if !strings.Contains(page, "250 g added to 500 g, now <b>750 g</b>. The entry “2 packs” is gone.") {
		t.Errorf("the line:\n%s", page)
	}
	if got := s.lines(); !same(got, []string{"Rolled oats 750 g"}) {
		t.Errorf("%v", got)
	}
}

func TestAnErrorWhileChangingStaysAtTheNumberAndSavesNothing(t *testing.T) {
	s := newShop(t)
	id := s.seed("eggs", "Eggs", 6, "piece")
	r := s.postEN(s.path("/entries/"+id+"/quantity"), url.Values{"qmode": {"qty"}, "amount": {"0"}, "qunit": {"piece"}})
	if r.status != http.StatusUnprocessableEntity || !strings.Contains(r.body, `id="err-qa-`+id+`"`) || !strings.Contains(r.body, `value="0"`) || !strings.Contains(r.body, `class="editing"`) {
		t.Errorf("%d\n%s", r.status, r.body)
	}
	if got := s.lines(); !same(got, []string{"Eggs 6 piece"}) {
		t.Errorf("nothing is saved: %v", got)
	}
}

func TestChangingAnEntryThatWasTickedOffMeanwhileSaysSo(t *testing.T) {
	s := newShop(t)
	id := s.seed("eggs", "Eggs", 6, "piece")
	s.db.SetEntryDone(s.h.ID, s.id, id, true)
	r := s.postEN(s.path("/entries/"+id+"/quantity"), url.Values{"qmode": {"qty"}, "amount": {"7"}, "qunit": {"piece"}})
	if r.status != http.StatusConflict || !strings.Contains(r.body, "ticked off or removed") {
		t.Errorf("%d\n%s", r.status, r.body)
	}
	if got := s.lines(); !same(got, []string{"Eggs 6 piece"}) {
		t.Errorf("%v", got)
	}
}

func TestOnlyParentsChangeAQuantityOnTheWeb(t *testing.T) {
	s := newShop(t)
	id := s.seed("eggs", "Eggs", 6, "piece")
	s.logout()
	if r := s.postEN(s.path("/entries/"+id+"/quantity"), url.Values{"qmode": {"qty"}, "amount": {"7"}, "qunit": {"piece"}}); r.status == http.StatusSeeOther && !strings.HasPrefix(r.location, "/login") {
		t.Errorf("signed out: %d %q", r.status, r.location)
	}
}

func TestTheQuantityPartOffersTheDefaultUnitsFirstAndTheOthersUnderMoreUnits(t *testing.T) {
	s := newShop(t)
	body := s.en(s.path("/quantity?article=eggs&amount=1&unit=dozen")).body
	for _, want := range []string{`class="picked"`, "From the library", `<fieldset class="qty">`, `name="unit" value="dozen" checked`, "<small>12 pieces</small>", "More units", `class="more more-units"`, `href="/settings/lists#units"`, `class="stepper"`} {
		if !strings.Contains(body, want) {
			t.Errorf("lacks %q:\n%s", want, body)
		}
	}
	first := strings.Index(body, `name="unit" value="dozen"`)
	second := strings.Index(body, `name="unit" value="piece"`)
	more := strings.Index(body, "More units")
	if !(first >= 0 && first < second && second < more) {
		t.Errorf("Dozen, piece, then the others: %d %d %d", first, second, more)
	}
	if strings.Contains(body, `value="oz"`) || strings.Contains(body, "(UK)") || strings.Contains(body, "(US)") {
		t.Errorf("no imperial units in Germany")
	}
	// Free text: pieces and packs.
	free := s.en(s.path("/quantity?text=Quinoa&amount=1")).body
	if !strings.Contains(free, `name="unit" value="piece" checked`) || strings.Index(free, `value="pack"`) > strings.Index(free, "More units") {
		t.Errorf("free text offers piece and pack first:\n%s", free)
	}
}

func TestTheSentenceUnderTheUnitSaysTheResultInAdvance(t *testing.T) {
	s := newShop(t)
	s.seed("eggs", "Eggs", 6, "piece")
	if b := s.en(s.path("/quantity?article=eggs&amount=1&unit=dozen")).body; !strings.Contains(b, "Already on the list: <b>6 pieces</b>. With <b>1 dozen</b> that makes <b>18 pieces</b>.") {
		t.Errorf("same kind:\n%s", b)
	}
	s.seed("rolled-oats", "Rolled oats", 2, "pack")
	b := s.en(s.path("/quantity?article=rolled-oats&amount=500&unit=g")).body
	if !strings.Contains(b, "Already on the list: <b>2 packs</b>. How many grams are in a pack, Hejmo does not know. So Rolled oats will be there twice: <b>2 packs</b> and <b>500 g</b>. Choose “pack” and Hejmo adds them up.") {
		t.Errorf("other kind:\n%s", b)
	}
	if b := s.en(s.path("/quantity?article=bananas&amount=3&unit=piece")).body; strings.Contains(b, "Already on the list") {
		t.Errorf("nothing on the list yet:\n%s", b)
	}
}

func TestAFreshQuantityStartsWithTheUnitLastUsed(t *testing.T) {
	s := newShop(t)
	s.add(s.item("eggs", "6", "piece"))
	body := s.en(s.path("/quantity?article=eggs")).body
	if !strings.Contains(body, `name="unit" value="piece" checked`) {
		t.Errorf("the unit last used is preselected:\n%s", body)
	}
	if b := s.en(s.path("/quantity?article=milk")).body; !strings.Contains(b, `name="unit" value="l" checked`) {
		t.Errorf("otherwise the first default unit:\n%s", b)
	}
}

func TestNoQuantityFieldsSayQuantityInGermanToo(t *testing.T) {
	s := newShop(t)
	de := s.de(s.path("/quantity?article=eggs")).body
	for _, want := range []string{"Aus der Bibliothek", "Weitere Einheiten", "Dutzend", "12 Stück", "Weniger", "Mehr"} {
		if !strings.Contains(de, want) {
			t.Errorf("German lacks %q", want)
		}
	}
}

func TestTheSuggestionSwitchIsInTheListSettings(t *testing.T) {
	s := newShop(t)
	page := s.en(s.path("")).body
	if !strings.Contains(page, `name="suggestions"`) || !strings.Contains(page, "Suggestions from the article library") {
		t.Errorf("the switch:\n%s", page)
	}
	r := s.postEN(s.path("/settings"), url.Values{"name": {"Einkauf"}, "color": {"green"}, "symbol": {"square-filled"}, "sort": {"manual"}})
	if r.status != http.StatusSeeOther {
		t.Fatalf("%d\n%s", r.status, r.body)
	}
	l, _ := s.db.List(s.h.ID, s.id)
	if l.Suggestions {
		t.Errorf("an unticked switch turns suggestions off")
	}
	r = s.postEN(s.path("/settings"), url.Values{"name": {"Einkauf"}, "color": {"green"}, "symbol": {"square-filled"}, "sort": {"manual"}, "suggestions": {"on"}})
	if l, _ = s.db.List(s.h.ID, s.id); !l.Suggestions {
		t.Errorf("on again")
	}
}
