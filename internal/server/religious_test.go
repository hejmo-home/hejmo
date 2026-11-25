package server_test

import (
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"testing"
	"time"
)

// Ticket 339: the religious holidays calendar: the settings page, the hub row, the events in the calendar, the
// event page, the display strip, the licences and the REST twin.

type rhBox struct{ peopleBox }

func newRhBox(t *testing.T) rhBox {
	t.Helper()
	return rhBox{newPeopleBox(t)}
}

func (b rhBox) page(path, lang string) resp {
	return b.do(http.MethodGet, path, nil, map[string]string{"Accept-Language": lang})
}

func (b rhBox) save(f url.Values, lang string) resp {
	return b.do(http.MethodPost, "/settings/religious-holidays", f, map[string]string{"Accept-Language": lang})
}

func rhForm(religions ...string) url.Values {
	f := url.Values{"religious_holidays": {"on"}}
	for _, r := range religions {
		f.Add("religions", r)
	}
	return f
}

func rhHas(t *testing.T, what, page string, want ...string) {
	t.Helper()
	for _, w := range want {
		if !strings.Contains(page, w) {
			t.Errorf("%s misses %q\n%s", what, w, page)
		}
	}
}

func rhHasNot(t *testing.T, what, page string, bad ...string) {
	t.Helper()
	for _, w := range bad {
		if strings.Contains(page, w) {
			t.Errorf("%s has %q", what, w)
		}
	}
}

func TestSettingsHubHasTheReligiousHolidaysRowRightAfterCalendarConnectionsWithItsState(t *testing.T) {
	t.Parallel()
	b := newRhBox(t)
	de := b.page("/settings", "de").body
	rhHas(t, "hub", de, `href="/settings/religious-holidays"`, "<strong>Religiöse Feiertage</strong>", "<b>Aus.</b> Feiertage eurer Religion, etwa Pessach, Ramadan, Diwali oder Vesak, im Kalender und auf den Displays.")
	if strings.Index(de, `href="/calendars"`) > strings.Index(de, `href="/settings/religious-holidays"`) || strings.Index(de, `href="/settings/religious-holidays"`) > strings.Index(de, `href="/caldav"`) {
		t.Errorf("the row is not right after Calendar connections\n%s", de)
	}
	if r := b.save(rhForm("muslim", "christian"), "de"); r.status != http.StatusOK {
		t.Fatalf("save = %d\n%s", r.status, r.body)
	}
	rhHas(t, "hub on", b.page("/settings", "de").body, "<b>An: Christentum (katholisch und evangelisch) und Islam.</b>")
	rhHas(t, "English hub", b.page("/settings", "en").body, "<strong>Religious holidays</strong>", "<b>On: Christianity (Catholic and Protestant) and Islam.</b>")
}

func TestThePageStartsOffWithNineReligionsInAlphabeticalOrderAndNoneChosen(t *testing.T) {
	t.Parallel()
	b := newRhBox(t)
	r := b.page("/settings/religious-holidays", "de")
	if r.status != http.StatusOK {
		t.Fatalf("GET = %d", r.status)
	}
	rhHas(t, "page", r.body, `<h1>Religiöse Feiertage</h1>`, `class="facts rh-now"`, "<dt>Gesetzliche Feiertage</dt>", "Bayern, aus dem Ort eures Zuhauses.", "<dd>Aus.</dd>",
		`name="religious_holidays"`, `role="switch"`, `class="crumbs"`, `<button class="button" type="submit">Speichern</button>`, "Welche Religionen feiert ihr? Du kannst mehrere wählen.")
	rhHasNot(t, "page", r.body, `checked`, `id="days"`, `details class="more"`)
	order := regexp.MustCompile(`name="religions" value="([a-z]+)"`).FindAllStringSubmatch(r.body, -1)
	var got []string
	for _, m := range order {
		got = append(got, m[1])
	}
	want := "alevi bahai buddhist christian orthodox hindu muslim jewish sikh"
	if strings.Join(got, " ") != want {
		t.Errorf("order %v, want %s", got, want)
	}
	// English sorts by the English names: Alevism, Bahá'í, Buddhism, Christianity x2, Hinduism, Islam, Judaism, Sikhism.
	en := b.page("/settings/religious-holidays", "en").body
	rhHas(t, "English", en, "<h1>Religious holidays</h1>", "Christianity: Orthodox", "Judaism")
}

func TestSwitchingOnWithoutAReligionIsAFieldErrorAtTheChoiceAndSavesNothing(t *testing.T) {
	t.Parallel()
	b := newRhBox(t)
	r := b.save(url.Values{"religious_holidays": {"on"}}, "de")
	if r.status != http.StatusUnprocessableEntity {
		t.Fatalf("status %d\n%s", r.status, r.body)
	}
	rhHas(t, "error", r.body, `class="ferr rh-ferr" id="err-religions" role="alert"`, "Wähle mindestens eine Religion.", `aria-describedby="err-religions"`, `aria-invalid="true"`)
	rhHas(t, "stored state", r.body, "<dd>Aus.</dd>")
	if h := b.page("/settings", "de").body; !strings.Contains(h, "<b>Aus.</b>") {
		t.Error("something was saved")
	}
}

func TestHinduismWithoutAHomeLocationIsAFieldErrorWithALinkToHouseholdAndKeepsTheTicks(t *testing.T) {
	t.Parallel()
	b := newRhBox(t)
	if _, err := b.db.SQL().Exec(`UPDATE households SET latitude = NULL, longitude = NULL, home_place_id = NULL`); err != nil {
		t.Fatal(err)
	}
	r := b.save(rhForm("hindu", "buddhist"), "de")
	if r.status != http.StatusUnprocessableEntity {
		t.Fatalf("status %d\n%s", r.status, r.body)
	}
	rhHas(t, "error", r.body, `id="err-religions"`, `<a href="/settings/household">Ort unter Haushalt festlegen</a>`, "Nichts wurde gespeichert.",
		`value="hindu" checked`, `value="buddhist" checked`)
	if !strings.Contains(b.page("/settings", "de").body, "<b>Aus.</b>") {
		t.Error("something was saved")
	}
	// Without Hinduism the same household can switch on.
	if r := b.save(rhForm("buddhist"), "de"); r.status != http.StatusOK {
		t.Errorf("Buddhism without a location = %d", r.status)
	}
}

func TestSavingWritesTheCalendarAndTheSuccessLineAndListsWhatIsInIt(t *testing.T) {
	t.Parallel()
	b := newRhBox(t)
	r := b.save(rhForm("muslim", "christian"), "de")
	if r.status != http.StatusOK {
		t.Fatalf("status %d\n%s", r.status, r.body)
	}
	rhHas(t, "success", r.body, `class="msg ok"`, "Religiöse Feiertage an: Christentum (katholisch und evangelisch) und Islam. ", " Feiertage bis Ende 2027 stehen jetzt im Kalender.")
	rhHas(t, "state", r.body, "<dd>An: Christentum (katholisch und evangelisch) und Islam.</dd>", `value="muslim" checked`, `value="christian" checked`)
	rhHas(t, "list", r.body, `<section aria-labelledby="days">`, `<h2 id="days">Was im Kalender steht</h2>`, `<li class="rh-rel">`, "<h3>Islam</h3>", "<h3>Christentum: katholisch und evangelisch</h3>",
		`<span class="d">29. November 2026</span><span class="n">1. Advent</span>`, "Zuckerfest (Eid al-Fitr)<small>Ramazan Bayramı, 3 Tage</small>", `<span class="d">9. – 11. März 2027</span>`,
		`<span class="vary">oder 1 Tag später</span>`, `class="rh-note"`, "<b>Diyanet</b>", `class="rh-dup"`, "Schon als gesetzliche Feiertage in Bayern im Kalender", "Mariä Himmelfahrt (in den meisten Orten)", "Karfreitag")
	// The Islamic list is already in the calendar as events.
	status, _, cals := b.api.call(http.MethodGet, "/calendars", nil)
	if status != http.StatusOK {
		t.Fatalf("calendars = %d", status)
	}
	var found bool
	for _, c := range cals {
		m := c.(map[string]any)
		if m["kind"] == "religious" && m["read_only"] == true && m["name"] == "Religiöse Feiertage" {
			found = true
		}
	}
	if !found {
		t.Errorf("no read-only religious calendar named Religiöse Feiertage: %v", cals)
	}
	// Public holidays of Bavaria are not in it twice.
	status, _, events := b.api.call(http.MethodGet, "/events?from=2027-03-01T00:00:00Z&to=2027-04-30T00:00:00Z", nil)
	if status != http.StatusOK {
		t.Fatalf("events = %d", status)
	}
	titles := map[string]bool{}
	for _, e := range events {
		titles[e.(map[string]any)["title"].(string)] = true
	}
	for _, want := range []string{"Zuckerfest (Eid al-Fitr)", "Ostersonntag", "Palmsonntag"} {
		if !titles[want] {
			t.Errorf("no %q in the calendar: %v", want, titles)
		}
	}
	for _, bad := range []string{"Karfreitag", "Ostermontag"} {
		if titles[bad] {
			t.Errorf("%q is a public holiday in Bavaria and is in the calendar twice", bad)
		}
	}
}

func TestChangingTheIslamicCalendarSaysHowManyDatesMoved(t *testing.T) {
	t.Parallel()
	b := newRhBox(t)
	b.save(rhForm("muslim"), "de")
	f := rhForm("muslim")
	f.Set("islamic_calendar", "umm_al_qura")
	r := b.save(f, "de")
	if r.status != http.StatusOK {
		t.Fatalf("status %d", r.status)
	}
	rhHas(t, "success", r.body, "Gespeichert. Die islamischen Feiertage richten sich jetzt nach Umm al-Qura; ", " Termine haben ein neues Datum.",
		`<details class="more" open>`, `value="umm_al_qura" checked`, "Islamischer Kalender: Umm al-Qura", "Nach dem Kalender <b>Umm al-Qura</b>")
}

func TestOrthodoxChristmasCalendarIsAnOptionOfTheOrthodoxChoiceOnly(t *testing.T) {
	t.Parallel()
	b := newRhBox(t)
	r := b.save(rhForm("orthodox"), "de")
	rhHas(t, "options", r.body, `<details class="more">`, "Weitere Optionen", "Orthodoxe Feste: neuer Kalender", `name="orthodox_calendar" value="revised_julian" checked`)
	rhHasNot(t, "options", r.body, `name="islamic_calendar"`)
	f := rhForm("orthodox")
	f.Set("orthodox_calendar", "julian")
	r = b.save(f, "de")
	rhHas(t, "old calendar", r.body, "Orthodoxe Feste: alter Kalender", "<span class=\"d\">7. Januar 2027</span><span class=\"n\">Weihnachten</span>")
}

func TestSwitchingOffKeepsTheChoiceAndSaysSoInWords(t *testing.T) {
	t.Parallel()
	b := newRhBox(t)
	b.save(rhForm("muslim", "christian"), "de")
	r := b.save(url.Values{}, "de")
	if r.status != http.StatusOK {
		t.Fatalf("status %d", r.status)
	}
	rhHas(t, "off", r.body, "Religiöse Feiertage aus. Sie sind aus dem Kalender verschwunden. Deine Wahl bleibt gespeichert, falls du sie wieder einschaltest.",
		"<dd>Aus. Zuletzt gewählt: Christentum (katholisch und evangelisch) und Islam.</dd>", `value="muslim" checked`)
	rhHasNot(t, "off", r.body, `id="days"`)
	if h := b.page("/settings", "de").body; !strings.Contains(h, "<b>Aus.</b>") {
		t.Error("the hub does not say off")
	}
	status, _, events := b.api.call(http.MethodGet, "/events?from=2027-03-01T00:00:00Z&to=2027-04-30T00:00:00Z", nil)
	if status != http.StatusOK || len(events) != 0 {
		t.Errorf("%d events after switching off", len(events))
	}
}

func TestChildrenAndSignedOutVisitorsCannotOpenThePage(t *testing.T) {
	t.Parallel()
	b := newRhBox(t)
	b.add(t, "Sebastian", "child")
	b.logout()
	if r := b.get("/settings/religious-holidays"); r.status != http.StatusSeeOther || !strings.HasPrefix(r.location, "/login") {
		t.Errorf("signed out = %d %q", r.status, r.location)
	}
}

func TestTheSettingsApiCarriesTheChoiceAndRefusesWhatThePageRefuses(t *testing.T) {
	t.Parallel()
	b := newRhBox(t)
	status, got, _ := b.api.call(http.MethodGet, "/settings", nil)
	rel, _ := got["religious_holidays"].(map[string]any)
	if status != http.StatusOK || rel["on"] != false || rel["islamic_calendar"] != "diyanet" || rel["orthodox_calendar"] != "revised_julian" || len(rel["religions"].([]any)) != 0 {
		t.Fatalf("GET /settings = %d %v", status, got)
	}
	// Switching on without a religion is a 422 and nothing else of the request is saved.
	status, body, _ := b.api.call(http.MethodPatch, "/settings", map[string]any{"time_format": "12h", "religious_holidays": map[string]any{"on": true}})
	if status != http.StatusUnprocessableEntity || !strings.Contains(body["error"].(string), "religions") {
		t.Errorf("on without a religion = %d %v", status, body)
	}
	_, got, _ = b.api.call(http.MethodGet, "/settings", nil)
	if got["time_format"] != "24h" {
		t.Error("the refused request saved the time format")
	}
	for _, bad := range []map[string]any{{"religions": []string{"pastafarian"}}, {"islamic_calendar": "moon"}, {"orthodox_calendar": "gregorian"}} {
		bad["on"] = true
		if bad["religions"] == nil {
			bad["religions"] = []string{"muslim"}
		}
		if status, _, _ := b.api.call(http.MethodPatch, "/settings", map[string]any{"religious_holidays": bad}); status != http.StatusUnprocessableEntity {
			t.Errorf("%v = %d", bad, status)
		}
	}
	status, got, _ = b.api.call(http.MethodPatch, "/settings", map[string]any{"religious_holidays": map[string]any{"on": true, "religions": []string{"muslim", "jewish"}, "islamic_calendar": "umm_al_qura"}})
	rel, _ = got["religious_holidays"].(map[string]any)
	if status != http.StatusOK || rel["on"] != true || rel["islamic_calendar"] != "umm_al_qura" || len(rel["religions"].([]any)) != 2 {
		t.Fatalf("PATCH = %d %v", status, got)
	}
	// Off keeps the choice.
	_, got, _ = b.api.call(http.MethodPatch, "/settings", map[string]any{"religious_holidays": map[string]any{"on": false}})
	rel = got["religious_holidays"].(map[string]any)
	if rel["on"] != false || len(rel["religions"].([]any)) != 2 {
		t.Errorf("off: %v", rel)
	}
}

func TestTheSettingsApiRefusesHinduismWithoutALocationUnlessTheSameRequestGivesOne(t *testing.T) {
	t.Parallel()
	b := newRhBox(t)
	if _, err := b.db.SQL().Exec(`UPDATE households SET latitude = NULL, longitude = NULL, home_place_id = NULL`); err != nil {
		t.Fatal(err)
	}
	status, body, _ := b.api.call(http.MethodPatch, "/settings", map[string]any{"religious_holidays": map[string]any{"on": true, "religions": []string{"hindu"}}})
	if status != http.StatusUnprocessableEntity || !strings.Contains(body["error"].(string), "home location") {
		t.Errorf("hindu without a location = %d %v", status, body)
	}
	status, _, _ = b.api.call(http.MethodPatch, "/settings", map[string]any{"latitude": 48.15, "longitude": 11.46, "home_name": "Pasing", "religious_holidays": map[string]any{"on": true, "religions": []string{"hindu"}}})
	if status != http.StatusOK {
		t.Errorf("hindu with a location in the same request = %d", status)
	}
}

func TestEventsExposeTheReadOnlyAndSightingFlags(t *testing.T) {
	t.Parallel()
	b := newRhBox(t)
	b.save(rhForm("muslim", "jewish"), "de")
	status, _, events := b.api.call(http.MethodGet, "/events?from=2027-03-01T00:00:00Z&to=2027-04-30T00:00:00Z", nil)
	if status != http.StatusOK {
		t.Fatalf("events = %d", status)
	}
	var fitr, pessach map[string]any
	for _, e := range events {
		m := e.(map[string]any)
		switch m["title"] {
		case "Zuckerfest (Eid al-Fitr)":
			fitr = m
		case "Pessach":
			pessach = m
		}
	}
	if fitr == nil || pessach == nil {
		t.Fatalf("Zuckerfest %v, Pessach %v among %d events", fitr, pessach, len(events))
	}
	if fitr["read_only"] != true || fitr["varies_by_sighting"] != true || pessach["read_only"] != true || pessach["varies_by_sighting"] != false {
		t.Errorf("Zuckerfest %v, Pessach %v", fitr, pessach)
	}
	// A family event is neither.
	status, ev, _ := b.api.call(http.MethodPost, "/events", map[string]any{"title": "Zahnarzt", "start": "2027-03-02T10:00:00+01:00", "end": "2027-03-02T11:00:00+01:00"})
	if status != http.StatusCreated || ev["read_only"] != false || ev["varies_by_sighting"] != false {
		t.Errorf("own event = %d %v", status, ev)
	}
}

func TestTheWeekShowsOrOneDayLaterOnMoonSightingDaysOnBarsAndDayItemsAndInSearch(t *testing.T) {
	t.Parallel()
	b := newRhBox(t)
	b.save(rhForm("muslim"), "de")
	week := b.page("/calendar?week=2027-03-08", "de").body
	rhHas(t, "week", week, `Zuckerfest (Eid al-Fitr)</a>`, `<span class="vary">oder 1 Tag später</span>`)
	if n := strings.Count(week, `<span class="vary">oder 1 Tag später</span>`); n < 4 { // bar plus three days, Ramadan on Monday too
		t.Errorf("%d vary pills in the week, want a bar and a day item for every day", n)
	}
	search := b.page("/calendar/search?q=Zuckerfest", "de").body
	rhHas(t, "search", search, "Zuckerfest (Eid al-Fitr)", `<span class="vary">oder 1 Tag später</span>`)
	rhHas(t, "English", b.page("/calendar?week=2027-03-08", "en").body, `<span class="vary">or 1 day later</span>`)
}

func TestTheEventPageOfAMoonSightingFestivalSpellsOutTheAlternativeDate(t *testing.T) {
	t.Parallel()
	b := newRhBox(t)
	b.save(rhForm("muslim", "jewish"), "de")
	week := b.page("/calendar?week=2027-03-08", "de").body
	href := regexp.MustCompile(`href="(/calendar/events/[^"]+)">Zuckerfest`).FindStringSubmatch(week)
	if href == nil {
		t.Fatalf("no link to the festival\n%s", week)
	}
	r := b.page(strings.ReplaceAll(href[1], "&amp;", "&"), "de")
	if r.status != http.StatusOK {
		t.Fatalf("event page = %d", r.status)
	}
	rhHas(t, "event page", r.body, `class="ev-gen"`, "<h1>Zuckerfest (Eid al-Fitr)</h1>", "Dienstag, 09.03. bis Donnerstag, 11.03.2027 · ganztägig",
		`class="notice ev-moved"`, "Gemeinden, die den neuen Mond selbst sichten, feiern oft einen Tag später: von Mittwoch, 10. März, bis Freitag, 12. März.",
		`data-readonly>Diesen Termin macht Hejmo aus den religiösen Feiertagen.`, "<dt>Kalender</dt><dd>Religiöse Feiertage</dd>", "<dt>Für</dt><dd>Ganze Familie</dd>", "<dt>Religion</dt><dd>Islam</dd>",
		"<dt>Im islamischen Kalender</dt><dd>1. bis 3. Schawwal 1448</dd>", "<dt>Wiederholung</dt>", "etwa 11 Tage früher", "<dt>Quelle</dt>", "Diyanet İşleri Başkanlığı", `<span class="vary">Noch nicht mit der Quelle abgeglichen</span>`,
		`<a href="/settings/religious-holidays">Religiöse Feiertage</a>`)
	rhHasNot(t, "event page", r.body, `name="title"`, `name="scope"`)
}

func TestTheEventPageOfAJewishFestivalNamesTheEveAndTheEveHasItsOwnEvent(t *testing.T) {
	t.Parallel()
	b := newRhBox(t)
	b.save(rhForm("jewish"), "de")
	week := b.page("/calendar?week=2027-04-19", "de").body
	rhHas(t, "week", week, "Pessach beginnt am Abend")
	href := regexp.MustCompile(`href="(/calendar/events/[^"]+)">Pessach</a>`).FindStringSubmatch(week)
	if href == nil {
		t.Fatalf("no link to Pessach\n%s", week)
	}
	r := b.page(strings.ReplaceAll(href[1], "&amp;", "&"), "de")
	rhHas(t, "page", r.body, "<h1>Pessach</h1>", "Donnerstag, 22.04. bis Donnerstag, 29.04.2027", "Pessach beginnt am Vorabend, Mittwoch, 21. April, bei Sonnenuntergang.", "„Pessach beginnt am Abend“ im Kalender",
		"<dt>Im jüdischen Kalender</dt><dd>15. bis 22. Nisan 5787</dd>", "Berechnet mit hebcal-go")
	rhHasNot(t, "page", r.body, `class="vary"`)
}

func TestTheDisplayStripShowsReligiousHolidaysAsEventsWithDayCountAndSightingNote(t *testing.T) {
	t.Parallel()
	b := newRhBox(t)
	b.save(rhForm("muslim"), "de")
	b.now = time.Date(2027, 3, 9, 12, 0, 0, 0, time.UTC)
	d := b.display
	r := d.get("/display", "de")
	if r.status != http.StatusOK {
		t.Fatalf("display = %d", r.status)
	}
	if !regexp.MustCompile(`data-kind="event"[^>]*data-bar="1/3"><span class="title">Zuckerfest \(Eid al-Fitr\)</span> <span class="meta">Tag 1/3</span> <span class="vary">oder 1 Tag später</span>`).MatchString(r.body) {
		t.Errorf("the strip has no Zuckerfest day 1/3 with the note\n%s", r.body)
	}
}

func TestLicencesListTheLibrariesAndEveryTableWithSourceRangeAndWhatIsUnchecked(t *testing.T) {
	t.Parallel()
	b := newRhBox(t)
	r := b.page("/settings/licences", "de")
	rhHas(t, "licences", r.body, `data-licence="religious_holidays"`, `class="lic-list"`, "hebcal-go, GPL-2.0 oder später", "go-hijri, MIT", "meeus v3, MIT",
		"Diyanet İşleri Başkanlığı, Tabelle der religiösen Tage 2020–2030", "Nanakshahi-Jantri der SGPC", "Tibetischer Kalender", "Alevitische Gemeinde Deutschland (AABF)",
		`<li class="unch">`, "noch nicht mit der Quelle abgeglichen")
	rhHas(t, "English", b.page("/settings/licences", "en").body, "hebcal-go, GPL-2.0 or later", "not yet checked against the source")
}

func TestStartRewritesTheCalendarSoARegionChangeIsNotLost(t *testing.T) {
	t.Parallel()
	b := newRhBox(t)
	b.save(rhForm("christian"), "de")
	// Bavaria has Corpus Christi, Saxony does not: after the region changes the calendar is written again.
	status, _, events := b.api.call(http.MethodGet, "/events?from=2027-05-20T00:00:00Z&to=2027-06-05T00:00:00Z", nil)
	if status != http.StatusOK {
		t.Fatalf("events = %d", status)
	}
	for _, e := range events {
		if e.(map[string]any)["title"] == "Fronleichnam" {
			t.Fatal("Corpus Christi is a public holiday in Bavaria and is in the calendar twice")
		}
	}
	if _, err := b.db.SQL().Exec(`UPDATE households SET region = 'DE-SN'`); err != nil {
		t.Fatal(err)
	}
	if err := b.db.SyncAllReligious(); err != nil {
		t.Fatal(err)
	}
	_, _, events = b.api.call(http.MethodGet, "/events?from=2027-05-20T00:00:00Z&to=2027-06-05T00:00:00Z", nil)
	found := false
	for _, e := range events {
		found = found || e.(map[string]any)["title"] == "Fronleichnam"
	}
	if !found {
		t.Error("Corpus Christi is missing in Saxony")
	}
}

func TestAnUnknownCalendarOrReligionIsRefusedAtTheChoiceAndSavesNothing(t *testing.T) {
	t.Parallel()
	b := newRhBox(t)
	f := rhForm("muslim")
	f.Set("islamic_calendar", "moon")
	r := b.save(f, "de")
	if r.status != http.StatusUnprocessableEntity {
		t.Fatalf("unknown calendar = %d\n%s", r.status, r.body)
	}
	rhHas(t, "error", r.body, `id="err-religions"`, "Diese Auswahl gibt es nicht. Nichts wurde gespeichert.")
	if r := b.save(rhForm("muslim", "pastafarian"), "de"); r.status != http.StatusUnprocessableEntity {
		t.Errorf("unknown religion = %d", r.status)
	}
	if h := b.page("/settings", "de").body; !strings.Contains(h, "<b>Aus.</b>") {
		t.Error("something was saved")
	}
}

func TestMCPListEventsCarriesTheSightingFlag(t *testing.T) {
	t.Parallel()
	b := newRhBox(t)
	b.save(rhForm("muslim", "jewish"), "de")
	b.api.switchMCP(t, true)
	s := b.api.mcpSession(t, b.api.newMCPToken(t, "Claude", allAreas))
	out, failed := callTool(s, "list_events", map[string]any{"from": "2027-03-01T00:00:00Z", "to": "2027-04-30T00:00:00Z"})
	if failed {
		t.Fatalf("list_events: %s", out)
	}
	flag := func(title string) string {
		m := regexp.MustCompile(`(?s)"title":\s*"` + regexp.QuoteMeta(title) + `".*?"varies_by_sighting":\s*(true|false)`).FindStringSubmatch(out)
		if m == nil {
			return "missing"
		}
		return m[1]
	}
	if got := flag("Zuckerfest (Eid al-Fitr)"); got != "true" {
		t.Errorf("Zuckerfest varies_by_sighting = %s\n%s", got, out)
	}
	if got := flag("Pessach"); got != "false" {
		t.Errorf("Pessach varies_by_sighting = %s\n%s", got, out)
	}
}
