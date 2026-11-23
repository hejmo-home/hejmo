package server_test

import (
	"errors"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"testing"

	"github.com/hejmo-home/hejmo/internal/caldavclient"
	"github.com/hejmo-home/hejmo/internal/calsync"
)

// Ticket #183 (preview docs/design/preview-183.html): the calendar cards on
// Choose calendars. The head says where the calendar lives and what it is
// called there; the field "Name in Hejmo" is the editable one. The state is
// said in words, an off card shows no fields, and an event count is
// information unless the calendar is on and empty.

// cardsBox connects two calendars, "Home" and "Work", and switches only Home
// on, under the Hejmo name "Our home".
func cardsBox(t *testing.T) (*box, string) {
	t.Helper()
	b, id := twoCalendarBox(t)
	form := url.Values{
		"calendar": {flowCalendar}, "shown": {flowCalendar, flowCalendar2},
		"name_" + flowCalendar: {"Our home"}, "color_" + flowCalendar: {"#2F5E7A"}, "interval": {"15"},
	}
	if r := b.post("/connections/"+id+"/calendars", form); r.status != http.StatusSeeOther {
		t.Fatalf("choose = %d\n%s", r.status, r.body)
	}
	return b, id
}

func getLang(b *box, path, lang string) resp {
	return b.do(http.MethodGet, path, nil, map[string]string{"Accept-Language": lang})
}

// cardsOf splits the form of the page into its calendar cards.
func cardsOf(t *testing.T, page, id string) []string {
	t.Helper()
	form := formOf(t, page, "/connections/"+id+"/calendars")
	parts := strings.Split(form, `<fieldset class="calchoice`)
	if len(parts) < 2 {
		t.Fatalf("no calendar cards in the form:\n%s", form)
	}
	var cards []string
	for _, p := range parts[1:] {
		cards = append(cards, `<fieldset class="calchoice`+p)
	}
	return cards
}

func TestCardHeadNamesTheSourceAndTheNameInHejmoIsAFieldOfItsOwn(t *testing.T) {
	t.Parallel()
	b, id := cardsBox(t)
	cards := cardsOf(t, getEN(b, "/connections/"+id).body, id)
	if len(cards) != 2 {
		t.Fatalf("%d cards", len(cards))
	}
	home := cards[0]
	head := home[:strings.Index(home, `</div>`)]
	for _, want := range []string{`<span class="src">Calendar on caldav.example.org</span>`, `<b>Home</b>`} {
		if !strings.Contains(head, want) {
			t.Errorf("head lacks %q:\n%s", want, head)
		}
	}
	if strings.Contains(head, "Our home") {
		t.Errorf("the Hejmo name is in the head, where only the name at the source belongs:\n%s", head)
	}
	for _, want := range []string{
		`Name in Hejmo<input name="name_` + flowCalendar + `" value="Our home"`,
		`Only in Hejmo. On caldav.example.org it keeps the name <q>Home</q>.`,
	} {
		if !strings.Contains(home, want) {
			t.Errorf("fields lack %q:\n%s", want, home)
		}
	}
	// The name at the source is never an input.
	if strings.Contains(head, "<input name=\"name_") || strings.Count(home, `name="name_`) != 1 {
		t.Errorf("the name at the source is editable:\n%s", home)
	}

	de := cardsOf(t, getLang(b, "/connections/"+id, "de").body, id)[0]
	for _, want := range []string{`Kalender auf caldav.example.org`, `Name in Hejmo`, `Nur in Hejmo. Auf caldav.example.org heißt er weiter <q>Home</q>.`} {
		if !strings.Contains(de, want) {
			t.Errorf("German card lacks %q:\n%s", want, de)
		}
	}
}

func TestCardKickerSaysICloudForICloud(t *testing.T) {
	t.Parallel()
	b := signedInAPI(t).b
	b.sync.SetDialer(func(string, string, string) (calsync.Remote, error) {
		return hostedRemote{found: []caldavclient.Calendar{{URL: "https://caldav.icloud.com/1/calendars/family/", Name: "Family"}}}, nil
	})
	if r := b.post("/connections", url.Values{"provider": {"icloud"}, "username": {"mama@example.org"}, "password": {"abcd-efgh-ijkl-mnop"}}); r.status != http.StatusOK {
		t.Fatalf("connect = %d\n%s", r.status, r.body)
	}
	h, _ := b.db.HomeHousehold()
	conns, _ := b.db.Connections(h.ID)
	id := conns[0].ID
	form := url.Values{"calendar": {"https://caldav.icloud.com/1/calendars/family/"}, "name_https://caldav.icloud.com/1/calendars/family/": {"Familie"}, "interval": {"15"}, "shown": {"https://caldav.icloud.com/1/calendars/family/"}}
	if r := b.post("/connections/"+id+"/calendars", form); r.status != http.StatusSeeOther {
		t.Fatalf("choose = %d\n%s", r.status, r.body)
	}
	en := cardsOf(t, getEN(b, "/connections/"+id).body, id)[0]
	for _, want := range []string{`<span class="src">Calendar in iCloud</span>`, `<b>Family</b>`, `Only in Hejmo. In iCloud it keeps the name <q>Family</q>.`} {
		if !strings.Contains(en, want) {
			t.Errorf("card lacks %q:\n%s", want, en)
		}
	}
	de := cardsOf(t, getLang(b, "/connections/"+id, "de").body, id)[0]
	for _, want := range []string{`<span class="src">Kalender in iCloud</span>`, `Nur in Hejmo. In iCloud heißt er weiter <q>Family</q>.`} {
		if !strings.Contains(de, want) {
			t.Errorf("German card lacks %q:\n%s", want, de)
		}
	}
}

func TestCardKeepsAnUnsafeSourceNameInsideItsElements(t *testing.T) {
	t.Parallel()
	b := signedInAPI(t).b
	const addr = "https://caldav.example.org/cal/x/"
	b.sync.SetDialer(func(string, string, string) (calsync.Remote, error) {
		return hostedRemote{found: []caldavclient.Calendar{{URL: addr, Name: `A & <i>B</i> "C"`}}}, nil
	})
	if r := b.post("/connections", url.Values{"provider": {"caldav"}, "url": {"https://caldav.example.org"}, "username": {"u"}, "password": {"pw-1"}}); r.status != http.StatusOK {
		t.Fatalf("connect = %d", r.status)
	}
	h, _ := b.db.HomeHousehold()
	conns, _ := b.db.Connections(h.ID)
	page := getEN(b, "/connections/"+conns[0].ID).body
	if strings.Contains(page, "<i>B</i>") || !strings.Contains(page, `<b>A &amp; &lt;i&gt;B&lt;/i&gt; &#34;C&#34;</b>`) {
		t.Errorf("the source name is not escaped in the head:\n%s", page)
	}
	if !strings.Contains(page, `<q>A &amp; &lt;i&gt;B&lt;/i&gt; &#34;C&#34;</q>`) {
		t.Errorf("the source name is not escaped in the hint:\n%s", page)
	}
}

func TestCardDoesNotClaimASourceNameWhenTheServerCannotBeAsked(t *testing.T) {
	t.Parallel()
	b, id := cardsBox(t)
	b.sync.SetDialer(func(string, string, string) (calsync.Remote, error) {
		return errRemote{err: errors.New("offline in test")}, nil
	})
	page := getEN(b, "/connections/"+id).body
	cards := cardsOf(t, page, id)
	if strings.Contains(cards[0], "it keeps the name") {
		t.Errorf("the hint names a source name the box could not read:\n%s", cards[0])
	}
	if !strings.Contains(cards[0], "Name in Hejmo") {
		t.Errorf("the field is gone:\n%s", cards[0])
	}
}

func TestCardsSayTheirStateInWordsAndTheServerMarksWhichIsOn(t *testing.T) {
	t.Parallel()
	b, id := cardsBox(t)
	for lang, want := range map[string]struct{ on, off string }{
		"en": {`<span class="w">On:</span> shown on the box`, `<span class="w">Off:</span> not on the box`},
		"de": {`<span class="w">An:</span> auf der Box sichtbar`, `<span class="w">Aus:</span> nicht auf der Box`},
	} {
		page := getLang(b, "/connections/"+id, lang).body
		cards := cardsOf(t, page, id)
		on, off := cards[0], cards[1]
		if !strings.HasPrefix(on, `<fieldset class="calchoice is-on"`) || strings.HasPrefix(off, `<fieldset class="calchoice is-on"`) {
			t.Errorf("%s: the cards do not carry their state:\n%.200s\n%.200s", lang, on, off)
		}
		if !strings.Contains(on, `<span class="state is-on">`) || strings.Contains(off, `<span class="state is-on">`) {
			t.Errorf("%s: the state is not set by the server", lang)
		}
		// Both phrases are in every card (the live switch shows one), each starting with its state word.
		for _, card := range []string{on, off} {
			if !strings.Contains(card, `<span class="on-text">`+want.on+`</span><span class="off-text">`+want.off+`</span>`) {
				t.Errorf("%s: card lacks the state words:\n%s", lang, card)
			}
		}
		// The old run-together text is gone.
		if strings.Contains(page, "Shown on the boxOff") || strings.Contains(page, "Auf der Box sichtbarAus") {
			t.Errorf("%s: the state words run together", lang)
		}
		// The fields are in every card (the live switch shows them); the stylesheet hides them
		// for a card that is not .is-on (see the next test and the rendered check).
		for _, card := range []string{on, off} {
			if !strings.Contains(card, `<div class="calfields">`) || !strings.Contains(card, `name="people_`) {
				t.Errorf("%s: a card lacks its fields:\n%s", lang, card)
			}
		}
		if !strings.Contains(off, `data-count-for="`+flowCalendar2+`"`) {
			t.Errorf("%s: the off card lacks its event count", lang)
		}
	}
}

func TestStylesheetHidesTheFieldsOfOffCardsAndShowsThemLiveWithTheSwitch(t *testing.T) {
	t.Parallel()
	// The fields of an off card are in the page but hidden until the switch goes on.
	b, _ := cardsBox(t)
	css := b.get("/static/hejmo.css").body
	for _, want := range []string{
		`.calchoice:not(.is-on) > .calfields { display: none; }`,
		`.calchoice:has(> .card-row .switch input:checked) > .calfields { display: grid; }`,
		`.calchoice:has(> .card-row .switch input:not(:checked)) > .calfields { display: none; }`,
	} {
		if !strings.Contains(css, want) {
			t.Errorf("stylesheet lacks %q", want)
		}
	}
}

func TestEventCountIsAQuietLineWithBothTextsForAnEmptyCalendar(t *testing.T) {
	t.Parallel()
	b, id := cardsBox(t)
	cards := cardsOf(t, getEN(b, "/connections/"+id).body, id)
	for _, card := range cards {
		facts := card[strings.Index(card, `<div class="calfacts">`):]
		facts = facts[:strings.Index(facts, `</div>`)]
		count := regexp.MustCompile(`<p class="count" data-count-for="[^"]+" data-state="loading">`)
		if !count.MatchString(facts) {
			t.Errorf("the count is not a calm line of the facts:\n%s", facts)
		}
		for _, want := range []string{
			`<span class="ct">The number of events is shown when JavaScript is on.</span>`,
			`<span class="info-text">No events in the last 12 months or later</span>`,
			`<span class="warn-text">Shows nothing on the box right now: no events in the last 12 months or later</span>`,
		} {
			if !strings.Contains(facts, want) {
				t.Errorf("count lacks %q:\n%s", want, facts)
			}
		}
		if strings.Contains(card, `class="hint count"`) {
			t.Error("the count is still a hint pill")
		}
	}
	// Only the card that is synced has a last sync line.
	if !strings.Contains(cards[0], "Last synced") && !strings.Contains(cards[0], "Not synced yet") {
		t.Errorf("the on card lacks its sync line:\n%s", cards[0])
	}
	if strings.Contains(cards[1], "synced") {
		t.Errorf("the off card has a sync line:\n%s", cards[1])
	}
	de := getLang(b, "/connections/"+id, "de").body
	for _, want := range []string{"Zeigt zurzeit nichts auf der Box: keine Termine in den letzten 12 Monaten oder später"} {
		if !strings.Contains(de, want) {
			t.Errorf("German page lacks %q", want)
		}
	}
}

func TestCardColourSitsBesideMoreOptionsWithItsSwatch(t *testing.T) {
	t.Parallel()
	b, id := cardsBox(t)
	home := cardsOf(t, getEN(b, "/connections/"+id).body, id)[0]
	want := `More options<span class="sum"><span class="swatch" style="--sw:#2F5E7A"></span>Colour</span>`
	if !strings.Contains(home, want) {
		t.Errorf("summary lacks %q:\n%s", want, home)
	}
}

func TestIntervalBelongsToAllCalendarsAndSaysItsValue(t *testing.T) {
	t.Parallel()
	b, id := cardsBox(t)
	page := getEN(b, "/connections/"+id).body
	if !strings.Contains(page, `<details class="more all-cals">`) ||
		!strings.Contains(page, `More options for all calendars<span class="sum">update every 5 minutes</span>`) {
		t.Errorf("the interval is not marked as for all calendars:\n%s", formOf(t, page, "/connections/"+id+"/calendars"))
	}
	de := getLang(b, "/connections/"+id, "de").body
	if !strings.Contains(de, `Weitere Optionen für alle Kalender<span class="sum">alle 5 Minuten aktualisieren</span>`) {
		t.Error("German interval summary missing")
	}
}

func TestNothingSwitchedOnShowsTheErrorUnderTheFirstCardsSwitch(t *testing.T) {
	t.Parallel()
	b, id := twoCalendarBox(t)
	form := url.Values{"interval": {"60"}, "shown": {flowCalendar, flowCalendar2}}
	r := b.do(http.MethodPost, "/connections/"+id+"/calendars", form, map[string]string{"Accept-Language": "en"})
	if r.status != http.StatusUnprocessableEntity {
		t.Fatalf("status %d\n%s", r.status, r.body)
	}
	cards := cardsOf(t, r.body, id)
	if !strings.HasPrefix(cards[0], `<fieldset class="calchoice invalid"`) || strings.Contains(cards[1], "invalid") {
		t.Errorf("only the first card is marked:\n%.160s\n%.160s", cards[0], cards[1])
	}
	if !strings.Contains(cards[0], `<p class="ferr" id="err-calendar" role="alert">`) || !strings.Contains(cards[0], "Please switch on at least one calendar.") {
		t.Errorf("the error is not under the first card's switch:\n%s", cards[0])
	}
	if strings.Contains(r.body, `<p class="error"`) || strings.Count(r.body, `class="ferr"`) != 1 {
		t.Error("the error is also shown in the general line, or twice")
	}
	if !strings.Contains(cards[0], `aria-invalid="true" aria-describedby="err-calendar"`) {
		t.Errorf("the switch is not tied to its error:\n%s", cards[0])
	}
}

func TestAServerWithoutCalendarsGetsOneCalmCardAndNoSaveButton(t *testing.T) {
	t.Parallel()
	b := signedInAPI(t).b
	b.sync.SetDialer(func(string, string, string) (calsync.Remote, error) { return hostedRemote{}, nil })
	if r := b.post("/connections", url.Values{"provider": {"icloud"}, "username": {"mama@example.org"}, "password": {"abcd-efgh-ijkl-mnop"}}); r.status != http.StatusOK {
		t.Fatalf("connect = %d\n%s", r.status, r.body)
	}
	h, _ := b.db.HomeHousehold()
	conns, _ := b.db.Connections(h.ID)
	for lang, want := range map[string][]string{
		"en": {`<h2>Choose calendars</h2>`, `<div class="choose-none">`, `<strong>No calendars were found on this server.</strong>`, `Create a calendar in iCloud; it appears here when you reload the page.`},
		"de": {`<strong>Auf diesem Server wurden keine Kalender gefunden.</strong>`, `Lege in iCloud einen Kalender an; er erscheint hier, sobald du die Seite neu lädst.`},
	} {
		page := getLang(b, "/connections/"+conns[0].ID, lang).body
		for _, w := range want {
			if !strings.Contains(page, w) {
				t.Errorf("%s: page lacks %q", lang, w)
			}
		}
		if strings.Contains(page, `/calendars" class="steps"`) {
			t.Errorf("%s: a form without calendars is offered", lang)
		}
	}
}

func TestAServerWithoutCalendarsNamesItsHostInTheHint(t *testing.T) {
	t.Parallel()
	b := signedInAPI(t).b
	b.sync.SetDialer(func(string, string, string) (calsync.Remote, error) { return hostedRemote{}, nil })
	if r := b.post("/connections", url.Values{"provider": {"caldav"}, "url": {"https://caldav.example.org"}, "username": {"u"}, "password": {"pw-1"}}); r.status != http.StatusOK {
		t.Fatalf("connect = %d", r.status)
	}
	h, _ := b.db.HomeHousehold()
	conns, _ := b.db.Connections(h.ID)
	page := getEN(b, "/connections/"+conns[0].ID).body
	if !strings.Contains(page, `Create a calendar on caldav.example.org; it appears here when you reload the page.`) {
		t.Errorf("hint lacks the host:\n%s", page)
	}
}
