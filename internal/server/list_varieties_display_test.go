package server_test

import (
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"testing"

	"github.com/hejmo-home/hejmo/internal/store"
)

// Ticket 277 on a display: the variety is shown and can be typed and chosen there.

func TestTheDisplayShowsTheVarietyAndTakesItInTheBackground(t *testing.T) {
	f, id, ids := shopOnDisplay(t)
	r, err := f.db.AddItem(f.householdID, id, store.Item{Text: "Wheat flour", ArticleID: "wheat-flour", Variety: "Type 405"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	body := f.display.get("/display/lists/"+id, "en").body
	for _, want := range []string{`<span class="title">Wheat flour<span class="variety"> · Type 405</span></span>`, `aria-label="Tick off: Wheat flour · Type 405"`, `aria-label="Add a quantity or variety: Wheat flour · Type 405"`} {
		if !strings.Contains(body, want) {
			t.Errorf("the display lacks %q:\n%s", want, body)
		}
	}
	add := f.display.postLang("/display/lists/"+id+"/entries", url.Values{"article": {"wheat-flour"}, "mode": {"quick"}, "variety_pick": {"new"}, "variety": {"Type 550"}, "partial": {"1"}}, "en")
	if add.status != http.StatusOK || !strings.Contains(add.body, "“Wheat flour · Type 550” added. It stands next to “Wheat flour · Type 405”") {
		t.Errorf("partial add = %d\n%s", add.status, add.body)
	}
	bad := f.display.postLang("/display/lists/"+id+"/entries", url.Values{"article": {"wheat-flour"}, "mode": {"qty"}, "amount": {"1"}, "unit": {"kg"}, "variety_pick": {"new"}, "variety": {""}, "partial": {"1"}}, "en")
	if bad.status != http.StatusUnprocessableEntity || !strings.Contains(bad.body, `id="err-variety"`) || !strings.Contains(bad.body, `variety-more" open`) {
		t.Errorf("empty variety = %d\n%s", bad.status, bad.body)
	}
	ed := f.display.get("/display/lists/"+id+"/view?qty="+r.Entry.ID, "en").body
	if !strings.Contains(ed, `name="qvariety_pick" value="Type 405" checked`) || strings.Contains(ed, "/settings/lists#units") {
		t.Errorf("the editor has the Sorte row:\n%s", ed)
	}
	save := f.display.postLang("/display/lists/"+id+"/entries/"+ids["Eggs"]+"/quantity", url.Values{"qmode": {"qty"}, "amount": {"6"}, "qunit": {"piece"}, "qvariety_pick": {"new"}, "qvariety": {"Freiland"}, "partial": {"1"}}, "en")
	if save.status != http.StatusOK {
		t.Fatalf("%d\n%s", save.status, save.body)
	}
	if es, _ := f.db.Entries(f.householdID, id); es[0].Variety != "Freiland" {
		t.Errorf("%+v", es[0])
	}
}

// The list tile of the family view names the variety: "Mehl" and "Mehl · Type 405" are two lines.
func TestTheListBlockInTheFooterNamesTheVariety(t *testing.T) {
	f := newFamilyBox(t, "eink_color")
	id, _ := f.onlyList(t, "Shopping", nil)
	for _, v := range []string{"", "Type 405"} {
		if _, err := f.db.AddItem(f.householdID, id, store.Item{Text: "Wheat flour", ArticleID: "wheat-flour", Variety: v}, nil); err != nil {
			t.Fatal(err)
		}
	}
	form := f.layoutForm(true, []string{"Mama", "Papa", "Sebastian", "Mia"}, append(defaultOn, "list")...)
	form.Set("list", id)
	f.saveLayout(t, form)
	page := f.view(t, "en", f.at(14, 9, 0))
	tile := regexp.MustCompile(`(?s)<a class="tile list".*?</a>`).FindString(page)
	if !strings.Contains(inText(tile), "Wheat flour · Type 405") || strings.Count(inText(tile), "Wheat flour") != 2 {
		t.Errorf("tile:\n%s", tile)
	}
}
