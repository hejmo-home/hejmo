package server_test

import (
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/hejmo-home/hejmo/internal/list"
	"github.com/hejmo-home/hejmo/internal/store"
	"github.com/hejmo-home/hejmo/internal/unit"
)

// Ticket 165 (preview-165 frames 28 to 34): adding and changing quantities on a display.

// shopOnDisplay is a display on a list with suggestions and some entries.
func shopOnDisplay(t *testing.T) (*familyBox, string, map[string]string) {
	t.Helper()
	f := newFamilyBox(t, "eink_color")
	for _, l := range f.homeLists(t) {
		f.db.DeleteList(f.householdID, l.ID)
	}
	l, err := f.db.CreateList(f.householdID, list.List{Name: "Shopping", Color: "green", Symbol: "square-filled", Sort: list.Manual, Suggestions: true})
	if err != nil {
		t.Fatal(err)
	}
	ids := map[string]string{}
	show := func(b float64, k unit.Kind) string {
		return unit.ShowUnit(b, k, []string{"dozen", "piece"}, unit.DefaultActive)
	}
	for _, e := range []struct {
		text, art string
		amount    float64
		u         string
	}{{"Eggs", "eggs", 6, "piece"}, {"Bananas", "bananas", 0, ""}, {"Rolled oats", "rolled-oats", 2, "pack"}} {
		it := store.Item{Text: e.text, ArticleID: e.art}
		if e.u != "" {
			it.Qty = list.NewQuantity(e.amount, e.u)
		}
		r, err := f.db.AddItem(f.householdID, l.ID, it, show)
		if err != nil {
			t.Fatal(err)
		}
		ids[e.text] = r.Entry.ID
	}
	f.showRows(t) // the editor belongs to the rows, tiles show the quantity only
	f.now = f.at(14, 9, 0)
	return f, l.ID, ids
}

func TestTheDisplayShowsTheAddCardAndAQuantityButtonOnEveryOpenEntry(t *testing.T) {
	f, id, ids := shopOnDisplay(t)
	body := f.display.get("/display/lists/"+id, "en").body
	for _, want := range []string{`class="add-entry adder"`, `data-adder="/display/lists/` + id + `"`, `name="mode" value="qty"`, `<button class="qty-open" type="button" data-qty-open="` + ids["Eggs"] + `" aria-expanded="false"`, "6 pieces", `class="qty-open none"`, "Change quantity: Eggs, 6 pieces", `<script src="/static/adder.js"`} {
		if !strings.Contains(body, want) {
			t.Errorf("the display lacks %q", want)
		}
	}
	if strings.Contains(body, "/settings/lists#units") {
		t.Errorf("a display links to no settings")
	}
}

func TestTheDisplayOffersSuggestionsAndTheFragmentsLikeTheWeb(t *testing.T) {
	f, id, _ := shopOnDisplay(t)
	b := f.display.get("/display/lists/"+id+"/suggest?q=Egg&mode=quick", "en")
	if b.status != http.StatusOK || !strings.Contains(b.body, "Eggs") || !strings.Contains(b.body, "Already on the list: 6 pieces") || !strings.Contains(b.body, `data-article="eggs"`) {
		t.Errorf("suggest = %d\n%s", b.status, b.body)
	}
	q := f.display.get("/display/lists/"+id+"/quantity?article=eggs&amount=1&unit=dozen", "en").body
	if !strings.Contains(q, `class="picked"`) || !strings.Contains(q, "With <b>1 dozen</b> that makes <b>18 pieces</b>.") || !strings.Contains(q, "Parents switch on more units in the settings.") {
		t.Errorf("quantity:\n%s", q)
	}
}

func TestAnyoneAtADisplayAddsInTheBackgroundAndGetsTheLineBackInTheView(t *testing.T) {
	f, id, _ := shopOnDisplay(t)
	r := f.display.postLang("/display/lists/"+id+"/entries", url.Values{"article": {"eggs"}, "mode": {"qty"}, "amount": {"1"}, "unit": {"dozen"}, "partial": {"1"}}, "en")
	if r.status != http.StatusOK || strings.Contains(r.body, "<html") || !strings.Contains(r.body, "<b>1 dozen</b> Eggs added to <b>6 pieces</b>: now <b>18 pieces</b>.") || !strings.Contains(r.body, `class="added"`) {
		t.Errorf("partial add = %d\n%s", r.status, r.body)
	}
	es, _ := f.db.Entries(f.householdID, id)
	if len(es) != 3 || es[0].Qty.Base != 18 {
		t.Errorf("%+v", es)
	}
	free := f.display.postLang("/display/lists/"+id+"/entries", url.Values{"text": {"Wasserbomben"}, "mode": {"quick"}, "partial": {"1"}}, "en")
	if !strings.Contains(free.body, "Hejmo does not know this and does not warn about allergens here.") || strings.Contains(free.body, "Add to the library") {
		t.Errorf("free text on the display says it, and offers no library:\n%s", free.body)
	}
}

func TestAMissingAmountOnTheDisplayIsAnErrorAtTheNumber(t *testing.T) {
	f, id, _ := shopOnDisplay(t)
	r := f.display.postLang("/display/lists/"+id+"/entries", url.Values{"article": {"eggs"}, "mode": {"qty"}, "amount": {""}, "unit": {"dozen"}, "partial": {"1"}}, "en")
	if r.status != http.StatusUnprocessableEntity || !strings.Contains(r.body, "How much? Tap &#43; or enter a number above 0.") || !strings.Contains(r.body, `id="err-amount"`) {
		t.Errorf("%d\n%s", r.status, r.body)
	}
	es, _ := f.db.Entries(f.householdID, id)
	if len(es) != 3 {
		t.Errorf("nothing was added: %d", len(es))
	}
}

func TestTheEditorOpensUnderTheEntryOnTheDisplayAndSavesInTheBackground(t *testing.T) {
	f, id, ids := shopOnDisplay(t)
	body := f.display.get("/display/lists/"+id+"/view?qty="+ids["Eggs"], "en")
	for _, want := range []string{`class="editing"`, `class="qty-edit"`, `aria-expanded="true"`, "Now <b>6 pieces</b>, after that <b>6 pieces</b>.", `data-qty-close`, `class="button secondary adder-go"`} {
		if !strings.Contains(body.body, want) {
			t.Errorf("the editor lacks %q:\n%s", want, body.body)
		}
	}
	if strings.Contains(body.body, "/settings/lists#units") {
		t.Errorf("no settings link on a display")
	}
	r := f.display.postLang("/display/lists/"+id+"/entries/"+ids["Eggs"]+"/quantity", url.Values{"qmode": {"qty"}, "amount": {"10"}, "qunit": {"piece"}, "partial": {"1"}}, "en")
	if r.status != http.StatusOK || strings.Contains(r.body, "<html") || !strings.Contains(r.body, "<b>Eggs</b>: now <b>10 pieces</b>, before 6 pieces.") || strings.Contains(r.body, `class="qty-edit"`) {
		t.Errorf("save = %d\n%s", r.status, r.body)
	}
	es, _ := f.db.Entries(f.householdID, id)
	if es[0].Qty.Base != 10 {
		t.Errorf("%+v", es[0])
	}
	bad := f.display.postLang("/display/lists/"+id+"/entries/"+ids["Eggs"]+"/quantity", url.Values{"qmode": {"qty"}, "amount": {""}, "qunit": {"piece"}, "partial": {"1"}}, "en")
	if bad.status != http.StatusUnprocessableEntity || !strings.Contains(bad.body, "How much? Tap &#43; or enter a number above 0.") || !strings.Contains(bad.body, `class="qty-edit"`) {
		t.Errorf("an empty number = %d\n%s", bad.status, bad.body)
	}
	if es, _ := f.db.Entries(f.householdID, id); es[0].Qty.Base != 10 {
		t.Errorf("nothing saved")
	}
}

func TestChangingAnEntryTickedOffMeanwhileOnTheDisplaySaysSoAndChangesNothing(t *testing.T) {
	f, id, ids := shopOnDisplay(t)
	f.db.SetEntryDone(f.householdID, id, ids["Eggs"], true)
	r := f.display.postLang("/display/lists/"+id+"/entries/"+ids["Eggs"]+"/quantity", url.Values{"qmode": {"qty"}, "amount": {"7"}, "qunit": {"piece"}, "partial": {"1"}}, "en")
	if r.status != http.StatusConflict || !strings.Contains(r.body, "ticked off or removed") {
		t.Errorf("%d\n%s", r.status, r.body)
	}
}

func TestADisplayListWithoutSuggestionsIsAsBefore(t *testing.T) {
	f := newFamilyBox(t, "eink_color")
	id, _ := f.onlyList(t, "To-dos", nil, "Call grandma")
	body := f.listsPage(t, "en", "/display/lists/"+id).body
	if strings.Contains(body, `class="add-entry adder"`) || strings.Contains(body, "qty-open") {
		t.Errorf("a to-do list on a display has the plain add row:\n%s", body)
	}
}
