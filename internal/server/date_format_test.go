package server_test

import (
	"time"

	"github.com/hejmo-home/hejmo/internal/i18n"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// Ticket #147: the household page offers the date format first, with the sample
// date 24 December in numbers and in words, and one hint.
func TestSettingsScreenOffersTheDateFormatFirst(t *testing.T) {
	b := signedInBox(t)
	en := b.do(http.MethodGet, "/settings/household", nil, map[string]string{"Accept-Language": "en"}).body
	year := "2026" // the test clock stands at 14/11/2026
	for _, want := range []string{
		`<fieldset class="seg"><legend>Date format</legend>`,
		`<p class="hint">For every date written in numbers: calendar, tasks, meals, notes, phones and sync. Dates in words, like on the displays, stay as they are.</p>`,
		`<input type="radio" name="date_format" value="dmy" checked><b>24.12.` + year + `</b><small>Day first: 24` + "\u00a0" + `December` + "\u00a0" + year + `</small>`,
		`<input type="radio" name="date_format" value="mdy"><b>12/24/` + year + `</b><small>Month first: 24` + "\u00a0" + `December` + "\u00a0" + year + `</small>`,
	} {
		if !strings.Contains(en, want) {
			t.Fatalf("settings page lacks %q:\n%s", want, en)
		}
	}
	if strings.Index(en, `name="date_format"`) > strings.Index(en, `name="time_format"`) {
		t.Error("date format does not come before time format")
	}
	de := b.do(http.MethodGet, "/settings/household", nil, map[string]string{"Accept-Language": "de"}).body
	for _, want := range []string{"<legend>Datumsformat</legend>", "Für jedes Datum in Ziffern: Kalender, Aufgaben, Essen, Notizen, Handys und Synchronisierung.", "Tag zuerst: 24.\u00a0Dezember\u00a0" + year, "Monat zuerst: 24.\u00a0Dezember\u00a0" + year} {
		if !strings.Contains(de, want) {
			t.Errorf("German settings page lacks %q", want)
		}
	}
}

func TestSettingsScreenSavesTheDateFormat(t *testing.T) {
	b := signedInBox(t)
	if r := b.post("/settings/household", url.Values{"date_format": {"mdy"}, "time_format": {"24h"}, "temperature_unit": {"c"}}); r.status != http.StatusSeeOther || r.location != "/settings/household" {
		t.Fatalf("save = %d %q", r.status, r.location)
	}
	if body := b.get("/settings/household").body; !strings.Contains(body, `value="mdy" checked`) {
		t.Fatalf("saved value not selected:\n%s", body)
	}
	// A form without the field leaves the setting alone.
	b.post("/settings/household", url.Values{"time_format": {"12h"}, "temperature_unit": {"c"}})
	if h, _ := b.db.HomeHousehold(); h.DateFormat != "mdy" {
		t.Fatalf("date format = %q after a form without the field", h.DateFormat)
	}
}

func TestSettingsScreenRefusesAnUnknownDateFormatAtTheField(t *testing.T) {
	b := signedInBox(t)
	r := b.do(http.MethodPost, "/settings/household", url.Values{"date_format": {"ymd"}, "time_format": {"24h"}, "temperature_unit": {"c"}}, map[string]string{"Accept-Language": "en"})
	if r.status != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422", r.status)
	}
	for _, want := range []string{`<fieldset class="seg" aria-invalid="true" aria-describedby="err-date_format">`, `id="err-date_format"`, "Choose one of the two date formats."} {
		if !strings.Contains(r.body, want) {
			t.Errorf("page lacks %q:\n%s", want, r.body)
		}
	}
	if regexp.MustCompile(`name="date_format"[^>]* checked`).MatchString(r.body) {
		t.Error("an option is checked after a refused value")
	}
	if h, _ := b.db.HomeHousehold(); h.DateFormat != "dmy" {
		t.Errorf("stored %q", h.DateFormat)
	}
}

func TestDateFormatWorksThroughTheAPI(t *testing.T) {
	c := signedInAPI(t)
	status, got, _ := c.call(http.MethodGet, "/settings", nil)
	if status != http.StatusOK || got["date_format"] != "dmy" {
		t.Fatalf("get = %d %v", status, got)
	}
	status, got, _ = c.call(http.MethodPatch, "/settings", map[string]any{"date_format": "mdy"})
	if status != http.StatusOK || got["date_format"] != "mdy" || got["time_format"] != "24h" {
		t.Fatalf("patch = %d %v", status, got)
	}
	if status, _, _ := c.call(http.MethodPatch, "/settings", map[string]any{"date_format": "ymd"}); status != http.StatusBadRequest {
		t.Fatalf("bad date format = %d, want 400", status)
	}
	if _, got, _ = c.call(http.MethodGet, "/settings", nil); got["date_format"] != "mdy" {
		t.Fatalf("a refused patch changed the setting: %v", got)
	}
}

// useDateFormat sets the household's date format the way a parent does.
func useDateFormat(t *testing.T, b *box, format string) {
	t.Helper()
	if r := b.post("/settings/household", url.Values{"date_format": {format}, "time_format": {"24h"}, "temperature_unit": {"c"}}); r.status != http.StatusSeeOther {
		t.Fatalf("settings = %d", r.status)
	}
}

// Ticket #147: every date in numbers follows the setting. The test clock stands
// at Saturday 14/11/2026, 13:00 in Berlin.
func TestDatesInNumbersFollowTheHouseholdDateFormat(t *testing.T) {
	formats := []struct {
		name, setting                                         string
		week, weekDay, weekRange, mealWeek, mealDay, taskDate string
	}{
		{"day first", "dmy", "09.11. – 15.11.2026", "<span class=\"dd\">14.11.</span>", "09.11. to 15.11.2026", "09.11. – 15.11.2026", `<span class="date">14.11.</span>`, "Once on 25.12.2026"},
		{"month first", "mdy", "11/09 – 11/15/2026", "<span class=\"dd\">11/14</span>", "11/09 to 11/15/2026", "11/09 – 11/15/2026", `<span class="date">11/14</span>`, "Once on 12/25/2026"},
	}
	for _, f := range formats {
		t.Run(f.name, func(t *testing.T) {
			tb := newTaskBox(t)
			b := tb.parent.b
			useDateFormat(t, b, f.setting)
			if r := b.post("/tasks", url.Values{"person": {tb.miaID}, "title": {"Tidy"}, "repeat": {"once"}, "date": {"2026-12-25"}}); r.status != http.StatusSeeOther {
				t.Fatalf("task = %d\n%s", r.status, r.body)
			}
			en := map[string]string{"Accept-Language": "en"}
			for path, wants := range map[string][]string{
				"/calendar": {f.weekRange, f.weekDay},
				"/meals":    {f.mealWeek, f.mealDay},
				"/tasks":    {f.taskDate},
			} {
				body := b.do(http.MethodGet, path, nil, en).body
				for _, want := range wants {
					if !strings.Contains(body, want) {
						t.Errorf("%s lacks %q:\n%s", path, want, body)
					}
				}
			}
		})
	}
}

// A week across New Year names the year at both ends.
func TestAWeekAcrossNewYearNamesBothYears(t *testing.T) {
	b := signedInBox(t)
	for setting, want := range map[string]string{"dmy": "28.12.2026 to 03.01.2027", "mdy": "12/28/2026 to 01/03/2027"} {
		useDateFormat(t, b, setting)
		if body := b.do(http.MethodGet, "/calendar?week=2026-12-30", nil, map[string]string{"Accept-Language": "en"}).body; !strings.Contains(body, want) {
			t.Errorf("%s: calendar lacks %q:\n%s", setting, want, body)
		}
		if body := b.do(http.MethodGet, "/meals?week=2026-12-30", nil, map[string]string{"Accept-Language": "en"}).body; !strings.Contains(body, strings.Replace(want, " to ", " – ", 1)) {
			t.Errorf("%s: meals lack the year change", setting)
		}
	}
}

func TestHomeAndNotePageFollowTheDateFormat(t *testing.T) {
	f := newFamilyBox(t, "eink_color")
	f.save("/notes", url.Values{"day": {"2026-11-16"}, "text": {"Mia: Schwimmsachen"}}, "en")
	useDateFormat(t, f.box, "mdy")
	en := f.page("en", "/").body
	for _, want := range []string{"Monday 11/16", "Sat 11/14", "Sun 11/15"} {
		if !strings.Contains(en, want) {
			t.Errorf("home lacks %q", want)
		}
	}
	if note := f.page("en", "/notes/2026-11-16").body; !strings.Contains(note, "Note for Monday, 11/16") {
		t.Errorf("note page title:\n%s", note)
	}
}

// The QR code of a phone pairing says until when it is valid, and the sync
// problems carry the date with the time and seconds.
func TestPhoneExpiryAndSyncLogFollowTheDateFormat(t *testing.T) {
	b, cal := icsBox(t)
	useDateFormat(t, b, "mdy")
	r := b.post("/phones/pairings", url.Values{})
	if !regexp.MustCompile(`\d\d/\d\d/\d{4}\x{00a0}\d\d:\d\d`).MatchString(r.body) || regexp.MustCompile(`\d\d\.\d\d\.\d{4}`).MatchString(r.body) {
		t.Errorf("phone pairing does not write month first:\n%s", r.body)
	}
	h, _ := b.db.HomeHousehold()
	if err := b.db.FailSync(h.ID, cal.ID, i18n.Msg("sync.fail.status", "status", "503"), b.now, b.now.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if page := getEN(b, "/calendars").body; !strings.Contains(page, "11/14/2026\u00a021:46:26") {
		t.Errorf("sync log is not month first:\n%s", page)
	}
}

// Machine formats keep ISO 8601; no hand-built numeric layout remains outside datefmt.
func TestNoHandBuiltNumericDateLayoutsRemainInTheServer(t *testing.T) {
	files, _ := filepath.Glob("*.go")
	for _, name := range files {
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		src, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		for _, bad := range []string{`"02/01`, `"01/02`, `02.01.2006`} {
			if strings.Contains(string(src), bad) {
				t.Errorf("%s hand-builds a date layout (%s): use internal/datefmt", name, bad)
			}
		}
	}
}

// dayFirstDate is a date in numbers the way day first writes it: 17.11. or 17.11.2026.
var dayFirstDate = regexp.MustCompile(`\b\d{2}\.\d{2}\.(\d{4})?`)

// Month first on the pages the first round missed: event, search, star log and
// the CalDAV lists. Nothing on them is left day first.
func TestEventSearchStarLogAndCalDAVListsFollowMonthFirst(t *testing.T) {
	f := calendarBox(t)
	f.postEvent(t, eventForm("Dentist", "2026-11-17", "10:00", "11:00"))
	f.box.post("/tasks/stars", url.Values{"person": {f.people["Mia"].ID}, "delta": {"3"}, "note": {"weekend chores"}})
	f.box.post("/caldav/tokens", url.Values{"name": {"iPhone Mama"}})
	useDateFormat(t, f.box, "mdy")
	week := getEN(f.box, "/calendar?week=2026-11-16").body
	// The event page shows its dates in date fields (ISO), so it only has to be free of day-first dates.
	pages := map[string]*regexp.Regexp{
		"/calendar/search?q=dentist": regexp.MustCompile(`11/17`),
		"/tasks":                     regexp.MustCompile(`11/14`),
		"/caldav":                    regexp.MustCompile(`iPhone Mama`),
		linkTo(t, week, "Dentist"):   nil,
	}
	for path, want := range pages {
		body := getEN(f.box, path).body
		if want != nil && !want.MatchString(body) {
			t.Errorf("%s lacks %s:\n%s", path, want, body)
		}
		if m := dayFirstDate.FindString(body); m != "" {
			t.Errorf("%s still has the day-first date %q", path, m)
		}
	}
}

// The displays keep dates in words: switching the format changes nothing on them.
func TestDisplaysStayFreeOfNumericDatesWhenTheFormatSwitches(t *testing.T) {
	f := newFamilyBox(t, "screen")
	f.event(t, "Dentist", f.at(16, 10, 0), f.at(16, 11, 0), false, "Mia")
	paths := []string{"/display", "/display/view"}
	before := map[string]string{}
	for _, p := range paths {
		before[p] = f.display.get(p, "en").body
		if m := dayFirstDate.FindString(before[p]); m != "" {
			t.Errorf("%s has the numeric date %q", p, m)
		}
	}
	useDateFormat(t, f.box, "mdy")
	for _, p := range paths {
		if after := f.display.get(p, "en").body; after != before[p] {
			t.Errorf("%s changed with the date format", p)
		}
	}
}

// The detail pages say their dates through datefmt too: a connection, a
// display, a phone and a token, in both formats.
func TestDetailPagesFollowTheDateFormat(t *testing.T) {
	b := signedInBox(t)
	connID := chosenConnection(t, b)
	displayID := pairedDisplay(t, b, "Kitchen", "screen")
	pairedPhone(t, b, personNamed(t, b, "Mama"), "Mama's phone")
	h, _ := b.db.HomeHousehold()
	phones, _ := b.db.Phones(h.ID)
	tokenID, _ := newDeviceToken(t, b, "iPhone Mama")
	pages := []string{"/connections/" + connID, "/displays/" + displayID, "/phones/" + phones[0].ID, "/caldav/tokens/" + tokenID}
	// The token is stamped with the real clock, so its date is matched by shape.
	dmy := regexp.MustCompile(`\b\d{2}\.\d{2}\.\d{4}\b`)
	mdy := regexp.MustCompile(`\b\d{2}/\d{2}/\d{4}\b`)
	for _, format := range []string{"dmy", "mdy"} {
		useDateFormat(t, b, format)
		want, other := dmy, mdy
		if format == "mdy" {
			want, other = mdy, dmy
		}
		for _, page := range pages {
			body := getEN(b, page).body
			if !want.MatchString(body) {
				t.Errorf("%s (%s) has no date in that format:\n%s", page, format, body)
			}
			if m := other.FindString(body); m != "" {
				t.Errorf("%s (%s) has the other format's date %q", page, format, m)
			}
		}
	}
}
