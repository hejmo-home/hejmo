package server_test

import (
	"encoding/json"
	"html"
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/hejmo-home/hejmo/internal/nameday"
	rh "github.com/hejmo-home/hejmo/internal/religiousholiday"
)

// Ticket 377: every holiday and name-day choice names its source, from the same records as Settings > Licences.

var citeRe = regexp.MustCompile(`<small class="cite"([^>]*)>(.*?)</small>`)

// citeLines are the source lines of the label of a choice: the text of each small.cite and its data-islamic.
func citeLines(t *testing.T, page, input string) []string {
	t.Helper()
	i := strings.Index(page, input)
	if i < 0 {
		t.Fatalf("page has no %s", input)
	}
	end := strings.Index(page[i:], "</label>")
	var out []string
	for _, m := range citeRe.FindAllStringSubmatch(page[i:i+end], -1) {
		out = append(out, strings.TrimSpace(html.UnescapeString(m[1]+"|"+m[2])))
	}
	return out
}

func TestEveryReligionCardNamesItsSourceUnderItsHint(t *testing.T) {
	t.Parallel()
	b := newRhBox(t)
	de := b.page("/settings/religious-holidays", "de").body
	for _, c := range []struct{ religion, want string }{
		{"christian", "|Quelle: Berechnet nach dem Kirchenjahr (Ostern nach dem Verfahren von Meeus)"},
		{"orthodox", "|Quelle: Berechnet nach dem Kalender der orthodoxen Kirche (Ostern nach dem julianischen Kalender)"},
		{"jewish", "|Quelle: Berechnet mit hebcal-go"},
		{"bahai", "|Quelle: Bahá’í-Weltzentrum, Tabelle des Universalen Hauses der Gerechtigkeit 2015–2064"},
		{"buddhist", "|Quellen: Berechnet aus den Mondphasen (meeus); Fester Tag im Jahr (8. Dezember)"},
		{"hindu", "|Quelle: Berechnet aus Sonne und Mond für den Ort eures Zuhauses (Regeln des Panchang)"},
		{"sikh", "|Quelle: Nanakshahi-Jantri der SGPC, Tabelle 2025–2027"},
	} {
		got := citeLines(t, de, `value="`+c.religion+`"`)
		if len(got) != 1 || got[0] != c.want {
			t.Errorf("%s: cite lines %q, want [%q]", c.religion, got, c.want)
		}
	}
	// the line is the second small of the card, right after the hint
	if !regexp.MustCompile(`<b>Judentum</b><small>[^<]*</small><small class="cite">Quelle: Berechnet mit hebcal-go</small></label>`).MatchString(de) {
		t.Errorf("the Judentum card has no cite line after its hint\n%s", de)
	}
	en := b.page("/settings/religious-holidays", "en").body
	if got := citeLines(t, en, `value="jewish"`); len(got) != 1 || got[0] != "|Source: Worked out with hebcal-go" {
		t.Errorf("English: %q", got)
	}
}

func TestIslamAndAlevismRenderOneLinePerIslamicCalendarAndTheCalendarRadiosHaveTheirOwn(t *testing.T) {
	t.Parallel()
	b := newRhBox(t)
	b.save(rhForm("muslim", "alevi"), "de") // the calendar radios are there once Islam or Alevism is chosen
	de := b.page("/settings/religious-holidays", "de").body
	diy := "Diyanet İşleri Başkanlığı, Tabelle der religiösen Tage 2020–2035"
	umm := "Berechnet mit go-hijri (Umm al-Qura)"
	unch := `<span class="src-unchecked">Noch nicht alle Einträge mit der Quelle abgeglichen</span>`
	aabf := "Alevitische Gemeinde Deutschland (AABF), feste Tage"
	for _, c := range []struct {
		input string
		want  []string
	}{
		{`value="muslim"`, []string{`data-islamic="diyanet"|Quelle: ` + diy, `data-islamic="umm_al_qura"|Quelle: ` + umm}},
		{`value="alevi"`, []string{`data-islamic="diyanet"|Quellen: ` + diy + "; " + aabf + " " + unch, `data-islamic="umm_al_qura"|Quellen: ` + umm + "; " + aabf + " " + unch}},
		{`name="islamic_calendar" value="diyanet"`, []string{"|Quelle: " + diy}},
		{`name="islamic_calendar" value="umm_al_qura"`, []string{"|Quelle: " + umm}},
	} {
		got := citeLines(t, de, c.input)
		if strings.Join(got, "\n") != strings.Join(c.want, "\n") {
			t.Errorf("%s: %q, want %q", c.input, got, c.want)
		}
	}
	// the Orthodox calendar radios share a source and get no line of their own
	b.save(rhForm("orthodox"), "de")
	page := b.page("/settings/religious-holidays", "de").body
	for _, v := range []string{`name="orthodox_calendar" value="revised_julian"`, `name="orthodox_calendar" value="julian"`} {
		if got := citeLines(t, page, v); len(got) != 0 {
			t.Errorf("%s has cite lines %q", v, got)
		}
	}
}

func TestEveryReligionHasASourceLineInBothLanguagesAndItsKeys(t *testing.T) {
	t.Parallel()
	b := newRhBox(t)
	for _, lang := range []string{"de", "en"} {
		page := b.page("/settings/religious-holidays", lang).body
		for _, r := range rh.Religions() {
			if got := citeLines(t, page, `value="`+string(r)+`"`); len(got) == 0 {
				t.Errorf("%s %s: no source line", lang, r)
			}
		}
		raw, err := os.ReadFile("../i18n/locales/" + lang + ".json")
		if err != nil {
			t.Fatal(err)
		}
		var keys map[string]string
		if err := json.Unmarshal(raw, &keys); err != nil {
			t.Fatal(err)
		}
		for _, k := range []string{"src.line", "src.lines"} {
			if keys[k] == "" {
				t.Errorf("%s lacks %s", lang, k)
			}
		}
		for _, r := range rh.Religions() {
			for _, o := range []rh.Options{{}, {Islamic: rh.UmmAlQura}} {
				for _, id := range rh.SourcesOf(r, o) {
					if keys["rh.source."+id] == "" {
						t.Errorf("%s lacks rh.source.%s (religion %s)", lang, id, r)
					}
				}
			}
		}
	}
}

var licenceRowRe = regexp.MustCompile(`(?s)<li><b>([^<]*)</b>(.*?)</li>`)
var licenceSpanRe = regexp.MustCompile(`<span>(.*?)</span>`)

// licenceSources maps the name of a row of the name-day card of Licences to the texts of its sources.
func licenceSources(t *testing.T, page string) map[string][]string {
	t.Helper()
	i := strings.Index(page, `<ul class="nd-lic">`)
	if i < 0 {
		t.Fatal("no name-day licences")
	}
	page = page[i : i+strings.Index(page[i:], "</ul>")]
	out := map[string][]string{}
	for _, m := range licenceRowRe.FindAllStringSubmatch(page, -1) {
		for _, s := range licenceSpanRe.FindAllStringSubmatch(m[2], -1) {
			out[html.UnescapeString(m[1])] = append(out[html.UnescapeString(m[1])], html.UnescapeString(s[1]))
		}
	}
	return out
}

func TestEveryNameDayListAndTheSlavaNameTheSameSourcesAsTheLicencesRow(t *testing.T) {
	t.Parallel()
	c := newCtBox(t)
	for _, lang := range []string{"de", "en"} {
		settings := c.page("/contacts/birthdays", lang).body
		rows := licenceSources(t, c.page("/settings/licences", lang).body)
		lists := append([]string{}, "slava")
		for _, l := range nameday.Lists() {
			lists = append(lists, string(l))
		}
		for _, l := range lists {
			var got []string
			var name string
			if l == "slava" {
				m := regexp.MustCompile(`<p class="hint cite" id="src-slava">(.*?)</p>`).FindStringSubmatch(settings)
				if m == nil {
					t.Fatalf("%s: no Slava source line", lang)
				}
				got = []string{"|" + html.UnescapeString(m[1])}
				name = map[string]string{"de": "Slava", "en": "Slava"}[lang]
			} else {
				got = citeLines(t, settings, `name="name_day_list" value="`+l+`"`)
				name = nameOfList(t, settings, l)
			}
			if len(got) != 1 {
				t.Errorf("%s %s: cite lines %q, want one", lang, l, got)
				continue
			}
			line := strings.SplitN(got[0], "|", 2)[1]
			sources := rows[name]
			if len(sources) == 0 {
				t.Errorf("%s %s: no Licences row %q", lang, l, name)
				continue
			}
			prefix := map[bool]string{false: "Source: ", true: "Sources: "}[len(sources) > 1]
			if lang == "de" {
				prefix = map[bool]string{false: "Quelle: ", true: "Quellen: "}[len(sources) > 1]
			}
			if want := prefix + strings.Join(sources, "; "); line != want {
				t.Errorf("%s %s: settings line\n%s\nLicences row\n%s", lang, l, line, want)
			}
		}
	}
}

func nameOfList(t *testing.T, page, list string) string {
	t.Helper()
	m := regexp.MustCompile(`value="` + list + `"[^>]*><b>([^<]*)</b>`).FindStringSubmatch(page)
	if m == nil {
		t.Fatalf("no name of list %s", list)
	}
	return html.UnescapeString(m[1])
}

func TestTheSlavaSelectIsDescribedByItsHintAndItsSourceAndTheRussianListShowsNothing(t *testing.T) {
	t.Parallel()
	c := newCtBox(t)
	page := c.page("/contacts/birthdays", "de").body
	ctHas(t, "birthdays", page, `aria-describedby="hint-slava src-slava"`, `<p class="hint" id="hint-slava">`,
		`</p><p class="hint cite" id="src-slava">Quelle: Kalender der Serbischen Orthodoxen Kirche · Календар Српске православне цркве, geprüft an den Heiligenseiten von spc.rs/sr/news/kalendar/ (Stand 26/11/2026)</p>`)
	ctHasNot(t, "birthdays", page, "russian_orthodox", "Russisch-orthodox")
	// the English line, with the long Cyrillic record of Bulgaria
	en := html.UnescapeString(c.page("/contacts/birthdays", "en").body)
	ctHas(t, "English", en, "Source: Calendar of the Bulgarian Orthodox Church · Българска Патриаршия (https://www.bg-patriarshia.bg): Православен календар, geprüft an den Monatsseiten 2026")
}

func TestTheNameDayRuleShowsTheRecordAloneWhenItStartsWithTheNameElseNameAndRecord(t *testing.T) {
	t.Parallel()
	c := newCtBox(t)
	de := licenceSources(t, c.page("/settings/licences", "de").body)
	en := licenceSources(t, c.page("/settings/licences", "en").body)
	for _, x := range []struct {
		what string
		rows map[string][]string
		name string
		want []string // the start of each source
	}{
		// the record starts with the name: the record alone, so the edition shows (it used to be dropped in German)
		{"de Evangelisch", de, "Evangelisch", []string{"Evangelischer Namenkalender (Evangelische Kirche in Deutschland), Anhang zu"}},
		{"de Katholisch", de, "Katholisch", []string{"Martyrologium Romanum, geprüft an den Tagesseiten", "Regionalkalender für das deutsche Sprachgebiet, geprüft an den Direktorien 2026"}},
		// it does not: name and record
		{"de Rumänisch", de, "Rumänisch-orthodox", []string{"Kalender der Rumänischen Orthodoxen Kirche · Calendarul ortodox"}},
		{"de Slava", de, "Slava", []string{"Kalender der Serbischen Orthodoxen Kirche · Календар Српске"}},
		{"en Catholic", en, "Catholic", []string{"Roman Martyrology · Martyrologium Romanum, geprüft an", "Regional calendar for the German-speaking area · Regionalkalender für das deutsche Sprachgebiet, geprüft an"}},
	} {
		if len(x.rows[x.name]) != len(x.want) {
			t.Errorf("%s: %q", x.what, x.rows[x.name])
			continue
		}
		for i, w := range x.want {
			if !strings.HasPrefix(x.rows[x.name][i], w) {
				t.Errorf("%s source %d = %q, want it to start %q", x.what, i, x.rows[x.name][i], w)
			}
		}
	}
}

func TestLicencesMarkTheAABFSourceAsUnchecked(t *testing.T) {
	t.Parallel()
	b := newRhBox(t)
	page := b.page("/settings/licences", "de").body
	rhHas(t, "licences", page, `<li class="unch">Alevitische Gemeinde Deutschland (AABF), feste Tage</li>`)
}
