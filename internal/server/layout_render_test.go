package server_test

import (
	"context"
	_ "embed"
	"encoding/json"
	"flag"
	"fmt"
	"html"
	"io"
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
	"github.com/hejmo-home/hejmo/internal/caldavclient"
	"github.com/hejmo-home/hejmo/internal/calsync"
	"github.com/hejmo-home/hejmo/internal/list"
	"github.com/hejmo-home/hejmo/internal/outbound"
	"github.com/hejmo-home/hejmo/internal/passkey/passkeytest"
	"github.com/hejmo-home/hejmo/internal/store"
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
	kid *screen  // the phone of a child, which shows lists as tiles

	lists       []string // list IDs
	inviteToken string   // the invitation of Maximilian, a parent without account
	hint        *screen  // a display that shows the screensaver's hint
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
	// Entries that name articles (#166): shipped ones that warn several people with long names, and the own articles made below, one with a photo and one with a symbol. The tiles of the child's phone show them.
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
	r, err := db.CreateRoutine(h, task.Routine{PersonID: mia, Name: "Morgenroutine vor der Schule", Part: task.Morning})
	must(err)
	for _, title := range []string{"Zähne putzen", "Schulranzen packen", "Frühstücksbrotdose einpacken"} {
		_, err := db.AddTask(h, task.Task{PersonID: mia, Title: title, Repeat: task.Daily, Date: "2026-11-14", RoutineID: r.ID, Stars: 1})
		must(err)
	}
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
	_ = knusper
	_, err = db.CreateArticle(h, f.ids[1], store.OwnArticle{Name: "Katzenfutter Huhn", Dept: "other", Kind: "nonfood", Unit1: "pack", PicSource: "symbol", Symbol: "🐈"}, nil)
	must(err)
	must(db.SetLibrarySettings(h, store.LibrarySettings{HideAllergen: true, Everyone: true, ConfirmedBy: f.ids[0]}))
	must(db.SetArticleHidden(h, "walnuts", true))

	// Groups, a calendar, a day note.
	_, err = db.CreateGroup(h, "Kindergartenkinder-Elternbeirat", f.ids[:3])
	must(err)
	_, err = db.CreateCalendar(store.Calendar{HouseholdID: h, Name: "Fußballtrainingsplanung", Kind: "local", Color: "#3B82F6"})
	must(err)
	must(db.SetDayNote(h, "2026-11-14", "Bitte Schwimmsachen und Handtücher für Maximilian und Anna-Lena nicht vergessen"))

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
	// A paired phone, a calendar connection, a CalDAV device token.
	f.kid = pairedPhone(t, f.box, f.people["Anna-Lena"], "Handy von Anna-Lena im Kinderzimmer")
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
		"kiosk": map[string]any{"url": "http://192.168.178.20:8080"}, "quiet_mode": map[string]any{"enabled": true, "from": "22:00", "to": "06:00"}})
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
	notPageRe = regexp.MustCompile(`^/(static/|api/|healthz|manifest|sw\.js|logout|display(/|$|\?)|ca/|update/|\.well-known|i/|phones/join/)|\.(ics|crt|mobileconfig)(\?|$)`)
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
	"/healthz":                     "plain text",
	"/static/":                     "assets",
	"/api/v1/openapi.yaml":         "API document",
	"/ca/hejmo-ca.crt":             "download",
	"/ca/hejmo.mobileconfig":       "download",
	"/manifest.webmanifest":        "JSON",
	"/sw.js":                       "script",
	"/display/state":               "JSON",
	"/display/view":                "fragment of /display, rendered inside it",
	"/display/lists":               "redirect to the first list",
	"/display/lists/{id}/view":     "fragment of a list, rendered inside it",
	"/lists/{id}/suggest":          "fragment: the suggestions under the add card, rendered inside it",
	"/lists/{id}/quantity":         "fragment: the quantity part of the add card, rendered inside it",
	"/display/lists/{id}/suggest":  "fragment: the suggestions under the add card, rendered inside it",
	"/display/lists/{id}/quantity": "fragment: the quantity part of the add card, rendered inside it",
	"/display/events":              "event stream",
	"/display/person/{id}/view":    "fragment of the personal view, rendered inside it",
	"/otp":                         "redirect",
	"/i/{token}":                   "redirect",
	"/setup/https/check":           "answers a fragment for the setup page's script",
	"/places":                      "JSON list of places for the home location picker",
	"/phones/join/{code}":          "one-time pairing link of a phone",
	"/display/saver/photos":        "JSON",
	"/display/photo/{id}":          "image",

	"/articles/{id}/picture":                  "an image",
	"/display/articles/{id}/picture":          "an image",
	"/settings/lists/library/picture/{token}": "an image",
	"/settings/lists/library/suggest":         "JSON for the live list under the name field",
}

// matches reports whether a request path is served by a route pattern.
func matches(pattern, path string) bool {
	if i := strings.Index(path, "?"); i >= 0 {
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
	// The child's phone shows lists as tiles by default (#166): the tiles of articles with a picture, a symbol and the warning of several people, and of free text.
	kid := siteOf("child phone", f.box, f.kid.client)
	add(kid, "/lists")
	add(kid, "/lists/"+f.lists[0])
	add(kid, "/lists/"+f.lists[1])
	// The display: the family view and the personal views of two people.
	display := siteOf("display", f.box, f.display.client)
	add(display, "/display")
	add(display, "/display/person/"+f.ids[1])
	add(display, "/display/person/"+f.ids[3])
	add(display, "/display/lists/"+f.lists[0])
	add(display, "/display/lists/"+f.lists[1])
	add(display, "/display/saver")
	add(siteOf("display hint", f.box, f.hint.client), "/display/saver")

	// Offline mode (ticket 201): the pages as the box answers them while it is on.
	pages = append(pages, offlinePages(t, f)...)

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
	// A phone whose device session was removed (ticket 199), on a box of its own.
	oldBox, oldPhone, _ := unpairedPhone(t)
	add(siteOf("unpaired device", oldBox, oldPhone.client), "/login")
	// The invitation of a parent: the welcome page and the form for username and password.
	add(out, "/invite/"+f.inviteToken)
	add(out, "/invite/"+f.inviteToken+"/account")
	return pages
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

// The layout check can only find what its fixture shows: the child's phone
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
						// Displays and phones follow the browser's language, the web pages the cookie.
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
						t.Fatalf("the page is in %q (%v), not in %q: the check would measure the wrong language", got, err, lang)
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
	phone := siteOf("child's phone", f.box, pairedPhone(t, f.box, mia, "Handy").client)
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
		{phone, "/", "h1.child-h", long},
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
