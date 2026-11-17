package server_test

import (
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"testing"

	"github.com/hejmo-home/hejmo/internal/store"
)

func (f *familyBox) displays(t *testing.T) []store.Display {
	t.Helper()
	ds, err := f.db.Displays(f.householdID)
	if err != nil {
		t.Fatal(err)
	}
	return ds
}

func (f *familyBox) settingsPage(t *testing.T, lang map[string]string) string {
	t.Helper()
	r := f.do(http.MethodGet, "/displays/"+f.displayID(t), nil, lang)
	if r.status != http.StatusOK {
		t.Fatalf("display page = %d", r.status)
	}
	return r.body
}

var gapRe = regexp.MustCompile(`>\s+<`)

// squash takes the white space between tags out, so a test can name markup without its indentation.
func squash(page string) string { return gapRe.ReplaceAllString(page, "><") }

// bright cuts the Brightness block out of the display page.
func bright(t *testing.T, page string) string {
	t.Helper()
	page = squash(page)
	i := strings.Index(page, `<fieldset class="bright`)
	if i < 0 {
		t.Fatal("no Brightness block in the page")
	}
	j := strings.Index(page[i:], `</fieldset><details class="more"><summary>`) // "More options" follows the block
	if j < 0 {
		t.Fatal("the Brightness block does not end before More options")
	}
	j += i + len("</fieldset>")
	return page[i:j]
}

func want(t *testing.T, what, text string, parts ...string) {
	t.Helper()
	text = inText(text)
	for _, p := range parts {
		if !strings.Contains(text, p) && !strings.Contains(text, inText(p)) {
			t.Errorf("%s misses %q", what, p)
		}
	}
}

func wantNot(t *testing.T, what, text string, parts ...string) {
	t.Helper()
	for _, p := range parts {
		if strings.Contains(text, p) {
			t.Errorf("%s contains %q", what, p)
		}
	}
}

// #179, preview Q1 and Q3: its own block after "Nachts", the state in words on
// top, Day and Evening with today's sun times, then quiet mode.
func TestBrightnessBlockShowsTheStateAndTheValuesAfterTheNightBlock(t *testing.T) {
	f, _ := kioskBox(t, "screen", quietForm(url.Values{"bright_evening": {"40"}}))
	f.tick(f.at(14, 17, 0)) // 17:00, after the sun set at 16:3x: Evening since sunset
	page := squash(f.settingsPage(t, de))
	if strings.Index(page, "<legend>Helligkeit</legend>") < strings.Index(page, "<legend>Nachts</legend>") {
		t.Fatal("the Brightness block does not follow the night block")
	}
	b := bright(t, page)
	want(t, "state", b, "Gerade 40 % · Abends, seit 16:3", "Die Kiosk-App hat es bestätigt.")
	want(t, "day", b, "Tagsüber von Sonnenaufgang bis Sonnenuntergang, heute 07:", "bis 16:3")
	want(t, "evening", b, "Abends von Sonnenuntergang bis Sonnenaufgang, heute 16:3", "bis 07:")
	for _, name := range []string{"bright_day", "bright_evening", "bright_quiet", "wake_minutes"} {
		if !strings.Contains(b, `name="`+name+`"`) {
			t.Errorf("no field %s", name)
		}
	}
	for _, v := range []string{`name="bright_day" value="100"`, `name="bright_evening" value="40"`, `name="bright_quiet" value="5"`, `name="wake_minutes" value="1"`} {
		if !strings.Contains(b, v) {
			t.Errorf("block misses %s", v)
		}
	}
	want(t, "quiet", b, "Im Ruhemodus (22:00 bis 06:00)", "Aus", "Stark gedimmt")
	if !strings.Contains(b, `name="quiet_light" value="off" checked`) {
		t.Error("Off is not the default of quiet mode")
	}
	want(t, "wake", b, "Ein Tippen weckt es für", "mit Abend-Helligkeit (40 %)")
	wantNot(t, "block", b, " disabled", `class="bright unavailable`, "Experimentell", `name="light_source"`)
	want(t, "no sensor", b, "Dieses Gerät meldet keinen Lichtsensor, darum gilt das Sonnenlicht.")
	// The kiosk app fields moved into the block.
	if !strings.Contains(b, `name="kiosk_url"`) || !strings.Contains(b, `name="kiosk_key"`) {
		t.Error("kiosk fields are not in the Brightness block")
	}
	want(t, "kiosk", b, "Kiosk-App", "eingerichtet: 192.168.1.20:8080")
	rest := page[strings.Index(page, "</fieldset><details class=\"more\"><summary>")+10:]
	wantNot(t, "More options", rest, `name="kiosk_url"`)
	if !strings.Contains(rest, `name="sunrise_offset"`) {
		t.Error("the sun offsets left More options")
	}
}

func TestBrightnessBlockInEnglishSaysTheSame(t *testing.T) {
	f, _ := kioskBox(t, "screen", quietForm(nil))
	f.tick(f.at(14, 14, 0))
	b := bright(t, f.settingsPage(t, en))
	want(t, "English", b, "Brightness", "Now 100 % · Day, since 07:", "The kiosk app confirmed it.", "In quiet mode", "Strongly dimmed", "Kiosk app")
}

// Q80: without a kiosk app the block is greyed out, says why and links the guide.
func TestBrightnessBlockWithoutAKioskAppIsGreyedOutWithTheReasonAndTheGuide(t *testing.T) {
	f := newFamilyBox(t, "screen", withKiosk(&fakeKiosk{}))
	f.quietOn(t)
	b := bright(t, f.settingsPage(t, de))
	wantNot(t, "block", b, "Gerade ")
	for _, want := range []string{`class="bright unavailable"`, `Nicht verfügbar`, `target="_blank"`, `rel="noopener"`} {
		if !strings.Contains(b, want) {
			t.Errorf("block misses %s", want)
		}
	}
	want(t, "reason", b, "Dafür braucht das Display die Kiosk-App FreeKiosk.", "Ein Browser allein kann die Helligkeit nicht ändern.",
		"zeigt dieses Display im Ruhemodus eine schwarze Seite", "Anleitung: FreeKiosk einrichten", "So könntest du es dann einstellen:", "nicht eingerichtet")
	if n := strings.Count(b, "disabled"); n < 6 {
		t.Errorf("only %d disabled controls, want the levels, the quiet choice and the wake time", n)
	}
	if !strings.Contains(b, `<details class="more kiosk" open>`) {
		t.Error("the kiosk app fields are not open")
	}
	// The kiosk fields stay usable, and the disabled brightness fields are not sent: saving keeps the values.
	if strings.Contains(b[strings.Index(b, `<details class="more kiosk"`):], "disabled") {
		t.Error("the kiosk app fields are disabled")
	}
	// A black page is what a plain E-Ink browser does not show: it shows the night page.
	e := newFamilyBox(t, "eink_bw")
	want(t, "E-Ink without kiosk", bright(t, e.settingsPage(t, de)), "zeigt dieses Display im Ruhemodus die Nachtseite")
}

func TestBrightnessGuideLinksToTheFreeKioskGuideInTheRepository(t *testing.T) {
	f := newFamilyBox(t, "screen")
	b := bright(t, f.settingsPage(t, de))
	if !strings.Contains(b, `href="https://github.com/hejmo-home/hejmo/blob/main/docs/freekiosk.md"`) {
		t.Errorf("guide link missing:\n%s", b)
	}
}

// Q78: the choice sun or room light only when the device reports a sensor.
func TestRoomLightIsOfferedOnlyWithASensor(t *testing.T) {
	f, fake := kioskBox(t, "eink_bw", url.Values{"light_source": {"room"}})
	fake.mu.Lock()
	fake.sensor = true
	fake.mu.Unlock()
	f.tick(f.at(14, 14, 0))
	b := bright(t, f.settingsPage(t, de))
	want(t, "choice", b, "Nach Sonnenlicht", "Nach Raumlicht", "Der Lichtsensor des Geräts regelt selbst. Der Ruhemodus gilt trotzdem.",
		"Deine Werte (100 % und 40 %) bleiben gespeichert für „Nach Sonnenlicht“.", "Gerade regelt der Lichtsensor", "Experimentell",
		"Bei E-Ink stellt Hejmo das Frontlicht. Noch nicht auf einem echten Gerät getestet.")
	if !strings.Contains(b, `name="light_source" value="room" checked`) {
		t.Error("room light is not the saved choice")
	}
	wantNot(t, "block", b, "meldet keinen Lichtsensor")
}

// Q77: Off or dimmed, one explanation per display type; wake time only with Off, level only with Dimmed.
func TestQuietChoiceExplainsWhatHappensPerDisplayType(t *testing.T) {
	f, _ := kioskBox(t, "eink_bw", quietForm(nil))
	want(t, "E-Ink", bright(t, f.settingsPage(t, de)), "Frontlicht aus. Die Nachtseite bleibt stehen, im Dunkeln ist sie nicht zu sehen.", "Die Nachtseite bleibt leise sichtbar.")
	s, _ := kioskBox(t, "screen", quietForm(nil))
	want(t, "screen", bright(t, s.settingsPage(t, de)), "Der Bildschirm wird schwarz. Ein Tippen weckt ihn kurz.", "Die Nachtseite bleibt leise sichtbar.")
}

func TestQuietBrightnessIsASentenceWhileQuietModeIsOff(t *testing.T) {
	f, _ := kioskBox(t, "screen", nil)
	b := bright(t, f.settingsPage(t, de))
	want(t, "quiet off", b, "Der Ruhemodus ist aus. Nachts gilt die Abend-Helligkeit. Einschalten kannst du ihn oben unter „Nachts“.")
	wantNot(t, "block", b, `name="quiet_light"`, `name="wake_minutes"`, `name="bright_quiet"`)
}

func TestBrightnessShowsTheErrorWhenTheKioskAppDoesNotAnswer(t *testing.T) {
	f, fake := kioskBox(t, "screen", nil)
	fake.setFail(true)
	f.tick(f.at(14, 17, 0))
	b := bright(t, f.settingsPage(t, de))
	want(t, "problem", b, "Kiosk-App antwortet nicht. Um 16:3", "sollte die Helligkeit auf 40 % gehen. Ist das Display an und im WLAN? Hejmo versucht es jede Minute wieder.", "antwortet nicht: 192.168.1.20:8080")
	wantNot(t, "block", b, "Die Kiosk-App hat es bestätigt", "Gerade 40 %")
	if !strings.Contains(b, `class="flag problem"`) {
		t.Error("the problem is not a problem flag")
	}
}

func TestBrightnessWithoutAHomeLocationSaysDayAppliesAllDay(t *testing.T) {
	f, _ := kioskBox(t, "screen", nil)
	f.withoutLocation(t)
	b := bright(t, f.settingsPage(t, de))
	want(t, "no location", b, "Die Box kennt deinen Wohnort nicht", "gilt den ganzen Tag „Tagsüber“.", "Wohnort eintragen")
	wantNot(t, "block", b, "heute 07:")
	if !strings.Contains(b, `class="flag warn"`) || !strings.Contains(b, `href="/settings/household"`) {
		t.Error("no warning with a link to the household settings")
	}
}

// Saving: the form values reach the store, and the page shows them again.
func TestBrightnessValuesAreSavedFromTheForm(t *testing.T) {
	f, _ := kioskBox(t, "screen", quietForm(nil))
	form := url.Values{"bright_day": {"80"}, "bright_evening": {"25"}, "quiet_light": {"dim"}, "bright_quiet": {"3"}, "wake_minutes": {"5"}}
	if r := f.saveSettings(t, quietForm(form)); r.status != http.StatusSeeOther {
		t.Fatalf("save = %d\n%s", r.status, r.body)
	}
	b := bright(t, f.settingsPage(t, de))
	for _, v := range []string{`name="bright_day" value="80"`, `name="bright_evening" value="25"`, `name="bright_quiet" value="3"`, `name="wake_minutes" value="5"`, `name="quiet_light" value="dim" checked`} {
		if !strings.Contains(b, v) {
			t.Errorf("saved page misses %s", v)
		}
	}
}

// The wake time reads "Minute" for 1 and "Minuten" otherwise.
func TestWakeUnitFollowsTheNumber(t *testing.T) {
	f, _ := kioskBox(t, "screen", quietForm(url.Values{"wake_minutes": {"1"}}))
	if b := bright(t, f.settingsPage(t, de)); !strings.Contains(b, `<span class="unit">Minute</span>`) {
		t.Error("1 is not Minute")
	}
	f.saveSettings(t, quietForm(url.Values{"wake_minutes": {"2"}}))
	if b := bright(t, f.settingsPage(t, de)); !strings.Contains(b, `<span class="unit">Minuten</span>`) {
		t.Error("2 is not Minuten")
	}
}

// Preview 7: a number outside its range is refused at its field; the sentence on top names the block.
func TestBrightnessErrorsSitAtTheirFieldAndNameTheBlock(t *testing.T) {
	f, _ := kioskBox(t, "screen", quietForm(nil))
	r := f.do(http.MethodPost, "/displays/"+f.displayID(t)+"/settings", url.Values{"bright_evening": {"140"}, "wake_minutes": {"0"}}, de)
	if r.status != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422", r.status)
	}
	want(t, "top", r.body, "Bitte prüfe die markierten Felder unter „Helligkeit“.")
	b := bright(t, r.body)
	for _, v := range []string{`class="lvl invalid"`, `aria-invalid="true"`, `id="err-bright_evening"`, `value="140"`} {
		if !strings.Contains(b, v) {
			t.Errorf("error block misses %s", v)
		}
	}
	want(t, "error", b, "Gib eine Zahl von 1 bis 100 ein. 100 ist die volle Helligkeit.")
	// A value that is no number is kept and refused the same way.
	r = f.do(http.MethodPost, "/displays/"+f.displayID(t)+"/settings", url.Values{"wake_minutes": {"abc"}}, de)
	if r.status != http.StatusUnprocessableEntity || !strings.Contains(inText(bright(t, r.body)), "Gib eine Zahl von 1 bis 30 ein.") {
		t.Fatalf("wake_minutes=abc = %d", r.status)
	}
	// And nothing was saved.
	if got := f.displays(t)[0].Brightness.Evening; got != 40 {
		t.Errorf("evening = %d after a refused save", got)
	}
}

// A value that was changed but not yet sent is not shown as confirmed: the
// page works out the new target and says the box passes it on.
func TestBrightnessBlockDoesNotSayConfirmedForAChangeNotYetSent(t *testing.T) {
	f, fake := kioskBox(t, "eink_bw", url.Values{"bright_evening": {"25"}})
	f.tick(f.at(14, 17, 0))
	fake.take()
	b := bright(t, f.settingsPage(t, en))
	want(t, "before the change", b, "Now 25 %", "The kiosk app confirmed it.")
	f.saveSettings(t, url.Values{"bright_evening": {"30"}})
	b = bright(t, f.settingsPage(t, en))
	want(t, "after the change", b, "Now 30 %", "The box passes it on to the kiosk app within the next minute.")
	wantNot(t, "after the change", b, "The kiosk app confirmed it.")
	expect(t, "nothing sent yet", fake.take())
	f.tick(f.at(14, 17, 1))
	expect(t, "sent", fake.take(), "brightness 30")
	want(t, "after the tick", bright(t, f.settingsPage(t, en)), "Now 30 %", "The kiosk app confirmed it.")
}
