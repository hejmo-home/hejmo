package server_test

import (
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"testing"
)

func displayIDOf(t *testing.T, c *apiClient) string {
	t.Helper()
	_, _, list := c.call(http.MethodGet, "/displays", nil)
	return list[0].(map[string]any)["id"].(string)
}

func saverFixture(t *testing.T, typ string) (*familyBox, *apiClient, string) {
	f := newFamilyBox(t, typ)
	f.now = f.at(14, 13, 0)
	c := newAPIClient(t, f.box)
	c.cookie = true
	return f, c, displayIDOf(t, c)
}

func TestDisplayPageHasTheScreensaverBlockBetweenWhatItShowsAndNight(t *testing.T) {
	f, c, id := saverFixture(t, "screen")
	uploaded(t, c, defaultAlbum(t, c), "a.jpg")
	page := f.get("/displays/" + id).body
	i, j, k := strings.Index(page, "Was es zeigt"), strings.Index(page, `id="bildschirmschoner"`), strings.Index(page, "Nachts")
	if !(i < j && j < k) {
		t.Fatalf("order: shows %d, saver %d, night %d", i, j, k)
	}
	for _, want := range []string{"Wenn niemand tippt", "An: nach 10 Minuten ohne Tippen Fotos aus euren Alben", "Alle Alben", "Unsere Fotos", "1 Foto", "startet nach 10 Minuten, neues Foto jede Minute",
		"Im Ruhemodus ist der Bildschirmschoner aus. Bei diesem Display ist der Ruhemodus gerade aus.", "kein Bildschirmschoner", `href="/photos"`} {
		if !strings.Contains(page, want) {
			t.Errorf("the display page lacks %q", want)
		}
	}
	f.post("/displays/"+id+"/settings", url.Values{"quiet": {"1"}, "quiet_from": {"22:00"}, "quiet_to": {"06:00"}, "saver": {"1"}, "saver_idle": {"10"}, "saver_interval": {"1"}, "saver_all": {"1"}})
	if page := f.get("/displays/" + id).body; !strings.Contains(page, "Im Ruhemodus (22:00 bis 06:00) ist der Bildschirmschoner aus.") {
		t.Errorf("the quiet mode times are not named:\n%s", page)
	}
}

func TestDisplayWithoutPhotosOffersToAddSome(t *testing.T) {
	f, _, id := saverFixture(t, "screen")
	page := f.get("/displays/" + id).body
	if !strings.Contains(page, "Noch keine Fotos.") || !strings.Contains(page, "Fotos hinzufügen") || strings.Contains(page, `name="saver_album"`) {
		t.Errorf("the block does not replace the chips by a notice:\n%s", page)
	}
}

func TestSavingTheScreensaverBlockChoosesAlbumsAndMinutes(t *testing.T) {
	f, c, id := saverFixture(t, "eink_bw")
	trip := newAlbum(t, c, "Urlaub")
	uploaded(t, c, trip, "a.jpg")
	r := f.post("/displays/"+id+"/settings", url.Values{"saver": {"1"}, "saver_idle": {"5"}, "saver_interval": {"20"}, "saver_album": {trip}})
	if r.status != http.StatusSeeOther {
		t.Fatalf("save = %d\n%s", r.status, r.body)
	}
	_, _, list := c.call(http.MethodGet, "/displays", nil)
	sv := list[0].(map[string]any)["screensaver"].(map[string]any)
	if sv["idle_minutes"].(float64) != 5 || sv["interval_minutes"].(float64) != 20 || len(sv["album_ids"].([]any)) != 1 {
		t.Errorf("saved = %v", sv)
	}
	page := f.get("/displays/" + id).body
	if !strings.Contains(page, "neues Foto alle 20 Minuten") || !strings.Contains(page, "Graustufen") {
		t.Errorf("summary or E-Ink hint missing:\n%s", page)
	}
	// "All albums" wins and a switch left off turns it off.
	f.post("/displays/"+id+"/settings", url.Values{"saver_idle": {"5"}, "saver_interval": {"20"}, "saver_all": {"1"}, "saver_album": {trip}})
	_, _, list = c.call(http.MethodGet, "/displays", nil)
	sv = list[0].(map[string]any)["screensaver"].(map[string]any)
	if sv["enabled"] != false || len(sv["album_ids"].([]any)) != 0 {
		t.Errorf("after all albums and the switch off: %v", sv)
	}
	r = f.post("/displays/"+id+"/settings", url.Values{"saver": {"1"}, "saver_idle": {"0"}, "saver_interval": {"1"}})
	if r.status != http.StatusUnprocessableEntity || !strings.Contains(r.body, "1 bis 240 Minuten") || !strings.Contains(r.body, `value="0"`) {
		t.Errorf("0 minutes = %d, want 422 with the error at the field and the entry kept", r.status)
	}
}

func TestScreensaverPageShowsAPhotoWithTimeAndDateInWords(t *testing.T) {
	f, c, _ := saverFixture(t, "screen")
	uploaded(t, c, defaultAlbum(t, c), "a.jpg")
	r := f.display.get("/display/saver", "de")
	if r.status != http.StatusOK {
		t.Fatalf("saver = %d", r.status)
	}
	for _, want := range []string{`class="saver-photo" src="/display/photo/`, "13:00", "Samstag, 14. November", `data-interval="60"`, `data-display-type="screen"`, "/static/saver.js"} {
		if !strings.Contains(r.body, want) {
			t.Errorf("the page lacks %q:\n%s", want, r.body)
		}
	}
	if strings.Contains(r.body, "Unsere Fotos") {
		t.Error("the screensaver names an album")
	}
	en := f.display.get("/display/saver", "en").body
	if !strings.Contains(en, "Saturday 14 November") {
		t.Errorf("English date missing:\n%s", en)
	}
	id := regexpFirst(r.body, `/display/photo/([0-9a-f-]{36})`)
	if got := f.display.get("/display/photo/"+id, ""); got.status != http.StatusOK || len(got.body) == 0 {
		t.Errorf("display photo = %d", got.status)
	}
	if got := newScreen(f.box).get("/display/photo/"+id, ""); got.status != http.StatusUnauthorized {
		t.Errorf("an unpaired screen fetches a photo = %d, want 401", got.status)
	}
	if got := f.get("/display/photo/" + id); got.status != http.StatusUnauthorized {
		t.Errorf("a browser without display token fetches a photo = %d, want 401", got.status)
	}
}

func regexpFirst(s, re string) string {
	m := regexp.MustCompile(re).FindStringSubmatch(s)
	if m == nil {
		return ""
	}
	return m[1]
}

func TestScreensaverWithoutPhotosShowsAHintWithABigWayBack(t *testing.T) {
	f, c, id := saverFixture(t, "screen")
	r := f.display.get("/display/saver", "de").body
	for _, want := range []string{"Hier erscheinen bald eure Fotos", "tippt auf „Einstellungen“ und dann auf „Fotos“", `class="button big" href="/display"`, "Zurück zum Tag", "Oder tippe irgendwo auf den Bildschirm.", "13:00"} {
		if !strings.Contains(r, want) {
			t.Errorf("the hint lacks %q", want)
		}
	}
	hint := r[strings.Index(r, `class="saver-hint"`):strings.Index(r, `class="saver-when"`)]
	if strings.Contains(hint, "/photos") || strings.Contains(strings.ToLower(hint), "http") {
		t.Error("the hint shows an address to type")
	}
	// Photos exist, but not for this display: the second variant names its albums.
	empty := newAlbum(t, c, "Kindergartenabschluss Mia")
	uploaded(t, c, defaultAlbum(t, c), "a.jpg")
	hidden := uploaded(t, c, empty, "b.jpg")
	c.call(http.MethodPatch, "/photos/"+hidden, map[string]any{"hidden": true})
	c.call(http.MethodPatch, "/displays/"+id, map[string]any{"screensaver": map[string]any{"album_ids": []string{empty}}})
	r = f.display.get("/display/saver", "de").body
	if !strings.Contains(r, "Für dieses Display gibt es noch keine Fotos") || !strings.Contains(r, "„Kindergartenabschluss Mia“") {
		t.Errorf("the second hint:\n%s", r)
	}
	if en := f.display.get("/display/saver", "en").body; !strings.Contains(en, "Back to the day") {
		t.Error("English hint missing")
	}
}

func TestScreensaverNeverRunsInQuietModeOrWhenOff(t *testing.T) {
	f, c, id := saverFixture(t, "screen")
	uploaded(t, c, defaultAlbum(t, c), "a.jpg")
	if page := f.display.get("/display", "de").body; !strings.Contains(page, `data-saver-seconds="600"`) {
		t.Error("the family view does not start the screensaver after 10 minutes")
	}
	f.post("/displays/"+id+"/settings", url.Values{"quiet": {"1"}, "quiet_from": {"12:00"}, "quiet_to": {"14:00"}, "saver": {"1"}, "saver_idle": {"10"}, "saver_interval": {"1"}, "saver_all": {"1"}})
	if r := f.display.get("/display/saver", "de"); r.status != http.StatusSeeOther || r.location != "/display" {
		t.Errorf("saver in quiet mode = %d %s, want a redirect to the day", r.status, r.location)
	}
	if page := f.display.get("/display", "de").body; strings.Contains(page, "data-saver-seconds") {
		t.Error("the family view starts the screensaver in quiet mode")
	}
	f.post("/displays/"+id+"/settings", url.Values{"saver_idle": {"10"}, "saver_interval": {"1"}, "saver_all": {"1"}})
	f.post("/displays/"+id+"/settings", url.Values{"quiet_from": {"22:00"}, "quiet_to": {"06:00"}})
	if r := f.display.get("/display/saver", "de"); r.status != http.StatusSeeOther {
		t.Errorf("saver switched off = %d, want a redirect", r.status)
	}
	if got := newScreen(f.box).get("/display/saver", "de"); got.status != http.StatusSeeOther {
		t.Errorf("unpaired screen = %d, want a redirect", got.status)
	}
}

func TestBlackAndWhiteEInkMarksItsScreensaverForGreyscale(t *testing.T) {
	f, c, _ := saverFixture(t, "eink_bw")
	uploaded(t, c, defaultAlbum(t, c), "a.jpg")
	r := f.display.get("/display/saver", "de")
	if !strings.Contains(r.body, `data-display-type="eink_bw"`) || !strings.Contains(r.body, `data-interval="900"`) {
		t.Errorf("E-Ink page:\n%s", r.body)
	}
}
