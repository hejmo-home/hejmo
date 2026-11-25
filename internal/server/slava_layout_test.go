package server_test

import (
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"testing"
)

// birthdaysPages are the Birthdays calendar page of ticket 338 in the states a crawl does not reach, in German and
// English, with the eight people of the fixture and their long names: an Orthodox church with the old calendar and a
// Slava saved, a church that follows the other calendar (the notice), a Slava with errors at its fields, an own Slava,
// and the event pages of a name day in the old calendar and of the Slava. The box is put back as it was.
func birthdaysPages(t *testing.T, f *layoutFixture) []layoutPage {
	t.Helper()
	site := siteOf("app", f.box, f.client)
	var pages []layoutPage
	post := func(form url.Values, lang string) resp {
		return f.do(http.MethodPost, "/contacts/birthdays", form, map[string]string{"Accept-Language": lang})
	}
	page := func(name, path string, get func(lang string) resp) {
		t.Helper()
		out := map[string]string{}
		for _, lang := range []string{"de", "en"} {
			r := get(lang)
			if r.status != http.StatusOK && r.status != http.StatusUnprocessableEntity {
				t.Fatalf("%s %s = %d\n%s", name, path, r.status, r.body)
			}
			out[lang] = r.body
		}
		pages = append(pages, layoutPage{site: site, path: path + "#" + name, html: out})
	}
	view := func(name string) {
		page(name, "/contacts/birthdays", func(lang string) resp {
			return f.do(http.MethodGet, "/contacts/birthdays", nil, map[string]string{"Accept-Language": lang})
		})
	}
	saved := url.Values{"name_days": {"1"}, "name_day_list": {"russian_orthodox"}, "orthodox_calendar": {"julian"}, "slava_on": {"1"}, "slava": {"nikola"}}
	if r := post(saved, "de"); r.status != http.StatusSeeOther {
		t.Fatalf("save = %d\n%s", r.status, r.body)
	}
	view("saved-russian-julian-slava")
	linkOf := func(week, title string) string {
		t.Helper()
		body := f.do(http.MethodGet, "/calendar?week="+week, nil, map[string]string{"Accept-Language": "de"}).body
		m := regexp.MustCompile(`<a class="title" href="(/calendar/events/[^"]+)">` + regexp.QuoteMeta(title) + `</a>`).FindStringSubmatch(body)
		if m == nil {
			t.Fatalf("no link for %q in the week of %s", title, week)
		}
		return strings.ReplaceAll(m[1], "&amp;", "&")
	}
	slavaHref := linkOf("2026-12-19", "Slava: Nikoljdan")
	page("slava-event", "/calendar/events/slava", func(lang string) resp {
		return f.do(http.MethodGet, slavaHref, nil, map[string]string{"Accept-Language": lang})
	})
	if href := nameDayLink(f, "2026-12-07"); href != "" {
		page("name-day-julian", "/calendar/events/nameday", func(lang string) resp {
			return f.do(http.MethodGet, href, nil, map[string]string{"Accept-Language": lang})
		})
	}
	// A church that follows the other calendar than the one set: the notice, "Mehr Optionen" open.
	post(url.Values{"name_days": {"1"}, "name_day_list": {"bulgarian_orthodox"}, "orthodox_calendar": {"julian"}, "slava_on": {"1"}, "slava": {"spas"}}, "de")
	view("mismatch-bulgarian-julian-easter-slava")
	post(url.Values{"name_days": {"1"}, "name_day_list": {"ukrainian_orthodox"}, "orthodox_calendar": {"revised_julian"}, "slava_on": {"1"}, "slava": {"own"},
		"slava_name": {"Sveti Vasilije Ostroški Čudotvorac"}, "slava_day": {"25"}, "slava_month": {"5"}}, "de")
	view("ukrainian-own-slava")
	// Errors at the fields.
	for name, form := range map[string]url.Values{
		"error-no-saint":    {"name_days": {"1"}, "name_day_list": {"russian_orthodox"}, "orthodox_calendar": {"julian"}, "slava_on": {"1"}},
		"error-own-day":     {"slava_on": {"1"}, "slava": {"own"}, "slava_name": {strings.Repeat("Sveti ", 9)}, "slava_day": {"31"}, "slava_month": {"6"}},
		"error-own-no-name": {"slava_on": {"1"}, "slava": {"own"}, "slava_day": {"25"}, "slava_month": {"5"}},
	} {
		form := form
		page(name, "/contacts/birthdays", func(lang string) resp {
			r := post(form, lang)
			if r.status != http.StatusUnprocessableEntity {
				t.Fatalf("%s = %d\n%s", name, r.status, r.body)
			}
			return r
		})
	}
	// The box as it was: name days and the Slava off.
	if r := post(url.Values{}, "de"); r.status != http.StatusSeeOther {
		t.Fatalf("reset = %d\n%s", r.status, r.body)
	}
	return pages
}

// nameDayLink finds a name-day event of a week by its link; "" when the week has none.
func nameDayLink(f *layoutFixture, week string) string {
	body := f.do(http.MethodGet, "/calendar?week="+week, nil, map[string]string{"Accept-Language": "de"}).body
	m := regexp.MustCompile(`<a class="title" href="(/calendar/events/[^"]+)">Namenstag [^<]*</a>`).FindStringSubmatch(body)
	if m == nil {
		return ""
	}
	return strings.ReplaceAll(m[1], "&amp;", "&")
}
