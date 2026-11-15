package server_test

import (
	"regexp"
	"strings"
	"testing"
)

var (
	whoRe       = regexp.MustCompile(`<(?:div|span) class="who">((?:[^<]|<span class="nm">[^<]*</span>)*)</(?:div|span)>`)
	nmTagRe     = regexp.MustCompile(`</?span[^>]*>`)
	markerOnRe  = regexp.MustCompile(`class="marker small"|class="markers"`)
	headerMarkR = regexp.MustCompile(`<h2><a class="person-link"[^>]*><span class="marker" data-shape="[a-z-]+"></span>`)
)

// 5.22: Given an event for two people, then its card says in words who it is
// for, in every theme: the column's own person is left out, a card for one
// person names nobody, and no card or family strip item renders a person marker.
// The column headers keep theirs, the legend for black and white.
func TestCardsAndAllDayRowSayInWordsWhoTheEventIsFor(t *testing.T) {
	for _, theme := range []string{"eink_color", "eink_bw", "screen"} {
		f := newFamilyBox(t, theme)
		f.event(t, "Football", f.at(14, 15, 0), f.at(14, 16, 0), false, "Sebastian", "Mia")
		f.event(t, "Dentist", f.at(14, 9, 0), f.at(14, 10, 0), false, "Mama")
		f.event(t, "Grandparents visit", f.at(14, 0, 0), f.at(15, 0, 0), true, "Mama", "Papa")
		f.event(t, "Hut weekend", f.at(14, 0, 0), f.at(15, 0, 0), true)
		page := f.view(t, "en", f.at(14, 8, 0))

		if markerOnRe.MatchString(page) {
			t.Errorf("%s: a card or the family strip renders a person marker", theme)
		}
		if got := len(headerMarkR.FindAllString(page, -1)); got != 4 {
			t.Errorf("%s: %d column headers with a marker, want 4", theme, got)
		}
		cols := map[string]string{}
		for _, c := range columnRe.FindAllStringSubmatch(page, -1) {
			cols[c[1]] = strings.Join(flat(whoRe.FindAllStringSubmatch(c[0], -1)), "|")
		}
		for name, want := range map[string]string{"Sebastian": "everyone except Mama and Papa", "Mia": "everyone except Mama and Papa", "Mama": "", "Papa": ""} {
			if cols[name] != want {
				t.Errorf("%s: %s column who-lines = %q, want %q", theme, name, cols[name], want)
			}
		}
		row := strip(t, page)
		if !strings.Contains(row, `<span class="who">[everyone except <span class="nm">Sebastian</span> and <span class="nm">Mia</span>]</span>`) {
			t.Errorf("%s: family strip lacks the brackets:\n%s", theme, row)
		}
		if strings.Count(row, `class="who"`) != 1 {
			t.Errorf("%s: the whole family needs no brackets:\n%s", theme, row)
		}
	}
}

// plain is markup of a who-line without its name spans.
func plain(s string) string { return nmTagRe.ReplaceAllString(s, "") }

func flat(m [][]string) []string {
	var out []string
	for _, x := range m {
		out = append(out, plain(x[1]))
	}
	return out
}

// 5.22: the German page uses the German words.
func TestWhoLineIsGerman(t *testing.T) {
	f := newFamilyBox(t, "eink_color")
	f.event(t, "Fußball", f.at(14, 15, 0), f.at(14, 16, 0), false, "Sebastian", "Mia")
	page := f.view(t, "de", f.at(14, 8, 0))
	if !strings.Contains(page, `<div class="who">alle außer <span class="nm">Mama</span> und <span class="nm">Papa</span></div>`) {
		t.Errorf("no German who-line:\n%s", page)
	}
}

// 5.22: the web week shows the who-line under the time on every card, with
// nobody left out, and no person markers.
func TestWeekCalendarShowsTheWhoLineOnEveryCard(t *testing.T) {
	f := calendarBox(t)
	ev := func(title, date string, who ...string) {
		v := eventForm(title, date, "15:00", "16:00")
		if len(who) > 0 {
			v.Del("whole_family")
			for _, w := range who {
				v.Add("people", f.people[w].ID)
			}
		}
		f.postEvent(t, v)
	}
	ev("Lunch", "2026-11-16")
	ev("Football", "2026-11-17", "Sebastian", "Mia")
	ev("Dentist", "2026-11-18", "Mama")
	week := getEN(f.box, "/calendar?week=2026-11-16").body
	if markerOnRe.MatchString(week) || strings.Contains(week, `class="marker`) {
		t.Error("the week renders person markers")
	}
	for title, want := range map[string]string{"Lunch": "Whole family", "Football": "everyone except Mama and Papa", "Dentist": "with Mama"} {
		card := ""
		for _, chunk := range strings.Split(week, `<li class="ev"`) {
			if strings.Contains(chunk, ">"+title+"<") {
				card, _, _ = strings.Cut(chunk, "</li>")
			}
		}
		if got := whoRe.FindStringSubmatch(card); got == nil || plain(got[1]) != want {
			t.Errorf("%s card lacks %q:\n%s", title, want, card)
		}
		if title == "Football" && !strings.Contains(card, `<span class="nm">Mama</span> and <span class="nm">Papa</span>`) {
			t.Errorf("names of the Football card are not in .nm spans:\n%s", card)
		}
	}
}

// 5.22: German subgroup is "mit Gruppe <name>" on the card and the bare name in
// the strip's brackets; a group name is kept whole like a person's.
func TestGermanSubgroupSaysGruppeOnTheCardAndNothingInTheBrackets(t *testing.T) {
	f := newFamilyBox(t, "eink_color")
	var ids []string
	for _, n := range []string{"Sebastian", "Mia"} {
		ids = append(ids, f.people[n].ID)
	}
	if _, err := f.db.CreateGroup(f.householdID, "Kinder", ids); err != nil {
		t.Fatal(err)
	}
	f.event(t, "Schwimmen", f.at(14, 15, 0), f.at(14, 16, 0), false, "Sebastian", "Mia")
	f.event(t, "Kindertag", f.at(14, 0, 0), f.at(15, 0, 0), true, "Sebastian", "Mia")
	page := f.view(t, "de", f.at(14, 8, 0))
	if !strings.Contains(page, `<div class="who">mit Gruppe <span class="nm">Kinder</span></div>`) {
		t.Errorf("no German subgroup line:\n%s", page)
	}
	if row := strip(t, page); !strings.Contains(row, `<span class="who">[<span class="nm">Kinder</span>]</span>`) {
		t.Errorf("no bare group name in the brackets:\n%s", row)
	}
}

// 5.22: every name in a who-line sits in a .nm span, so it never splits across
// lines: on a card, in the strip and in the web week.
func TestEveryNameOfAWhoLineSitsInANameSpan(t *testing.T) {
	f := newFamilyBox(t, "eink_color")
	f.event(t, "Training", f.at(14, 17, 0), f.at(14, 18, 0), false, "Mama", "Papa", "Sebastian")
	f.event(t, "Visit", f.at(14, 0, 0), f.at(15, 0, 0), true, "Mama", "Papa", "Sebastian")
	page := f.view(t, "en", f.at(14, 8, 0))
	for _, m := range whoRe.FindAllStringSubmatch(page, -1) {
		text := plain(m[1])
		if text == "Whole family" {
			continue
		}
		for _, name := range []string{"Mama", "Papa", "Sebastian", "Mia"} {
			if strings.Contains(text, name) && !regexp.MustCompile(`<span class="nm">`+name+`,?</span>`).MatchString(m[1]) {
				t.Errorf("%s is not in a .nm span: %s", name, m[0])
			}
		}
	}
	if len(whoRe.FindAllString(page, -1)) < 4 {
		t.Errorf("expected who-lines on the cards and the strip:\n%s", page)
	}
}
