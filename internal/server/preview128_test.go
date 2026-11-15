package server_test

import (
	"encoding/base64"
	"fmt"
	"github.com/hejmo-home/hejmo/internal/i18n"
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

// germanView is the family view in German for a household with long names,
// showing three of its eight columns (Mama, Maximilian, Konstantin-Alexander).
func germanView(t *testing.T, d func(day, h, m int) time.Time) string {
	t.Helper()
	names := []string{"Mama", "Papa", "Maximilian", "Anna-Lena", "Friederike", "Konstantin-Alexander", "Ben", "Emma"}
	slots := []int{1, 2, 3, 4, 6, 5, 7, 8}
	shapes := []string{"circle-filled", "circle-empty", "square-filled", "square-empty", "diamond-filled", "triangle-filled", "cross-filled", "star-filled"}
	var people []familyview.Person
	for i, n := range names {
		people = append(people, familyview.Person{ID: n, Name: n, Slot: slots[i], Shape: shapes[i]})
	}
	occ := func(title string, s, e time.Time, who ...string) calendar.Occurrence {
		return calendar.Occurrence{EventID: title, CalendarID: "c", Title: title, Start: s, End: e, People: who}
	}
	allDay := func(title string, who ...string) calendar.Occurrence {
		o := occ(title, d(16, 0, 0), d(17, 0, 0), who...)
		o.AllDay = true
		return o
	}
	kids := names[2:]
	occs := []calendar.Occurrence{
		allDay("Besuch bei Oma Hildegard", "Mama", "Papa"),
		allDay("Ausflug Klasse 3b", "Maximilian", "Anna-Lena", "Friederike"),
		allDay("Kindertag", kids...),
		allDay("Papas Geburtstag"),
		occ("Kino", d(16, 19, 0), d(16, 21, 0), names...),
		occ("Zahnarzt", d(16, 8, 0), d(16, 9, 0), "Maximilian"),
		occ("Wanderung", d(16, 10, 0), d(16, 12, 0), "Mama", "Maximilian", "Friederike", "Konstantin-Alexander", "Ben", "Emma"),
		occ("Klavierunterricht", d(16, 15, 0), d(16, 16, 0), "Maximilian", "Friederike", "Konstantin-Alexander"),
		occ("Schwimmen", d(16, 16, 30), d(16, 17, 30), kids...),
		occ("Fußballtraining", d(16, 17, 0), d(16, 18, 0), "Papa", "Maximilian", "Konstantin-Alexander", "Ben", "Emma"),
		occ("Elternabend Grundschule", d(16, 19, 30), d(16, 21, 0), "Mama", "Papa"),
	}
	v := familyview.Build(familyview.Input{I18n: i18n.MustLoad(), Now: d(16, 8, 0), Lang: "de", Region: "DE-BY", People: people,
		Groups: []wholine.Group{{Name: "Kinder", Members: kids}}, Occurrences: occs})
	var shown []familyview.Column
	for _, c := range v.Columns {
		if c.Person.Name == "Mama" || c.Person.Name == "Maximilian" || c.Person.Name == "Konstantin-Alexander" {
			shown = append(shown, c)
		}
	}
	v.Columns = shown
	out, err := server.RenderFamilyView("de", v)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

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

	// The family view: one example per rule, in the columns and the family strip.
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
		occ("Football training", d(16, 17, 0), d(16, 18, 0), "Papa", "Sebastian", "Jonas", "Ben", "Emma"),
		occ("Cinema", d(16, 19, 0), d(16, 21, 0), family8...),
		occ("Team meeting", d(16, 9, 30), d(16, 10, 30), "Papa"),
		occ("Family lunch", d(16, 12, 30), d(16, 14, 0)),
		allDay("Visit to Oma", "Mama", "Papa"),
		allDay("School trip", "Sebastian", "Mia", "Jonas"),
		allDay("Kids' day", kids...),
		allDay("Papa's birthday"),
	}
	groups := []wholine.Group{{Name: "Kids", Members: kids}}
	view, err := server.RenderFamilyView("en", familyview.Build(familyview.Input{I18n: i18n.MustLoad(), Now: d(16, 8, 0), Lang: "en", Region: "DE-BY", People: people, Groups: groups, Occurrences: occs}))
	if err != nil {
		t.Fatal(err)
	}

	// German, long names, three of the eight columns (section 5).
	viewDE := germanView(t, d)

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
	ev("Football training", "2026-11-18", "17:00", "18:00", "Papa", "Sebastian", "Jonas", "Ben", "Emma")
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
	frameDE := func(theme string, w, h int) {
		fmt.Fprintf(&body, `<figure><figcaption>%s, %d x %d, German</figcaption><div class="frame" data-theme="%s" lang="de" style="width:%dpx;height:%dpx">%s</div></figure>`+"\n", themes[theme], w, h, theme, w, h, viewDE)
	}
	week_ := func(theme string, w, h int) {
		caption := fmt.Sprintf("%s, %d x %d", themes[theme], w, h)
		if w < 500 {
			caption = fmt.Sprintf("Phone, light, %d x %d", w, h)
		}
		fmt.Fprintf(&body, `<figure><figcaption>%s</figcaption><iframe data-theme="%s" width="%d" height="%d" title="Week"></iframe></figure>`+"\n", caption, theme, w, h)
	}
	body.WriteString(`<h2>1. Family view, landscape 1024 x 768</h2><p class="note"><b>Design pass (Opus).</b> Each card says in words who the event is for, on its own line under the title. The column already names its person, so that person is left out, and a card for one person has no who-line (Dentist, Team meeting). Markers appear only in the column headers, where they are the legend for black and white. One example per rule: <b>rule 4</b>, the names: Piano "with Jonas, Ben" in Mia's column, and Football training "with Papa, Jonas, Ben, Emma" in Sebastian's column. The long list wraps at the spaces; names are never cut, hyphenated or replaced by "+2". <b>Rule 2</b>, the subgroup: Swimming "with Kids". <b>Rule 3</b>, everyone except one or two: Hike "everyone except Papa and Mia", Dinner out "everyone except Emma". <b>Rule 1</b>, the whole family: Cinema has all eight people, so it goes to the family strip with Family lunch and is not repeated as eight identical cards. With eight people, the columns that do not fit scroll sideways (the layout for eight is #76); every frame shows the view scrolled to the left.</p><div class="row">
`)
	frame("light", 1024, 768)
	frame("dark", 1024, 768)
	frame("bw", 1024, 768)
	body.WriteString(`</div><h2>2. Family view, portrait 768 x 1024</h2><div class="row">
`)
	frame("light", 768, 1024)
	frame("dark", 768, 1024)
	frame("bw", 768, 1024)
	body.WriteString(`</div><h2>3. The family strip</h2><p class="note">The family strip is the top row of every frame above. Square brackets after the title say who the item is for, and they never contain "with". They hold the names (Visit to Oma [Mama, Papa], School trip [Sebastian, Mia, Jonas]), the subgroup name (Kids' day [Kids]) or the "everyone except" line. Whole-family items have no brackets (Papa's birthday, Family lunch, Cinema). Timed whole-family items show their time after the title.</p>
<h2>4. The week in the web interface</h2><p class="note">Every card shows the who-line under the time. Nobody is left out, because the week has no person columns. Events for everyone, explicitly or with no audience, read "Whole family" (Family lunch, Cinema). The week has no markers. Football training shows a five-name list wrapping in a narrow day column. The 375 px phone frame is the narrowest case.</p><div class="row">
`)
	week_("light", 1024, 768)
	week_("dark", 1024, 768)
	week_("bw", 1024, 768)
	week_("light", 768, 1024)
	week_("light", 375, 812)
	body.WriteString(`</div>
<h2>5. German, long names, narrow columns</h2><p class="note">The same rules in German, with long names (Maximilian, Anna-Lena, Friederike, Konstantin-Alexander) and three of the eight columns. German words: „ganze Familie“, „alle außer Papa und Anna-Lena“, „mit Friederike, Konstantin-Alexander“. The subgroup line is „mit Gruppe Kinder“, which stays grammatical for any group name. „mit Kinder“ is wrong German, and the dative „mit den Kindern“ cannot be built from a free name. In the brackets the subgroup is the bare name: [Kinder]. Each name, with its comma, stays together on one line: Anna-Lena and Konstantin-Alexander move to the next line whole. A name breaks only when it is wider than the whole card.</p><div class="row">
`)
	frameDE("light", 768, 820)
	frameDE("bw", 768, 820)
	body.WriteString(`</div>
<h2>6. The rules at a glance</h2><div class="note" style="background:#fff;color:#111;padding:12px;border-radius:12px"><table style="border-collapse:collapse;font-size:15px" cellpadding="6">
<tr style="text-align:left;border-bottom:2px solid #111"><th>Rule (first match wins)</th><th>Card (English)</th><th>Card (German)</th><th>Family strip</th></tr>
<tr><td>1 Everyone (explicitly or no audience)</td><td>Whole family (web week only; on the display it goes to the family strip)</td><td>ganze Familie</td><td>no brackets</td></tr>
<tr><td>2 Exactly one subgroup</td><td>with Kids</td><td>mit Gruppe Kinder</td><td>[Kids] / [Kinder]</td></tr>
<tr><td>3 Everyone except one or two (two or more people in the event)</td><td>everyone except Papa and Mia</td><td>alle außer Papa und Mia</td><td>[everyone except Papa and Mia]</td></tr>
<tr><td>4 Otherwise</td><td>with Mia, Jonas, Ben</td><td>mit Mia, Jonas, Ben</td><td>[Mia, Jonas, Ben]</td></tr>
<tr><td>Card in a person's column</td><td colspan="3">That person is left out of rule 4 names; rules 1 to 3 use the full set. Card for that person alone: no who-line.</td></tr>
<tr><td>Never</td><td colspan="3">"+2", "…", cut or hyphenated names, markers on cards or in the strip.</td></tr>
</table></div>
`)

	page := `<!doctype html>
<!-- Generated by TestWritePreview128 from the real templates (ticket 5.22), then revised by the design pass (Opus): Cinema in the family strip, [Kids] in brackets, Football training, unbroken who-line names, phone frame, sections 5 and 6. The generator must reproduce this page. The stylesheet, script and font are inlined copies of web/static and do not follow later changes. No external requests. -->
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
