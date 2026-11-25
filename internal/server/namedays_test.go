package server_test

import (
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"testing"
)

// Ticket 318: name days in the Birthdays calendar.

func nameDays(t *testing.T, c ctBox, on bool, list string) resp {
	t.Helper()
	f := url.Values{}
	if on {
		f.Set("name_days", "1")
	}
	if list != "" {
		f.Set("name_day_list", list)
	}
	return c.send("/contacts/birthdays", f)
}

// nameDaysOn switches name days on and returns the page that follows.
func nameDaysOn(t *testing.T, c ctBox, list string) string {
	t.Helper()
	if r := nameDays(t, c, true, list); r.status != http.StatusSeeOther || r.location != "/contacts/birthdays" {
		t.Fatalf("save = %d %q\n%s", r.status, r.location, r.body)
	}
	return c.page("/contacts/birthdays", "de").body
}

func TestBirthdaysPageIsForParents(t *testing.T) {
	t.Parallel()
	b, _ := configuredBox(t)
	if r := b.get("/contacts/birthdays"); r.status != http.StatusSeeOther || !strings.HasPrefix(r.location, "/login") {
		t.Fatalf("signed out = %d %q", r.status, r.location)
	}
	child := childSession(t, signedInBox(t))
	if r := child.get("/contacts/birthdays"); r.status != http.StatusForbidden {
		t.Errorf("child GET = %d, want 403", r.status)
	}
	if r := child.post("/contacts/birthdays", url.Values{"name_days": {"1"}, "name_day_list": {"catholic"}}); r.status != http.StatusForbidden {
		t.Errorf("child POST = %d, want 403", r.status)
	}
}

func TestBirthdaysPageStartsOffWithoutAChoice(t *testing.T) {
	t.Parallel()
	c := newCtBox(t)
	page := c.page("/contacts/birthdays", "de").body
	ctHas(t, "page", page, `<h1>Geburtstagskalender</h1>`, `<li><a href="/settings">Einstellungen</a></li>`, `<a href="/contacts">Kontakte</a>`, `<span aria-current="page">Geburtstagskalender</span>`,
		`<dl class="facts bd-now">`, `<dt>Namenstage</dt><dd>Aus.</dd>`, `<fieldset class="nd">`, `name="name_days"`,
		`Aus: nur Geburtstage`, `<p class="grp">Katholisch und evangelisch</p>`, `<p class="grp">Orthodox</p>`,
		`value="catholic"`, `value="protestant"`, `value="romanian_orthodox"`, `value="greek_orthodox"`, `<button class="button" type="submit">Speichern</button>`)
	ctHasNot(t, "page", page, ` checked`, `nd-rows`, `nd-names`, `class="ferr"`)
	ctHas(t, "English", c.page("/contacts/birthdays", "en").body, "<h1>Birthdays calendar</h1>", "Off: birthdays only", "Greek Orthodox")
}

func TestSwitchingNameDaysOnNeedsAChurchAndShowsTheErrorAtTheChoice(t *testing.T) {
	t.Parallel()
	c := newCtBox(t)
	r := nameDays(t, c, true, "")
	if r.status != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d", r.status)
	}
	ctHas(t, "error", r.body, `class="ferr" id="err-name_day_list"`, "Choose a church. Otherwise Hejmo does not know on which days your name days are.", `name="name_days" value="1" checked`, `aria-describedby="err-name_day_list"`)
	if strings.Contains(c.page("/contacts/birthdays", "de").body, `name="name_days" value="1" checked`) {
		t.Error("the setting was saved without a church")
	}
}

func TestSavingNameDaysSaysHowManyHaveOne(t *testing.T) {
	t.Parallel()
	c := newCtBox(t)
	f := contactForm("Opa", "Thomas Maier", nil, nil)
	c.add(t, f)
	c.add(t, contactForm("Kinderarzt", "", nil, nil))
	page := nameDaysOn(t, c, "catholic")
	ctHas(t, "page", page, `Namenstage an, nach den katholischen Namenstagen.`, `<dt>Namenstage</dt><dd>An, nach den katholischen Namenstagen.</dd>`, `name="name_day_list" value="catholic" checked`,
		`<h2 id="fam">Namenstage in eurer Familie</h2>`, `class="list-rows nd-rows"`)
	m := regexp.MustCompile(`(\d+) von (\d+) Personen und (\d+) weitere Kontakte haben jetzt ihren Namenstag im Kalender`).FindStringSubmatch(page)
	if m == nil || m[3] != "1" {
		t.Errorf("success line = %v\n%s", m, page)
	}
	// Further contacts without a name day are listed by name, never as a count.
	ctHas(t, "others", page, `<h2 id="miss">Weitere Kontakte ohne Namenstag</h2>`, `<ul class="nd-names">`, `>Kinderarzt</a>`, `Alle anderen Kontakte haben ihren Namenstag im Kalender.`)
	ctHasNot(t, "others", page, `>Opa</a>`)
}

func TestSwitchingNameDaysOffKeepsBirthdays(t *testing.T) {
	t.Parallel()
	c := newCtBox(t)
	nameDaysOn(t, c, "catholic")
	if r := nameDays(t, c, false, "catholic"); r.status != http.StatusSeeOther {
		t.Fatalf("off = %d\n%s", r.status, r.body)
	}
	page := c.page("/contacts/birthdays", "de").body
	ctHas(t, "page", page, "Namenstage aus. Die Geburtstage bleiben im Kalender.", `<dt>Namenstage</dt><dd>Aus.</dd>`, `name="name_day_list" value="catholic" checked`)
	ctHasNot(t, "page", page, "nd-rows")
}

func TestChangingTheChurchNamesTheContactsWhoseChoiceWentAway(t *testing.T) {
	t.Parallel()
	c := newCtBox(t)
	f := contactForm("Ben", "Ben Maier", nil, nil)
	id := c.add(t, f)
	nameDaysOn(t, c, "catholic")
	f.Set("name_day", "name")
	f.Set("name_day_name", "Benedikt")
	if r := c.send("/contacts/"+id+"/edit", f); r.status != http.StatusSeeOther {
		t.Fatalf("choose = %d\n%s", r.status, r.body)
	}
	page := nameDaysOn(t, c, "greek_orthodox")
	ctHas(t, "page", page, "Namenstage jetzt nach den griechisch-orthodoxen Namenstagen.", "stehen nicht mehr im Kalender: Ben.")
	// The same church again changes nothing.
	page = nameDaysOn(t, c, "greek_orthodox")
	ctHasNot(t, "page", page, "stehen nicht mehr im Kalender")
}

func TestContactFormHasTheCardNamenstagOnlyWhileNameDaysAreOn(t *testing.T) {
	t.Parallel()
	c := newCtBox(t)
	id := c.add(t, contactForm("Opa", "Thomas Maier", nil, nil))
	if strings.Contains(c.page("/contacts/"+id+"/edit", "de").body, `id="nameday"`) {
		t.Error("the card is there while name days are off")
	}
	nameDaysOn(t, c, "catholic")
	page := c.page("/contacts/"+id+"/edit", "de").body
	ctHas(t, "form", page, `<fieldset id="nameday" class="nd-pick">`, `<legend>Namenstag</legend>`, `Aus dem Vornamen in „Thomas Maier“.`,
		`name="name_day" value="auto" checked`, `<span class="tdate">3. Juli</span> · Thomas`, `Der übliche Tag`, `name="name_day" value="entry:thomas-01-28"`,
		`Nach einem anderen Namen`, `Kein Namenstag`, `name="name_day_name" type="text" list="nd-list" maxlength="40"`, `<datalist id="nd-list">`, `<option value="Benedikt">`,
		`class="nd-src"`, `href="/contacts/birthdays"`)
	if strings.Index(page, `id="birthday"`) > strings.Index(page, `id="nameday"`) || strings.Index(page, `id="nameday"`) > strings.Index(page, `id="groups"`) {
		t.Error("the card does not stand after Geburtstag")
	}
}

func TestContactWithoutAMatchStartsOnNoNameDayAndSaysWhy(t *testing.T) {
	t.Parallel()
	c := newCtBox(t)
	id := c.add(t, contactForm("Mia2", "Mia Maier", nil, nil))
	nameDaysOn(t, c, "catholic")
	page := c.page("/contacts/"+id+"/edit", "de").body
	ctHas(t, "form", page, `name="name_day" value="none" checked`, `Die katholischen Namenstage kennen „Mia“ nicht. Darum hat Mia2 noch keinen Namenstag.`, `class="notice"`, `„Mia“ ist oft eine Kurzform von „Maria“`)
	ctHasNot(t, "form", page, `name="name_day" value="auto"`)
	// Saving it as it stands does not pin the contact to "none": a new name finds its day again.
	f := contactForm("Mia2", "Mia Maier", nil, nil)
	f.Set("name_day", "none")
	c.send("/contacts/"+id+"/edit", f)
	f = contactForm("Mia2", "Maria Maier", nil, nil)
	c.send("/contacts/"+id+"/edit", f)
	ctHas(t, "contact", c.page("/contacts/"+id, "de").body, "15.08. · Maria")
}

func TestAnUnknownOtherNameIsAFieldErrorAndKeepsWhatWasTyped(t *testing.T) {
	t.Parallel()
	c := newCtBox(t)
	id := c.add(t, contactForm("Mia2", "Mia Maier", nil, nil))
	nameDaysOn(t, c, "catholic")
	f := contactForm("Mia2", "Mia Maier", nil, nil)
	f.Set("name_day", "name")
	f.Set("name_day_name", "Mariella")
	r := c.send("/contacts/"+id+"/edit", f)
	if r.status != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d", r.status)
	}
	r.body = strings.ReplaceAll(r.body, "&#34;", `"`)
	ctHas(t, "form", r.body, `<a href="#nameday">Name day</a>`, `id="err-nd_name"`, `The Catholic name days do not know the name “Mariella”.`, `value="Mariella"`, `name="name_day" value="name" checked`)
	f.Set("name_day_name", "")
	if r := c.send("/contacts/"+id+"/edit", f); r.status != http.StatusUnprocessableEntity {
		t.Errorf("an empty name = %d", r.status)
	}
	f.Set("name_day_name", strings.Repeat("a", 41))
	if r := c.send("/contacts/"+id+"/edit", f); r.status != http.StatusUnprocessableEntity {
		t.Errorf("a long name = %d", r.status)
	}
}

func TestContactPageShowsTheNameDayAfterTheBirthday(t *testing.T) {
	t.Parallel()
	c := newCtBox(t)
	f := contactForm("Oma Gerda", "Gerda Müller", nil, nil)
	f.Set("bday_day", "14")
	f.Set("bday_month", "3")
	id := c.add(t, f)
	ctHasNot(t, "page", c.page("/contacts/"+id, "de").body, "<dt>Namenstag</dt>")
	nameDaysOn(t, c, "catholic")
	ctHasNot(t, "no match", c.page("/contacts/"+id, "de").body, "<dt>Namenstag</dt>")
	f.Set("name_day", "name")
	f.Set("name_day_name", "Gertrud")
	if r := c.send("/contacts/"+id+"/edit", f); r.status != http.StatusSeeOther {
		t.Fatalf("save = %d\n%s", r.status, r.body)
	}
	page := c.page("/contacts/"+id, "de").body
	ctHas(t, "page", page, "<dt>Namenstag</dt><dd>17.03. · Gertrud, von ")
	if strings.Index(page, "<dt>Geburtstag</dt>") > strings.Index(page, "<dt>Namenstag</dt>") {
		t.Error("the row Namenstag does not stand after Geburtstag")
	}
	nameDays(t, c, false, "catholic")
	ctHasNot(t, "off", c.page("/contacts/"+id, "de").body, "<dt>Namenstag</dt>")
}

func TestContactsPageLinksTheBirthdaysCalendar(t *testing.T) {
	t.Parallel()
	c := newCtBox(t)
	ctHas(t, "without others", c.page("/contacts", "de").body, `<a href="/contacts/birthdays">Geburtstagskalender</a>`)
	c.add(t, contactForm("Opa", "", nil, nil))
	page := c.page("/contacts", "de").body
	ctHas(t, "overview", page, `<p class="hint grp-link"><a href="/contacts/groups">Gruppen verwalten</a>`, `<a href="/contacts/birthdays">Geburtstagskalender</a>`)
	ctHas(t, "hub", c.page("/settings", "de").body, "Geburtstage und Namenstage eurer Familie")
}
