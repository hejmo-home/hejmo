package server_test

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/base64"
	"encoding/json"
	"flag"
	"fmt"
	"html"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/chromedp/cdproto/cdp"
	"github.com/chromedp/cdproto/emulation"
	"github.com/chromedp/cdproto/network"
	"github.com/chromedp/chromedp"

	"github.com/hejmo-home/hejmo/internal/auth"
	"github.com/hejmo-home/hejmo/internal/badge"
	"github.com/hejmo-home/hejmo/internal/caldavclient"
	"github.com/hejmo-home/hejmo/internal/calsync"
	"github.com/hejmo-home/hejmo/internal/contact"
	"github.com/hejmo-home/hejmo/internal/list"
	"github.com/hejmo-home/hejmo/internal/outbound"
	"github.com/hejmo-home/hejmo/internal/passkey/passkeytest"
	"github.com/hejmo-home/hejmo/internal/recipe"
	"github.com/hejmo-home/hejmo/internal/recipephoto"
	"github.com/hejmo-home/hejmo/internal/reward"
	"github.com/hejmo-home/hejmo/internal/starter"
	"github.com/hejmo-home/hejmo/internal/store"
	"github.com/hejmo-home/hejmo/internal/streak"
	"github.com/hejmo-home/hejmo/internal/task"
	"github.com/hejmo-home/hejmo/internal/unit"
)

// The rendered layout check (ticket 161, retro of 16/11/2026): the layout tests
// in web/layout_test.go read the stylesheet, so a rule that exists but does not
// work passes (the test of #150 did). This test renders every page in headless
// Chrome (chromedp) and measures the result: no page wider than the viewport,
// no text container overflowing its box, no person's name broken inside a word.
//
// It needs Chrome. Locally it skips without one; CI sets HEJMO_REQUIRE_CHROME=1
// so a missing Chrome fails there. HEJMO_CHROME names a Chrome binary to use
// instead of the one found on the machine. HEJMO_LAYOUT_SHOTS names a directory
// for the screenshots of failing pages (CI uploads it); the default is the
// test's temp directory.

//go:embed testdata/layout_probe.js
var layoutProbeJS string

// chromeBinary returns the Chrome to drive, or "".
func chromeBinary() string {
	if p := os.Getenv("HEJMO_CHROME"); p != "" {
		if _, err := os.Stat(p); err != nil {
			return ""
		}
		return p
	}
	for _, name := range []string{"google-chrome", "google-chrome-stable", "chromium", "chromium-browser", "chrome"} {
		if p, err := exec.LookPath(name); err == nil {
			return p
		}
	}
	for _, p := range []string{
		"/Applications/Google Chrome.app/Contents/MacOS/Google Chrome",
		"/Applications/Chromium.app/Contents/MacOS/Chromium",
	} {
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	return ""
}

// requireChrome returns the Chrome binary. Without one the test skips, or
// fails when HEJMO_REQUIRE_CHROME=1.
func requireChrome(t *testing.T) string {
	t.Helper()
	if p := chromeBinary(); p != "" {
		return p
	}
	if os.Getenv("HEJMO_REQUIRE_CHROME") == "1" {
		t.Fatal("Chrome is required (HEJMO_REQUIRE_CHROME=1) but none was found; install Chrome or set HEJMO_CHROME")
	}
	t.Skip("no Chrome found; set HEJMO_REQUIRE_CHROME=1 to make this a failure")
	return ""
}

// With HEJMO_REQUIRE_CHROME=1 a missing Chrome fails the rendered layout check
// instead of skipping it, so CI cannot pass without running it. The test runs
// it in a child process pointed at a Chrome that does not exist.
func TestMissingChromeFailsWhenChromeIsRequired(t *testing.T) {
	t.Parallel()
	run := func(require string) (string, error) {
		cmd := exec.Command(os.Args[0], "-test.run=^TestRenderedLayout$", "-test.v")
		cmd.Env = []string{"PATH=", "HEJMO_CHROME=/nonexistent/chrome", "HEJMO_REQUIRE_CHROME=" + require}
		out, err := cmd.CombinedOutput()
		return string(out), err
	}
	out, err := run("1")
	if err == nil || !strings.Contains(out, "Chrome is required") {
		t.Errorf("with HEJMO_REQUIRE_CHROME=1: err = %v, want a failure saying Chrome is required; output:\n%s", err, out)
	}
	out, err = run("")
	if err != nil || !strings.Contains(out, "SKIP") {
		t.Errorf("without HEJMO_REQUIRE_CHROME: err = %v, want a skip; output:\n%s", err, out)
	}
}

// layoutNames are the household's people: long German names, one with a hyphen.
var layoutNames = []string{"Friedrich", "Maximilian", "Anna-Lena", "Konstantin", "Wilhelmine", "Christoph", "Sophie-Marie", "Leopoldine"}

// layoutFixture is a household of eight people with long German texts, a paired
// display and every kind of content the pages show.
type layoutFixture struct {
	*familyBox
	ids []string // person IDs in layoutNames order
	kid *screen  // the personal device of a child, which shows lists as tiles

	lists         []string // list IDs
	omaID         string   // the contact with a photo (ticket 297)
	longVariety   string   // the entry of an article with a very long variety, on the first list
	contactGroups []string // the ids of the contact groups in their order
	photoRecipe   string   // the recipe with three photos (ticket 280)
	fullRecipe    string   // a starter recipe with twelve photos
	nutriRecipe   string   // the recipe whose nutrition card names what is not counted and what is missing (ticket 288)
	foodArticle   string   // the own article with a chosen food
	inviteToken   string   // the invitation of Maximilian, a parent without account
	hint          *screen  // a display that shows the screensaver's hint
	outlook       []string // the Outlook pages (#177) that no link leads to
	recipes       []string // recipe IDs: the long one with twelve steps, then the plain one
	rewards       []string // reward IDs (#291): redeemed, never redeemed, archived by hand, archived because its person left
	recipeIDs     []string // recipe IDs (#279): the long Käsespätzle, then the Gurkensalat
}

func newLayoutFixture(t *testing.T) *layoutFixture {
	t.Helper()
	f := &layoutFixture{familyBox: newFamilyBox(t, "eink_color")}
	h, db := f.householdID, f.db
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	// Rename the example family, then add four more people.
	for i, old := range []string{"Mama", "Papa", "Sebastian", "Mia"} {
		p := f.people[old]
		must(db.UpdatePerson(h, p.ID, layoutNames[i], p.Color, p.Shape))
		f.ids = append(f.ids, p.ID)
	}
	for i := 4; i < len(layoutNames); i++ {
		role := store.RoleChild
		if i%3 == 0 {
			role = store.RoleParent
		}
		p, err := db.AddPerson(h, store.Person{Name: layoutNames[i], Role: role})
		must(err)
		f.ids = append(f.ids, p.ID)
	}
	f.people = map[string]store.Person{}
	for i, id := range f.ids {
		p, err := db.Person(h, id)
		must(err)
		f.people[layoutNames[i]] = p
	}

	// Events of today (Saturday 14/11/2026) and the week, for everyone and for pairs.
	f.now = f.at(14, 13, 0)
	f.event(t, "Kinderarztvorsorgeuntersuchung", f.at(14, 14, 0), f.at(14, 15, 0), false, "Maximilian", "Anna-Lena")
	f.event(t, "Elternabend der Grundschule Pasing", f.at(14, 18, 0), f.at(14, 20, 0), false, "Friedrich", "Christoph")
	f.event(t, "Wochenendausflug zur Schwimmhalle", f.at(14, 0, 0), f.at(16, 0, 0), true, layoutNames...)
	f.event(t, "Geburtstagsfeier der Großeltern", f.at(15, 11, 0), f.at(15, 14, 0), false, layoutNames...)
	f.event(t, "Zahnarzttermin", f.at(16, 9, 0), f.at(16, 10, 0), false, "Konstantin", "Wilhelmine", "Sophie-Marie")

	// Lists with long entries.
	l, err := db.CreateList(h, list.List{Name: "Wochenendeinkaufsliste", Color: "blue", Symbol: "circle-empty", Sort: list.Alphabetical, Suggestions: true})
	must(err)
	f.lists = append(f.lists, l.ID)
	// Entries that name articles (#166): shipped ones that warn several people with long names, and the own articles made below, one with a photo and one with a symbol. The tiles of the child's personal device show them.
	for _, e := range []string{"Haferflockenpackung groß", "Kartoffelsalatzutaten für Samstag", "Vollkornspaghetti", "Bio-Pfefferminztee", "Toastbrot", "Milch", "Haselnuss-Mandel-Knusperflocken mit Schokostückchen", "Katzenfutter Huhn"} {
		_, err := db.AddEntry(h, l.ID, e)
		must(err)
	}
	// Articles with quantities (ticket 165): a quantity button on every open entry, warnings under it, one ticked off.
	for _, q := range []struct {
		text, art, unit string
		amount          float64
	}{{"Milch", "milk", "l", 3}, {"Eier", "eggs", "piece", 6}, {"Erdnussbutter", "peanut-butter", "pack", 1}, {"Bananen", "bananas", "", 0}, {"Zucker", "sugar", "g", 500}} {
		it := store.Item{Text: q.text, ArticleID: q.art}
		if q.unit != "" {
			it.Qty = list.NewQuantity(q.amount, q.unit)
		}
		_, err := db.AddItem(h, l.ID, it, func(base float64, _ unit.Kind) string { return q.unit })
		must(err)
	}
	// Varieties (ticket 277): the same article without and with varieties, one of them very long.
	for _, q := range []struct {
		text, art, variety string
		amount             float64
		unit               string
	}{{"Weizenmehl", "wheat-flour", "", 500, "g"}, {"Weizenmehl", "wheat-flour", "Type 405", 1, "kg"}, {"Dinkelmehl", "spelt-flour", "Dinkel-Vollkorn Type 1050 aus der Mühle Schäftlarn", 0, ""}, {"Milch", "milk", "laktosefrei", 3, "l"}} {
		it := store.Item{Text: q.text, ArticleID: q.art, Variety: q.variety}
		if q.unit != "" {
			it.Qty = list.NewQuantity(q.amount, q.unit)
		}
		r, err := db.AddItem(h, l.ID, it, func(base float64, _ unit.Kind) string { return q.unit })
		must(err)
		if len(q.variety) > 40 {
			f.longVariety = r.Entry.ID
		}
	}
	l2, err := db.CreateList(h, list.List{Name: "Urlaubsvorbereitungen Ostsee", Color: "green", Symbol: "circle-empty", Sort: list.Manual})
	must(err)
	f.lists = append(f.lists, l2.ID)
	for _, e := range []string{"Sonnenschutzmittelflaschen einpacken", "Regenjacken", "Reiseapotheke prüfen"} {
		_, err := db.AddEntry(h, l2.ID, e)
		must(err)
	}

	// Tasks, a routine, stars.
	for i, who := range layoutNames[:5] {
		_, err := db.AddTask(h, task.Task{PersonID: f.people[who].ID, Title: "Hausaufgabenheft kontrollieren", Repeat: task.Daily, Date: "2026-11-14", Stars: i % 3})
		must(err)
	}
	mia := f.people["Maximilian"].ID
	r, err := db.CreateRoutine(h, task.Routine{PersonID: mia, Name: "Morgenroutine vor der Schule", Part: task.Morning, Created: time.Date(2026, 11, 2, 6, 0, 0, 0, berlinTZ)})
	must(err)
	for _, title := range []string{"Zähne putzen", "Schulranzen packen", "Frühstücksbrotdose einpacken"} {
		k, err := db.AddTask(h, task.Task{PersonID: mia, Title: title, Repeat: task.Daily, Date: "2026-11-02", RoutineID: r.ID, Stars: 1})
		must(err)
		// The week of Mon 9 to Sun 15 November (#294): Monday to Wednesday done, Thursday half, Friday and today paused.
		for _, day := range []string{"2026-11-09", "2026-11-10", "2026-11-11"} {
			must(db.SetTaskDone(h, k.ID, day, true, "", f.at(14, 13, 0)))
		}
		if title == "Zähne putzen" {
			must(db.SetTaskDone(h, k.ID, "2026-11-12", true, "", f.at(14, 13, 0)))
		}
	}
	_, err = db.SavePause(h, streak.Pause{From: "2026-11-13", To: "2026-11-14", People: []string{mia}}, "")
	must(err)
	_, err = db.SavePause(h, streak.Pause{From: "2026-12-19", To: "2027-01-03"}, "")
	must(err)
	_, err = db.SavePause(h, streak.Pause{From: "2026-11-02", To: "2026-11-03", People: []string{f.people["Anna-Lena"].ID, mia}}, "")
	must(err)
	_, err = db.SaveRoutineAsTemplate(h, r.ID, "Morgenroutine für Grundschulkinder")
	must(err)

	// Diets, allergies and the article library (ticket 164): warnings that name people, own articles with a long name, a symbol and a photo, hiding switched on.
	for who, food := range map[string]store.Food{
		"Friedrich":    {Diet: "omnivore", Allergens: []string{"milk"}},
		"Maximilian":   {Diet: "vegetarian", Allergens: []string{"peanuts", "nuts"}, Other: "Kiwi"},
		"Anna-Lena":    {Diet: "pescatarian", Allergens: []string{"fish", "crustaceans", "molluscs"}},
		"Konstantin":   {Diet: "omnivore", Allergens: []string{"milk"}},
		"Sophie-Marie": {Diet: "vegan", Allergens: []string{"gluten", "soy", "celery"}, Other: "Erdbeeren, Tomaten, Pfirsiche (roh), Nachtschattengewächse"},
		"Leopoldine":   {Diet: "omnivore", Other: "Kiwi"},
	} {
		must(db.SetPersonFood(h, f.people[who].ID, food))
	}
	knusper, err := db.CreateArticle(h, f.ids[0], store.OwnArticle{Name: "Haselnuss-Mandel-Knusperflocken mit Schokostückchen", Dept: "pantry", Kind: "veg", Allergens: []string{"nuts", "milk", "soy", "gluten"}, Unit1: "pack", Unit2: "g"},
		&store.ArticlePicture{JPEG: testJPEG(t, 400, 512), Source: "upload", FromW: 3024, FromH: 4032, FromBytes: 3 << 20})
	must(err)
	_, err = db.CreateArticle(h, f.ids[1], store.OwnArticle{Name: "Katzenfutter Huhn", Dept: "other", Kind: "nonfood", Unit1: "pack", PicSource: "symbol", Symbol: "🐈"}, nil)
	must(err)
	must(db.SetLibrarySettings(h, store.LibrarySettings{HideAllergen: true, Everyone: true, ConfirmedBy: f.ids[0]}))
	must(db.SetArticleHidden(h, "walnuts", true))
	// Recipes (ticket 279): long names that break between words, a long variety and a long web address, several
	// people of the same allergen named in one line, free-text ingredients, a recipe without anything but a name.
	for _, r := range []recipe.Recipe{
		{Name: "Omas schwäbische Käsespätzle mit Röstzwiebeln und Bergkäse aus dem Allgäu", Category: recipe.Main, Portions: 8, Minutes: 50, Mealtimes: []string{"lunch", "dinner"},
			Tags: []string{"Kinder mögen es", "Sonntagsessen bei Oma und Opa"}, Tips: "Den Teig lieber zu fest als zu weich rühren.\nDie Spätzle schmecken am nächsten Tag in der Pfanne geröstet noch besser.",
			Source: "https://www.beispiel-kochseite.de/rezepte/omas-schwaebische-kaesespaetzle-mit-roestzwiebeln-und-bergkaese-aus-dem-allgaeu",
			Ingredients: []recipe.Ingredient{{Amount: 500, Unit: "g", Text: "Weizenmehl", ArticleID: "wheat-flour", Variety: "Type 405"}, {Amount: 6, Text: "Eier", ArticleID: "eggs"},
				{Amount: 400, Unit: "g", Text: "Butter", ArticleID: "butter", Variety: "Allgäuer Bergkäse, mindestens 6 Monate gereift, aus der Sennerei"}, {Amount: 1, Unit: "pinch", Text: "Muskatnuss"}, {Amount: 2, Unit: "can", Text: "Röstzwiebeln"}},
			// Twelve steps for the cooking view (#286): the numbers wrap to a second line, a long step keeps its line break.
			Steps: []string{"Mehl, Eier, 1 TL Salz und etwa 150 ml Wasser mit dem Kochlöffel schlagen, bis der Teig Blasen wirft.", "Den Teig portionsweise durch die Spätzlepresse in kochendes Salzwasser drücken.",
				"Die Spätzle mit der Schaumkelle herausnehmen, sobald sie oben schwimmen, und kurz in kaltem Wasser abschrecken.", "Für die Röstzwiebeln die Zwiebeln in feine Ringe schneiden und in Butter goldbraun braten.\nWichtig: nicht aus den Augen lassen, sonst werden sie bitter.",
				"Den Bergkäse reiben.", "Eine Auflaufform einfetten.", "Spätzle und Käse schichten.", "Mit Muskatnuss würzen.", "Im Ofen bei 180 Grad Umluft überbacken, bis der Käse Blasen wirft.",
				"Die Röstzwiebeln darüberstreuen.", "Fünf Minuten ruhen lassen.", "Mit Salat servieren und Oma Gerda zum Probieren rufen, bevor die Kinder alles aufgegessen haben."}},
		{Name: "Gurkensalat", Category: recipe.Side, Portions: 4},
	} {
		got, err := db.CreateRecipe(h, f.ids[0], r)
		if err != nil {
			t.Fatal(err)
		}
		f.recipes = append(f.recipes, got.ID)
	}

	// Own photos of recipes (ticket 280): the long Käsespätzle with a cover and two more, a starter recipe with all twelve.
	f.photoRecipe, f.fullRecipe = f.recipes[0], starter.All()[0].Recipe("de").ID
	for ref, n := range map[string]int{f.photoRecipe: 3, f.fullRecipe: store.MaxRecipePhotos} {
		for i := 0; i < n; i++ {
			set, err := recipephoto.Prepare(context.Background(), bytes.NewReader(testJPEG(t, 1200+i, 900)))
			must(err)
			ph, err := db.AddRecipePhoto(h, ref, store.NewRecipePhoto{PersonID: f.ids[i%2], FileName: "IMG.jpg", Width: set.Width, Height: set.Height})
			must(err)
			must(recipephoto.NewFiles(filepath.Join(f.box.photoDir, "recipe-photos")).Put(ph.ID, set))
		}
	}

	// Nutrition values (ticket 288): two of the five children see them, the card of a recipe that cannot count
	// everything (a long free-text line, a unit without weight, an own article without a food, a value the food lacks),
	// and an own article with a chosen food.
	for _, who := range []string{"Anna-Lena", "Konstantin"} {
		must(db.SetNutritionShown(h, f.people[who].ID, true))
	}
	food, err := db.CreateArticle(h, f.ids[0], store.OwnArticle{Name: "Omas Zwetschgenmus nach Familienrezept aus dem Allgäu", Dept: "pantry", Kind: "vegan", Unit1: "pack", Food: "bls:S161000", PieceG: 350}, nil)
	must(err)
	f.foodArticle = food.ID
	nr, err := db.CreateRecipe(h, f.ids[0], recipe.Recipe{Name: "Zwetschgenmus-Pfannkuchen mit Röstzwiebeln und Bergkäse aus dem Allgäu", Category: recipe.Main, Portions: 8,
		Ingredients: []recipe.Ingredient{{Amount: 500, Unit: "g", Text: "Weizenmehl", ArticleID: "wheat-flour"}, {Amount: 6, Text: "Eier", ArticleID: "eggs"},
			{Amount: 1, Unit: "jar", Text: "Röstzwiebeln aus dem Glas vom Wochenmarkt in Kempten"}, {Amount: 1, Unit: "handful", Text: "Heidelbeeren", ArticleID: "blueberries"},
			{Amount: 2, Unit: "piece", Text: "Haselnuss-Mandel-Knusperflocken mit Schokostückchen", ArticleID: knusper.ID}, {Amount: 200, Unit: "ml", Text: "Apfelsaft", ArticleID: "apple-juice"}, {Text: "Salz", ArticleID: "salt"}}})
	must(err)
	f.nutriRecipe = nr.ID

	// Groups, a calendar, a day note.
	_, err = db.CreateGroup(h, "Kindergartenkinder-Elternbeirat", f.ids[:3])
	must(err)
	_, err = db.CreateCalendar(store.Calendar{HouseholdID: h, Name: "Fußballtrainingsplanung", Kind: "local", Color: "#3B82F6"})
	must(err)
	must(db.SetDayNote(h, "2026-11-14", "Bitte Schwimmsachen und Handtücher für Maximilian und Anna-Lena nicht vergessen"))

	// The address book (ticket 296): long names, several numbers, own labels, long e-mail addresses, a long note and groups.
	f.addContacts(t)

	// Meal plan and template through the API, like a parent.
	c := newAPIClient(t, f.box)
	c.cookie = true
	for i, text := range []string{"Spätzle mit Linsen und Saitenwürstchen", "Kürbiscremesuppe mit Kürbiskernöl", "Gemüselasagne mit Büffelmozzarella"} {
		day := fmt.Sprintf("2026-11-%02d", 16+i)
		if status, body, _ := c.call(http.MethodPut, "/meals/"+day+"/dinner", map[string]any{"text": text}); status != http.StatusOK {
			t.Fatalf("meal %s = %d %v", day, status, body)
		}
	}
	// A subscribed calendar and an MCP token.
	f.feeds.set(schoolFeed, feedOf("UID:ferien\r\nSUMMARY:Herbstferienwochenprogramm\r\nDTSTART:20261102T080000Z\r\nDTEND:20261102T120000Z"), nil)
	c.addICS(t, map[string]any{"kind": "ics", "name": "Schulkalender der Grundschule", "url": schoolFeed})
	c.switchMCP(t, true)
	c.newMCPToken(t, "Claude Desktop auf dem Küchenrechner", map[string]string{"lists": "write"})
	// A paired personal device, a calendar connection, a CalDAV device token.
	f.kid = pairedPersonalDevice(t, f.box, f.people["Anna-Lena"], "Handy von Anna-Lena im Kinderzimmer")
	connID := connectedBox(t, f.box)
	// Two calendars with long names (#183): the first is on, for all eight people and under a longer name of its own, the second is off.
	f.sync.SetDialer(func(string, string, string) (calsync.Remote, error) { // the connection page asks for event counts
		return countingRemote{hostedRemote{found: []caldavclient.Calendar{
			{URL: flowCalendar, Name: "Familienkalender auf dem Nextcloud-Server"},
			{URL: flowCalendar2, Name: "Schulferienkalender der Grundschule Pasing"},
		}}}, nil
	})
	if r := f.post("/connections/"+connID+"/calendars", url.Values{
		"calendar": {flowCalendar}, "shown": {flowCalendar, flowCalendar2}, "interval": {"15"},
		"name_" + flowCalendar: {"Wochenendplanung der Familie Maier-Schmidt"}, "color_" + flowCalendar: {"#2F5E7A"},
		"people_" + flowCalendar: f.ids,
	}); r.status != http.StatusSeeOther {
		t.Fatalf("choose calendars = %d\n%s", r.status, r.body)
	}
	f.outlook = f.addOutlook(t)
	f.rewards = f.addRewards(t)
	f.addRequests(t)
	f.addBadges(t)
	if r := f.post("/caldav/tokens", url.Values{"name": {"iPhone von Friedrich mit langem Gerätenamen"}}); r.status != http.StatusOK {
		t.Fatalf("CalDAV token = %d\n%s", r.status, r.body)
	}
	// A passkey of the signed-in parent, created under the box's name.
	if r := addPasskeyAt(t, f.box, passkeytest.New(), boxName, "Mac im Arbeitszimmer"); r.status != http.StatusOK {
		t.Fatalf("passkey = %d %s", r.status, r.body)
	}
	// A second parent with an account (the page that resets his codes), and an
	// invitation for a parent without one.
	hash, _ := auth.HashPassword("sophie-maries long password")
	must(db.CreateAccount(store.Account{PersonID: f.ids[6], Username: "sophie-marie", PasswordHash: hash, TOTPSecret: auth.NewTOTPSecret()}))
	must(db.ConfirmTOTP(f.ids[6]))
	inv := f.do(http.MethodPost, "/people/"+f.ids[1]+"/invitation", url.Values{}, nil)
	if f.inviteToken = inviteTokenRe.FindString(inv.body); inv.status != http.StatusOK || f.inviteToken == "" {
		t.Fatalf("invitation = %d, no token\n%s", inv.status, inv.body)
	}
	// Photos: albums with long names, a hidden photo, an album from the box's folder and a display that chose some.
	def := defaultAlbum(t, c)
	trip := newAlbum(t, c, "Geburtstagsfeier zum siebzigsten Geburtstag von Uroma Hildegard")
	for _, a := range []string{def, trip} {
		id := uploaded(t, c, a, "Sommerfestbild_mit_sehr_langem_Dateinamen_ohne_Leerzeichen.jpg")
		uploaded(t, c, a, "IMG_0002.jpg")
		c.call(http.MethodPatch, "/photos/"+id, map[string]any{"hidden": true})
	}
	folder := filepath.Join(f.photoDir, "photos", "Wochenendausflug zur Schwimmhalle")
	must(os.MkdirAll(folder, 0o755))
	must(os.WriteFile(filepath.Join(folder, "a.jpg"), jpegPhoto(t, 640, 480), 0o644))
	_, _, shown := c.call(http.MethodGet, "/displays", nil)
	c.call(http.MethodPatch, "/displays/"+shown[0].(map[string]any)["id"].(string), map[string]any{"screensaver": map[string]any{"album_ids": []string{trip}},
		// A kiosk app and quiet mode: the Brightness block with its levels and the quiet choice (ticket 179); the second display has no kiosk app, so its block is greyed out.
		"layout": map[string]any{"blocks": []map[string]any{{"id": "emergency", "enabled": true}}}, // the emergency numbers tile (ticket 303) with long names and numbers
		"kiosk":  map[string]any{"url": "http://192.168.178.20:8080"}, "quiet_mode": map[string]any{"enabled": true, "from": "22:00", "to": "06:00"}})
	// A second display whose only album is empty: it shows the hint.
	f.hint = newScreen(f.box)
	if r := f.post("/displays", pairForm(codeOn(t, f.hint.get("/display", "en").body))); r.status != http.StatusSeeOther {
		t.Fatalf("pair the second display = %d", r.status)
	}
	empty := newAlbum(t, c, "Noch leeres Album mit einem sehr langen Namen für den Hinweis")
	_, _, both := c.call(http.MethodGet, "/displays", nil)
	c.call(http.MethodPatch, "/displays/"+both[1].(map[string]any)["id"].(string), map[string]any{"screensaver": map[string]any{"album_ids": []string{empty}}})
	tpl := dinnerWeek("Wochenplan mit Lieblingsgerichten", "2026-11-16", 2)
	if status, body := createTemplate(t, c, tpl); status != http.StatusCreated {
		t.Fatalf("meal template = %d %v", status, body)
	}
	// Recipes in the plan (ticket 281), after the template so that the first is a week's adjustment of it: a long name for all eight people,
	// one for five, one more for all eight, and an earlier free-text dish to suggest.
	recipes, err := db.Recipes(h)
	must(err)
	for _, r := range recipes {
		if strings.HasPrefix(r.Name, "Omas") { // the long Käsespätzle first, then the Gurkensalat
			f.recipeIDs = append([]string{r.ID}, f.recipeIDs...)
		} else {
			f.recipeIDs = append(f.recipeIDs, r.ID)
		}
	}
	for _, m := range []struct {
		day, mealtime string
		recipe        int
		portions      int
	}{{"2026-11-16", "dinner", 0, 0}, {"2026-11-17", "lunch", 1, 5}, {"2026-11-19", "dinner", 0, 0}} {
		body := map[string]any{"recipe_id": f.recipeIDs[m.recipe]}
		if m.portions > 0 {
			body["portions"] = m.portions
		}
		if status, resp, _ := c.call(http.MethodPut, "/meals/"+m.day+"/"+m.mealtime, body); status != http.StatusOK {
			t.Fatalf("recipe meal %s = %d %v", m.day, status, resp)
		}
	}
	if status, resp, _ := c.call(http.MethodPut, "/meals/2026-11-03/dinner", map[string]any{"text": "Spätzle mit Linsen"}); status != http.StatusOK {
		t.Fatalf("earlier dish = %d %v", status, resp)
	}
	return f
}

// countingRemote is a CalDAV server that also answers the event count the connection page asks for.
type countingRemote struct{ hostedRemote }

func (countingRemote) CountEvents(context.Context, string, time.Time) (int, error) { return 3, nil }

// layoutSite is one origin with the cookies a browser would carry to it.
type layoutSite struct {
	name    string
	base    string
	host    string       // the name the box was reached under, if not its address
	client  *http.Client // fetches pages the way the browser does, to crawl and to check the status
	cookies []*http.Cookie
}

// siteOf snapshots a client's cookies, so later changes to the client (a
// logout) do not reach the site. host is the name the box is reached under, "" for its address.
func siteOf(name string, b *box, c *http.Client, host ...string) layoutSite {
	u, _ := url.Parse(b.srv.URL)
	cookies := c.Jar.Cookies(u)
	snapshot := *c
	snapshot.Jar, _ = cookiejar.New(nil)
	snapshot.Jar.SetCookies(u, cookies)
	s := layoutSite{name: name, base: b.srv.URL, client: &snapshot, cookies: cookies}
	if len(host) > 0 {
		s.host = host[0]
	}
	return s
}

// chromeBase is the address Chrome opens: the box's address, or the name it is reached under.
func (s layoutSite) chromeBase() string {
	if s.host == "" {
		return s.base
	}
	u, _ := url.Parse(s.base)
	return u.Scheme + "://" + s.host + ":" + u.Port()
}

func (s layoutSite) get(t *testing.T, path string) (int, string) {
	t.Helper()
	req, _ := http.NewRequest(http.MethodGet, s.base+path, nil)
	if s.host != "" { // the Go client keeps cookies per Host header: carry them by hand
		req.Host = s.host
		for _, c := range s.cookies {
			req.AddCookie(c)
		}
	}
	res, err := s.client.Do(req)
	if err != nil {
		t.Fatalf("%s: GET %s: %v", s.name, path, err)
	}
	defer res.Body.Close()
	raw, _ := io.ReadAll(res.Body)
	return res.StatusCode, string(raw)
}

// A page is one URL to render, in a named site.
type layoutPage struct {
	site layoutSite
	path string
	// html is a page that only a form reaches (a POST answer): per language, the
	// markup the box sent. Chrome opens the box, then writes this into the page,
	// so the stylesheet and scripts load as they do for the person.
	html map[string]string
}

var (
	pageHrefRe = regexp.MustCompile(`href="(/[^"#]*)`)
	uuidRe     = regexp.MustCompile(`[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}`)
	isoDayRe   = regexp.MustCompile(`\d{4}-\d{2}-\d{2}`)
	// Links that are no framed pages: downloads, the API, assets, fragments.
	notPageRe = regexp.MustCompile(`^/(static/|api/|healthz|manifest|sw\.js|logout|display(/|$|\?)|ca/|update/|\.well-known|i/|personal-devices/join/|meals/recipes/[^/]+/photos/[^/]+)|\.(ics|crt|mobileconfig)(\?|$)`)
)

// shape reduces a path to the page it is: IDs and days become placeholders.
func pageShape(path string) string {
	p := path
	if i := strings.IndexAny(p, "?"); i >= 0 {
		p = p[:i]
	}
	p = isoDayRe.ReplaceAllString(uuidRe.ReplaceAllString(p, "{id}"), "{day}")
	if libraryNotArticle[p] {
		return p
	}
	return shippedArticleRe.ReplaceAllString(p, "$1/{article}")
}

// shippedArticleRe is the page of an article Hejmo ships: 248 pages of one kind, rendered once (ticket 164).
var shippedArticleRe = regexp.MustCompile(`^(/settings/lists/library)/(?:[a-z0-9]+-)*[a-z0-9]+$`)

// libraryNotArticle are the paths under the library that the shape above would take for an article.
var libraryNotArticle = map[string]bool{"/settings/lists/library/new": true, "/settings/lists/library/suggest": true}

// crawl follows every link of the signed-in parent from the start page and
// returns one path per kind of page. Pages that only a form reaches are added by hand.
func crawl(t *testing.T, s layoutSite, extra ...string) []string {
	t.Helper()
	seen := map[string]string{} // shape -> path
	queue := append([]string{"/"}, extra...)
	for len(queue) > 0 {
		path := queue[0]
		queue = queue[1:]
		if _, ok := seen[pageShape(path)]; ok {
			continue
		}
		status, body := s.get(t, path)
		if status != http.StatusOK {
			continue
		}
		seen[pageShape(path)] = path
		for _, m := range pageHrefRe.FindAllStringSubmatch(body, -1) {
			link := html.UnescapeString(m[1])
			if strings.HasPrefix(link, "//") || notPageRe.MatchString(link) {
				continue
			}
			if _, ok := seen[pageShape(link)]; !ok {
				queue = append(queue, link)
			}
		}
	}
	var out []string
	for _, p := range seen {
		out = append(out, p)
	}
	sort.Strings(out)
	return out
}

// htmlRoutes are the GET routes of server.go that answer a page for a
// browser, read from the source so a new page cannot be forgotten.
func htmlRoutes(t *testing.T) []string {
	t.Helper()
	files, _ := filepath.Glob("*.go")
	var out []string
	re := regexp.MustCompile(`"GET (/[^"]*)"`)
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		raw, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		for _, m := range re.FindAllStringSubmatch(string(raw), -1) {
			out = append(out, m[1])
		}
	}
	sort.Strings(out)
	return out
}

// notRendered are GET routes the check does not render, each with its reason.
var notRendered = map[string]string{
	"/healthz":                         "plain text",
	"/static/":                         "assets",
	"/api/v1/openapi.yaml":             "API document",
	"/ca/hejmo-ca.crt":                 "download",
	"/ca/hejmo.mobileconfig":           "download",
	"/manifest.webmanifest":            "JSON",
	"/sw.js":                           "script",
	"/display/state":                   "JSON",
	"/display/view":                    "fragment of /display, rendered inside it",
	"/display/lists":                   "redirect to the first list",
	"/display/lists/{id}/view":         "fragment of a list, rendered inside it",
	"/lists/{id}/suggest":              "fragment: the suggestions under the add card, rendered inside it",
	"/lists/{id}/quantity":             "fragment: the quantity part of the add card, rendered inside it",
	"/display/lists/{id}/suggest":      "fragment: the suggestions under the add card, rendered inside it",
	"/display/lists/{id}/quantity":     "fragment: the quantity part of the add card, rendered inside it",
	"/display/events":                  "event stream",
	"/display/person/{id}/view":        "fragment of the personal view, rendered inside it",
	"/otp":                             "redirect",
	"/i/{token}":                       "redirect",
	"/setup/https/check":               "answers a fragment for the setup page's script",
	"/places":                          "JSON list of places for the home location picker",
	"/personal-devices/join/{code}":    "one-time pairing link of a personal device",
	"/display/saver/photos":            "JSON",
	"/display/photo/{id}":              "image",
	"/numbers/{id}/photo":              "the photo of a contact on the children's page, an image",
	"/meals/recipes/{id}/photos/{pid}": "a photo of a recipe, an image (ticket 280)",

	"/articles/{id}/picture":                  "an image",
	"/tasks/rewards/{id}/picture":             "the photo of a reward, an image",
	"/tasks/rewards/requests/{id}/picture":    "the photo a reward had when it was asked for, an image",
	"/display/articles/{id}/picture":          "an image",
	"/display/rewards/{id}/picture":           "an image",
	"/display/contacts/{id}/photo":            "the photo of a contact of the emergency numbers, an image",
	"/settings/lists/library/picture/{token}": "an image",
	"/contacts/import/{id}/photo/{n}":         "the photo of a card of an uploaded contact file, an image",
	"/settings/lists/library/suggest":         "JSON for the live list under the name field",
	"/settings/lists/library/plan/{id}":       "fragment: the sentence under the card of an article, rendered inside it",
	"/settings/lists/always/plan":             "fragment: the sentence under the holiday, rendered inside its page",
	"/meals/recipes/suggest":                  "fragment: the suggestions under an ingredient line, rendered inside the recipe form",
	"/meals/suggest":                          "fragment: the suggestions under a meal field, rendered inside the week form (mealRecipePages)",
}

// matches reports whether a request path is served by a route pattern.
func matches(pattern, path string) bool {
	if i := strings.IndexAny(path, "?#"); i >= 0 { // a query, or the "#state" a page of a POST is recorded under
		path = path[:i]
	}
	re := regexp.QuoteMeta(pattern)
	re = strings.ReplaceAll(re, `\{id\}`, `[0-9a-f-]{36}`) // not a sibling route such as /connections/new
	re = regexp.MustCompile(`\\\{[a-z]+\\\}`).ReplaceAllString(re, `[^/]+`)
	re = strings.ReplaceAll(re, `\{\$\}`, ``)
	return regexp.MustCompile("^" + re + "$").MatchString(path)
}

// layoutPages returns every page to render: the parent's, the display's and the
// setup wizard's.
func layoutPages(t *testing.T, f *layoutFixture) []layoutPage {
	t.Helper()
	parent := siteOf("app", f.box, f.client)
	var pages []layoutPage
	add := func(s layoutSite, path string) {
		t.Helper()
		if status, _ := s.get(t, path); status != http.StatusOK {
			t.Fatalf("%s %s = %d, want 200", s.name, path, status)
		}
		pages = append(pages, layoutPage{site: s, path: path})
	}
	for _, p := range crawl(t, parent, "/calendar/search?q=Schwimm", "/calendar/new", "/tasks", "/settings/lists/library/walnuts", "/routines/new?person="+f.ids[1]+"&part=morning") {
		add(parent, p)
	}
	for _, p := range f.outlook {
		add(parent, p)
	}
	// The reward catalogue (#291): the crawl renders one page of each kind; the new reward form and the four
	// states of a reward's page (redeemed with the archive box, never redeemed with the delete box, archived by
	// hand, archived because its person left) are reached by hand, and the form with its errors by a POST.
	have := map[string]bool{}
	for _, pg := range pages {
		have[pg.path] = true
	}
	for _, p := range append([]string{"/tasks/rewards/new"}, prefixed("/tasks/rewards/", f.rewards)...) {
		if !have[p] {
			add(parent, p)
		}
	}
	pages = append(pages, rewardFormPages(t, f)...)
	// The cooking view (#286): the crawl reaches it once; the phone's two parts, a middle and the last of twelve
	// steps, other portions with ticks, a wrong number in the field, and a recipe with only a name.
	cook := "/meals/recipes/" + f.recipes[0] + "/cook"
	for _, q := range []string{"?view=steps&step=4", "?view=steps&step=12&have=0&have=2", "?portions=12&have=1&have=3&have=4", "?portions=0&step=2", "?view=steps"} {
		add(parent, cook+q)
	}
	add(parent, "/meals/recipes/"+f.recipes[1]+"/cook?view=steps")
	// Nutrition values (#288): the card with everything it can say, a child's switch, the section of an own article.
	for _, p := range []string{"/meals/recipes/" + f.nutriRecipe, "/people/" + f.ids[2], "/settings/lists/library/" + f.foodArticle} {
		if !have[p] {
			add(parent, p)
		}
	}
	// Own photos of recipes (#280): the recipe page with a cover and the section Fotos, the page Fotos (with a photo just
	// uploaded, and full), a recipe without photo, a child's view and a starter recipe with twelve photos.
	for _, p := range []string{"/meals/recipes/" + f.photoRecipe, "/meals/recipes/" + f.photoRecipe + "/photos", "/meals/recipes/" + f.photoRecipe + "/photos?new=x",
		"/meals/recipes/" + f.fullRecipe, "/meals/recipes/" + f.fullRecipe + "/photos", "/meals/recipes/" + f.recipes[1], "/meals/recipes", "/meals/recipes/starter"} {
		add(parent, p)
	}
	add(siteOf("child personal device", f.box, f.kid.client), "/meals/recipes/"+f.photoRecipe)
	add(siteOf("child personal device", f.box, f.kid.client), "/meals/recipes")
	// The badges of three children (#295): six of them two new, all ten, none. The crawl renders only the first
	// person's page, so the others are added by hand.
	for _, id := range []string{f.ids[3], f.ids[2], f.ids[4]} {
		if p := "/tasks/badges/" + id; !have[p] {
			add(parent, p)
		}
	}
	// The child's personal device shows lists as tiles by default (#166): the tiles of articles with a picture, a symbol and the warning of several people, and of free text.
	kid := siteOf("child personal device", f.box, f.kid.client)
	add(kid, "/tasks")
	add(kid, "/tasks/rewards")
	add(kid, "/tasks/badges") // the child with all ten badges (#295)
	add(kid, "/lists")
	add(kid, "/lists/"+f.lists[0])
	add(kid, "/lists/"+f.lists[1])
	add(kid, "/meals/recipes/"+f.recipes[0]+"/cook?step=3") // a child reads the cooking view too (#286)
	add(kid, "/numbers")                                    // the children's important numbers (ticket 301): buttons on a phone ...
	tablet := pairedPersonalDeviceAs(t, f.box, f.people["Anna-Lena"], "Tablet von Anna-Lena", uaIPad)
	add(siteOf("child tablet", f.box, tablet.client), "/numbers") // ... and the numbers as text on a tablet
	add(parent, "/contacts/for-children")
	add(parent, "/lists/"+f.lists[0]+"?qty="+f.longVariety) // the editor with the Sorte row and the long variety as its pill
	// The display: the family view and the personal views of two people.
	display := siteOf("display", f.box, f.display.client)
	add(display, "/display")
	add(display, "/display/person/"+f.ids[1])
	add(display, "/display/person/"+f.ids[3])
	add(display, "/display/person/"+f.ids[2]) // the badges at the foot of the days (#295): all ten, six of them (two new), none
	add(display, "/display/person/"+f.ids[4])
	add(display, "/display/lists/"+f.lists[0])
	add(display, "/display/lists/"+f.lists[1])
	add(display, "/display/saver")
	add(siteOf("display hint", f.box, f.hint.client), "/display/saver")

	// Offline mode (ticket 201): the pages as the box answers them while it is on.
	pages = append(pages, offlinePages(t, f)...)
	// Uploading an update file (ticket 221): the page, its errors, the confirmation and the installation.
	pages = append(pages, updateUploadPages(t, f)...)

	// Google (ticket 18): the guide, the sign-in in its variants, the failures that replace the steps,
	// Choose calendars with its access lines, and the notices to sign in again.
	pages = append(pages, googlePages(t, f)...)
	// The choice page of ticket 231: each way chosen, and the link guide with its errors.
	pages = append(pages, connectChoicePages(t, f)...)

	// The address book (ticket 296): a search without a match and the form with every kind of error.
	for _, p := range []string{"/contacts?q=Bäckereiverkäuferinnenvereinigung", "/contacts?g=" + f.contactGroups[0], "/contacts/" + f.ids[1], "/contacts/" + f.ids[1] + "/edit", "/people/" + f.ids[1],
		"/contacts/" + f.omaID, "/contacts/" + f.omaID + "/edit"} { // with and without a photo (ticket 297)
		add(parent, p)
	}
	pages = append(pages, contactPages(t, f)...)
	// Contacts from a vCard file (ticket 300): the upload page, its errors and the review in its states.
	add(parent, "/contacts/import")
	pages = append(pages, contactImportPages(t, f)...)

	// The meal plan with recipes (ticket 281): a week with a recipe for all eight, one for five and an adjusted one,
	// the child's reading view, the recipe page with the portions of the plan, and the states a form or a script reaches.
	pages = append(pages, mealRecipePages(t, f, kid)...)

	// The card of recovery codes (#176) is reached only by POST: rendered with
	// the box's answer, in the wizard, the invitation and My account, and in
	// the state after a continue without the tick.
	pages = append(pages, recoveryCardPages(t, f)...)

	// The settings page "Sichere Verbindung" (#216): the page itself is crawled
	// from the hub; the box's answer to the form (success line, next step) and
	// the form with a refused name are reached only by POST. A long router name
	// must wrap, not widen the page.
	pages = append(pages, secureConnectionPages(t, f)...)

	// Signed out: the login page and the trust page.
	f.logout()
	out := siteOf("signed out", f.box, f.client)
	add(out, "/login")
	add(out, "/ca")
	// A personal device whose device session was removed (ticket 199), on a box of its own.
	oldBox, oldPersonalDevice, _ := unpairedPersonalDevice(t)
	add(siteOf("unpaired device", oldBox, oldPersonalDevice.client), "/login")
	// The invitation of a parent: the welcome page and the form for username and password.
	add(out, "/invite/"+f.inviteToken)
	add(out, "/invite/"+f.inviteToken+"/account")
	return pages
}

// mealRecipePages returns the pages of ticket 281 that the crawl does not reach: the week of recipes for a
// parent and for a child, the recipe opened from the plan with many and with one portion, the week after a
// refused portions field, and the week with the suggestions open under a field (the fragment the script asks
// for, put into the page where the script puts it).
func mealRecipePages(t *testing.T, f *layoutFixture, kid layoutSite) []layoutPage {
	t.Helper()
	parent := siteOf("app", f.box, f.client)
	var pages []layoutPage
	for _, path := range []string{"/meals?week=2026-11-16", "/meals/recipes/" + f.recipeIDs[0] + "?portions=99&plan=2026-11-16/dinner", "/meals/recipes/" + f.recipeIDs[0] + "?portions=1&plan=2026-11-16/dinner",
		"/meals/recipes/" + f.recipeIDs[0] + "?portions=8"} {
		if status, _ := parent.get(t, path); status != http.StatusOK {
			t.Fatalf("%s = %d, want 200", path, status)
		}
		pages = append(pages, layoutPage{site: parent, path: path})
	}
	if status, _ := kid.get(t, "/meals?week=2026-11-16"); status != http.StatusOK {
		t.Fatalf("the child's meal plan = %d", status)
	}
	pages = append(pages, layoutPage{site: kid, path: "/meals?week=2026-11-16"},
		layoutPage{site: kid, path: "/meals/recipes/" + f.recipeIDs[0] + "?portions=8&plan=2026-11-16/dinner"})
	cell := mealField("2026-11-16", "dinner")
	bad, open := map[string]string{}, map[string]string{}
	for _, lang := range []string{"de", "en"} {
		header := map[string]string{"Accept-Language": lang}
		refused := f.do(http.MethodPost, "/meals", url.Values{"week": {"2026-11-16"}, cell + ".recipe": {f.recipeIDs[0]}, cell + ".portions": {"hundert und acht"},
			mealField("2026-11-17", "dinner"): {"Eine lange Beschreibung des Abendessens, das am Dienstag gekocht wird"}}, header)
		if refused.status != http.StatusUnprocessableEntity {
			t.Fatalf("refused portions = %d", refused.status)
		}
		bad[lang] = refused.body
		page := f.do(http.MethodGet, "/meals?week=2026-11-16", nil, header)
		field := mealField("2026-11-18", "snack")
		sugg := f.do(http.MethodGet, "/meals/suggest?q=sp%C3%A4tzle&cell="+field, nil, header)
		if sugg.status != http.StatusOK {
			t.Fatalf("suggestions = %d", sugg.status)
		}
		at := `<textarea id="` + field + `" name="` + field + `" rows="1" maxlength="200"></textarea>`
		if !strings.Contains(page.body, at) {
			t.Fatalf("no field %s in the week", field)
		}
		open[lang] = strings.Replace(page.body, at, at+sugg.body, 1)
	}
	return append(pages, layoutPage{site: parent, path: "/meals?week=2026-11-16#portions-error", html: bad}, layoutPage{site: parent, path: "/meals?week=2026-11-16#suggestions", html: open})
}

// connectChoicePages returns the choice page "Connect a calendar" (ticket 231)
// with each way chosen in turn (only the chosen guide shows), and the answer to
// a link that is none and a missing name: both errors at their fields, with a
// long link that must wrap.
func connectChoicePages(t *testing.T, f *layoutFixture) []layoutPage {
	t.Helper()
	parent := siteOf("app", f.box, f.client)
	var pages []layoutPage
	for _, way := range []string{"icloud", "caldav", "ics"} {
		path := "/connections/new?service=" + way
		if status, _ := parent.get(t, path); status != http.StatusOK {
			t.Fatalf("%s = %d, want 200", path, status)
		}
		pages = append(pages, layoutPage{site: parent, path: path})
	}
	out := map[string]string{}
	for _, lang := range []string{"de", "en"} {
		r := f.do(http.MethodPost, "/calendars", url.Values{
			"name": {""}, "url": {"grundschule-am-lindenpark-mit-einem-sehr-langen-namen.de/termine/kalender für die Klassen 2b und 4a"},
			"interval": {"60"}, "color": {"#4E7D52"}, "whole_family": {"1"},
		}, map[string]string{"Accept-Language": lang})
		if r.status != http.StatusUnprocessableEntity {
			t.Fatalf("subscribe with errors = %d", r.status)
		}
		out[lang] = r.body
	}
	return append(pages, layoutPage{site: parent, path: "/connections/new?state=link-errors", html: out})
}

// offlinePages returns the pages of ticket 201 in the states a crawl does not
// reach: with offline mode on (Home, the hub, the connections page with refused
// attempts, the calendar pages with their paused tags and notices, the
// confirmation to switch off) and with a public name (the change to the home
// network, and its error). The box is put back as it was.
func offlinePages(t *testing.T, f *layoutFixture) []layoutPage {
	t.Helper()
	parent := siteOf("app", f.box, f.client)
	var pages []layoutPage
	grab := func(name, path string) {
		t.Helper()
		out := map[string]string{}
		for _, lang := range []string{"de", "en"} {
			r := f.do(http.MethodGet, path, nil, map[string]string{"Accept-Language": lang})
			if r.status != http.StatusOK {
				t.Fatalf("%s %s = %d", name, path, r.status)
			}
			out[lang] = r.body
		}
		pages = append(pages, layoutPage{site: parent, path: path + "?state=" + name, html: out})
	}
	db := f.box.db
	if err := db.SetOffline(true, f.box.now); err != nil {
		t.Fatal(err)
	}
	led := db.Outbound()
	led.Refused(f.box.now, outbound.Calendars, "caldav.example.org")
	led.Refused(f.box.now.Add(time.Minute), outbound.Images, "www.example-supermarkt.de/bilder/hafer-drink-barista-edition-1-liter-in-der-besonders-langen-verpackung.jpg")
	hh, _ := db.HomeHousehold()
	conns, err := db.Connections(hh.ID)
	if err != nil || len(conns) == 0 {
		t.Fatalf("the fixture has no calendar connection: %v", err)
	}
	connID := conns[0].ID
	for _, p := range []struct{ name, path string }{
		{"offline-home", "/"}, {"offline-hub", "/settings"}, {"offline-connections", "/settings/connections"}, {"offline-calendars", "/calendars"},
		{"offline-connect", "/connections/new"}, {"offline-connection", "/connections/" + connID}, {"offline-switch-off", "/settings/connections/online"},
	} {
		grab(p.name, p.path)
	}
	if err := db.SetOffline(false, f.box.now); err != nil {
		t.Fatal(err)
	}
	if err := db.SetServiceEnabled(outbound.Calendars, false); err != nil {
		t.Fatal(err)
	}
	grab("calendars-off", "/connections/new")
	grab("connections-single", "/settings/connections")
	if err := db.SetServiceEnabled(outbound.Calendars, true); err != nil {
		t.Fatal(err)
	}
	f.box.public.name = "familie-maier-mit-einem-langen-namen.dedyn.io"
	if err := db.SavePublicHTTPS(store.PublicHTTPS{Name: f.box.public.name, Zone: "dedyn.io", Provider: "desec", Issuer: "letsencrypt", LANIP: "192.168.178.40", Token: []byte("x")}); err != nil {
		t.Fatal(err)
	}
	grab("public-name", "/settings/connections/offline")
	grab("public-connections", "/settings/connections")
	f.box.public.name = ""
	if err := db.DeletePublicHTTPS(); err != nil {
		t.Fatal(err)
	}
	return pages
}

// recoveryCardPages returns the card of recovery codes as the box answers it,
// in German and English: the card (200) and the card after a continue without
// the tick (422, the error at the checkbox), in the setup wizard, the
// invitation's last step and My account. The wizard and the invitation each get
// a box of their own, because their steps move on.
func recoveryCardPages(t *testing.T, f *layoutFixture) []layoutPage {
	t.Helper()
	var pages []layoutPage
	add := func(name string, s layoutSite, card, refused map[string]string) {
		pages = append(pages, layoutPage{site: s, path: "/account/recovery-codes#" + name, html: card}, layoutPage{site: s, path: "/account/recovery-codes#" + name + "-refused", html: refused})
	}
	// answer posts the continue without the tick for the codes on the card.
	answer := func(do func(method, path string, form url.Values) resp, flow, card string) string {
		t.Helper()
		codes := recoveryCodesOn(t, card)
		if len(codes) != 10 {
			t.Fatalf("%s: the card shows %d codes", flow, len(codes))
		}
		r := do(http.MethodPost, "/account/recovery-codes/saved", savedForm(flow, codes, false))
		if r.status != http.StatusUnprocessableEntity || !strings.Contains(r.body, `id="err-saved"`) {
			t.Fatalf("%s: continue without the tick = %d\n%s", flow, r.status, r.body)
		}
		return r.body
	}
	for _, lang := range []string{"de", "en"} {
		hdr := map[string]string{"Accept-Language": lang}
		// The wizard.
		b := newBox(t)
		b.do(http.MethodPost, "/setup", validSetupForm(), hdr)
		secret := secretOn(t, b.do(http.MethodGet, "/setup/totp", nil, hdr).body)
		code, _ := auth.TOTPCode(secret, b.now)
		card := b.do(http.MethodPost, "/setup/totp", url.Values{"code": {code}}, hdr)
		if card.status != http.StatusOK {
			t.Fatalf("wizard card = %d\n%s", card.status, card.body)
		}
		do := func(b *box) func(string, string, url.Values) resp {
			return func(method, path string, form url.Values) resp { return b.do(method, path, form, hdr) }
		}
		refused := answer(do(b), "wizard", card.body)
		add("wizard", siteOf("recovery card, wizard", b, b.client), map[string]string{lang: card.body}, map[string]string{lang: refused})

		// The invited parent's last step.
		e := newInviteBox(t)
		token := e.create()
		e.b.logout()
		e.b.do(http.MethodPost, "/invite/"+token+"/account", acceptForm("papa", "papas long password"), hdr)
		secret = secretOn(t, e.b.do(http.MethodGet, "/setup/totp", nil, hdr).body)
		code, _ = auth.TOTPCode(secret, e.b.now)
		card = e.b.do(http.MethodPost, "/setup/totp", url.Values{"code": {code}}, hdr)
		if card.status != http.StatusOK {
			t.Fatalf("invitation card = %d\n%s", card.status, card.body)
		}
		refused = answer(do(e.b), "invite", card.body)
		add("invite", siteOf("recovery card, invitation", e.b, e.b.client), map[string]string{lang: card.body}, map[string]string{lang: refused})

		// My account: the signed-in parent of the fixture asks for new codes.
		card = f.do(http.MethodPost, "/account/recovery-codes", url.Values{"fresh": {"1"}}, hdr)
		if card.status != http.StatusOK {
			t.Fatalf("My account card = %d\n%s", card.status, card.body)
		}
		refused = answer(do(f.box), "account", card.body)
		add("account", siteOf("recovery card, My account", f.box, f.client), map[string]string{lang: card.body}, map[string]string{lang: refused})
	}
	return mergeCardPages(pages)
}

const longRouterName = "das-wohnzimmer-der-familie-mueller.hejmo-box-mit-sehr-langem-namen.fritz.box"

// secureConnectionPages returns the answers of the page "Sichere Verbindung"
// that only a form reaches, in German and English: a refused name (422, the
// error at the field, the input kept) and the entered name (200, the success
// line and the next step). The fixture's signed-in parent posts the forms.
func secureConnectionPages(t *testing.T, f *layoutFixture) []layoutPage {
	t.Helper()
	var pages []layoutPage
	site := siteOf("secure connection", f.box, f.client)
	for _, lang := range []string{"de", "en"} {
		hdr := map[string]string{"Accept-Language": lang}
		refused := f.do(http.MethodPost, "/settings/https", url.Values{"router_name": {"hejmo box mit leerzeichen." + longRouterName}}, hdr)
		if refused.status != http.StatusUnprocessableEntity {
			t.Fatalf("secure connection, refused name = %d\n%s", refused.status, refused.body)
		}
		saved := f.do(http.MethodPost, "/settings/https", url.Values{"router_name": {lang + "-" + longRouterName}}, hdr)
		if saved.status != http.StatusOK {
			t.Fatalf("secure connection, entered name = %d\n%s", saved.status, saved.body)
		}
		pages = append(pages,
			layoutPage{site: site, path: "/settings/https#refused", html: map[string]string{lang: refused.body}},
			layoutPage{site: site, path: "/settings/https#entered", html: map[string]string{lang: saved.body}})
	}
	return mergeCardPages(pages)
}

// mergeCardPages joins the German and English markup of the same card into one page.
func mergeCardPages(in []layoutPage) []layoutPage {
	var out []layoutPage
	for _, p := range in {
		if i := slices.IndexFunc(out, func(o layoutPage) bool { return o.path == p.path }); i >= 0 {
			for lang, h := range p.html {
				out[i].html[lang] = h
			}
			continue
		}
		out = append(out, p)
	}
	return out
}

// wizardPages returns the setup wizard's pages. Each page gets a box in the
// state that serves it, because the wizard moves on: a fresh box without HTTPS,
// one with HTTPS but no household, one whose household has just been created
// (the code of the authenticator app), one whose sign-in is set up (passkeys).
func wizardPages(t *testing.T) []layoutPage {
	t.Helper()
	var pages []layoutPage
	add := func(name string, b *box, path string, host ...string) {
		t.Helper()
		s := siteOf(name, b, b.client, host...)
		if status, _ := s.get(t, path); status != http.StatusOK {
			t.Fatalf("%s %s = %d, want 200", name, path, status)
		}
		pages = append(pages, layoutPage{site: s, path: path})
	}
	fresh := newFreshBox(t)
	add("fresh box", fresh, "/setup/https")
	add("fresh box", fresh, "/setup/https?way=public")
	add("fresh box", fresh, "/setup/https?way=domain")
	add("fresh box", fresh, "/setup/offline") // the question of ticket 201
	add("fresh box", fresh, "/setup/https?way=own")
	// A router name found by the box, long enough to wrap on a phone.
	named := newFreshBox(t, withResolver(&fakeResolver{names: map[string][]string{"127.0.0.1": {longRouterName + "."}}}))
	add("fresh box with a router name", named, "/setup/https?way=own")

	add("https box", newBox(t), "/setup")

	household := func() *box {
		b := newBox(t)
		form := validSetupForm()
		form.Set("name", "Wilhelmine")
		if r := b.post("/setup", form); r.status != http.StatusSeeOther {
			t.Fatalf("POST /setup = %d", r.status)
		}
		return b
	}
	b := household()
	add("household box", b, "/setup/totp")

	// Reached under its name, the box offers a passkey right after the account (ADR 0018).
	b = newBox(t)
	form := validSetupForm()
	form.Set("name", "Wilhelmine")
	if r := b.at(boxName, http.MethodPost, "/setup", form); r.status != http.StatusSeeOther || r.location != "/setup/passkey" {
		t.Fatalf("POST /setup under the box name = %d %q", r.status, r.location)
	}
	add("household box under its name", b, "/setup/passkey", boxName)
	return pages
}

// Every GET route that answers a page is rendered by the check, or is listed
// in notRendered with its reason: a page added later cannot slip through.
func TestEveryPageIsInTheRenderedLayoutCheck(t *testing.T) {
	t.Parallel()
	f := newLayoutFixture(t)
	pages := append(layoutPages(t, f), wizardPages(t)...)
	for _, route := range htmlRoutes(t) {
		if _, skip := notRendered[route]; skip {
			continue
		}
		covered := false
		for _, p := range pages {
			if matches(route, p.path) {
				covered = true
				break
			}
		}
		if !covered {
			t.Errorf("GET %s is not rendered by TestRenderedLayout: make the fixture reach it, or list it in notRendered with the reason", route)
		}
	}
	for route := range notRendered {
		found := false
		for _, r := range htmlRoutes(t) {
			found = found || r == route
		}
		if !found {
			t.Errorf("notRendered lists %s, which is no GET route any more", route)
		}
		for _, p := range pages {
			if matches(route, p.path) {
				t.Errorf("notRendered lists %s, but %s %s renders it: remove the entry", route, p.site.name, p.path)
				break
			}
		}
	}
}

// The layout check can only find what its fixture shows: the child's personal device
// must lay out tiles of an article with a picture, one with a symbol, free
// text, and a tile that warns several people (#166).
func TestTheLayoutFixtureShowsEveryKindOfTile(t *testing.T) {
	t.Parallel()
	f := newLayoutFixture(t)
	page := f.kid.get("/lists/"+f.lists[0], "en").body
	for _, want := range []string{`<ul class="tiles"`, `<img src="/articles/`, `🐈`, `<span class="pic letter"`, `<span class="tile-warn">`} {
		if !strings.Contains(page, want) {
			t.Errorf("the child's tiles lack %q", want)
		}
	}
	if tile := tileOf(t, page, "Dinkelmehl"); !strings.Contains(tile, `<span class="variety"><span class="vh">Variety: </span>Dinkel-Vollkorn Type 1050 aus der Mühle Schäftlarn</span>`) {
		t.Errorf("the tile of an article with a long variety:\n%s", tile)
	}
	if toast := tileOf(t, page, "Toastbrot"); strings.Count(toast, `<span class="line">`) < 3 {
		t.Errorf("the toast does not warn three people:\n%s", toast)
	}
}

// startChrome starts headless Chrome and returns a context that drives it; the browser stops with the test.
func startChrome(t *testing.T, chrome string) context.Context {
	t.Helper()
	alloc, cancelAlloc := chromedp.NewExecAllocator(context.Background(), append(chromedp.DefaultExecAllocatorOptions[:],
		chromedp.ExecPath(chrome), chromedp.NoSandbox, chromedp.Flag("ignore-certificate-errors", true),
		chromedp.Flag("hide-scrollbars", true), chromedp.Flag("host-resolver-rules", "MAP "+boxName+" 127.0.0.1"), chromedp.Flag("force-device-scale-factor", "1"))...)
	t.Cleanup(cancelAlloc)
	ctx, cancel := chromedp.NewContext(alloc)
	t.Cleanup(cancel)
	if _, err := chromedp.Call(ctx, network.ClearBrowserCookies, cdp.Empty{}); err != nil {
		t.Fatalf("start Chrome %s: %v", chrome, err)
	}
	return ctx
}

// A viewport is a size the pages are rendered at. Heights follow the devices:
// phone, tablet in portrait, E-Ink display in landscape, desktop browsers (and the tablet and display
// sizes serve both orientations of the display views).
type viewport struct{ w, h int }

var layoutViewports = []viewport{{375, 812}, {768, 1024}, {1024, 768}, {1440, 900}, {1900, 1000}}

// layoutProblem is one finding of the probe.
type layoutProblem struct {
	Kind   string `json:"kind"`
	El     string `json:"el"`
	Detail string `json:"detail"`
}

// knownLayoutProblem is a finding the check made on main when it came in (path
// as pageShape writes it). Each
// has an issue; the entry goes when the issue is fixed (the check fails while
// an entry no longer occurs, so the list cannot go stale).
type knownLayoutProblem struct {
	path, kind string
	width      int
	mentions   []string // the finding must name one of these
	issue      int
	platform   bool // Chrome's hyphenation decides: it may not occur in every Chrome, so it need not occur
}

var knownLayoutProblems = []knownLayoutProblem{}

func (k knownLayoutProblem) covers(path string, width int, p layoutProblem) bool {
	if k.path != pageShape(path) || k.kind != p.Kind || k.width != width {
		return false
	}
	for _, m := range k.mentions {
		if strings.Contains(p.El+" "+p.Detail, m) {
			return true
		}
	}
	return false
}

// TestRenderedLayout renders every page at phone, tablet, display and desktop
// widths (the navigation stays beside the content from 1024 px up), in light
// and black-and-white, in German and English, and fails with the page, width,
// theme and element where text overflows or a name breaks.
func TestRenderedLayout(t *testing.T) {
	t.Parallel()
	chrome := requireChrome(t)
	f := newLayoutFixture(t)
	pages := append(layoutPages(t, f), wizardPages(t)...)

	shots := os.Getenv("HEJMO_LAYOUT_SHOTS")
	if shots == "" {
		shots = t.TempDir()
	} else if err := os.MkdirAll(shots, 0o755); err != nil {
		t.Fatal(err)
	}

	ctx := startChrome(t, chrome)
	// The timeout comes after the first call: a timeout on that call would stop the browser.
	ctx, cancelTimeout := context.WithTimeout(ctx, 15*time.Minute)
	defer cancelTimeout()

	names, _ := json.Marshal(layoutNames)
	seen := map[int]bool{} // the known problems that occurred
	failing := false
	for _, page := range pages {
		for _, lang := range []string{"de", "en"} {
			for _, vp := range layoutViewports {
				name := fmt.Sprintf("%s%s/%s/%dx%d", page.site.name, page.path, lang, vp.w, vp.h)
				t.Run(name, func(t *testing.T) {
					baseURL, _ := url.Parse(page.site.chromeBase())
					cookies := append([]*http.Cookie{{Name: langCookieForTest, Value: lang}}, page.site.cookies...)
					err := func() error {
						if _, err := chromedp.Call(ctx, network.ClearBrowserCookies, cdp.Empty{}); err != nil {
							return err
						}
						for _, c := range cookies {
							if _, err := chromedp.Call(ctx, network.SetCookie, network.SetCookieParams{Name: c.Name, Value: c.Value, URL: baseURL.String()}); err != nil {
								return err
							}
						}
						// Displays and personal devices follow the browser's language, the web pages the cookie.
						if _, err := chromedp.Call(ctx, network.SetExtraHTTPHeaders, network.SetExtraHTTPHeadersParams{Headers: network.Headers{"Accept-Language": lang}}); err != nil {
							return err
						}
						if _, err := chromedp.Call(ctx, emulation.SetDeviceMetricsOverride, emulation.SetDeviceMetricsOverrideParams{Width: int64(vp.w), Height: int64(vp.h), DeviceScaleFactor: 1}); err != nil {
							return err
						}
						if page.html == nil {
							return chromedp.Do(ctx, chromedp.Navigate(page.site.chromeBase()+page.path))
						}
						// A page only a form reaches: open the box, then write the answer into the document.
						if err := chromedp.Do(ctx, chromedp.Navigate(page.site.chromeBase()+"/healthz")); err != nil {
							return err
						}
						markup, _ := json.Marshal(page.html[lang])
						_, err := chromedp.Run(ctx, chromedp.Evaluate[string](fmt.Sprintf(`(async () => {
							await new Promise(r => setTimeout(r, 0));
							document.open(); document.write(%s); document.close();
							while (document.readyState !== "complete") await new Promise(r => setTimeout(r, 20));
							return "ok";
						})()`, markup), chromedp.EvalAwaitPromise))
						return err
					}()
					if err != nil {
						t.Fatalf("open: %v", err)
					}
					if got, err := chromedp.Run(ctx, chromedp.Evaluate[string](`document.documentElement.lang`)); err != nil || got != lang {
						t.Fatalf("%s: the page is in %q (%v), not in %q: the check would measure the wrong language", page.path, got, err, lang)
					}
					for _, theme := range []string{"light", "bw"} {
						script := fmt.Sprintf(`(async () => {
							document.documentElement.setAttribute("data-theme", %q);
							%s
							return JSON.stringify(await probe(%d, %s));
						})()`, theme, layoutProbeJS, vp.w, names)
						got, err := chromedp.Run(ctx, chromedp.Evaluate[string](script, chromedp.EvalAwaitPromise))
						if err != nil {
							t.Fatalf("measure: %v", err)
						}
						// display.js sets data-theme to the server's on a state update (every 30 s);
						// had one come in during the measurement, it measured the wrong theme.
						if now, err := chromedp.Run(ctx, chromedp.Evaluate[string](`document.documentElement.getAttribute("data-theme")`)); err != nil || now != theme {
							t.Fatalf("the page changed its theme to %q (%v) while it was measured as %q", now, err, theme)
						}
						var probs []layoutProblem
						if err := json.Unmarshal([]byte(got), &probs); err != nil {
							t.Fatalf("probe result %q: %v", got, err)
						}
						for _, p := range probs {
							if i := slices.IndexFunc(knownLayoutProblems, func(k knownLayoutProblem) bool { return k.covers(page.path, vp.w, p) }); i >= 0 {
								seen[i] = true
								t.Logf("known problem (#%d): %s %s, width %d, theme %s, language %s: %s: %s (%s)", knownLayoutProblems[i].issue, page.site.name, page.path, vp.w, theme, lang, p.Kind, p.El, p.Detail)
								continue
							}
							failing = true
							t.Errorf("%s %s, width %d, theme %s, language %s: %s: %s (%s)", page.site.name, page.path, vp.w, theme, lang, p.Kind, p.El, p.Detail)
						}
						if failing {
							failing = false
							if png, err := chromedp.Run(ctx, chromedp.FullScreenshot(90)); err == nil {
								file := filepath.Join(shots, strings.NewReplacer("/", "_", "?", "_", "=", "_", "&", "_").Replace(name)+"-"+theme+".png")
								if err := os.WriteFile(file, png, 0o644); err == nil {
									t.Logf("screenshot: %s", file)
								}
							}
						}
					}
				})
			}
		}
	}
	// A known problem that no longer occurs is fixed: its entry goes. Not checked
	// when a subtest was picked with -run, which sees only some of the pages.
	if run := flag.Lookup("test.run"); run == nil || !strings.Contains(run.Value.String(), "/") {
		for i, k := range knownLayoutProblems {
			if !seen[i] && !k.platform {
				t.Errorf("known layout problem #%d (%s, %s, width %d) did not occur: remove it from knownLayoutProblems", k.issue, k.path, k.kind, k.width)
			}
		}
	}
}

// langCookieForTest is the language cookie of the box (language.go).
const langCookieForTest = "hejmo_lang"

// The probe itself is tested on small pages: each case is a page body, the
// width it is rendered at, and the kinds of problem the probe must report.
func TestLayoutProbeFindsWhatItShould(t *testing.T) {
	t.Parallel()
	ctx := startChrome(t, requireChrome(t))
	const clip = `style="width:100px;overflow:hidden"`
	for _, c := range []struct {
		name, body string
		want       []string
		width      int // 375 when 0
	}{
		{"text of the element itself, clipped", `<div ` + clip + `>Kinderarztvorsorgeuntersuchung</div>`, []string{"text-overflows-its-box"}, 0},
		{"text in an inline child, clipped", `<div ` + clip + `><a>Kinderarztvorsorgeuntersuchung</a></div>`, []string{"text-overflows-its-box"}, 0},
		{"text in nested inline children, clipped", `<div ` + clip + `><a><b>Kinderarztvorsorgeuntersuchung</b></a> <span>x</span></div>`, []string{"text-overflows-its-box"}, 0},
		{"text in a grid item, clipped (the calendar's event card)", `<div style="display:grid;width:100px;overflow:hidden"><a>Kinderarztvorsorgeuntersuchung</a> <span>14:00</span></div>`, []string{"text-overflows-its-box"}, 0},
		{"text in a block child, clipped", `<div ` + clip + `><p>Kinderarztvorsorgeuntersuchung</p></div>`, []string{"text-overflows-its-box"}, 0},
		{"navigation stacked above the content at desktop width", `<div class="frame has-nav" style="display:block"><nav class="frame-nav">Menu</nav><main>Inhalt</main></div>`, []string{"navigation-not-beside-content"}, 1440},
		{"navigation beside the content at desktop width", `<div class="frame has-nav" style="display:grid;grid-template-columns:100px 1fr"><nav class="frame-nav">Menu</nav><main>Inhalt</main></div>`, nil, 1440},
		{"navigation stacked above the content at 1024, the threshold", `<div class="frame has-nav" style="display:block"><nav class="frame-nav">Menu</nav><main>Inhalt</main></div>`, []string{"navigation-not-beside-content"}, 1024},
		{"navigation stacked above the content at 768, below the threshold", `<div class="frame has-nav" style="display:block"><nav class="frame-nav">Menu</nav><main>Inhalt</main></div>`, nil, 768},
		{"text that fits", `<div ` + clip + `><a>kurz</a></div>`, nil, 0},
		{"text that wraps", `<div ` + clip + `><a>ein Text mit vielen kurzen Wörtern</a></div>`, nil, 0},
		{"an empty box", `<div ` + clip + `><a></a></div>`, nil, 0},
	} {
		t.Run(c.name, func(t *testing.T) {
			if c.width == 0 {
				c.width = 375
			}
			script := fmt.Sprintf(`(async () => {
				document.body.innerHTML = %q;
				%s
				return JSON.stringify(await probe(%d, []));
			})()`, c.body, layoutProbeJS, c.width)
			if _, err := chromedp.Call(ctx, emulation.SetDeviceMetricsOverride, emulation.SetDeviceMetricsOverrideParams{Width: int64(c.width), Height: 812, DeviceScaleFactor: 1}); err != nil {
				t.Fatal(err)
			}
			if err := chromedp.Do(ctx, chromedp.Navigate("about:blank")); err != nil {
				t.Fatal(err)
			}
			got, err := chromedp.Run(ctx, chromedp.Evaluate[string](script, chromedp.EvalAwaitPromise))
			if err != nil {
				t.Fatal(err)
			}
			var probs []layoutProblem
			if err := json.Unmarshal([]byte(got), &probs); err != nil {
				t.Fatal(err)
			}
			var kinds []string
			for _, p := range probs {
				kinds = append(kinds, p.Kind)
			}
			if !slices.Equal(kinds, c.want) {
				t.Errorf("problems = %v, want %v\n%s", kinds, c.want, got)
			}
		})
	}
}

func TestTheFormAndAShippedArticleAreDifferentPages(t *testing.T) {
	t.Parallel()
	form, article := pageShape("/settings/lists/library/new"), pageShape("/settings/lists/library/walnuts")
	if form == article {
		t.Errorf("the form and a shipped article share the shape %q, so the crawl renders only one of them", form)
	}
	if pageShape("/settings/lists/library/suggest") == article {
		t.Error("suggest is no article")
	}
	if pageShape("/settings/lists/library/apples") != article {
		t.Error("two shipped articles are two pages of one kind")
	}
}

// A name wider than the line breaks inside the word at the line's edge, in
// every heading, whatever the heading's layout (#175): headings that are grid
// items (the wizard's) or flex containers (the personal view's, the child's
// Home) need a shrinkable box for the name, or the name overflows instead.
// The check opens each heading at 300 px with a one-word name of 30 letters.
func TestLongSingleWordNameStaysInsideHeadings(t *testing.T) {
	t.Parallel()
	chrome := requireChrome(t)
	const long = "Hubertusmariaaloisiusferdinand"
	f := newFamilyBox(t, "eink_color")
	h, db := f.householdID, f.db
	for _, old := range []string{"Mia", "Papa", "Mama"} {
		p := f.people[old]
		if err := db.UpdatePerson(h, p.ID, long, p.Color, p.Shape); err != nil {
			t.Fatal(err)
		}
	}
	mia, papa := f.people["Mia"], f.people["Papa"]
	mia.Name = long
	display := siteOf("display", f.box, f.display.client)
	personalDevice := siteOf("child's personal device", f.box, pairedPersonalDevice(t, f.box, mia, "Handy").client)
	inv := f.do(http.MethodPost, "/people/"+papa.ID+"/invitation", url.Values{}, nil)
	token := inviteTokenRe.FindString(inv.body)
	if inv.status != http.StatusOK || token == "" {
		t.Fatalf("invitation = %d, no token\n%s", inv.status, inv.body)
	}
	// An own article of the library: its name may be 60 characters without a space.
	const longArticle = "Haselnussmandelknusperflockenmitschokostueckchenundrosinen12"
	art, err := db.CreateArticle(h, papa.ID, store.OwnArticle{Name: longArticle, Dept: "pantry", Kind: "veg", Unit1: "pack"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	parent := siteOf("parent", f.box, f.client)
	f.logout()
	out := siteOf("signed out", f.box, f.client)
	type headingCase struct {
		site layoutSite
		path string
		h1   string // the heading that holds the name
		name string // the name it holds
	}
	cases := []headingCase{
		{display, "/display/person/" + mia.ID, ".pv-header h1", long},
		{personalDevice, "/", "h1.child-h", long},
		{out, "/invite/" + token, ".wizard h1", long},
		{parent, "/settings/lists/library/" + art.ID, "h1.art-title", longArticle},
	}
	for _, c := range cases {
		if status, body := c.site.get(t, c.path); status != http.StatusOK || !strings.Contains(body, c.name) {
			t.Fatalf("%s %s = %d, want a page naming %s", c.site.name, c.path, status, c.name)
		}
	}

	ctx := startChrome(t, chrome)
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	for _, c := range cases {
		t.Run(c.site.name+c.path, func(t *testing.T) {
			baseURL, _ := url.Parse(c.site.chromeBase())
			if _, err := chromedp.Call(ctx, network.ClearBrowserCookies, cdp.Empty{}); err != nil {
				t.Fatal(err)
			}
			for _, ck := range append([]*http.Cookie{{Name: langCookieForTest, Value: "en"}}, c.site.cookies...) {
				if _, err := chromedp.Call(ctx, network.SetCookie, network.SetCookieParams{Name: ck.Name, Value: ck.Value, URL: baseURL.String()}); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := chromedp.Call(ctx, emulation.SetDeviceMetricsOverride, emulation.SetDeviceMetricsOverrideParams{Width: 300, Height: 812, DeviceScaleFactor: 1}); err != nil {
				t.Fatal(err)
			}
			if err := chromedp.Do(ctx, chromedp.Navigate(c.site.chromeBase()+c.path)); err != nil {
				t.Fatal(err)
			}
			got, err := chromedp.Run(ctx, chromedp.Evaluate[string](fmt.Sprintf(`(() => {
				const h = document.querySelector(%q);
				if (!h || !h.textContent.includes(%q)) return "no heading with the name";
				const r = h.getBoundingClientRect();
				return JSON.stringify({scroll: h.scrollWidth, box: h.clientWidth, right: Math.round(r.right), page: document.documentElement.scrollWidth});
			})()`, c.h1, c.name)))
			if err != nil {
				t.Fatal(err)
			}
			var m struct{ Scroll, Box, Right, Page int }
			if err := json.Unmarshal([]byte(got), &m); err != nil {
				t.Fatalf("measure %q: %v", got, err)
			}
			if m.Scroll > m.Box || m.Right > 300 || m.Page > 300 {
				t.Errorf("%s at 300 px: the name overflows its heading (heading scrollWidth %d > clientWidth %d, right edge %d, page %d wide)", c.h1, m.Scroll, m.Box, m.Right, m.Page)
			}
		})
	}
}

func prefixed(prefix string, ids []string) []string {
	var out []string
	for _, id := range ids {
		out = append(out, prefix+id)
	}
	return out
}

// addRewards makes the catalogue of the layout fixture (#291): long names, a limit, a photo-less and a
// symbol reward, one meant for people, and the four states of a reward's page.
func (f *layoutFixture) addRewards(t *testing.T) []string {
	t.Helper()
	db, h := f.db, f.householdID
	mk := func(r store.Reward) string {
		t.Helper()
		got, err := db.CreateReward(h, f.ids[0], r, nil)
		if err != nil {
			t.Fatal(err)
		}
		return got.ID
	}
	redeem := func(id, person string) {
		t.Helper()
		if _, err := db.SQL().Exec(`INSERT INTO reward_redemptions (id, household_id, reward_id, person_id, status, price, requested_at, decided_at) SELECT lower(hex(randomblob(8))), household_id, id, ?, 'confirmed', 5, ?, ? FROM rewards WHERE id = ?`, person, "2026-11-12T10:00:00Z", "2026-11-12T10:00:00Z", id); err != nil {
			t.Fatal(err)
		}
	}
	anna := f.people["Anna-Lena"].ID
	if _, err := db.BookStars(h, anna, 12, "layout", f.ids[0], f.now); err != nil {
		t.Fatal(err)
	}
	redeemed := mk(store.Reward{Name: "Am Wochenende länger aufbleiben und den Film aussuchen", Symbol: "🌙", Price: 15, Limit: reward.Limit{Period: reward.Week, Count: 1}, PersonIDs: f.ids[1:7]})
	for _, who := range f.ids[1:4] {
		redeem(redeemed, who)
	}
	redeem(redeemed, anna)
	never := mk(store.Reward{Name: "Eis aus der Eisdiele", Symbol: "🍦", Price: 8, Limit: reward.Limit{Period: reward.Day, Count: 2}})
	mk(store.Reward{Name: "Spielenachmittag mit Freundinnen und Freunden daheim", Symbol: "🎲", Price: 5})
	mk(store.Reward{Name: "Kinoabend", Symbol: "🎬", Price: 30, Limit: reward.Limit{Period: reward.Month, Count: 1}, PersonIDs: []string{anna}})
	byHand := mk(store.Reward{Name: "Fahrradtour mit Papa", Symbol: "🚲", Price: 25})
	redeem(byHand, anna)
	if _, err := db.ArchiveReward(h, byHand, f.now); err != nil {
		t.Fatal(err)
	}
	gone := mk(store.Reward{Name: "Neues Kuscheltier", Symbol: "🧸", Price: 40, PersonIDs: []string{f.ids[7]}})
	if _, err := db.SQL().Exec(`UPDATE rewards SET archived_at = '2026-11-02T10:00:00Z', archived_reason = 'audience_gone', archived_person = 'Leopoldine' WHERE id = ?`, gone); err != nil {
		t.Fatal(err)
	}
	if _, err := db.SQL().Exec(`DELETE FROM reward_people WHERE reward_id = ?`, gone); err != nil {
		t.Fatal(err)
	}
	return []string{redeemed, never, byHand, gone}
}

// addBadges gives three children their badges (#295): Konstantin six, two of them new (today and yesterday),
// Anna-Lena all ten (the child of the personal device), Wilhelmine none. The store awards by history only, so
// the rows are written as if earned at these moments.
func (f *layoutFixture) addBadges(t *testing.T) {
	t.Helper()
	give := func(who string, keys []badge.Key, days []int) {
		t.Helper()
		// What the fixture's own history awarded on the way is replaced by the dates below.
		if _, err := f.db.SQL().Exec(`DELETE FROM badges WHERE person_id = ?`, f.people[who].ID); err != nil {
			t.Fatal(err)
		}
		for i, k := range keys {
			at := f.at(days[i], 9, 30).UTC().Format(time.RFC3339)
			if _, err := f.db.SQL().Exec(`INSERT OR IGNORE INTO badges (household_id, person_id, key, earned_at, quiet) VALUES (?, ?, ?, ?, 0)`, f.householdID, f.people[who].ID, string(k), at); err != nil {
				t.Fatal(err)
			}
		}
	}
	give("Konstantin", []badge.Key{badge.FirstStar, badge.FirstRoutine, badge.WholeDay, badge.Streak7, badge.Stars100, badge.FirstReward}, []int{2, 2, 5, 9, 13, 14})
	give("Anna-Lena", badge.Catalogue, []int{1, 1, 3, 6, 8, 1, 9, 12, 7, 14})
	give("Wilhelmine", nil, nil)
}

// addRequests makes the open requests of the layout fixture (#292): every child asks for two rewards, so
// Start and the rewards page carry many rows with long names, and Anna-Lena's own page holds an open request,
// a yes and a no with a note.
func (f *layoutFixture) addRequests(t *testing.T) {
	t.Helper()
	db, h := f.db, f.householdID
	rewards, err := db.Rewards(h)
	if err != nil {
		t.Fatal(err)
	}
	byName := map[string]string{}
	for _, r := range rewards {
		byName[r.Name] = r.ID
	}
	spiel, eis, kino := byName["Spielenachmittag mit Freundinnen und Freunden daheim"], byName["Eis aus der Eisdiele"], byName["Kinoabend"]
	anna := f.people["Anna-Lena"].ID
	ask := func(child, rw string, at time.Time) string {
		t.Helper()
		d, err := db.AskReward(h, child, rw, at, time.UTC)
		if err != nil {
			t.Fatal(err)
		}
		return d.ID
	}
	for i, id := range f.ids {
		p := f.people[layoutNames[i]]
		if p.Role != store.RoleChild || id == anna {
			continue
		}
		if _, err := db.BookStars(h, id, 40, "layout", f.ids[0], f.now.Add(-48*time.Hour)); err != nil {
			t.Fatal(err)
		}
		ask(id, spiel, f.now.Add(-26*time.Hour))
		ask(id, eis, f.now.Add(-3*time.Hour))
	}
	if _, err := db.BookStars(h, anna, 40, "layout", f.ids[0], f.now.Add(-48*time.Hour)); err != nil {
		t.Fatal(err)
	}
	yes := ask(anna, spiel, f.now.Add(-30*time.Hour))
	if _, err := db.ConfirmRedemption(h, yes, f.ids[0], f.now.Add(-29*time.Hour), time.UTC); err != nil {
		t.Fatal(err)
	}
	no := ask(anna, kino, f.now.Add(-28*time.Hour))
	if _, err := db.DeclineRedemption(h, no, f.ids[1], "Erst am Wochenende, heute ist Training und danach gibt es Abendessen bei Oma", f.now.Add(-27*time.Hour)); err != nil {
		t.Fatal(err)
	}
	ask(anna, eis, f.now.Add(-2*time.Hour))
}

// rewardFormPages returns the reward form as the box answers a form with an error at every field.
func rewardFormPages(t *testing.T, f *layoutFixture) []layoutPage {
	t.Helper()
	var pages []layoutPage
	site := siteOf("reward form", f.box, f.client)
	for _, lang := range []string{"de", "en"} {
		bad := f.do(http.MethodPost, "/tasks/rewards", url.Values{"name": {""}, "price": {"0"}, "pic_source": {"symbol"}, "symbol": {"🎁"}, "limit_period": {"w"}, "limit_count": {"0"}}, map[string]string{"Accept-Language": lang})
		if bad.status != http.StatusUnprocessableEntity {
			t.Fatalf("reward form with errors = %d\n%s", bad.status, bad.body)
		}
		pages = append(pages, layoutPage{site: site, path: "/tasks/rewards/new#errors", html: map[string]string{lang: bad.body}})
	}
	return mergeCardPages(pages)
}

// addContacts fills the address book with long texts: names that must wrap, numbers with own labels, long
// e-mail addresses and a note of many lines.
func (f *layoutFixture) addContacts(t *testing.T) {
	t.Helper()
	h, db, by := f.householdID, f.db, f.ids[0]
	groups, err := db.ContactGroups(h)
	if err != nil {
		t.Fatal(err)
	}
	for _, g := range groups {
		f.contactGroups = append(f.contactGroups, g.ID)
	}
	long := strings.Repeat("Sehr lange Notiz mit vielen Wörtern und Zeilenumbrüchen. ", 12)
	for i, in := range []store.ContactInput{
		{HejmoName: "Oma Gerda im Gartenfreundeverein", PhotoChange: store.SetPicture, Photo: &store.ContactPhoto{JPEG: testJPEG(t, 512, 512)}, FullName: "Gerda Müller-Schulze-Hohenstein-Wöllenstein", Street: "Am Erlenbach zehn, Haus 7, Hinterhof", Postcode: "83022", City: "Rosenheim", Country: "Deutschland",
			BdayDay: 14, BdayMonth: 3, Note: long, GroupIDs: []string{f.contactGroups[0], f.contactGroups[3]},
			Numbers: []contact.Line{{Kind: contact.Cell, Value: "+49 (0) 171 3456 789 12 34"}, {Kind: contact.Home, Value: "08031 87 65 43"}, {Kind: contact.Custom, Label: "Gartenhaus im Urlaub", Value: "0160 111 2223"}, {Kind: contact.Work, Value: "089 4455 6677-12"}},
			Emails:  []contact.Line{{Kind: contact.Home, Value: "gerda.mueller-schulze@gartenfreunde-am-erlenbach-rosenheim.example"}, {Kind: contact.Custom, Label: "Gartenfreundeverein", Value: "gerda@example.de"}}},
		{HejmoName: "Kinderarztpraxis Yılmaz-Hoffmann", ForChildren: true, FullName: "Kinderarztpraxis Dr. Elif Yılmaz-Hoffmann und Kollegen", Numbers: []contact.Line{{Kind: contact.Work, Value: "089 1234 5670"}}, BdayDay: 2, BdayMonth: 9, BdayYear: 1950, GroupIDs: []string{f.contactGroups[2]}},
		{HejmoName: "Kinderärztin Dr. Okonkwo-Brandt", GroupIDs: []string{f.contactGroups[3]}, Numbers: []contact.Line{{Kind: contact.Custom, Label: "Notfallsprechstunde Wochenende", Value: "+49 89 1234 5678 9012"}, {Kind: contact.Work, Value: "089 1234 5670"}}},
		// On the children's page (ticket 301): a long name, a photo, a long own kind and the longest numbers that fit a button at 375 px.
		{HejmoName: "Oma Käthe und Opa Heinrich am Waldrand", ForChildren: true, PhotoChange: store.SetPicture, Photo: &store.ContactPhoto{JPEG: testJPEG(t, 512, 512)}, GroupIDs: []string{f.contactGroups[0]},
			Numbers: []contact.Line{{Kind: contact.Cell, Value: "+49 (0) 171 3456 789"}, {Kind: contact.Custom, Label: "Gartenhaus im Urlaub am See", Value: "08031 87 65 43"}}},
		{HejmoName: "Ärztlicher Bereitschaftsdienst", ForChildren: true, Numbers: []contact.Line{{Kind: contact.Other, Value: "116 117"}}, GroupIDs: []string{f.contactGroups[2], f.contactGroups[3]}, NewGroup: "Fußball-Eltern der U11 am Erlenweg"},
	} {
		c, err := db.CreateContact(h, by, in)
		if err != nil {
			t.Fatal(err)
		}
		if i == 0 {
			f.omaID = c.ID
		}
	}
	// The people have contact data too.
	for i, id := range f.ids[:3] {
		if _, err := db.UpdateContact(h, id, by, store.ContactInput{ForChildren: true, FullName: layoutNames[i] + " Maier-Schmidt-Wöllenstein", Numbers: []contact.Line{{Kind: contact.Cell, Value: "0176 2345 6789"}}, BdayDay: 21, BdayMonth: 6, BdayYear: 2018}); err != nil {
			t.Fatal(err)
		}
	}
}

// contactPages are the contact form after a refused save (every error at its field, in both languages), and the
// form for a person, which only a link of the contact page reaches in one shape.
func contactPages(t *testing.T, f *layoutFixture) []layoutPage {
	t.Helper()
	var pages []layoutPage
	pages = append(pages, contactPhotoPages(t, f)...)
	site := siteOf("contact form", f.box, f.client)
	form := url.Values{"name": {layoutNames[1]}, "full_name": {"Maximilian Schulz"}, "bday_day": {"31"}, "bday_month": {"2"}, "bday_year": {"2018"}, "new_group": {"Familie"},
		"note": {strings.Repeat("n", 1001)}}
	for i, row := range [][3]string{{"cell", "", "0172 4455 6677"}, {"home", "", "089 23x4 5678"}, {"own", "", "0160 2223 334"}} {
		_ = i
		form.Add("number_label", row[0])
		form.Add("number_own", row[1])
		form.Add("number", row[2])
	}
	form.Add("email_label", "home")
	form.Add("email_own", "")
	form.Add("email", "familie.schulz-hohenstein-wöllenstein@example")
	for _, lang := range []string{"de", "en"} {
		refused := f.do(http.MethodPost, "/contacts/new", form, map[string]string{"Accept-Language": lang})
		if refused.status != http.StatusUnprocessableEntity {
			t.Fatalf("contact form with errors = %d\n%s", refused.status, refused.body)
		}
		pages = append(pages, layoutPage{site: site, path: "/contacts/new#refused", html: map[string]string{lang: refused.body}})
	}
	return mergeCardPages(pages)
}

// contactImportPages are the pages of the contact import that an upload or a confirm reaches (ticket 300): the
// upload refused, the review with duplicates of the fixture's contacts and long texts, the review with a
// missing choice, a file with nothing new, a single card and an expired review, in both languages.
func contactImportPages(t *testing.T, f *layoutFixture) []layoutPage {
	t.Helper()
	var pages []layoutPage
	site := siteOf("contact import", f.box, f.client)
	by := f.ids[0]
	pic := base64.StdEncoding.EncodeToString(testJPEG(t, 640, 480))
	file := func(cards ...string) []byte {
		return []byte(strings.ReplaceAll(strings.Join(cards, ""), "\n", "\r\n"))
	}
	long := "Sehr-langer-Nachname-der-Familie-Müller-Schulze-Hohenstein-Wöllenstein"
	cards := file(
		"BEGIN:VCARD\nVERSION:3.0\nFN:Gerda "+long+"\nTEL;TYPE=CELL:+49 171 3456789\nTEL;TYPE=WORK:08031 40 12 77\nEMAIL:gerda.mueller-schulze@gartenfreunde-am-erlenbach-rosenheim.example\nPHOTO;ENCODING=b;TYPE=JPEG:"+pic+"\nEND:VCARD\n",
		"BEGIN:VCARD\nVERSION:3.0\nFN:Kinderarztpraxis Dr. Elif Yılmaz-Hoffmann und Kollegen\nTEL;TYPE=WORK:089 1234 5670\nTEL;TYPE=FAX:089 1234 5679\nEND:VCARD\n",
		"BEGIN:VCARD\nVERSION:3.0\nFN:Ärztlicher Bereitschaftsdienst\nTEL:116117\nTEL:0171 9988 7766\nEND:VCARD\n",
		"BEGIN:VCARD\nVERSION:3.0\nFN:Herr Brandl\nTEL:0160 5544 3322\nPHOTO;ENCODING=b;TYPE=JPEG:"+pic+"\nEND:VCARD\nBEGIN:VCARD\nVERSION:3.0\nFN:Sepp Brandl\nTEL:0160 5544 3322\nTEL:089 700 12 34\nEND:VCARD\n",
		"BEGIN:VCARD\nVERSION:3.0\nFN:Zahnarztpraxis Dr. Römer & Partner, Kieferorthopädie und Prophylaxe\nNICKNAME:"+layoutNames[3]+"\nTEL:089 3322 1100\nBDAY:1948-09-02\nADR:;;Lindwurmstraße 30;München;;80337;\nPHOTO;VALUE=uri:https://example.com/x.jpg\nEND:VCARD\n",
		"BEGIN:VCARD\nVERSION:3.0\nNOTE:nichts\nEND:VCARD\n",
		"BEGIN:VCARD\nVERSION:3.0\nFN:Rudi Wagn\nTEL:0171",
	)
	photos := map[int][]byte{1: testJPEG(t, 512, 512), 4: testJPEG(t, 512, 512)}
	review, err := f.db.AddContactImport(f.householdID, by, "Kontakte "+long+".vcf", cards, photos, f.box.now)
	if err != nil {
		t.Fatal(err)
	}
	single, err := f.db.AddContactImport(f.householdID, by, "Susanne Maier.vcf", file("BEGIN:VCARD\nVERSION:3.0\nFN:Tante Susanne\nTEL:0172 4455 6600\nEND:VCARD\n"), nil, f.box.now)
	if err != nil {
		t.Fatal(err)
	}
	old, err := f.db.AddContactImport(f.householdID, by, "Kontakte Papa.vcf", cards, nil, f.box.now.Add(-2*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	upload := func(lang, name string, data []byte) resp {
		var buf bytes.Buffer
		mw := multipart.NewWriter(&buf)
		part, _ := mw.CreateFormFile("file", name)
		_, _ = part.Write(data)
		_ = mw.Close()
		req, _ := http.NewRequest(http.MethodPost, f.srv.URL+"/contacts/import", &buf)
		req.Header.Set("Content-Type", mw.FormDataContentType())
		req.Header.Set("Origin", f.srv.URL)
		req.Header.Set("Accept-Language", lang)
		res, err := f.client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		var out bytes.Buffer
		_, _ = out.ReadFrom(res.Body)
		return resp{res.StatusCode, res.Header.Get("Location"), out.String()}
	}
	same := file("BEGIN:VCARD\nVERSION:3.0\nFN:Kinderarztpraxis Dr. Elif Yılmaz-Hoffmann und Kollegen\nTEL:089 1234 5670\nEND:VCARD\nBEGIN:VCARD\nVERSION:3.0\nNOTE:x\nEND:VCARD\n")
	for _, lang := range []string{"de", "en"} {
		hdr := map[string]string{"Accept-Language": lang}
		capture := func(path, name string, r resp, want int) {
			if r.status != want {
				t.Fatalf("%s = %d, want %d\n%s", name, r.status, want, r.body)
			}
			pages = append(pages, layoutPage{site: site, path: path + "#" + name, html: map[string]string{lang: r.body}})
		}
		capture("/contacts/import", "refused", upload(lang, "Urlaub Gardasee mit sehr langem Dateinamen.jpg", testJPEG(t, 40, 30)), http.StatusUnprocessableEntity)
		capture("/contacts/import", "nothing-new", upload(lang, "Kontakte Papa.vcf", same), http.StatusOK)
		capture("/contacts/import/"+review.ID, "review", f.do(http.MethodGet, "/contacts/import/"+review.ID, nil, hdr), http.StatusOK)
		capture("/contacts/import/"+review.ID, "missing", f.do(http.MethodPost, "/contacts/import/"+review.ID, url.Values{"take": {"1"}}, hdr), http.StatusUnprocessableEntity)
		capture("/contacts/import/"+single.ID, "single", f.do(http.MethodGet, "/contacts/import/"+single.ID, nil, hdr), http.StatusOK)
		capture("/contacts/import/"+old.ID, "expired", f.do(http.MethodGet, "/contacts/import/"+old.ID, nil, hdr), http.StatusOK)
	}
	return mergeCardPages(pages)
}

// contactPhotoPages are the photo card in the states only a POST reaches (ticket 297): a photo prepared and not
// saved yet, marked for removal, and refused with its error, in both languages.
func contactPhotoPages(t *testing.T, f *layoutFixture) []layoutPage {
	t.Helper()
	site := siteOf("contact photo card", f.box, f.client)
	post := func(lang, path string, fields url.Values, name string, data []byte) string {
		var buf bytes.Buffer
		mw := multipart.NewWriter(&buf)
		for k, vs := range fields {
			for _, v := range vs {
				_ = mw.WriteField(k, v)
			}
		}
		if data != nil {
			part, _ := mw.CreateFormFile("photo", name)
			_, _ = part.Write(data)
		}
		_ = mw.Close()
		req, _ := http.NewRequest(http.MethodPost, f.srv.URL+path, &buf)
		req.Header.Set("Content-Type", mw.FormDataContentType())
		req.Header.Set("Origin", f.srv.URL)
		req.Header.Set("Accept-Language", lang)
		res, err := f.client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		var out bytes.Buffer
		_, _ = out.ReadFrom(res.Body)
		return out.String()
	}
	edit := "/contacts/" + f.omaID + "/edit"
	name := url.Values{"name": {"Oma Gerda im Gartenfreundeverein"}}
	var pages []layoutPage
	for _, c := range []struct {
		path, label string
		fields      url.Values
		file        string
		data        []byte
	}{
		{edit, "#prepared", url.Values{"name": name["name"], "picture": {"upload"}}, "Familienfoto-vom-Sommerfest-am-Erlenbach-2026-mit-sehr-langem-Namen.jpg", testJPEG(t, 3024, 4032)},
		{edit, "#removal", url.Values{"name": name["name"], "picture": {"remove"}}, "", nil},
		{edit, "#refused", url.Values{"name": name["name"], "picture": {"upload"}}, "Mietvertrag-der-Praxis-in-der-Hauptstrasse-mit-langem-Namen.pdf", []byte("%PDF-1.4")},
		{"/contacts/new", "#new-prepared", url.Values{"name": {"Leonie (Babysitterin)"}, "picture": {"upload"}}, "leonie.jpg", testJPEG(t, 800, 600)},
		{"/contacts/new", "#new-refused", url.Values{"name": {"Leonie (Babysitterin)"}, "picture": {"upload"}}, "IMG_4031.png", bytes.Repeat([]byte{0}, 16<<20)},
	} {
		html := map[string]string{}
		for _, lang := range []string{"de", "en"} {
			html[lang] = post(lang, c.path, c.fields, c.file, c.data)
		}
		pages = append(pages, layoutPage{site: site, path: c.path + c.label, html: html})
	}
	return pages
}
