package server_test

import (
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/hejmo-home/hejmo/internal/store"
	"github.com/hejmo-home/hejmo/internal/theme"
)

// Ticket 111 (ADR 0017): an overview lists calm rows that open a detail page;
// everything about one display, and unpairing, happens only on that page.

// pairedDisplay pairs a display of the given type and returns its id.
func pairedDisplay(t *testing.T, b *box, name, typ string) string {
	t.Helper()
	code := codeOn(t, newScreen(b).get("/display", "en").body)
	if r := b.post("/displays", url.Values{"code": {code}, "name": {name}, "type": {typ}}); r.status != http.StatusSeeOther {
		t.Fatalf("pair = %d\n%s", r.status, r.body)
	}
	h, _ := b.db.HomeHousehold()
	list, err := b.db.Displays(h.ID)
	if err != nil || len(list) == 0 {
		t.Fatalf("displays = %v, %v", list, err)
	}
	return list[len(list)-1].ID
}

var formTagRe = regexp.MustCompile(`(?s)<form[^>]*action="([^"]*)"`)

// noDestructiveForm fails when an overview page holds a form that removes,
// revokes, disconnects or unpairs something.
func noDestructiveForm(t *testing.T, page, body string) {
	t.Helper()
	for _, m := range formTagRe.FindAllStringSubmatch(body, -1) {
		for _, bad := range []string{"/unpair", "/revoke", "/delete"} {
			if strings.HasSuffix(m[1], bad) {
				t.Errorf("%s still has a destructive form: %s", page, m[1])
			}
		}
	}
	if strings.Contains(body, `class="button danger"`) || strings.Contains(body, `<details class="danger"`) {
		t.Errorf("%s still has a danger button or disclosure", page)
	}
}

func TestDisplaysOverviewListsRowsThatOpenTheDetailPage(t *testing.T) {
	t.Parallel()
	b := signedInBox(t)
	id := pairedDisplay(t, b, "Hallway", "eink_color")
	body := getEN(b, "/displays").body
	for _, want := range []string{`<ul class="list-rows">`, `<a href="/displays/` + id + `">`, "<b>Hallway</b>", "E-Ink colour", "Last seen "} {
		if !strings.Contains(body, want) {
			t.Errorf("overview lacks %q\n%s", want, body)
		}
	}
	if strings.Contains(body, "/settings\"") && strings.Contains(body, `action="/displays/`+id+`/settings"`) {
		t.Error("the overview still holds the settings form")
	}
	noDestructiveForm(t, "/displays", body)
	if strings.Contains(body, `class="flag warn"`) {
		t.Error("a display seen just now has a warning")
	}
}

func TestADisplayNotSeenForFifteenMinutesGetsAWarningFlag(t *testing.T) {
	t.Parallel()
	b := signedInBox(t)
	id := pairedDisplay(t, b, "Hallway", "screen")
	b.now = b.now.Add(16 * time.Minute)
	for _, page := range []string{"/displays", "/displays/" + id} {
		body := getEN(b, page).body
		if !strings.Contains(body, `class="flag warn"`) || !strings.Contains(body, "Check power and Wi-Fi") {
			t.Errorf("%s: no warning after 16 minutes of silence\n%s", page, body)
		}
	}
}

func TestDisplayDetailShowsEverythingAndConfirmsByName(t *testing.T) {
	t.Parallel()
	b := signedInBox(t)
	id := pairedDisplay(t, b, "Hallway", "eink_color")
	r := getEN(b, "/displays/"+id)
	if r.status != http.StatusOK {
		t.Fatalf("GET = %d", r.status)
	}
	for _, want := range []string{
		`<nav class="crumbs" aria-label="Breadcrumb">`, `<a href="/displays">Displays</a>`, "<h1>Hallway</h1>", `<dl class="facts">`, "Last seen", "Paired on",
		`<form method="post" action="/displays/` + id + `/settings" class="steps">`,
		`name="name"`, `name="type"`, `name="show_tomorrow"`, `name="theme"`, `name="quiet"`, `name="quiet_from"`, `name="quiet_to"`,
		`class="pair when-on"`, `<details class="more">`, "Kiosk app: not set up", `name="kiosk_url"`, `name="sunrise_offset"`,
		`<details class="danger">`, `action="/displays/` + id + `/unpair"`, "Yes, unpair “Hallway”", "No, change nothing",
	} {
		if !strings.Contains(r.body, want) {
			t.Errorf("detail lacks %q\n%s", want, r.body)
		}
	}
	if strings.Contains(r.body, `<a class="back" href="/settings">`) {
		t.Error("a detail page shows the hub link as well as its own")
	}
	if c := b.get("/displays/nope"); c.status != http.StatusNotFound {
		t.Errorf("unknown display = %d", c.status)
	}
}

func TestDisplayRenameAndTypeSaveWithASuccessLine(t *testing.T) {
	t.Parallel()
	b := signedInBox(t)
	id := pairedDisplay(t, b, "Hallway", "eink_bw")
	r := b.post("/displays/"+id+"/settings", url.Values{"name": {"Kitchen"}, "type": {"screen"}, "theme": {"light"}, "sunrise_offset": {"0"}, "sunset_offset": {"0"}})
	if r.status != http.StatusSeeOther || r.location != "/displays/"+id {
		t.Fatalf("save = %d to %q\n%s", r.status, r.location, r.body)
	}
	body := getEN(b, "/displays/"+id).body
	for _, want := range []string{"<h1>Kitchen</h1>", "Saved.", `value="screen" checked`} {
		if !strings.Contains(body, want) {
			t.Errorf("after saving the page lacks %q", want)
		}
	}
	if strings.Contains(getEN(b, "/displays/"+id).body, "Saved.") {
		t.Error("the success line shows twice")
	}
}

func TestDisplayNameAndTypeErrorsShowAtTheirField(t *testing.T) {
	t.Parallel()
	b := signedInBox(t)
	id := pairedDisplay(t, b, "Hallway", "screen")
	r := b.post("/displays/"+id+"/settings", url.Values{"name": {"  "}, "type": {"screen"}})
	if r.status != http.StatusUnprocessableEntity || !strings.Contains(r.body, `id="err-name"`) {
		t.Errorf("blank name = %d, no error at the field", r.status)
	}
	r = b.post("/displays/"+id+"/settings", url.Values{"name": {"Kitchen"}, "type": {"hologram"}})
	if r.status != http.StatusUnprocessableEntity || !strings.Contains(r.body, `id="err-type"`) || !strings.Contains(r.body, `value="Kitchen"`) {
		t.Errorf("unknown type = %d, no error at the field or the name was not kept", r.status)
	}
}

// ADR 0015: a dark theme is allowed on a black-and-white display (it takes the dark palette), so the page
// offers the choice there as on every display, and a theme saved on it can be changed back.
func TestABlackAndWhiteDisplayKeepsItsThemeControl(t *testing.T) {
	t.Parallel()
	b := signedInBox(t)
	id := pairedDisplay(t, b, "Hallway", "eink_bw")
	body := getEN(b, "/displays/"+id).body
	if !strings.Contains(body, `name="theme"`) {
		t.Error("a black-and-white display has no theme control")
	}
	if strings.Contains(body, "always light") {
		t.Error("the page says a black-and-white display is always light")
	}
	h, _ := b.db.HomeHousehold()
	for _, th := range []string{"dark", "light"} {
		if r := b.post("/displays/"+id+"/settings", url.Values{"theme": {th}, "sunrise_offset": {"0"}, "sunset_offset": {"0"}}); r.status != http.StatusSeeOther {
			t.Fatalf("theme %s = %d\n%s", th, r.status, r.body)
		}
		if d := displayByID(t, b, h.ID, id); string(d.Theme) != th {
			t.Errorf("theme = %q after saving %q", d.Theme, th)
		}
	}
}

// The sun offsets are saved on a black-and-white display, with or without a theme in the same post.
func TestSunOffsetsAreSavedOnABlackAndWhiteDisplay(t *testing.T) {
	t.Parallel()
	b := signedInBox(t)
	id := pairedDisplay(t, b, "Hallway", "eink_bw")
	h, _ := b.db.HomeHousehold()
	if r := b.post("/displays/"+id+"/settings", url.Values{"theme": {"auto"}, "sunrise_offset": {"15"}, "sunset_offset": {"-20"}}); r.status != http.StatusSeeOther {
		t.Fatalf("save = %d\n%s", r.status, r.body)
	}
	if d := displayByID(t, b, h.ID, id); d.SunriseOffset != 15 || d.SunsetOffset != -20 || d.Theme != theme.Auto {
		t.Fatalf("saved %q %d %d, want auto 15 -20", d.Theme, d.SunriseOffset, d.SunsetOffset)
	}
	// Offsets alone (no theme field) are not dropped with a success line.
	if r := b.post("/displays/"+id+"/settings", url.Values{"sunrise_offset": {"30"}, "sunset_offset": {"45"}}); r.status != http.StatusSeeOther {
		t.Fatalf("offsets only = %d\n%s", r.status, r.body)
	}
	if d := displayByID(t, b, h.ID, id); d.SunriseOffset != 30 || d.SunsetOffset != 45 || d.Theme != theme.Auto {
		t.Fatalf("saved %q %d %d, want auto 30 45", d.Theme, d.SunriseOffset, d.SunsetOffset)
	}
}

func displayByID(t *testing.T, b *box, householdID, id string) store.Display {
	t.Helper()
	list, err := b.db.Displays(householdID)
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range list {
		if d.ID == id {
			return d
		}
	}
	t.Fatalf("display %s not found", id)
	return store.Display{}
}

func TestQuietFieldsAreHiddenWhileTheSwitchIsOffAndTheErrorShowsAtQuietTo(t *testing.T) {
	t.Parallel()
	b := signedInBox(t)
	id := pairedDisplay(t, b, "Hallway", "screen")
	body := getEN(b, "/displays/"+id).body
	at := strings.Index(body, `name="quiet"`)
	if at < 0 || strings.Contains(body[at:at+80], "checked") {
		t.Error("quiet mode starts on")
	}
	if !strings.Contains(body, "<legend>At night</legend>") {
		t.Error("the quiet mode group has no legend")
	}
	// The times sit in a container that the stylesheet hides while the switch is off.
	if !strings.Contains(body, `class="pair when-on"`) {
		t.Error("the quiet times are not in a .when-on container")
	}
	r := b.post("/displays/"+id+"/settings", url.Values{"quiet": {"on"}, "quiet_from": {"21:00"}, "quiet_to": {"21:00"}})
	if r.status != http.StatusUnprocessableEntity || !strings.Contains(r.body, `id="err-quiet_to"`) {
		t.Fatalf("equal times = %d, no error at quiet_to\n%s", r.status, r.body)
	}
	if !strings.Contains(r.body, `value="21:00"`) || !regexp.MustCompile(`name="quiet"[^>]*checked`).MatchString(r.body) {
		t.Error("what was entered is not kept")
	}
}

func TestAKioskErrorOpensMoreOptionsAndShowsAtTheField(t *testing.T) {
	t.Parallel()
	b := signedInBox(t)
	id := pairedDisplay(t, b, "Hallway", "screen")
	r := b.post("/displays/"+id+"/settings", url.Values{"kiosk_url": {"192.168.1.20:8080"}})
	if r.status != http.StatusUnprocessableEntity || !strings.Contains(r.body, `id="err-kiosk_url"`) || !strings.Contains(r.body, `<details class="more" open>`) {
		t.Errorf("kiosk error = %d, field error or open details missing\n%s", r.status, r.body)
	}
	if !strings.Contains(r.body, `value="192.168.1.20:8080"`) {
		t.Error("the address was not kept")
	}
}

func TestUnpairingHappensOnTheDetailPageAndNamesTheDisplay(t *testing.T) {
	t.Parallel()
	b := signedInBox(t)
	id := pairedDisplay(t, b, "Hallway", "screen")
	r := b.post("/displays/"+id+"/unpair", url.Values{})
	if r.status != http.StatusSeeOther || r.location != "/displays" {
		t.Fatalf("unpair = %d to %q", r.status, r.location)
	}
	body := getEN(b, "/displays").body
	if !strings.Contains(body, "“Hallway” unpaired.") {
		t.Errorf("the success line does not name the display\n%s", body)
	}
	if strings.Contains(getEN(b, "/displays").body, "unpaired.") {
		t.Error("the success line shows twice")
	}
	if c := b.get("/displays/" + id); c.status != http.StatusNotFound {
		t.Errorf("an unpaired display's page = %d, want 404", c.status)
	}
}

func TestDisplayNamesInTheSuccessLineAreEscaped(t *testing.T) {
	t.Parallel()
	b := signedInBox(t)
	id := pairedDisplay(t, b, "<b>Flur</b> & Co", "screen")
	b.post("/displays/"+id+"/unpair", url.Values{})
	body := getEN(b, "/displays").body
	if strings.Contains(body, "<b>Flur</b>") || !strings.Contains(body, "&lt;b&gt;Flur&lt;/b&gt; &amp; Co") {
		t.Errorf("the name is not escaped in the success line\n%s", body)
	}
}

func TestALongNameOfWideCharactersStillShowsInTheSuccessLine(t *testing.T) {
	t.Parallel()
	b := signedInBox(t)
	name := strings.Repeat("😀", 20)
	id := pairedDisplay(t, b, name, "screen")
	b.post("/displays/"+id+"/unpair", url.Values{})
	if body := getEN(b, "/displays").body; !strings.Contains(body, "“"+name+"” unpaired.") {
		t.Errorf("the success line lost the name\n%s", body)
	}
}

// PATCH /displays/{id} renames and retypes a display, so the contract lists both with their limits.
func TestOpenAPIListsNameAndTypeOfUpdateDisplay(t *testing.T) {
	t.Parallel()
	props := loadSpec(t).Components.Schemas["UpdateDisplay"].Value.Properties
	name, typ := props["name"], props["type"]
	if name == nil || name.Value.MinLength != 1 || name.Value.MaxLength == nil || *name.Value.MaxLength != 80 {
		t.Errorf("UpdateDisplay.name = %+v, want a string of 1 to 80 characters", name)
	}
	if typ == nil || typ.Ref != "#/components/schemas/DisplayType" {
		t.Errorf("UpdateDisplay.type = %+v, want a reference to DisplayType", typ)
	}
}
