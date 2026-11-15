package server_test

import (
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"testing"

	"github.com/hejmo-home/hejmo/internal/list"
)

// Design pass on 6.1 (docs/design/preview-32.html): the page structure, the
// wording and the behaviour the designer asked for.

// listWith creates a manual list in the home household with the given entries;
// the texts in ticked are ticked off. It returns the list id and the entry ids by text.
func listWith(t *testing.T, b *box, name string, ticked []string, texts ...string) (string, map[string]string) {
	t.Helper()
	h, _ := b.db.HomeHousehold()
	l, err := b.db.CreateList(h.ID, list.List{Name: name, Color: "green", Symbol: "square-filled", Sort: list.Manual})
	if err != nil {
		t.Fatal(err)
	}
	ids := map[string]string{}
	for _, s := range texts {
		e, err := b.db.AddEntry(h.ID, l.ID, s)
		if err != nil {
			t.Fatal(err)
		}
		ids[s] = e.ID
		for _, x := range ticked {
			if x == s {
				if err := b.db.SetEntryDone(h.ID, l.ID, e.ID, true); err != nil {
					t.Fatal(err)
				}
			}
		}
	}
	return l.ID, ids
}

func before(t *testing.T, body string, parts ...string) {
	t.Helper()
	at := 0
	for _, p := range parts {
		i := strings.Index(body[at:], p)
		if i < 0 {
			t.Fatalf("%q missing after offset %d in\n%s", p, at, body)
		}
		at += i + len(p)
	}
}

func TestListsOverviewShowsOneRowPerListWithItsStateInWords(t *testing.T) {
	b := signedInBox(t)
	open, _ := listWith(t, b, "Mixed", []string{"b"}, "a", "b")
	_, _ = listWith(t, b, "Only open", nil, "x", "y", "z")
	_, _ = listWith(t, b, "Finished", []string{"p", "q"}, "p", "q")
	_, _ = listWith(t, b, "Blank", nil)
	body := getEN(b, "/lists").body
	for _, want := range []string{`<ul class="list-rows">`, `<a href="/lists/` + open + `">`, `<span class="state">1 open, 1 ticked off</span>`,
		`<span class="state">3 open</span>`, `<span class="state">All 2 ticked off</span>`, `<span class="state">No entries yet</span>`,
		`<span class="t"><b>Mixed</b>`, `class="icon chev"`, `Shopping, to-dos and anything else the family collects. Open a list to add entries and tick them off.`} {
		if !strings.Contains(body, want) {
			t.Errorf("overview lacks %q\n%s", want, body)
		}
	}
	if strings.Contains(body, `class="cards lists"`) || strings.Contains(body, "Nothing open") {
		t.Errorf("overview still has the old cards")
	}
	de := b.do(http.MethodGet, "/lists", nil, map[string]string{"Accept-Language": "de"}).body
	for _, want := range []string{"1 offen, 1 abgehakt", "Alle 2 abgehakt", "Noch keine Einträge", "Öffne eine Liste, um Einträge hinzuzufügen und abzuhaken."} {
		if !strings.Contains(de, want) {
			t.Errorf("German overview lacks %q", want)
		}
	}
}

func TestListLookNamesWhatIsChosenAndKeepsItAfterAnError(t *testing.T) {
	b := signedInBox(t)
	en := getEN(b, "/lists").body
	for _, want := range []string{`<span class="sum">Green, filled square, by hand</span>`, `class="chips colour-chips"><legend>Colour on colour screens</legend>`,
		`<legend>Symbol on black-and-white displays</legend>`, `<legend>Order of the entries</legend>`, `>By hand</label>`} {
		if !strings.Contains(en, want) {
			t.Errorf("new list form lacks %q", want)
		}
	}
	r := b.do(http.MethodPost, "/lists", url.Values{"name": {" "}, "color": {"teal"}, "symbol": {"square-filled"}, "sort": {"alphabetical"}}, map[string]string{"Accept-Language": "en"})
	if r.status != http.StatusUnprocessableEntity || !strings.Contains(r.body, `<span class="sum">Teal, filled square, A to Z</span>`) {
		t.Fatalf("closed disclosure after an error = %d\n%s", r.status, r.body)
	}
	r = b.do(http.MethodPost, "/lists", url.Values{"name": {" "}, "color": {"teal"}, "symbol": {"square-filled"}, "sort": {"alphabetical"}}, map[string]string{"Accept-Language": "de"})
	if !strings.Contains(r.body, `<span class="sum">Petrol, volles Quadrat, A bis Z</span>`) {
		t.Errorf("German summary after an error:\n%s", r.body)
	}
	// An invalid choice must not print raw keys.
	r = b.do(http.MethodPost, "/lists", url.Values{"name": {"x"}, "color": {"pink"}, "symbol": {"square-filled"}, "sort": {"alphabetical"}}, map[string]string{"Accept-Language": "en"})
	if r.status != http.StatusUnprocessableEntity || strings.Contains(r.body, "lists.color.") {
		t.Errorf("invalid colour shows a raw key = %d", r.status)
	}
}

func TestListPageHeaderBackLinkTitleAndMeta(t *testing.T) {
	b := signedInBox(t)
	id, _ := listWith(t, b, "Packing", []string{"b", "c"}, "a", "b", "c")
	body := getEN(b, "/lists/"+id).body
	for _, want := range []string{`<a class="back" href="/lists">`, `<h1 class="list-title"><span class="marker" data-person="3" data-shape="square-filled" aria-hidden="true"></span><span>Packing</span></h1>`,
		`<p class="hint list-meta">Sorted by hand. 1 open, 2 ticked off.</p>`} {
		if !strings.Contains(body, want) {
			t.Errorf("header lacks %q\n%s", want, body)
		}
	}
	h, _ := b.db.HomeHousehold()
	az, _ := b.db.CreateList(h.ID, list.List{Name: "Drinks", Color: "teal", Symbol: "circle-empty", Sort: list.Alphabetical})
	empty, _ := b.db.CreateList(h.ID, list.List{Name: "Gifts", Color: "ochre", Symbol: "diamond-filled", Sort: list.Manual})
	if got := getEN(b, "/lists/"+az.ID).body; !strings.Contains(got, `Sorted A to Z. No entries yet.</p>`) {
		t.Errorf("A to Z meta:\n%s", got)
	}
	if got := getEN(b, "/lists/"+empty.ID).body; !strings.Contains(got, `<p class="hint list-meta">Sorted by hand. No entries yet.</p>`) {
		t.Errorf("empty meta:\n%s", got)
	}
	allDone, _ := listWith(t, b, "Done", []string{"a"}, "a")
	if got := getEN(b, "/lists/"+allDone).body; !strings.Contains(got, `Sorted by hand. All 1 ticked off.</p>`) {
		t.Errorf("all ticked meta:\n%s", got)
	}
	de := b.do(http.MethodGet, "/lists/"+id, nil, map[string]string{"Accept-Language": "de"}).body
	if !strings.Contains(de, "Von Hand sortiert. 1 offen, 2 abgehakt.") {
		t.Errorf("German meta:\n%s", de)
	}
}

func TestAddRowSitsAboveTheEntriesAndTheFieldIsFocusedOnlyAfterAnAdd(t *testing.T) {
	b := signedInBox(t)
	id, _ := listWith(t, b, "Shop", nil, "Milk")
	page := getEN(b, "/lists/"+id).body
	before(t, page, `<form method="post" action="/lists/`+id+`/entries" class="add-entry">`, `New entry`, `placeholder="e.g. Apples"`, `>Add</button>`, `<ul class="entries">`)
	if strings.Contains(page, "autofocus") {
		t.Errorf("plain visit focuses the field")
	}
	r := b.post("/lists/"+id+"/entries", url.Values{"text": {"Pasta"}})
	if r.status != http.StatusSeeOther || r.location != "/lists/"+id {
		t.Fatalf("add = %d %q", r.status, r.location)
	}
	after := getEN(b, r.location).body
	if !strings.Contains(after, `data-success`) || !strings.Contains(after, `&#34;Pasta&#34; added.`) {
		t.Errorf("no success line naming the entry:\n%s", after)
	}
	if !strings.Contains(after, "autofocus") {
		t.Errorf("field not focused after adding")
	}
	again := getEN(b, "/lists/"+id).body
	if strings.Contains(again, "autofocus") || strings.Contains(again, "data-success") {
		t.Errorf("focus or success line repeated on the next visit")
	}
	de := b.do(http.MethodPost, "/lists/"+id+"/entries", url.Values{"text": {"Äpfel"}}, nil)
	if de.status != http.StatusSeeOther {
		t.Fatal(de.status)
	}
	got := b.do(http.MethodGet, "/lists/"+id, nil, map[string]string{"Accept-Language": "de"}).body
	if !strings.Contains(got, "„Äpfel“ hinzugefügt.") || !strings.Contains(got, `placeholder="z. B. Äpfel"`) || !strings.Contains(got, ">Hinzufügen</button>") {
		t.Errorf("German add:\n%s", got)
	}
}

func TestSuccessLinesNeverShowTextFromAForgedCookie(t *testing.T) {
	b := signedInBox(t)
	id, _ := listWith(t, b, "Shop", nil, "Milk")
	got := b.do(http.MethodGet, "/lists/"+id, nil, map[string]string{"Accept-Language": "en", "Cookie": "hejmo_flash=flash.entry_added:deadbeef"}).body
	if strings.Contains(got, "data-success") && strings.Contains(got, "deadbeef") {
		t.Errorf("forged flash argument shown")
	}
}

func TestEverydayRowsHaveOnlyTheBigCheckForm(t *testing.T) {
	b := signedInBox(t)
	id, _ := listWith(t, b, "Shop", []string{"c"}, "a", "b", "c")
	body := getEN(b, "/lists/"+id).body
	everyday := strings.SplitN(body, `class="list-settings"`, 2)[0]
	for _, forbidden := range []string{"/move", `class="row"`, "Move up", "Move down"} {
		if strings.Contains(everyday, forbidden) {
			t.Errorf("everyday view contains %q", forbidden)
		}
	}
	if n := strings.Count(body, `class="big-check"`); n != 3 {
		t.Errorf("big-check forms = %d, want 3", n)
	}
	if regexp.MustCompile(`/entries/[^"]+/delete`).MatchString(body) {
		t.Errorf("per-row remove button in the everyday view")
	}
	if !strings.Contains(body, `aria-label="Tick off: a"`) || !strings.Contains(body, `aria-label="Mark as open: c"`) {
		t.Errorf("tick labels:\n%s", body)
	}
}

func TestTickedOffEntriesFormTheirOwnGroupAndUntickingReturnsThemToTheirPlace(t *testing.T) {
	b := signedInBox(t)
	id, ids := listWith(t, b, "Shop", []string{"b"}, "a", "b", "c", "d")
	body := getEN(b, "/lists/"+id).body
	before(t, body, `<ul class="entries">`, ">a<", ">c<", ">d<", `<div class="group-head"><h2>Ticked off (1)</h2><a href="?ticked=hide">Hide</a></div>`, `<ul class="entries">`, `<s>b</s>`)
	if got := strings.Count(body, `<ul class="entries">`); got != 2 {
		t.Errorf("ul.entries = %d, want 2", got)
	}
	// Stored positions are unchanged: unticking puts b back between a and c.
	if r := b.post("/lists/"+id+"/entries/"+ids["b"]+"/done", url.Values{"done": {"0"}}); r.status != http.StatusSeeOther {
		t.Fatal(r.status)
	}
	after := getEN(b, "/lists/"+id).body
	before(t, after, ">a<", ">b<", ">c<", ">d<")
	if strings.Contains(after, "group-head") {
		t.Errorf("empty ticked-off group shown")
	}
	b.post("/lists/"+id+"/entries/"+ids["b"]+"/done", url.Values{"done": {"1"}})
	de := b.do(http.MethodGet, "/lists/"+id, nil, map[string]string{"Accept-Language": "de"}).body
	if !strings.Contains(de, "<h2>Abgehakt (1)</h2>") || !strings.Contains(de, ">Ausblenden</a>") || !strings.Contains(de, `aria-label="Abhaken: a"`) {
		t.Errorf("German group:\n%s", de)
	}
}

func TestHiddenTickedOffEntriesShowALineAndTheChoiceSurvivesEveryRedirect(t *testing.T) {
	b := signedInBox(t)
	id, ids := listWith(t, b, "Shop", []string{"x", "y"}, "a", "b", "x", "y")
	body := getEN(b, "/lists/"+id+"?ticked=hide").body
	if strings.Contains(body, "<s>x</s>") || strings.Contains(body, "group-head") {
		t.Errorf("ticked-off entries not hidden:\n%s", body)
	}
	for _, want := range []string{`<p class="hidden-line">2 ticked-off entries are hidden. <a href="/lists/` + id + `">Show them</a></p>`, `name="ticked" value="hide"`} {
		if !strings.Contains(body, want) {
			t.Errorf("hidden view lacks %q\n%s", want, body)
		}
	}
	keep := url.Values{"ticked": {"hide"}}
	with := func(k, v string) url.Values { u := url.Values{"ticked": {"hide"}}; u.Set(k, v); return u }
	for name, r := range map[string]resp{
		"tick": b.post("/lists/"+id+"/entries/"+ids["a"]+"/done", with("done", "1")),
		"add":  b.post("/lists/"+id+"/entries", with("text", "new")),
	} {
		if r.status != http.StatusSeeOther || r.location != "/lists/"+id+"?ticked=hide" {
			t.Errorf("%s redirect = %d %q", name, r.status, r.location)
		}
	}
	mv := with("direction", "down")
	if r := b.post("/lists/"+id+"/entries/"+ids["b"]+"/move", mv); r.location != "/lists/"+id+"?edit=1&ticked=hide" {
		t.Errorf("move redirect = %q", r.location)
	}
	if r := b.post("/lists/"+id+"/entries/"+ids["b"]+"/delete", keep); r.location != "/lists/"+id+"?edit=1&ticked=hide" {
		t.Errorf("remove redirect = %q", r.location)
	}
	if r := b.post("/lists/"+id+"/completed/delete", keep); r.location != "/lists/"+id+"?ticked=hide" {
		t.Errorf("delete ticked redirect = %q", r.location)
	}
	// Without the choice nothing is added.
	if r := b.post("/lists/"+id+"/entries", url.Values{"text": {"more"}}); r.location != "/lists/"+id {
		t.Errorf("redirect without hide = %q", r.location)
	}
	// The singular reads right.
	id2, _ := listWith(t, b, "One", []string{"z"}, "z", "k")
	if got := getEN(b, "/lists/"+id2+"?ticked=hide").body; !strings.Contains(got, "1 ticked-off entry is hidden.") {
		t.Errorf("singular hidden line:\n%s", got)
	}
	// An error keeps the choice, too.
	id3, _ := listWith(t, b, "Three", []string{"u"}, "u", "v")
	r := b.do(http.MethodPost, "/lists/"+id3+"/entries", url.Values{"text": {""}, "ticked": {"hide"}}, map[string]string{"Accept-Language": "en"})
	if r.status != http.StatusUnprocessableEntity || !strings.Contains(r.body, "hidden-line") {
		t.Errorf("hide lost on an error = %d", r.status)
	}
}

func TestToolsLinkIsForParentsAndOnlyWhenThereAreEntries(t *testing.T) {
	cp := newChildPhone(t)
	b := cp.parent.b
	id, _ := listWith(t, b, "Shop", nil, "a")
	empty, _ := listWith(t, b, "Empty", nil)
	if got := getEN(b, "/lists/"+id).body; !strings.Contains(got, `<p class="tools"><a href="/lists/`+id+`?edit=1">Sort or remove entries</a></p>`) {
		t.Errorf("tools link:\n%s", got)
	}
	if got := getEN(b, "/lists/"+empty).body; strings.Contains(got, `class="tools"`) {
		t.Errorf("tools link on an empty list")
	}
	h, _ := b.db.HomeHousehold()
	az, _ := b.db.CreateList(h.ID, list.List{Name: "AZ", Color: "teal", Symbol: "circle-empty", Sort: list.Alphabetical})
	b.db.AddEntry(h.ID, az.ID, "x")
	if got := getEN(b, "/lists/"+az.ID).body; !strings.Contains(got, `?edit=1">Remove entries</a>`) {
		t.Errorf("A to Z tools link:\n%s", got)
	}
	if got := cp.screen.get("/lists/"+id, "en").body; strings.Contains(got, `class="tools"`) || strings.Contains(got, "edit=1") {
		t.Errorf("child sees the tools link")
	}
	de := b.do(http.MethodGet, "/lists/"+id, nil, map[string]string{"Accept-Language": "de"}).body
	if !strings.Contains(de, "Einträge sortieren oder entfernen</a>") {
		t.Errorf("German tools link")
	}
	if r := cp.screen.get("/lists/"+id+"?edit=1", "en"); r.status != http.StatusForbidden {
		t.Errorf("child edit view = %d, want 403", r.status)
	}
}

func TestEditViewOffersUpDownAndRemoveOverOpenEntriesOnly(t *testing.T) {
	b := signedInBox(t)
	id, ids := listWith(t, b, "Shop", []string{"b"}, "a", "b", "c")
	page := getEN(b, "/lists/"+id+"?edit=1&ticked=hide").body
	before(t, page, `<div class="edit">`, `<h1 class="list-title">`, `<p class="hint list-meta">Sort or remove entries</p>`,
		`<div class="edit-bar"><p>Changes are saved straight away.</p><a class="button" href="/lists/`+id+`?ticked=hide">Done</a></div>`,
		`<li><span class="title">a</span><span class="row">`, `<li><span class="title">c</span><span class="row">`,
		`<div class="group-head"><h2>Ticked off (1)</h2></div>`, `<li><span class="title"><s>b</s></span><span class="row">`)
	li := func(text string) string {
		m := regexp.MustCompile(`(?s)<li><span class="title">(<s>)?` + text + `(</s>)?</span>.*?</li>`).FindString(page)
		if m == "" {
			t.Fatalf("no row for %s", text)
		}
		return m
	}
	a, b2, c := li("a"), li("b"), li("c")
	if strings.Contains(a, "Move up") || !strings.Contains(a, "Move down: a") || !strings.Contains(a, ">Remove</button>") {
		t.Errorf("first open entry:\n%s", a)
	}
	if !strings.Contains(c, "Move up: c") || strings.Contains(c, "Move down") {
		t.Errorf("last open entry (b is ticked off):\n%s", c)
	}
	if strings.Contains(b2, "/move") || !strings.Contains(b2, "/entries/"+ids["b"]+"/delete") {
		t.Errorf("ticked-off entry:\n%s", b2)
	}
	for _, want := range []string{`class="button secondary"`, `>Down</button>`} {
		if !strings.Contains(a, want) {
			t.Errorf("row lacks %q", want)
		}
	}
	if strings.Contains(page, `big-check`) || strings.Contains(page, `class="add-entry"`) {
		t.Errorf("edit view shows everyday controls")
	}
	// Ticked-off entries stay visible even though the list is hidden.
	if !strings.Contains(page, "<s>b</s>") {
		t.Errorf("edit view hides ticked-off entries")
	}
	de := b.do(http.MethodGet, "/lists/"+id+"?edit=1", nil, map[string]string{"Accept-Language": "de"}).body
	for _, want := range []string{"Einträge sortieren oder entfernen", "Änderungen werden sofort gespeichert.", ">Fertig</a>", "Abgehakt (1)", ">Entfernen</button>", ">Nach unten</button>"} {
		if !strings.Contains(de, want) {
			t.Errorf("German edit view lacks %q", want)
		}
	}
	az := listIDByNameWeb(t, b)
	if got := getEN(b, "/lists/"+az+"?edit=1").body; strings.Contains(got, "/move") || !strings.Contains(got, "Remove entries</p>") {
		t.Errorf("A to Z edit view:\n%s", got)
	}
}

func listIDByNameWeb(t *testing.T, b *box) string {
	t.Helper()
	h, _ := b.db.HomeHousehold()
	l, _ := b.db.CreateList(h.ID, list.List{Name: "Zed", Color: "teal", Symbol: "circle-empty", Sort: list.Alphabetical})
	b.db.AddEntry(h.ID, l.ID, "b")
	b.db.AddEntry(h.ID, l.ID, "a")
	return l.ID
}

func entryOrder(t *testing.T, b *box, listID string) []string {
	t.Helper()
	h, _ := b.db.HomeHousehold()
	es, err := b.db.Entries(h.ID, listID)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, e := range es {
		out = append(out, e.Text)
	}
	return out
}

func TestMovingSwapsWithTheNeighbouringOpenEntryAndSkipsTickedOffOnes(t *testing.T) {
	b := signedInBox(t)
	id, ids := listWith(t, b, "Shop", []string{"t"}, "a", "t", "c", "d")
	// c moves up past the ticked-off t and swaps with a.
	r := b.post("/lists/"+id+"/entries/"+ids["c"]+"/move", url.Values{"direction": {"up"}})
	if r.status != http.StatusSeeOther || r.location != "/lists/"+id+"?edit=1" {
		t.Fatalf("move = %d %q", r.status, r.location)
	}
	if got := entryOrder(t, b, id); !sameStrings(got, []string{"c", "a", "t", "d"}) {
		t.Fatalf("after up: %v", got)
	}
	page := getEN(b, "/lists/"+id+"?edit=1").body
	if !strings.Contains(page, `&#34;c&#34; moved up.`) || !strings.Contains(page, `data-success`) {
		t.Errorf("success line after moving up:\n%s", page)
	}
	// a moves down past t and swaps with d.
	b.post("/lists/"+id+"/entries/"+ids["a"]+"/move", url.Values{"direction": {"down"}})
	if got := entryOrder(t, b, id); !sameStrings(got, []string{"c", "t", "d", "a"}) {
		t.Fatalf("after down: %v", got)
	}
	if !strings.Contains(getEN(b, "/lists/"+id+"?edit=1").body, `&#34;a&#34; moved down.`) {
		t.Errorf("success line after moving down")
	}
	// The first open entry cannot go up, the last cannot go down: nothing changes.
	if r := b.post("/lists/"+id+"/entries/"+ids["c"]+"/move", url.Values{"direction": {"up"}}); r.status != http.StatusSeeOther {
		t.Errorf("move first up = %d", r.status)
	}
	if got := entryOrder(t, b, id); !sameStrings(got, []string{"c", "t", "d", "a"}) {
		t.Errorf("order changed: %v", got)
	}
	// A ticked-off entry is not moved.
	if r := b.post("/lists/"+id+"/entries/"+ids["t"]+"/move", url.Values{"direction": {"up"}}); r.status != http.StatusUnprocessableEntity {
		t.Errorf("move ticked-off = %d", r.status)
	}
}

func TestRemovingAnEntryReturnsToTheEditViewWithASuccessLine(t *testing.T) {
	b := signedInBox(t)
	id, ids := listWith(t, b, "Shop", nil, "a", "Pasta")
	r := b.post("/lists/"+id+"/entries/"+ids["Pasta"]+"/delete", url.Values{})
	if r.status != http.StatusSeeOther || r.location != "/lists/"+id+"?edit=1" {
		t.Fatalf("remove = %d %q", r.status, r.location)
	}
	if page := getEN(b, r.location).body; !strings.Contains(page, `&#34;Pasta&#34; removed.`) || strings.Contains(page, ">Pasta<") {
		t.Errorf("after removing:\n%s", page)
	}
	if r := b.post("/lists/"+id+"/entries/"+ids["Pasta"]+"/delete", url.Values{}); r.status != http.StatusNotFound {
		t.Errorf("removing twice = %d", r.status)
	}
}

func TestDeleteTickedOffEntriesStatesTheNumberAndSitsBetweenTheGroupAndTheSettings(t *testing.T) {
	b := signedInBox(t)
	two, _ := listWith(t, b, "Two", []string{"a", "b"}, "a", "b", "c")
	one, _ := listWith(t, b, "One", []string{"a"}, "a", "b")
	body := getEN(b, "/lists/"+two).body
	before(t, body, `<h2>Ticked off (2)</h2>`, `<summary>Delete ticked-off entries (2)</summary>`,
		`<p>The 2 ticked-off entries of this list are deleted for good. Open entries stay.</p>`, `>Yes, delete 2 entries</button>`, `<section class="list-settings"`, `<summary>Delete list</summary>`)
	got := getEN(b, "/lists/"+one).body
	for _, want := range []string{`<summary>Delete ticked-off entry (1)</summary>`, `The ticked-off entry of this list is deleted for good. Open entries stay.`, `>Yes, delete 1 entry</button>`} {
		if !strings.Contains(got, want) {
			t.Errorf("singular lacks %q", want)
		}
	}
	none, _ := listWith(t, b, "None", nil, "a")
	if got := getEN(b, "/lists/"+none).body; strings.Contains(got, "Delete ticked-off") {
		t.Errorf("delete offered without ticked-off entries")
	}
	de := b.do(http.MethodGet, "/lists/"+two, nil, map[string]string{"Accept-Language": "de"}).body
	for _, want := range []string{"Abgehakte Einträge löschen (2)", "Ja, 2 Einträge löschen"} {
		if !strings.Contains(de, want) {
			t.Errorf("German lacks %q", want)
		}
	}
}

func TestListSettingsDisclosureSaysWhatIsChosen(t *testing.T) {
	b := signedInBox(t)
	id, _ := listWith(t, b, "Shop", nil, "a")
	body := getEN(b, "/lists/"+id).body
	if !strings.Contains(body, `<section class="list-settings"`) || !strings.Contains(body, `List settings<span class="sum">Green, filled square, by hand</span>`) {
		t.Errorf("settings disclosure:\n%s", body)
	}
	before(t, body, `list-settings`, `Delete list`)
	r := b.do(http.MethodPost, "/lists/"+id+"/settings", url.Values{"name": {""}, "color": {"plum"}, "symbol": {"diamond-filled"}, "sort": {"alphabetical"}}, map[string]string{"Accept-Language": "en"})
	if r.status != http.StatusUnprocessableEntity || !strings.Contains(r.body, `List settings<span class="sum">Plum, diamond, A to Z</span>`) {
		t.Errorf("summary after an error = %d\n%s", r.status, r.body)
	}
}

func TestEmptyListShowsTheAddRowAndOneSentence(t *testing.T) {
	b := signedInBox(t)
	id, _ := listWith(t, b, "Gifts", nil)
	body := getEN(b, "/lists/"+id).body
	before(t, body, `class="add-entry"`, `<div class="empty" data-empty><p>Nothing on this list yet. Type the first entry above and tap Add.</p></div>`)
	if strings.Contains(body, `<ul class="entries">`) || strings.Contains(body, "Sort or remove") {
		t.Errorf("empty list shows entries or tools")
	}
	if i := strings.Index(body, `data-empty`); strings.Contains(body[i:i+300], "<a ") || strings.Contains(body[i:i+300], "<button") {
		t.Errorf("empty box has an action")
	}
}

func TestChildViewHasNoAddRowNoToolsAndTheTickedOffGroup(t *testing.T) {
	cp := newChildPhone(t)
	b := cp.parent.b
	id, _ := listWith(t, b, "Shop", []string{"b", "c"}, "a", "b", "c")
	body := cp.screen.get("/lists/"+id, "en").body
	for _, want := range []string{`<p class="hint list-meta">1 open, 2 ticked off. Tap the box when you have it.</p>`, `<div class="group-head"><h2>Ticked off (2)</h2><a href="?ticked=hide">Hide</a></div>`} {
		if !strings.Contains(body, want) {
			t.Errorf("child view lacks %q\n%s", want, body)
		}
	}
	for _, no := range []string{"add-entry", `class="tools"`, "list-settings", "Delete"} {
		if strings.Contains(body, no) {
			t.Errorf("child view contains %q", no)
		}
	}
	hidden := cp.screen.get("/lists/"+id+"?ticked=hide", "en").body
	if !strings.Contains(hidden, `class="hidden-line"`) {
		t.Errorf("child hidden line missing")
	}
	// A child's tick keeps the hide choice.
	h, _ := b.db.HomeHousehold()
	es, _ := b.db.Entries(h.ID, id)
	if r := cp.screen.post("/lists/"+id+"/entries/"+es[0].ID+"/done", url.Values{"done": {"1"}, "ticked": {"hide"}}); r.location != "/lists/"+id+"?ticked=hide" {
		t.Errorf("child tick redirect = %q", r.location)
	}
}

func TestFailedFormsShowNoStaleSuccessLine(t *testing.T) {
	b := signedInBox(t)
	id, ids := listWith(t, b, "Shop", nil, "a")
	if r := b.post("/lists/"+id+"/entries/"+ids["a"]+"/done", url.Values{"done": {"1"}}); r.status != http.StatusSeeOther {
		t.Fatal(r.status)
	}
	failed := b.do(http.MethodPost, "/lists", url.Values{"name": {""}, "color": {"teal"}, "symbol": {"square-filled"}, "sort": {"manual"}}, map[string]string{"Accept-Language": "en"})
	if failed.status != http.StatusUnprocessableEntity || strings.Contains(failed.body, "data-success") {
		t.Fatalf("422 shows a success line = %d", failed.status)
	}
	if next := getEN(b, "/lists").body; strings.Contains(next, "data-success") {
		t.Errorf("the stale success line turned up on the next page")
	}
}
