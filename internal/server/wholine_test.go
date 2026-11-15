package server_test

import (
	"regexp"
	"strings"
	"testing"
)

var (
	whoRe       = regexp.MustCompile(`<[a-z]+ class="who">([^<]*)<`)
	markerOnRe  = regexp.MustCompile(`class="marker small"|class="markers"`)
	headerMarkR = regexp.MustCompile(`<h2><a class="person-link"[^>]*><span class="marker" data-shape="[a-z-]+"></span>`)
)

// 5.22: Given an event for two people, then its card says in words who it is
// for, in every theme: the column's own person is left out, a card for one
// person names nobody, and no card or all-day item renders a person marker.
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
			t.Errorf("%s: a card or the all-day row renders a person marker", theme)
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
		if !strings.Contains(row, `<span class="who">[everyone except Sebastian and Mia]</span>`) {
			t.Errorf("%s: all-day row lacks the brackets:\n%s", theme, row)
		}
		if strings.Count(row, `class="who"`) != 1 {
			t.Errorf("%s: the whole family needs no brackets:\n%s", theme, row)
		}
	}
}

func flat(m [][]string) []string {
	var out []string
	for _, x := range m {
		out = append(out, x[1])
	}
	return out
}

// 5.22: the German page uses the German words.
func TestWhoLineIsGerman(t *testing.T) {
	f := newFamilyBox(t, "eink_color")
	f.event(t, "Fußball", f.at(14, 15, 0), f.at(14, 16, 0), false, "Sebastian", "Mia")
	page := f.view(t, "de", f.at(14, 8, 0))
	if !strings.Contains(page, `<div class="who">alle außer Mama und Papa</div>`) {
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
		if !strings.Contains(card, `<span class="who">`+want+`</span>`) {
			t.Errorf("%s card lacks %q:\n%s", title, want, card)
		}
	}
}
