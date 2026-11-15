package server_test

import (
	"encoding/base64"
	"fmt"
	"net/url"
	"os"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/hejmo-home/hejmo/internal/calendar"
	"github.com/hejmo-home/hejmo/internal/familyview"
	"github.com/hejmo-home/hejmo/internal/server"
	"github.com/hejmo-home/hejmo/internal/store"
	"github.com/hejmo-home/hejmo/internal/wholine"
	"github.com/hejmo-home/hejmo/web"
)

var family8 = []string{"Mama", "Papa", "Sebastian", "Mia", "Jonas", "Lotta", "Ben", "Emma"}
var shapes8 = []string{"circle-filled", "circle-empty", "square-filled", "square-empty", "triangle-filled", "diamond-filled", "cross-filled", "star-filled"}

// TestWritePreview128 writes the self-contained preview page for ticket 5.22
// (who an event is for: names instead of markers): the real family view and
// the real week of the web interface, rendered by the real templates for the
// example family of eight. Run:
// HEJMO_WRITE_PREVIEW_128=$PWD/docs/design/preview-128.html go test ./internal/server -run WritePreview128
func TestWritePreview128(t *testing.T) {
	out := os.Getenv("HEJMO_WRITE_PREVIEW_128")
	if out == "" {
		t.Skip("set HEJMO_WRITE_PREVIEW_128 to the output path to write the preview")
	}
	css, _ := web.Static.ReadFile("static/hejmo.css")
	js, _ := web.Static.ReadFile("static/form.js")
	font, _ := web.Static.ReadFile("static/fonts/Figtree-wght.ttf")
	style := strings.Replace(string(css), `url("/static/fonts/Figtree-wght.ttf")`, "url(data:font/ttf;base64,"+base64.StdEncoding.EncodeToString(font)+")", 1)

	// The family view: one example per rule, in the columns and the all-day row.
	d := func(day, h, m int) time.Time { return time.Date(2026, 11, day, h, m, 0, 0, berlinTZ) }
	var people []familyview.Person
	for i, n := range family8 {
		people = append(people, familyview.Person{ID: n, Name: n, Slot: i + 1, Shape: shapes8[i]})
	}
	occ := func(title string, s, e time.Time, who ...string) calendar.Occurrence {
		return calendar.Occurrence{EventID: title, CalendarID: "c", Title: title, Start: s, End: e, People: who}
	}
	allDay := func(title string, who ...string) calendar.Occurrence {
		o := occ(title, d(16, 0, 0), d(17, 0, 0), who...)
		o.AllDay = true
		return o
	}
	kids := []string{"Sebastian", "Mia", "Jonas", "Lotta", "Ben", "Emma"}
	occs := []calendar.Occurrence{
		occ("Dentist", d(16, 8, 0), d(16, 9, 0), "Mia"),
		occ("Piano", d(16, 15, 0), d(16, 16, 0), "Mia", "Jonas", "Ben"),
		occ("Swimming", d(16, 16, 30), d(16, 17, 30), kids...),
		occ("Hike", d(16, 10, 0), d(16, 12, 0), "Mama", "Sebastian", "Lotta", "Ben", "Emma", "Jonas"),
		occ("Dinner out", d(16, 18, 30), d(16, 20, 0), "Mama", "Papa", "Sebastian", "Mia", "Jonas", "Lotta", "Ben"),
		occ("Cinema", d(16, 19, 0), d(16, 21, 0), family8...),
		occ("Team meeting", d(16, 9, 30), d(16, 10, 30), "Papa"),
		occ("Family lunch", d(16, 12, 30), d(16, 14, 0)),
		allDay("Visit to Oma", "Mama", "Papa"),
		allDay("School trip", "Sebastian", "Mia", "Jonas"),
		allDay("Kids' day", kids...),
		allDay("Papa's birthday"),
	}
	groups := []wholine.Group{{Name: "Kids", Members: kids}}
	view, err := server.RenderFamilyView("en", familyview.Build(familyview.Input{Now: d(16, 8, 0), Lang: "en", Region: "DE-BY", People: people, Groups: groups, Occurrences: occs}))
	if err != nil {
		t.Fatal(err)
	}

	// The web week: the same family in a real box.
	f := calendarBox(t)
	for _, n := range family8[4:] {
		p, err := f.db.AddPerson(f.householdID, store.Person{Name: n, Role: store.RoleChild})
		if err != nil {
			t.Fatal(err)
		}
		f.people[n] = p
	}
	var ids []string
	for _, n := range kids {
		ids = append(ids, f.people[n].ID)
	}
	if _, err := f.db.CreateGroup(f.householdID, "Kids", ids); err != nil {
		t.Fatal(err)
	}
	ev := func(title, date, from, to string, who ...string) {
		v := eventForm(title, date, from, to)
		if len(who) > 0 {
			v.Del("whole_family")
			for _, w := range who {
				v.Add("people", f.people[w].ID)
			}
		}
		f.postEvent(t, v)
	}
	f.now = time.Date(2026, 11, 16, 9, 0, 0, 0, berlinTZ)
	ev("Team meeting", "2026-11-16", "09:30", "10:30", "Papa")
	ev("Family lunch", "2026-11-16", "12:30", "14:00")
	ev("Dentist", "2026-11-17", "08:00", "09:00", "Mia")
	ev("Piano", "2026-11-17", "15:00", "16:00", "Mia", "Jonas", "Ben")
	ev("Swimming", "2026-11-18", "16:30", "17:30", kids...)
	ev("Dinner out", "2026-11-19", "18:30", "20:00", "Mama", "Papa", "Sebastian", "Mia", "Jonas", "Lotta", "Ben")
	ev("Hike", "2026-11-20", "10:00", "12:00", "Mama", "Sebastian", "Lotta", "Ben", "Emma", "Jonas")
	ev("Cinema", "2026-11-21", "19:00", "21:00", family8...)
	v := url.Values{"title": {"Visit to Oma"}, "all_day": {"on"}, "start_date": {"2026-11-21"}, "end_date": {"2026-11-22"}, "repeat": {"none"}}
	v.Add("people", f.people["Mama"].ID)
	v.Add("people", f.people["Papa"].ID)
	f.postEvent(t, v)
	week := getEN(f.box, "/calendar?week=2026-11-16").body
	week = regexp.MustCompile(`(?s)<script src="[^"]*"[^>]*></script>`).ReplaceAllString(week, "")
	week = regexp.MustCompile(`(?s)<link rel="stylesheet"[^>]*>`).ReplaceAllString(week, "")
	weekInner := week[strings.Index(week, "<body"):strings.LastIndex(week, "</body>")]

	var body strings.Builder
	themes := map[string]string{"light": "Light", "dark": "Dark", "bw": "Black and white"}
	frame := func(theme string, w, h int) {
		fmt.Fprintf(&body, `<figure><figcaption>%s, %d x %d</figcaption><div class="frame" data-theme="%s" style="width:%dpx;height:%dpx">%s</div></figure>`+"\n", themes[theme], w, h, theme, w, h, view)
	}
	week_ := func(theme string, w, h int) {
		fmt.Fprintf(&body, `<figure><figcaption>%s, %d x %d</figcaption><iframe data-theme="%s" width="%d" height="%d" title="Week"></iframe></figure>`+"\n", themes[theme], w, h, theme, w, h)
	}
	body.WriteString(`<h2>1. Family view, landscape 1024 x 768</h2><p class="note">Cards say who the event is for, in words, under the title; the column's own person is left out and a card for one person says nothing (Dentist, Team meeting). Markers stay only in the column headers, the legend for black and white. Rules by example, in Mia's, Sebastian's and Mama's columns: Piano is "with" the names (rule 4: Jonas, Ben), Swimming is the subgroup "with Kids" (rule 2), Hike is "everyone except Papa and Mia" (rule 3), Dinner out is "everyone except Emma" (rule 3), Cinema is "Whole family" (rule 1). Eight people sit in eight columns, six side by side, the rest scroll sideways; the whole view is shown scrolled to the left.</p><div class="row">
`)
	frame("light", 1024, 768)
	frame("dark", 1024, 768)
	frame("bw", 1024, 768)
	body.WriteString(`</div><h2>2. Family view, portrait 768 x 1024</h2><div class="row">
`)
	frame("light", 768, 1024)
	frame("dark", 768, 1024)
	frame("bw", 768, 1024)
	body.WriteString(`</div><h2>3. The all-day row</h2><p class="note">Title, then the who-line in square brackets: "Visit to Oma [Mama, Papa]", "School trip [Sebastian, Mia, Jonas]"; the six children are the subgroup ("[with Kids]"); the whole family has no brackets (Papa's birthday). It is the top row of every frame above.</p>
<h2>4. The week in the web interface</h2><p class="note">Every card has the who-line under the time, with nobody left out ("Whole family" for events without an audience). No markers.</p><div class="row">
`)
	week_("light", 1024, 768)
	week_("dark", 1024, 768)
	week_("bw", 1024, 768)
	week_("light", 768, 1024)
	body.WriteString("</div>\n")

	page := `<!doctype html>
<!-- Generated by TestWritePreview128 from the real templates (ticket 5.22). The stylesheet, script and font are inlined copies of web/static and do not follow later changes. No external requests. -->
<html lang="en"><head><meta charset="utf-8"><title>Hejmo preview 5.22 who an event is for</title>
<style>` + style + `
body{padding:16px;background:#888;font-family:Figtree,system-ui,sans-serif}
body > h1,body > h2,body > .note{color:#fff}body > h1{font-size:22px}body > h2{font-size:18px;margin:24px 0 4px}.note{max-width:900px;margin:0 0 10px}
figure{margin:0 0 28px}figcaption{color:#fff;margin-bottom:6px;font-weight:700}
.row{display:flex;flex-wrap:wrap;gap:24px;align-items:flex-start}
.frame{background:var(--background);color:var(--text);padding:var(--margin);overflow:hidden;box-sizing:border-box}
.frame .fv{height:100%}
iframe{border:0;background:#fff;display:block}
</style></head><body>
<h1>Hejmo 5.22 who an event is for: preview with the example family of eight (Mama, Papa, Sebastian, Mia, Jonas, Lotta, Ben, Emma)</h1>
` + body.String() + `
<template id="css">` + string(css) + `</template>
<template id="js">` + string(js) + `</template>
<template id="week">` + weekInner + `</template>
<script>
(function () {
  var font = Uint8Array.from(atob("` + base64.StdEncoding.EncodeToString(font) + `"), function (c) { return c.charCodeAt(0); });
  var fontUrl = URL.createObjectURL(new Blob([font], { type: "font/ttf" }));
  var css = document.getElementById("css").innerHTML.replace("/static/fonts/Figtree-wght.ttf", fontUrl).replace(/&amp;/g, "&").replace(/&gt;/g, ">").replace(/&lt;/g, "<");
  var js = document.getElementById("js").textContent;
  var inner = document.getElementById("week").innerHTML;
  document.querySelectorAll("iframe").forEach(function (f) {
    f.srcdoc = '<!doctype html><html lang="en" data-theme="' + f.getAttribute("data-theme") + '"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1"><style>' + css + '</style></head>' + inner + '<script>' + js + '<\/script></html>';
  });
})();
</script>
</body></html>
`
	if err := os.WriteFile(out, []byte(page), 0o644); err != nil {
		t.Fatal(err)
	}
}
