package server_test

import (
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"testing"
	"time"
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
	b := signedInBox(t)
	id := pairedDisplay(t, b, "Hallway", "eink_color")
	r := getEN(b, "/displays/"+id)
	if r.status != http.StatusOK {
		t.Fatalf("GET = %d", r.status)
	}
	for _, want := range []string{
		`<a class="back" href="/displays">`, "All displays", "<h1>Hallway</h1>", `<dl class="facts">`, "Last seen", "Paired on",
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

func TestABlackAndWhiteDisplayHasNoThemeControl(t *testing.T) {
	b := signedInBox(t)
	id := pairedDisplay(t, b, "Hallway", "eink_bw")
	body := getEN(b, "/displays/"+id).body
	if strings.Contains(body, `name="theme"`) {
		t.Error("a black-and-white display offers a theme")
	}
	if !strings.Contains(body, "always light") {
		t.Error("no sentence says why there is no theme choice")
	}
	other := pairedDisplay(t, b, "Kitchen", "eink_color")
	if !strings.Contains(getEN(b, "/displays/"+other).body, `name="theme"`) {
		t.Error("a colour display has no theme control")
	}
}

func TestQuietFieldsAreHiddenWhileTheSwitchIsOffAndTheErrorShowsAtQuietTo(t *testing.T) {
	b := signedInBox(t)
	id := pairedDisplay(t, b, "Hallway", "screen")
	body := getEN(b, "/displays/"+id).body
	at := strings.Index(body, `name="quiet"`)
	if at < 0 || strings.Contains(body[at:at+80], "checked") {
		t.Error("quiet mode starts on")
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
	b := signedInBox(t)
	id := pairedDisplay(t, b, "<b>Flur</b> & Co", "screen")
	b.post("/displays/"+id+"/unpair", url.Values{})
	body := getEN(b, "/displays").body
	if strings.Contains(body, "<b>Flur</b>") || !strings.Contains(body, "&lt;b&gt;Flur&lt;/b&gt; &amp; Co") {
		t.Errorf("the name is not escaped in the success line\n%s", body)
	}
}

func TestALongNameOfWideCharactersStillShowsInTheSuccessLine(t *testing.T) {
	b := signedInBox(t)
	name := strings.Repeat("😀", 80)
	id := pairedDisplay(t, b, name, "screen")
	b.post("/displays/"+id+"/unpair", url.Values{})
	if body := getEN(b, "/displays").body; !strings.Contains(body, "“"+name+"” unpaired.") {
		t.Errorf("the success line lost the name\n%s", body)
	}
}
